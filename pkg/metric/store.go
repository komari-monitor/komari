package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/komari-monitor/komari/internal/sqlitetune"
)

// Store is the main metric storage handle.
//
// Store 是 metric 包的主入口，封装数据库连接、SQL 方言和表名。
type Store struct {
	// cfg is the validated store configuration.
	//
	// cfg 是已校验的 Store 配置。
	cfg Config
	// db is the primary database pool used for writes and fallback reads.
	//
	// db 是用于写入和兜底读取的主数据库连接池。
	db *sql.DB
	// readDB is the optional dedicated read-only pool.
	//
	// readDB 是可选的专用只读连接池。
	readDB *sql.DB
	// ownedDB reports whether Store should close db.
	//
	// ownedDB 表示 Store 是否应关闭 db。
	ownedDB bool
	// ownedReadDB reports whether Store should close readDB.
	//
	// ownedReadDB 表示 Store 是否应关闭 readDB。
	ownedReadDB bool
	// dialect renders backend-specific SQL.
	//
	// dialect 渲染后端专用 SQL。
	dialect dialect
	// tables stores the physical table names for this store.
	//
	// tables 保存当前 Store 的实际表名。
	tables tables
	// maintenanceMu serializes physical storage maintenance while allowing
	// concurrent size reads.
	//
	// maintenanceMu 串行化物理存储维护，同时允许并发读取存储大小。
	maintenanceMu sync.RWMutex
	// retentionMu serializes a retention change with writes and compaction so a
	// disabled metric cannot be repopulated by an in-flight operation.
	retentionMu sync.RWMutex
	// ingestMu keeps the exact-sample upsert and its hot-rollup update atomic
	// with respect to other writers.
	ingestMu sync.Mutex
	// rollupViewMu keeps persisted and hot rollups as one queryable view while
	// closed buckets move into the database or replace an earlier summary.
	rollupViewMu sync.RWMutex
	// rawMu protects the compact exact-sample window used by Store.Query.
	rawMu sync.RWMutex
	raw   map[rawSeriesKey]*rawSeries
	// hotMu protects minute summaries that may still receive an exact upsert
	// from the ten-minute raw window. Exact samples are never persisted.
	hotMu      sync.RWMutex
	hot        map[hotRollupKey]*rollupBucket
	hotReplace map[hotRollupKey]struct{}
	// coarseMu protects in-memory parent buckets. Coarser tiers are only
	// materialized once their source window has remained closed long enough for
	// the raw late-arrival window to pass.
	coarseMu sync.RWMutex
	coarse   map[coarseRollupKey]*coarseRollup
	// mu protects closed state.
	//
	// mu 保护 closed 状态。
	mu sync.RWMutex
	// closed reports whether Close has been called.
	//
	// closed 表示 Close 是否已经被调用。
	closed bool
}

// Open initializes a Store from a Config.
//
// Open 根据配置打开 Store，初始化连接池，并在需要时执行自动迁移。
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.TablePrefix == "" {
		cfg.TablePrefix = "metric_"
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Driver == DriverSQLite {
		var err error
		cfg, err = prepareSQLiteConfig(cfg)
		if err != nil {
			return nil, err
		}
	}

	s := &Store{
		cfg:     cfg,
		dialect: newDialect(cfg.Driver),
		tables: tables{
			definitions: tableName(cfg.TablePrefix, "definitions"),
			points:      tableName(cfg.TablePrefix, "points"),
			series:      tableName(cfg.TablePrefix, "series"),
			resolutions: tableName(cfg.TablePrefix, "resolutions"),
			labels:      tableName(cfg.TablePrefix, "label_sets"),
			rollups:     tableName(cfg.TablePrefix, "rollups"),
			watermarks:  tableName(cfg.TablePrefix, "compaction_watermarks"),
			state:       tableName(cfg.TablePrefix, "store_state"),
		},
		raw:        make(map[rawSeriesKey]*rawSeries),
		hot:        make(map[hotRollupKey]*rollupBucket),
		hotReplace: make(map[hotRollupKey]struct{}),
		coarse:     make(map[coarseRollupKey]*coarseRollup),
	}

	if cfg.DB != nil {
		s.db = cfg.DB
	} else if cfg.Driver == DriverSQLite {
		db, err := sqlitetune.Open(cfg.DSN, sqliteTuneOptions(cfg.SQLite))
		if err != nil {
			return nil, err
		}
		s.db = db
		s.ownedDB = true
	} else {
		db, err := sql.Open(cfg.driverName(), cfg.DSN)
		if err != nil {
			return nil, err
		}
		s.db = db
		s.ownedDB = true
	}

	if cfg.MaxOpenConns > 0 {
		s.db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		s.db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		s.db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := s.db.PingContext(pingCtx); err != nil {
		if s.ownedDB {
			_ = s.db.Close()
		}
		return nil, err
	}

	// Caller-owned pools cannot install a connector hook. Configure one
	// currently acquired connection as a compatibility fallback; callers that
	// rotate or expand their own SQLite pools remain responsible for applying
	// the same settings to those new connections.
	if cfg.Driver == DriverSQLite && cfg.DB != nil {
		if err := s.configureSQLite(ctx, s.db); err != nil {
			if s.ownedDB {
				_ = s.db.Close()
			}
			return nil, err
		}
	}

	// Optional dedicated SQLite read pool. WAL lets readers run concurrently
	// while writes stay serialized on the primary connection. Only meaningful
	// for a file-backed database we own: a shared in-memory database cannot be
	// reopened as a second pool (each connection is a separate memory db), and a
	// caller-supplied *sql.DB owns its own pooling.
	if cfg.Driver == DriverSQLite && cfg.SQLite.ReadPoolSize > 1 && cfg.DB == nil && !isMemoryDSN(cfg.DSN) {
		readDB, err := sqlitetune.Open(cfg.DSN, sqliteTuneOptions(cfg.SQLite))
		if err != nil {
			if s.ownedDB {
				_ = s.db.Close()
			}
			return nil, err
		}
		readDB.SetMaxOpenConns(cfg.SQLite.ReadPoolSize)
		readDB.SetMaxIdleConns(cfg.SQLite.ReadPoolSize)
		if cfg.ConnMaxLifetime > 0 {
			readDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
		}
		if err := readDB.PingContext(pingCtx); err != nil {
			_ = readDB.Close()
			if s.ownedDB {
				_ = s.db.Close()
			}
			return nil, err
		}
		s.readDB = readDB
		s.ownedReadDB = true
	}

	if cfg.AutoMigrate {
		if err := s.Migrate(ctx); err != nil {
			s.closeDBs()
			return nil, err
		}
	}

	return s, nil
}

// reader returns the connection pool to use for read-only queries: the
// dedicated read pool when one is configured, otherwise the primary pool.
//
// reader 返回只读查询应使用的连接池；若配置了专用读池则使用读池，
// 否则使用主连接池。
func (s *Store) reader() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

// closeDBs closes database pools owned by the Store.
//
// closeDBs 关闭由 Store 自己创建并拥有的数据库连接池。
func (s *Store) closeDBs() {
	if s.ownedReadDB && s.readDB != nil {
		_ = s.readDB.Close()
	}
	if s.ownedDB && s.db != nil {
		_ = s.db.Close()
	}
}

// prepareSQLiteConfig fills SQLite defaults and prepares file storage.
//
// prepareSQLiteConfig 补齐 SQLite 默认参数，并确保文件数据库目录存在。
func prepareSQLiteConfig(cfg Config) (Config, error) {
	if cfg.SQLite.BusyTimeout == 0 {
		cfg.SQLite.BusyTimeout = 5 * time.Second
	}
	if cfg.SQLite.CacheSizeKB == 0 {
		cfg.SQLite.CacheSizeKB = 4 * 1024
	}
	// MMapSizeBytes == 0 表示显式禁用 mmap，而不是"未设置"：DefaultConfig 始终
	// 填充 SQLiteOptions，因此这里的 0 是"跳过 mmap"的明确选择，不能被覆盖为
	// 32MB。负数视为非法输入，归一为 0（禁用）。
	if cfg.SQLite.MMapSizeBytes < 0 {
		cfg.SQLite.MMapSizeBytes = 0
	}
	if cfg.SQLite.WALAutoCheckpoint == 0 {
		cfg.SQLite.WALAutoCheckpoint = 4000
	}
	if cfg.SQLite.JournalSizeLimitBytes == 0 {
		cfg.SQLite.JournalSizeLimitBytes = 4 * 1024 * 1024
	}

	if cfg.DB == nil {
		if err := ensureSQLiteDir(cfg.DSN); err != nil {
			return cfg, err
		}
		cfg.DSN = appendSQLiteDSNParam(cfg.DSN, "_busy_timeout", fmt.Sprintf("%d", durationMillis(cfg.SQLite.BusyTimeout)))
		cfg.DSN = appendSQLiteDSNParam(cfg.DSN, "_foreign_keys", "on")
	}
	return cfg, nil
}

// ensureSQLiteDir creates the directory for a file-backed SQLite DSN.
//
// ensureSQLiteDir 根据 SQLite DSN 创建文件数据库所在目录。
func ensureSQLiteDir(dsn string) error {
	path := sqliteFilePath(dsn)
	if path == "" || path == ":memory:" || strings.Contains(dsn, "mode=memory") {
		return nil
	}
	dir := filepath.Dir(filepath.FromSlash(path))
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// sqliteFilePath extracts the filesystem path portion of a SQLite DSN, dropping
// the "file:" scheme prefix and any query string.
//
// sqliteFilePath 从 SQLite DSN 中提取文件路径部分，并去掉 file: 前缀和
// 查询字符串。
func sqliteFilePath(dsn string) string {
	path := strings.TrimPrefix(dsn, "file:")
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return path
}

// isMemoryDSN reports whether the DSN refers to an in-memory SQLite database,
// which cannot be shared across independent connection pools.
//
// isMemoryDSN 判断 DSN 是否指向内存 SQLite 数据库；这种数据库不能在独立
// 连接池之间共享。
func isMemoryDSN(dsn string) bool {
	if strings.Contains(dsn, "mode=memory") {
		return true
	}
	return sqliteFilePath(dsn) == ":memory:"
}

// configureSQLite applies SQLite PRAGMA settings to one connection in a
// caller-owned pool. Store-owned pools use a connector hook so every physical
// connection receives the same configuration.
//
// configureSQLite 对调用方持有的 SQLite 连接池中的一个连接执行 PRAGMA；
// Store 自己持有的连接池会在每个物理连接建立时配置。
func (s *Store) configureSQLite(ctx context.Context, db *sql.DB) error {
	return sqlitetune.Apply(ctx, db, sqliteTuneOptions(s.cfg.SQLite))
}

func sqliteTuneOptions(options SQLiteOptions) sqlitetune.Options {
	return sqlitetune.Options{
		PageSize:              options.PageSize,
		ForeignKeys:           true,
		BusyTimeout:           options.BusyTimeout,
		CacheSizeKB:           options.CacheSizeKB,
		MMapSizeBytes:         options.MMapSizeBytes,
		TempStoreMemory:       options.TempStoreMemory,
		CacheSpill:            true,
		WALAutoCheckpoint:     options.WALAutoCheckpoint,
		JournalSizeLimitBytes: options.JournalSizeLimitBytes,
		Synchronous:           sqliteSynchronousMode(options.PerformanceProfile),
	}
}

// sqliteSynchronousMode returns the synchronous mode for a profile.
//
// sqliteSynchronousMode 根据性能预设返回 SQLite synchronous 模式。
func sqliteSynchronousMode(profile SQLitePerformanceProfile) sqlitetune.SynchronousMode {
	switch profile {
	case SQLiteProfilePerformance:
		return sqlitetune.SynchronousOff
	case SQLiteProfileDurable:
		return sqlitetune.SynchronousFull
	default:
		return sqlitetune.SynchronousNormal
	}
}

func durationMillis(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Millisecond - 1) / time.Millisecond)
}

// Close closes resources owned by the Store.
//
// Close 关闭 Store 拥有的连接池；外部传入的 DB 不会被关闭。
func (s *Store) Close() error {
	// Block new writes, mark the store closed, then persist the remaining partial
	// minute before closing owned pools. A clean shutdown should not discard the
	// last observations merely because their wall-clock minute has not ended.
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	var firstErr error
	flushCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := s.flushAllHotRollups(flushCtx); err != nil {
		firstErr = err
	}
	cancel()
	s.rawMu.Lock()
	s.raw = nil
	s.rawMu.Unlock()
	// Coarse summaries deliberately remain process-local until their window is
	// sealed. The configured behavior drops an unsealed parent on shutdown.
	s.coarseMu.Lock()
	s.coarse = nil
	s.coarseMu.Unlock()
	if s.ownedReadDB && s.readDB != nil {
		if err := s.readDB.Close(); err != nil {
			firstErr = err
		}
	}
	if s.ownedDB && s.db != nil {
		if err := s.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Ping verifies that the database connection is usable.
//
// Ping 检查底层数据库连接是否可用。
func (s *Store) Ping(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	return s.db.PingContext(ctx)
}

// QueryContext executes a raw read query against the Store's read pool.
func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	return s.reader().QueryContext(ctx, query, args...)
}

// ExecContext executes a raw statement against the Store's primary write pool.
func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	return s.db.ExecContext(ctx, query, args...)
}

// ensureOpen verifies that the Store is not closed.
//
// ensureOpen 检查 Store 是否仍处于打开状态。
func (s *Store) ensureOpen() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	if s.db == nil {
		return ErrClosed
	}
	return nil
}

// CreateMetric creates a metric definition.
//
// CreateMetric 创建新的指标定义；同名指标已存在时返回 ErrAlreadyExists。
func (s *Store) CreateMetric(ctx context.Context, def Definition) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	def = def.withDefaults()
	if err := def.Validate(); err != nil {
		return err
	}
	// Fail fast on an existing name so CreateMetric has create-only semantics.
	// The plain INSERT below still enforces this at the database via the
	// primary-key/unique constraint, closing the check-then-insert race.
	if _, err := s.GetMetric(ctx, def.Name); err == nil {
		return fmt.Errorf("%w: metric %q", ErrAlreadyExists, def.Name)
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	metadata, err := encodeMap(def.Metadata)
	if err != nil {
		return err
	}
	now := timeMillis(time.Now())
	_, err = s.db.ExecContext(
		ctx,
		insertDefinitionOnlySQL(s.dialect, s.tables),
		def.Name,
		string(def.Type),
		def.Unit,
		def.Description,
		def.RetentionDays,
		metadata,
		now,
		now,
	)
	if err != nil && isUniqueViolation(err) {
		return fmt.Errorf("%w: metric %q", ErrAlreadyExists, def.Name)
	}
	return err
}

// UpsertMetric inserts a metric definition or, if one with the same name already
// exists, updates its mutable fields (type, unit, description, retention,
// metadata). Use this when you intentionally want create-or-replace semantics;
// use CreateMetric when a duplicate name should be an error.
//
// UpsertMetric 插入指标定义；如果已存在同名定义，则更新其可变字段
// （type、unit、description、retention、metadata）。当你明确需要“创建或替换”
// 语义时使用它；当重复名称应视为错误时使用 CreateMetric。
func (s *Store) UpsertMetric(ctx context.Context, def Definition) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	def = def.withDefaults()
	if err := def.Validate(); err != nil {
		return err
	}
	if def.RetentionDays == 0 {
		// Serialize the zero-retention transition with writes and compaction before
		// changing the definition or removing data left by an enabled definition.
		s.retentionMu.Lock()
		defer s.retentionMu.Unlock()
	}
	metadata, err := encodeMap(def.Metadata)
	if err != nil {
		return err
	}
	now := timeMillis(time.Now())
	_, err = s.db.ExecContext(
		ctx,
		s.dialect.insertDefinitionSQL(s.tables),
		def.Name,
		string(def.Type),
		def.Unit,
		def.Description,
		def.RetentionDays,
		metadata,
		now,
		now,
	)
	if err != nil || def.RetentionDays != 0 {
		return err
	}
	_, err = s.deleteSeries(ctx, Query{MetricName: def.Name})
	return err
}

// GetMetric loads one metric definition by name.
//
// GetMetric 按名称读取指标定义，不存在时返回 ErrNotFound。
func (s *Store) GetMetric(ctx context.Context, name string) (Definition, error) {
	if err := s.ensureOpen(); err != nil {
		return Definition{}, err
	}
	if strings.TrimSpace(name) == "" {
		return Definition{}, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	row := s.reader().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli FROM %s WHERE name = %s`,
		s.tables.definitions, s.dialect.placeholder(1),
	), name)
	def, err := scanDefinition(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Definition{}, ErrNotFound
	}
	return def, err
}

// ListMetrics lists all metric definitions.
//
// ListMetrics 按名称升序列出所有指标定义。
func (s *Store) ListMetrics(ctx context.Context) ([]Definition, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	rows, err := s.reader().QueryContext(ctx, fmt.Sprintf(
		`SELECT name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli FROM %s ORDER BY name ASC`,
		s.tables.definitions,
	))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Definition
	for rows.Next() {
		def, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, rows.Err()
}

// DeleteMetric deletes a metric definition and all of its raw and rollup data.
//
// DeleteMetric 删除指标定义及其所有 rollup 数据。
func (s *Store) DeleteMetric(ctx context.Context, name string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err = s.deleteRollupsForMetricTx(ctx, name, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE name = %s`, s.tables.definitions, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, rawErr := s.deleteRawPoints(name, "", nil)
	_, hotErr := s.deleteHotRollups(name, "", nil, nil)
	_, coarseErr := s.deleteCoarseRollupsMatching(name, "", nil)
	return errors.Join(rawErr, hotErr, coarseErr)
}

// UpdateMetricRetention updates one metric's retention policy without deleting
// its existing data. A value of zero disables subsequent persistence. Negative
// values are invalid.
func (s *Store) UpdateMetricRetention(ctx context.Context, name string, retentionDays int) (Definition, error) {
	if err := s.ensureOpen(); err != nil {
		return Definition{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Definition{}, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if retentionDays < 0 {
		return Definition{}, fmt.Errorf("%w: retention days cannot be negative", ErrInvalidArgument)
	}

	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer func() { _ = tx.Rollback() }()

	updatedAt := timeMillis(time.Now())
	result, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET retention_days = %s, updated_at_milli = %s WHERE name = %s`,
			s.tables.definitions, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3)),
		retentionDays, updatedAt, name,
	)
	if err != nil {
		return Definition{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Definition{}, err
	}
	if affected == 0 {
		return Definition{}, fmt.Errorf("%w: metric %q", ErrNotFound, name)
	}
	if err := tx.Commit(); err != nil {
		return Definition{}, err
	}
	return s.GetMetric(ctx, name)
}

// SetMetricRetention updates one metric's retention policy. A value of zero
// disables persistence for that metric and removes its raw and rollup data.
// Negative values are invalid.
func (s *Store) SetMetricRetention(ctx context.Context, name string, retentionDays int) (Definition, error) {
	if err := s.ensureOpen(); err != nil {
		return Definition{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Definition{}, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if retentionDays < 0 {
		return Definition{}, fmt.Errorf("%w: retention days cannot be negative", ErrInvalidArgument)
	}

	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer func() { _ = tx.Rollback() }()

	updatedAt := timeMillis(time.Now())
	result, err := tx.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET retention_days = %s, updated_at_milli = %s WHERE name = %s`,
			s.tables.definitions, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3)),
		retentionDays, updatedAt, name,
	)
	if err != nil {
		return Definition{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Definition{}, err
	}
	if affected == 0 {
		return Definition{}, fmt.Errorf("%w: metric %q", ErrNotFound, name)
	}
	if retentionDays == 0 {
		if err := s.deleteRollupsForMetricTx(ctx, name, tx); err != nil {
			return Definition{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Definition{}, err
	}
	if retentionDays == 0 {
		_, rawErr := s.deleteRawPoints(name, "", nil)
		_, hotErr := s.deleteHotRollups(name, "", nil, nil)
		_, coarseErr := s.deleteCoarseRollupsMatching(name, "", nil)
		if err := errors.Join(rawErr, hotErr, coarseErr); err != nil {
			return Definition{}, err
		}
	}
	return s.GetMetric(ctx, name)
}

// DeleteMetricDataIfDisabled removes a metric's data only while its retention
// policy is still disabled. It prevents a delayed background cleanup from
// deleting data after an administrator has re-enabled the metric.
func (s *Store) DeleteMetricDataIfDisabled(ctx context.Context, name string) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}

	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var retentionDays int
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT retention_days FROM %s WHERE name = %s`, s.tables.definitions, s.dialect.placeholder(1)),
		name,
	).Scan(&retentionDays); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("%w: metric %q", ErrNotFound, name)
		}
		return false, err
	}
	if retentionDays != 0 {
		return false, nil
	}
	if err := s.deleteRollupsForMetricTx(ctx, name, tx); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	_, rawErr := s.deleteRawPoints(name, "", nil)
	_, hotErr := s.deleteHotRollups(name, "", nil, nil)
	_, coarseErr := s.deleteCoarseRollupsMatching(name, "", nil)
	if err := errors.Join(rawErr, hotErr, coarseErr); err != nil {
		return false, err
	}
	return true, nil
}

// DeleteEntity deletes all raw and rollup data for one entity across every metric.
//
// DeleteEntity 删除某个实体在所有指标下的 rollup 数据。
func (s *Store) DeleteEntity(ctx context.Context, entityID string) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(entityID) == "" {
		return 0, fmt.Errorf("%w: entity id is required", ErrInvalidArgument)
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE series_id IN (SELECT id FROM %s WHERE entity_id = %s)`, s.tables.rollups, s.tables.series, s.dialect.placeholder(1)), entityID)
	if err != nil {
		return 0, err
	}
	rollups, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return rollups, err
	}
	raw, rawErr := s.deleteRawPoints("", entityID, nil)
	hot, hotErr := s.deleteHotRollups("", entityID, nil, nil)
	_, coarseErr := s.deleteCoarseRollupsMatching("", entityID, nil)
	return rollups + raw + hot, errors.Join(rawErr, hotErr, coarseErr)
}

// DeleteSeries deletes raw and rollup data matching a query-shaped series filter.
// MetricName is required; EntityID and Tags are optional, so callers can delete
// one task tag across all agents or one tagged series for a single agent.
//
// DeleteSeries 删除匹配查询式序列过滤条件的 rollup 数据。MetricName 必填；
// EntityID 和 Tags 可选，因此调用方可以删除所有 agent 的某个 task 标签，或删除
// 单个 agent 的某条带标签序列。
func (s *Store) DeleteSeries(ctx context.Context, filter Query) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(filter.MetricName) == "" {
		return 0, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	return s.deleteSeries(ctx, filter)
}

func (s *Store) deleteSeries(ctx context.Context, filter Query) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	args := []any{filter.MetricName}
	parts := []string{"metric_name = " + s.dialect.placeholder(1)}
	if strings.TrimSpace(filter.EntityID) != "" {
		args = append(args, filter.EntityID)
		parts = append(parts, "entity_id = "+s.dialect.placeholder(len(args)))
	}
	for _, k := range sortedKeys(filter.Tags) {
		args = append(args, filter.Tags[k])
		parts = append(parts, s.dialect.jsonExtractEquals("tags", k, s.dialect.placeholder(len(args))))
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE series_id IN (SELECT id FROM %s WHERE %s)`, s.tables.rollups, s.tables.series, strings.Join(parts, " AND ")), args...)
	if err != nil {
		return 0, err
	}
	rollups, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return rollups, err
	}
	raw, rawErr := s.deleteRawPoints(filter.MetricName, filter.EntityID, filter.Tags)
	hot, hotErr := s.deleteHotRollups(filter.MetricName, filter.EntityID, filter.Tags, nil)
	_, coarseErr := s.deleteCoarseRollupsMatching(filter.MetricName, filter.EntityID, filter.Tags)
	return rollups + raw + hot, errors.Join(rawErr, hotErr, coarseErr)
}

// Write stores one metric point.
//
// Write 写入单个采样点。
func (s *Store) Write(ctx context.Context, point Point) error {
	return s.WriteBatch(ctx, []Point{point})
}

// writeBatch writes one chunk of metric points through an executor.
//
// WriteBatch 批量写入采样点，并在大批量分块时保持整体事务性。
func (s *Store) WriteBatch(ctx context.Context, points []Point) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if len(points) == 0 {
		return nil
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	if err := s.ensureOpen(); err != nil {
		return err
	}
	points, err := s.filterDisabledMetricPoints(ctx, points)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		return nil
	}
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	prepared, err := prepareMetricPoints(points)
	if err != nil {
		return err
	}
	rebuild, err := s.writeRawPoints(ctx, prepared)
	if err != nil {
		return err
	}
	return s.writePreparedHotRollups(ctx, prepared, time.Now().UTC(), rebuild)
}

// filterDisabledMetricPoints rejects points without a definition and drops
// points whose definition has zero retention. Definitions are the source of
// truth for a metric's retention and lifecycle, so accepting an unknown name
// would create data that compaction and retention cleanup cannot manage.
func (s *Store) filterDisabledMetricPoints(ctx context.Context, points []Point) ([]Point, error) {
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return nil, err
	}
	definitions := make(map[string]Definition, len(defs))
	for _, def := range defs {
		definitions[def.Name] = def
	}
	filtered := make([]Point, 0, len(points))
	for _, point := range points {
		def, ok := definitions[point.MetricName]
		if !ok {
			return nil, fmt.Errorf("%w: metric %q", ErrNotFound, point.MetricName)
		}
		if def.RetentionDays > 0 {
			filtered = append(filtered, point)
		}
	}
	return filtered, nil
}

// querier is satisfied by both *sql.DB and *sql.Tx, letting read helpers run
// either standalone (on the read pool / primary) or inside an existing
// transaction. Running a read on the owning *sql.Tx is required when the store
// holds a single connection (e.g. SQLite with MaxOpenConns=1): issuing the read
// against the pool instead would block forever waiting for the connection the
// transaction already holds.
//
// querier 同时由 *sql.DB 和 *sql.Tx 满足，使读取辅助函数既能独立执行（走读池
// 或主连接），也能在已有事务中执行。当 Store 只持有单个连接时（例如
// MaxOpenConns=1 的 SQLite），事务内的读取必须走其所属的 *sql.Tx；否则向连接池
// 发起读取会永远等待事务已占用的那个连接，造成死锁。
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Query loads exact raw samples from the fixed ten-minute memory window.
func (s *Store) Query(ctx context.Context, query Query) ([]Point, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	query = query.normalized()
	return s.queryRawPoints(ctx, query)
}

// QueryBatch loads exact raw samples for multiple metrics with one scan of the
// in-memory raw store.
func (s *Store) QueryBatch(ctx context.Context, query BatchQuery) (map[string][]Point, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	return s.queryRawPointsBatch(ctx, query.normalized())
}

// EntityIDs returns distinct entity ids that have exact in-memory samples or
// persisted/hot rollups.
func (s *Store) EntityIDs(ctx context.Context, query Query) ([]string, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	query = query.normalized()
	rawIDs, err := s.rawEntityIDs(ctx, query)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rawIDs))
	for _, entityID := range rawIDs {
		seen[entityID] = struct{}{}
	}
	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()

	args := []any{query.MetricName, bucketStartMillis(query.Start.UnixMilli(), time.Minute.Milliseconds()), query.End.UnixMilli()}
	parts := []string{
		"s.metric_name = " + s.dialect.placeholder(1),
		"r.bucket_milli >= " + s.dialect.placeholder(2),
		"r.bucket_milli <= " + s.dialect.placeholder(3),
	}
	if strings.TrimSpace(query.EntityID) != "" {
		args = append(args, query.EntityID)
		parts = append(parts, "s.entity_id = "+s.dialect.placeholder(len(args)))
	}
	for _, k := range sortedKeys(query.Tags) {
		args = append(args, query.Tags[k])
		parts = append(parts, s.dialect.jsonExtractEquals("s.tags", k, s.dialect.placeholder(len(args))))
	}
	sqlText := fmt.Sprintf(`SELECT DISTINCT s.entity_id FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s d ON d.id = r.resolution_id WHERE %s AND d.resolution_milli = %s ORDER BY s.entity_id ASC`, s.tables.rollups, s.tables.series, s.tables.resolutions, strings.Join(parts, " AND "), s.dialect.placeholder(len(args)+1))
	args = append(args, time.Minute.Milliseconds())
	rows, err := s.reader().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			return nil, err
		}
		if entityID != "" {
			seen[entityID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	hot, err := s.hotRollupRows(query.MetricName, query.EntityID, query.Tags, query.Start, query.End, false)
	if err != nil {
		return nil, err
	}
	for _, row := range hot {
		seen[row.entityID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for entityID := range seen {
		out = append(out, entityID)
	}
	sort.Strings(out)
	return out, nil
}

// Latest loads the newest points for a metric and entity.
//
// Latest 查询某指标和实体的最新采样点。
func (s *Store) Latest(ctx context.Context, metricName, entityID string, limit int) ([]Point, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(metricName) == "" {
		return nil, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(entityID) == "" {
		return nil, fmt.Errorf("%w: entity id is required", ErrInvalidArgument)
	}
	if limit <= 0 {
		limit = 1
	}
	return s.Query(ctx, Query{
		MetricName: metricName,
		EntityID:   entityID,
		Start:      time.Unix(0, 0),
		End:        time.Now().UTC(),
		Order:      OrderDesc,
		Limit:      limit,
	})
}

// LatestBefore returns the newest raw point or rollup representative before an
// exclusive boundary. The active in-memory minute is included.
func (s *Store) LatestBefore(ctx context.Context, metricName, entityID string, before time.Time) (Point, bool, error) {
	if err := s.ensureOpen(); err != nil {
		return Point{}, false, err
	}
	if strings.TrimSpace(metricName) == "" {
		return Point{}, false, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(entityID) == "" {
		return Point{}, false, fmt.Errorf("%w: entity id is required", ErrInvalidArgument)
	}
	if before.IsZero() {
		return Point{}, false, fmt.Errorf("%w: before time is required", ErrInvalidArgument)
	}

	latestRows, err := s.Query(ctx, Query{MetricName: metricName, EntityID: entityID, Start: time.Unix(0, 0), End: before.Add(-time.Nanosecond), Order: OrderDesc, Limit: 1})
	if err != nil {
		return Point{}, false, err
	}
	var latest Point
	found := len(latestRows) > 0
	if found {
		latest = latestRows[0]
	}
	rollup, rollupFound, err := s.latestRollupBefore(ctx, metricName, entityID, before)
	if err != nil {
		return Point{}, false, err
	}
	if rollupFound && (!found || rollup.Timestamp.After(latest.Timestamp)) {
		latest, found = rollup, true
	}
	return latest, found, nil
}

// Aggregate computes bucketed aggregates from retained raw samples.
func (s *Store) Aggregate(ctx context.Context, query AggregateQuery) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	rawQuery := query.Query
	rawQuery.Limit = 0
	rawQuery.Offset = 0
	points, err := s.Query(ctx, rawQuery)
	if err != nil {
		return nil, err
	}
	buckets, err := AggregatePoints(points, query)
	if err != nil {
		return nil, err
	}
	return pageBuckets(buckets, query.BucketLimit, query.BucketOffset), nil
}

// pageBuckets applies bucket-level paging to an ordered slice of aggregate
// points. offset buckets are skipped from the front; at most limit buckets are
// returned (limit <= 0 means no limit).
//
// pageBuckets 对有序 AggregatePoint 切片应用桶级分页。它会从前面跳过 offset
// 个桶，并最多返回 limit 个桶（limit <= 0 表示不限制）。
func pageBuckets(buckets []AggregatePoint, limit, offset int) []AggregatePoint {
	if offset > 0 {
		if offset >= len(buckets) {
			return []AggregatePoint{}
		}
		buckets = buckets[offset:]
	}
	if limit > 0 && limit < len(buckets) {
		buckets = buckets[:limit]
	}
	return buckets
}

// Stats computes summary statistics from persisted and active minute summaries.
func (s *Store) Stats(ctx context.Context, query Query) (Stats, error) {
	if err := s.ensureOpen(); err != nil {
		return Stats{}, err
	}
	if err := query.Validate(); err != nil {
		return Stats{}, err
	}
	query = query.normalized()
	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()
	rows, err := s.scanRollupRowsBetween(ctx, query.MetricName, query.EntityID, query.Tags,
		time.Minute.Milliseconds(), bucketStartMillis(query.Start.UnixMilli(), time.Minute.Milliseconds()), query.End.UnixMilli(), true)
	if err != nil {
		return Stats{}, err
	}
	hot, err := s.hotRollupRows(query.MetricName, query.EntityID, query.Tags, query.Start, query.End, true)
	if err != nil {
		return Stats{}, err
	}
	rows = append(rows, hot...)
	if len(rows) == 0 {
		// No samples in range. Disambiguate from a non-existent metric so the
		// caller can tell "empty window" apart from "unknown metric".
		if _, gerr := s.GetMetric(ctx, query.MetricName); errors.Is(gerr, ErrNotFound) {
			return Stats{}, ErrNotFound
		} else if gerr != nil {
			return Stats{}, gerr
		}
		return Stats{}, ErrNoData
	}
	bucket := newRollupBucket(s.cfg.RollupPolicy.compression())
	for _, row := range rows {
		bucket.mergeStored(row.bucketData)
	}
	value := func(aggregation Aggregation) float64 {
		result, _ := bucket.value(aggregation)
		return result
	}
	representatives := representativePoints(query.MetricName, rows)
	sort.Slice(representatives, func(i, j int) bool { return representatives[i].Timestamp.Before(representatives[j].Timestamp) })
	return Stats{
		Count: int(bucket.count), Min: bucket.min, Max: bucket.max,
		Avg: value(AggAvg), Sum: bucket.sum,
		P50: value(AggP50), P95: value(AggP95), P99: value(AggP99),
		First: bucket.firstVal, Last: bucket.lastVal,
		Rate:  valueFromPoints(representatives, AggRate),
		Start: fromMillis(bucket.firstTS), End: fromMillis(bucket.lastTS),
		StdDev: value(AggStdDev),
	}, nil
}

func valueFromPoints(points []Point, aggregation Aggregation) float64 {
	value, _ := aggregateValue(points, aggregation)
	return value
}

// DeleteBefore deletes retained raw points and summaries older than a cutoff
// across every resolution, including matching active in-memory minute buckets.
func (s *Store) DeleteBefore(ctx context.Context, metricName string, before time.Time) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, fmt.Errorf("%w: before time is required", ErrInvalidArgument)
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	before = before.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var deleted int64
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		args := []any{tier.Interval.Milliseconds(), bucketStartMillis(before.UnixMilli(), tier.Interval.Milliseconds())}
		where := "resolution_id IN (SELECT id FROM " + s.tables.resolutions + " WHERE resolution_milli = " + s.dialect.placeholder(1) + ") AND bucket_milli < " + s.dialect.placeholder(2)
		if strings.TrimSpace(metricName) != "" {
			args = append(args, metricName)
			where += " AND series_id IN (SELECT id FROM " + s.tables.series + " WHERE metric_name = " + s.dialect.placeholder(3) + ")"
		}
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tables.rollups, where), args...)
		if err != nil {
			return 0, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	raw := s.deleteRawBefore(metricName, before.UnixMilli())
	hotCutoff := fromMillis(bucketStartMillis(before.UnixMilli(), time.Minute.Milliseconds()))
	hot, hotErr := s.deleteHotRollups(metricName, "", nil, &hotCutoff)
	return deleted + raw + hot, hotErr
}

// CleanupExpired deletes data past each metric's effective retention in one
// transaction. Definitions that share a tier/cutoff share one DELETE, keeping
// the scheduled cleanup out of the report write path and avoiding per-metric
// index churn.
func (s *Store) CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()

	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	type cleanupGroupKey struct {
		interval    time.Duration
		beforeMilli int64
		all         bool
	}
	groups := make(map[cleanupGroupKey][]string)
	disabled := make([]string, 0)
	now = now.UTC()
	for _, def := range defs {
		if def.RetentionDays == 0 {
			disabled = append(disabled, def.Name)
			continue
		}
		policy := s.cfg.RollupPolicy.withMetricRetention(time.Duration(def.RetentionDays) * 24 * time.Hour)
		retained := make(map[time.Duration]time.Duration, len(policy.Tiers))
		for _, tier := range policy.Tiers {
			retained[tier.Interval] = tier.Retention
		}
		for _, tier := range s.cfg.RollupPolicy.Tiers {
			retention, keep := retained[tier.Interval]
			if !keep {
				key := cleanupGroupKey{interval: tier.Interval, all: true}
				groups[key] = append(groups[key], def.Name)
				continue
			}
			before := bucketStartMillis(now.Add(-retention).UnixMilli(), tier.Interval.Milliseconds())
			key := cleanupGroupKey{interval: tier.Interval, beforeMilli: before}
			groups[key] = append(groups[key], def.Name)
		}
	}

	var total int64
	if len(disabled) > 0 {
		deleted, err := s.deleteRollupsForMetricsTx(ctx, disabled, tx)
		if err != nil {
			return total, err
		}
		total += deleted
	}
	keys := make([]cleanupGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].interval != keys[j].interval {
			return keys[i].interval < keys[j].interval
		}
		if keys[i].all != keys[j].all {
			return keys[i].all
		}
		return keys[i].beforeMilli < keys[j].beforeMilli
	})
	for _, key := range keys {
		deleted, err := s.deleteRollupGroupTx(ctx, groups[key], key.interval, key.beforeMilli, key.all, tx)
		if err != nil {
			return total, err
		}
		total += deleted
	}
	if err := tx.Commit(); err != nil {
		return total, err
	}

	for _, name := range disabled {
		raw, rawErr := s.deleteRawPoints(name, "", nil)
		hot, hotErr := s.deleteHotRollups(name, "", nil, nil)
		total += raw + hot
		if rawErr != nil {
			return total, rawErr
		}
		if hotErr != nil {
			return total, hotErr
		}
		s.deleteCoarseRollups(name)
	}
	return total, nil
}

func (s *Store) deleteRollupsForMetricsTx(ctx context.Context, names []string, tx *sql.Tx) (int64, error) {
	if len(names) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, name := range names {
		placeholders[i] = s.dialect.placeholder(i + 1)
		args[i] = name
	}
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE series_id IN (SELECT id FROM %s WHERE metric_name IN (%s))", s.tables.rollups, s.tables.series, strings.Join(placeholders, ", "))
	res, err := tx.ExecContext(ctx, sqlText, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) deleteRollupGroupTx(ctx context.Context, names []string, interval time.Duration, beforeMilli int64, all bool, tx *sql.Tx) (int64, error) {
	if len(names) == 0 {
		return 0, nil
	}
	args := []any{interval.Milliseconds()}
	placeholders := make([]string, len(names))
	for i, name := range names {
		args = append(args, name)
		placeholders[i] = s.dialect.placeholder(len(args))
	}
	where := "resolution_id IN (SELECT id FROM " + s.tables.resolutions + " WHERE resolution_milli = " + s.dialect.placeholder(1) + ")" +
		" AND series_id IN (SELECT id FROM " + s.tables.series + " WHERE metric_name IN (" + strings.Join(placeholders, ", ") + "))"
	if !all {
		args = append(args, beforeMilli)
		where += " AND bucket_milli < " + s.dialect.placeholder(len(args))
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", s.tables.rollups, where), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// sortedKeys returns sorted map keys.
//
// sortedKeys 返回 map 的有序 key 列表，用于生成稳定 SQL。
func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scanDefinition scans a metric definition from one row.
//
// scanDefinition 从一行查询结果扫描指标定义。
func scanDefinition(scanner interface{ Scan(dest ...any) error }) (Definition, error) {
	var def Definition
	var typ string
	var rawMetadata any
	var created, updated int64
	if err := scanner.Scan(&def.Name, &typ, &def.Unit, &def.Description, &def.RetentionDays, &rawMetadata, &created, &updated); err != nil {
		return Definition{}, err
	}
	metadata, err := decodeMap(rawMetadata)
	if err != nil {
		return Definition{}, err
	}
	def.Type = MetricType(typ)
	def.Metadata = metadata
	def.CreatedAt = fromMillis(created)
	def.UpdatedAt = fromMillis(updated)
	return def, nil
}

// sortedPoints returns points ordered by timestamp.
//
// sortedPoints 返回按时间排序的点；若输入已排序则直接复用。
func sortedPoints(points []Point) []Point {
	// Most store queries already return time-ordered representatives. Detecting
	// that avoids a copy and sort allocation on the common path.
	if isTimeSorted(points) {
		return points
	}
	out := make([]Point, len(points))
	copy(out, points)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

// isTimeSorted reports whether points are already time sorted.
//
// isTimeSorted 判断点序列是否已按时间升序排列。
func isTimeSorted(points []Point) bool {
	for i := 1; i < len(points); i++ {
		if points[i].Timestamp.Before(points[i-1].Timestamp) {
			return false
		}
	}
	return true
}

// isUniqueViolation reports whether err is a unique/primary-key constraint
// violation. It matches on driver error text so the package stays free of
// driver-specific error type imports; this is a best-effort backstop behind the
// explicit existence check in CreateMetric.
//
// isUniqueViolation 判断 err 是否为唯一约束或主键约束冲突。它通过驱动错误文本
// 匹配，从而让 package 不需要导入驱动专用错误类型；这是 CreateMetric 中显式
// 存在性检查之后的尽力兜底。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unique constraint"): // sqlite, postgres
		return true
	case strings.Contains(msg, "duplicate entry"): // mysql
		return true
	case strings.Contains(msg, "duplicate key"): // postgres
		return true
	case strings.Contains(msg, "constraint failed"): // sqlite variants
		return true
	default:
		return false
	}
}
