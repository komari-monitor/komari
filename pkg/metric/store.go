package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
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
	if cfg.DefaultRetentionDays == 0 {
		cfg.DefaultRetentionDays = 90
	}
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
			rollups:     tableName(cfg.TablePrefix, "rollups"),
		},
	}

	if cfg.DB != nil {
		s.db = cfg.DB
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

	if cfg.Driver == DriverSQLite {
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
		readDB, err := sql.Open(cfg.driverName(), cfg.DSN)
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
		if err := s.configureSQLite(ctx, readDB); err != nil {
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
		cfg.SQLite.CacheSizeKB = 64 * 1024
	}
	if cfg.SQLite.MMapSizeBytes == 0 {
		cfg.SQLite.MMapSizeBytes = 256 * 1024 * 1024
	}
	if cfg.SQLite.WALAutoCheckpoint == 0 {
		cfg.SQLite.WALAutoCheckpoint = 1000
	}

	if cfg.DB == nil {
		if err := ensureSQLiteDir(cfg.DSN); err != nil {
			return cfg, err
		}
		cfg.DSN = appendSQLiteDSNParam(cfg.DSN, "_busy_timeout", fmt.Sprintf("%d", durationMillis(cfg.SQLite.BusyTimeout)))
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

// configureSQLite applies SQLite PRAGMA settings.
//
// configureSQLite 对 SQLite 连接执行 WAL、busy_timeout、cache 等 PRAGMA。
func (s *Store) configureSQLite(ctx context.Context, db *sql.DB) error {
	if s.cfg.SQLite.PageSize > 0 {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA page_size = %d", s.cfg.SQLite.PageSize)); err != nil {
			return err
		}
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		sqliteSynchronousPragma(s.cfg.SQLite.PerformanceProfile),
		fmt.Sprintf("PRAGMA busy_timeout = %d", durationMillis(s.cfg.SQLite.BusyTimeout)),
		fmt.Sprintf("PRAGMA cache_size = -%d", s.cfg.SQLite.CacheSizeKB),
		fmt.Sprintf("PRAGMA mmap_size = %d", s.cfg.SQLite.MMapSizeBytes),
		fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", s.cfg.SQLite.WALAutoCheckpoint),
	}
	if s.cfg.SQLite.TempStoreMemory {
		pragmas = append(pragmas, "PRAGMA temp_store = MEMORY")
	}

	for _, pragma := range pragmas {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}

// sqliteSynchronousPragma returns the synchronous PRAGMA for a profile.
//
// sqliteSynchronousPragma 根据性能预设返回 SQLite synchronous PRAGMA。
func sqliteSynchronousPragma(profile SQLitePerformanceProfile) string {
	switch profile {
	case SQLiteProfilePerformance:
		return "PRAGMA synchronous = OFF"
	case SQLiteProfileDurable:
		return "PRAGMA synchronous = FULL"
	default:
		return "PRAGMA synchronous = NORMAL"
	}
}

// durationMillis converts a duration to rounded-up milliseconds.
//
// durationMillis 将 duration 转换为向上取整的毫秒数。
func durationMillis(d time.Duration) int {
	return int(math.Ceil(float64(d) / float64(time.Millisecond)))
}

// Close closes resources owned by the Store.
//
// Close 关闭 Store 拥有的连接池；外部传入的 DB 不会被关闭。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
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
	def = def.withDefaults(s.cfg.DefaultRetentionDays)
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
	now := time.Now().UTC().UnixNano()
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
	def = def.withDefaults(s.cfg.DefaultRetentionDays)
	if err := def.Validate(); err != nil {
		return err
	}
	metadata, err := encodeMap(def.Metadata)
	if err != nil {
		return err
	}
	now := time.Now().UTC().UnixNano()
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
		`SELECT name, type, unit, description, retention_days, metadata, created_at, updated_at FROM %s WHERE name = %s`,
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
		`SELECT name, type, unit, description, retention_days, metadata, created_at, updated_at FROM %s ORDER BY name ASC`,
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
// DeleteMetric 删除指标定义及其所有原始点和 rollup 数据。
func (s *Store) DeleteMetric(ctx context.Context, name string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE metric_name = %s`, s.tables.rollups, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE metric_name = %s`, s.tables.points, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE name = %s`, s.tables.definitions, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteEntity deletes all raw and rollup data for one entity across every metric.
//
// DeleteEntity 删除某个实体在所有指标下的原始点和 rollup 数据。
func (s *Store) DeleteEntity(ctx context.Context, entityID string) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(entityID) == "" {
		return 0, fmt.Errorf("%w: entity id is required", ErrInvalidArgument)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var total int64
	for _, table := range []string{s.tables.points, s.tables.rollups} {
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE entity_id = %s`, table, s.dialect.placeholder(1)), entityID)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	if err := tx.Commit(); err != nil {
		return total, err
	}
	return total, nil
}

// DeleteSeries deletes raw and rollup data matching a query-shaped series filter.
// MetricName is required; EntityID and Tags are optional, so callers can delete
// one task tag across all agents or one tagged series for a single agent.
//
// DeleteSeries 删除匹配查询式序列过滤条件的原始点和 rollup 数据。MetricName 必填；
// EntityID 和 Tags 可选，因此调用方可以删除所有 agent 的某个 task 标签，或删除
// 单个 agent 的某条带标签序列。
func (s *Store) DeleteSeries(ctx context.Context, filter Query) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(filter.MetricName) == "" {
		return 0, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var total int64
	for _, table := range []string{s.tables.points, s.tables.rollups} {
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
		res, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, table, strings.Join(parts, " AND ")), args...)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	if err := tx.Commit(); err != nil {
		return total, err
	}
	return total, nil
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
	const batchSize = 1000
	// A single chunk is one statement; send it directly. Multiple chunks are
	// wrapped in one transaction so the batch is all-or-nothing rather than
	// leaving earlier chunks committed when a later one fails.
	if len(points) <= batchSize {
		return s.writeBatch(ctx, s.db, points)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < len(points); i += batchSize {
		end := i + batchSize
		if end > len(points) {
			end = len(points)
		}
		if err := s.writeBatch(ctx, tx, points[i:end]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// execer is satisfied by both *sql.DB and *sql.Tx, letting writeBatch run either
// standalone or inside the batch transaction.
//
// execer 同时由 *sql.DB 和 *sql.Tx 满足，使 writeBatch 既可独立执行，
// 也可在批量事务中执行。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
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

// writeBatch writes one chunk of metric points through an executor.
//
// writeBatch 使用给定执行器写入一批采样点。
func (s *Store) writeBatch(ctx context.Context, ex execer, points []Point) error {
	args := make([]any, 0, len(points)*8)
	now := time.Now().UTC().UnixNano()
	for i, point := range points {
		if err := point.Validate(); err != nil {
			return fmt.Errorf("point %d (metric %q, entity %q): %w", i, point.MetricName, point.EntityID, err)
		}
		point = point.normalized()
		// tagsFingerprint returns the canonical tag JSON too, so the tags column
		// reuses it rather than encoding the map a second time.
		tagsHash, tags, err := tagsFingerprint(point.Tags)
		if err != nil {
			return err
		}
		labels, err := encodeMap(point.Labels)
		if err != nil {
			return err
		}
		args = append(args, point.MetricName, point.EntityID, tagsHash, point.Timestamp.UnixNano(), point.Value, tags, labels, now)
	}
	_, err := ex.ExecContext(ctx, s.dialect.upsertPointSQL(s.tables, len(points)), args...)
	return err
}

// Query loads raw metric points matching a query.
//
// Query 按条件查询原始采样点。
func (s *Store) Query(ctx context.Context, query Query) ([]Point, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	query = query.normalized()
	where, args := s.buildWhere(query)
	order := "ASC"
	if query.Order == OrderDesc {
		order = "DESC"
	}

	sqlText := fmt.Sprintf(`SELECT metric_name, entity_id, ts_nano, value, tags, labels FROM %s WHERE %s ORDER BY ts_nano %s`,
		s.tables.points, where, order)
	// Tag filtering is now pushed into buildWhere, so paging always runs in SQL.
	if query.Limit > 0 {
		args = append(args, query.Limit)
		sqlText += " LIMIT " + s.dialect.placeholder(len(args))
	}
	if query.Offset > 0 {
		args = append(args, query.Offset)
		sqlText += " OFFSET " + s.dialect.placeholder(len(args))
	}

	rows, err := s.reader().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		var ts int64
		var rawTags, rawLabels any
		if err := rows.Scan(&p.MetricName, &p.EntityID, &ts, &p.Value, &rawTags, &rawLabels); err != nil {
			return nil, err
		}
		p.Timestamp = time.Unix(0, ts).UTC()
		p.Tags, err = decodeMap(rawTags)
		if err != nil {
			return nil, err
		}
		p.Labels, err = decodeMap(rawLabels)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
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
	// Dedicated query rather than a full-range Query: no synthetic time bounds,
	// and the index on (metric_name, entity_id, ts_nano) serves the ORDER BY.
	sqlText := fmt.Sprintf(
		`SELECT metric_name, entity_id, ts_nano, value, tags, labels FROM %s WHERE metric_name = %s AND entity_id = %s ORDER BY ts_nano DESC LIMIT %s`,
		s.tables.points, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
	)
	rows, err := s.reader().QueryContext(ctx, sqlText, metricName, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		var ts int64
		var rawTags, rawLabels any
		if err := rows.Scan(&p.MetricName, &p.EntityID, &ts, &p.Value, &rawTags, &rawLabels); err != nil {
			return nil, err
		}
		p.Timestamp = time.Unix(0, ts).UTC()
		p.Tags, err = decodeMap(rawTags)
		if err != nil {
			return nil, err
		}
		p.Labels, err = decodeMap(rawLabels)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Aggregate computes bucketed aggregates from raw points.
//
// Aggregate 对原始点执行分桶聚合，能下推到 SQL 的聚合会优先下推。
func (s *Store) Aggregate(ctx context.Context, query AggregateQuery) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	// Push simple reductions (avg/min/max/sum/count) down to SQL via GROUP BY on
	// a time bucket so large ranges don't pull every raw point into memory.
	// Percentiles, first/last, rate and empty-bucket filling need the ordered
	// raw series, so those fall back to the in-memory aggregator.
	if valueExpr, ok := sqlAggValueExpr(s.cfg.Driver, query.Aggregation); ok && !query.FillEmpty {
		return s.aggregateInSQL(ctx, query, valueExpr)
	}
	// In-memory fallback. Strip the embedded raw-point Limit/Offset so the full
	// series feeds the aggregator; paging is then applied per bucket, matching
	// the SQL pushdown path's BucketLimit/BucketOffset semantics.
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
// returned (limit <= 0 means no limit). It mirrors the SQL LIMIT/OFFSET applied
// in aggregateInSQL so both paths page identically.
//
// pageBuckets 对有序 AggregatePoint 切片应用桶级分页。它会从前面跳过 offset
// 个桶，并最多返回 limit 个桶（limit <= 0 表示不限制）。它镜像 aggregateInSQL
// 中应用的 SQL LIMIT/OFFSET，使两条路径的分页行为一致。
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

// aggregateInSQL computes a bucketed aggregate in the database.
//
// aggregateInSQL 使用数据库 GROUP BY 执行可下推的聚合查询。
func (s *Store) aggregateInSQL(ctx context.Context, query AggregateQuery, valueExpr string) ([]AggregatePoint, error) {
	q := query.Query.normalized()
	where, args := s.buildWhere(q)
	interval := query.Interval.Nanoseconds()
	// interval is a trusted int64 (validated > 0); inline it so bucket math and
	// GROUP BY/ORDER BY reference the same expression without extra binds.
	//
	// Note: this bucket expression is a computed (non-sargable) value, so the
	// GROUP BY cannot be served directly by the (metric_name, entity_id,
	// ts_nano) index — the database still range-scans the rows selected by the
	// WHERE clause (which IS index-served) and groups them on the fly. That is
	// fine for typical windows; for very large ranges the cost is the scan, not
	// the grouping. We keep the raw-timestamp index rather than materializing a
	// bucket column so writes stay cheap and the bucket size can vary per query.
	bucketExpr := fmt.Sprintf("(ts_nano - ((ts_nano %% %d) + %d) %% %d)", interval, interval, interval)
	sqlText := fmt.Sprintf(
		`SELECT %s AS bucket, %s AS agg_value, COUNT(*) AS agg_count FROM %s WHERE %s GROUP BY bucket ORDER BY bucket ASC`,
		bucketExpr, valueExpr, s.tables.points, where,
	)
	// Page over aggregate buckets (BucketLimit/BucketOffset), not raw points.
	if query.BucketLimit > 0 {
		args = append(args, query.BucketLimit)
		sqlText += " LIMIT " + s.dialect.placeholder(len(args))
	}
	if query.BucketOffset > 0 {
		args = append(args, query.BucketOffset)
		sqlText += " OFFSET " + s.dialect.placeholder(len(args))
	}

	rows, err := s.reader().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AggregatePoint, 0)
	for rows.Next() {
		var bucket int64
		var value float64
		var count int
		if err := rows.Scan(&bucket, &value, &count); err != nil {
			return nil, err
		}
		out = append(out, AggregatePoint{
			MetricName: q.MetricName,
			EntityID:   q.EntityID,
			Bucket:     time.Unix(0, bucket).UTC(),
			Value:      value,
			Count:      count,
		})
	}
	return out, rows.Err()
}

// Stats stores or computes summary statistics for a point series.
//
// Stats 查询原始点并计算统计摘要。
func (s *Store) Stats(ctx context.Context, query Query) (Stats, error) {
	points, err := s.Query(ctx, query)
	if err != nil {
		return Stats{}, err
	}
	stats, err := CalculateStats(points)
	if errors.Is(err, ErrNoData) {
		// No samples in range. Disambiguate from a non-existent metric so the
		// caller can tell "empty window" apart from "unknown metric".
		if _, gerr := s.GetMetric(ctx, query.MetricName); errors.Is(gerr, ErrNotFound) {
			return Stats{}, ErrNotFound
		} else if gerr != nil {
			return Stats{}, gerr
		}
	}
	return stats, err
}

// DeleteBefore deletes raw points older than a cutoff.
//
// DeleteBefore 删除指定时间之前的原始点，可按指标名限定范围。
func (s *Store) DeleteBefore(ctx context.Context, metricName string, before time.Time) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if before.IsZero() {
		return 0, fmt.Errorf("%w: before time is required", ErrInvalidArgument)
	}
	args := []any{before.UTC().UnixNano()}
	where := "ts_nano < " + s.dialect.placeholder(1)
	if strings.TrimSpace(metricName) != "" {
		args = append(args, metricName)
		where += " AND metric_name = " + s.dialect.placeholder(2)
	}
	res, err := s.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s`, s.tables.points, where), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CleanupExpired deletes expired raw points for every metric.
//
// CleanupExpired 根据各指标保留天数清理过期原始点。
func (s *Store) CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, def := range defs {
		retentionDays := def.RetentionDays
		if retentionDays <= 0 {
			retentionDays = s.cfg.DefaultRetentionDays
		}
		deleted, err := s.DeleteBefore(ctx, def.Name, now.AddDate(0, 0, -retentionDays))
		if err != nil {
			return total, err
		}
		total += deleted
	}
	return total, nil
}

// buildWhere renders the WHERE clause and arguments for a raw query.
//
// buildWhere 根据 Query 构造 SQL WHERE 条件和参数。
func (s *Store) buildWhere(query Query) (string, []any) {
	args := []any{query.MetricName, query.Start.UnixNano(), query.End.UnixNano()}
	parts := []string{
		"metric_name = " + s.dialect.placeholder(1),
		"ts_nano >= " + s.dialect.placeholder(2),
		"ts_nano <= " + s.dialect.placeholder(3),
	}
	if strings.TrimSpace(query.EntityID) != "" {
		args = append(args, query.EntityID)
		parts = append(parts, "entity_id = "+s.dialect.placeholder(len(args)))
	}
	// Push tag filtering down into SQL via the dialect's JSON accessor so that
	// LIMIT/OFFSET can also be applied by the database instead of pulling every
	// matching row into memory. Keys are sorted for deterministic SQL.
	for _, k := range sortedKeys(query.Tags) {
		args = append(args, query.Tags[k])
		parts = append(parts, s.dialect.jsonExtractEquals("tags", k, s.dialect.placeholder(len(args))))
	}
	return strings.Join(parts, " AND "), args
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
	def.CreatedAt = time.Unix(0, created).UTC()
	def.UpdatedAt = time.Unix(0, updated).UTC()
	return def, nil
}

// sortedPoints returns points ordered by timestamp.
//
// sortedPoints 返回按时间排序的点；若输入已排序则直接复用。
func sortedPoints(points []Point) []Point {
	// Callers frequently pass series that are already time-ordered (the SQL
	// queries ORDER BY ts_nano). Detecting that lets us return the input as-is
	// and skip the copy + sort allocation on the common path.
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
