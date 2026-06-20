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

type Store struct {
	cfg         Config
	db          *sql.DB
	readDB      *sql.DB
	ownedDB     bool
	ownedReadDB bool
	dialect     dialect
	tables      tables
	mu          sync.RWMutex
	closed      bool
}

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
func (s *Store) reader() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

func (s *Store) closeDBs() {
	if s.ownedReadDB && s.readDB != nil {
		_ = s.readDB.Close()
	}
	if s.ownedDB && s.db != nil {
		_ = s.db.Close()
	}
}

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
func sqliteFilePath(dsn string) string {
	path := strings.TrimPrefix(dsn, "file:")
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return path
}

// isMemoryDSN reports whether the DSN refers to an in-memory SQLite database,
// which cannot be shared across independent connection pools.
func isMemoryDSN(dsn string) bool {
	if strings.Contains(dsn, "mode=memory") {
		return true
	}
	return sqliteFilePath(dsn) == ":memory:"
}

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

func durationMillis(d time.Duration) int {
	return int(math.Ceil(float64(d) / float64(time.Millisecond)))
}

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

func (s *Store) Ping(ctx context.Context) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	return s.db.PingContext(ctx)
}

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

	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE metric_name = %s`, s.tables.points, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE name = %s`, s.tables.definitions, s.dialect.placeholder(1)), name); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Write(ctx context.Context, point Point) error {
	return s.WriteBatch(ctx, []Point{point})
}

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
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) writeBatch(ctx context.Context, ex execer, points []Point) error {
	args := make([]any, 0, len(points)*7)
	now := time.Now().UTC().UnixNano()
	for i, point := range points {
		if err := point.Validate(); err != nil {
			return fmt.Errorf("point %d (metric %q, entity %q): %w", i, point.MetricName, point.EntityID, err)
		}
		point = point.normalized()
		tags, err := encodeMap(point.Tags)
		if err != nil {
			return err
		}
		labels, err := encodeMap(point.Labels)
		if err != nil {
			return err
		}
		args = append(args, point.MetricName, point.EntityID, point.Timestamp.UnixNano(), point.Value, tags, labels, now)
	}
	_, err := ex.ExecContext(ctx, s.dialect.upsertPointSQL(s.tables, len(points)), args...)
	return err
}

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
