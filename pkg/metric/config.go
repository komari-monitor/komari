package metric

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Config describes database, pooling, migration, retention, and rollup settings.
//
// Config 描述 Store 的数据库后端、连接池、迁移、保留和 rollup 配置。
type Config struct {
	// Driver selects the database backend.
	//
	// Driver 选择数据库后端。
	Driver Driver
	// DSN is the database connection string when DB is not supplied.
	//
	// DSN 是未传入 DB 时使用的数据库连接字符串。
	DSN string

	// DB can be supplied by the host app when it owns the connection pool.
	// When DB is set, DSN is ignored and Close leaves the supplied DB open.
	//
	// 当宿主程序自己管理连接池时可以传入 DB；设置 DB 后会忽略 DSN，
	// Close 也不会关闭这个外部传入的连接池。
	DB *sql.DB

	// TablePrefix prefixes every table managed by the store.
	//
	// TablePrefix 是 Store 管理的所有表名前缀。
	TablePrefix string
	// DefaultRetentionDays is used when a metric definition omits retention.
	//
	// DefaultRetentionDays 在指标定义未设置保留时间时使用。
	DefaultRetentionDays int
	// AutoMigrate controls whether Open creates or updates the schema.
	//
	// AutoMigrate 控制 Open 是否创建或更新表结构。
	AutoMigrate bool
	// MaxOpenConns sets the primary pool's maximum open connections.
	//
	// MaxOpenConns 设置主连接池的最大打开连接数。
	MaxOpenConns int
	// MaxIdleConns sets the primary pool's maximum idle connections.
	//
	// MaxIdleConns 设置主连接池的最大空闲连接数。
	MaxIdleConns int
	// ConnMaxLifetime sets the maximum lifetime for pooled connections.
	//
	// ConnMaxLifetime 设置池化连接的最大生命周期。
	ConnMaxLifetime time.Duration
	// ConnectTimeout bounds the initial database ping in Open.
	//
	// ConnectTimeout 限制 Open 中首次 ping 数据库的时间。
	ConnectTimeout time.Duration
	// SQLite holds SQLite-specific options.
	//
	// SQLite 保存 SQLite 专用选项。
	SQLite SQLiteOptions

	// RollupPolicy configures downsampling tiers and tiered retention. When it
	// defines tiers, Store.Compact materializes them and enforces every
	// retention window. The zero value disables rollups (raw points only).
	//
	// RollupPolicy 配置降采样层级和分层保留时间；定义层级后，
	// Store.Compact 会生成 rollup 并执行保留策略。零值表示禁用 rollup。
	RollupPolicy RollupPolicy
}

// SQLiteOptions contains SQLite-specific performance and read-concurrency settings.
//
// SQLiteOptions 保存 SQLite 专用的性能和并发读取选项。
type SQLiteOptions struct {
	// PerformanceProfile selects the SQLite synchronous durability preset.
	//
	// PerformanceProfile 选择 SQLite synchronous 持久化预设。
	PerformanceProfile SQLitePerformanceProfile
	// BusyTimeout is applied to SQLite busy_timeout.
	//
	// BusyTimeout 会应用到 SQLite busy_timeout。
	BusyTimeout time.Duration
	// CacheSizeKB sets the SQLite page cache size in KB.
	//
	// CacheSizeKB 设置 SQLite 页缓存大小，单位为 KB。
	CacheSizeKB int
	// PageSize sets SQLite page_size when positive.
	//
	// PageSize 为正数时设置 SQLite page_size。
	PageSize int
	// TempStoreMemory enables memory-backed temporary storage.
	//
	// TempStoreMemory 启用基于内存的临时存储。
	TempStoreMemory bool
	// MMapSizeBytes sets SQLite mmap_size in bytes.
	//
	// MMapSizeBytes 设置 SQLite mmap_size，单位为字节。
	MMapSizeBytes int64
	// WALAutoCheckpoint sets the SQLite WAL auto-checkpoint page count.
	//
	// WALAutoCheckpoint 设置 SQLite WAL 自动 checkpoint 页数。
	WALAutoCheckpoint int
	// ReadPoolSize, when > 0, opens a second read-only connection pool with
	// this many connections for SELECT-style calls. SQLite serializes writes,
	// but WAL lets readers run concurrently, so a read pool lifts read
	// throughput while keeping writes on the single primary connection.
	// 0 (default) means all calls share the single primary pool, preserving
	// the previous single-connection behavior.
	//
	// ReadPoolSize 大于 0 时会为 SELECT 类调用打开独立只读连接池。
	// SQLite 写入会被串行化，但 WAL 允许读取并发执行，因此读池可以在写入仍走
	// 单主连接的同时提升读取吞吐。0（默认值）表示所有调用共享单个主连接池，
	// 保留之前的单连接行为。
	ReadPoolSize int
}

// SQLitePerformanceProfile names SQLite durability and performance presets.
//
// SQLitePerformanceProfile 表示 SQLite synchronous 等持久化策略预设。
type SQLitePerformanceProfile string

const (
	// SQLiteProfileDefault uses the package default SQLite profile.
	//
	// SQLiteProfileDefault 使用包默认的 SQLite 配置档。
	SQLiteProfileDefault SQLitePerformanceProfile = ""
	// SQLiteProfileBalanced uses NORMAL synchronous mode.
	//
	// SQLiteProfileBalanced 使用 NORMAL synchronous 模式。
	SQLiteProfileBalanced SQLitePerformanceProfile = "balanced"
	// SQLiteProfilePerformance favors throughput over durability.
	//
	// SQLiteProfilePerformance 优先吞吐而非持久性。
	SQLiteProfilePerformance SQLitePerformanceProfile = "performance"
	// SQLiteProfileDurable favors durability over write throughput.
	//
	// SQLiteProfileDurable 优先持久性而非写入吞吐。
	SQLiteProfileDurable SQLitePerformanceProfile = "durable"
)

// Option mutates a Config value.
//
// Option 是用于修改 Config 的函数式选项。
type Option func(*Config)

// DefaultConfig returns the default configuration for a backend.
//
// DefaultConfig 返回指定后端的默认配置。
func DefaultConfig(driver Driver, dsn string) Config {
	return Config{
		Driver:               driver,
		DSN:                  dsn,
		TablePrefix:          "metric_",
		DefaultRetentionDays: 90,
		AutoMigrate:          true,
		MaxOpenConns:         25,
		MaxIdleConns:         5,
		ConnMaxLifetime:      time.Hour,
		ConnectTimeout:       10 * time.Second,
		SQLite: SQLiteOptions{
			PerformanceProfile: SQLiteProfileBalanced,
			BusyTimeout:        5 * time.Second,
			CacheSizeKB:        64 * 1024,
			TempStoreMemory:    true,
			MMapSizeBytes:      256 * 1024 * 1024,
			WALAutoCheckpoint:  1000,
		},
	}
}

// Backend builds a generic backend configuration and applies options.
//
// Backend 构造通用后端配置，并应用额外选项。
func Backend(driver Driver, dsn string, opts ...Option) Config {
	cfg := DefaultConfig(driver, dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

// SQLite builds a SQLite backend configuration.
//
// SQLite 构造 SQLite 后端配置。
func SQLite(dsn string, opts ...Option) Config {
	cfg := SQLiteConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

// SQLiteInDir builds a managed SQLite file configuration rooted at a directory.
//
// SQLiteInDir 构造由 package 管理目录的 SQLite 文件数据库配置。
func SQLiteInDir(dir string, opts ...Option) Config {
	cfg := SQLiteConfig(sqliteFileDSN(filepath.Join(dir, "metrics.db")))
	cfg.MaxOpenConns = 1
	cfg.MaxIdleConns = 1
	applyOptions(&cfg, opts...)
	return cfg
}

// MySQL builds a MySQL backend configuration.
//
// MySQL 构造 MySQL 后端配置。
func MySQL(dsn string, opts ...Option) Config {
	cfg := MySQLConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

// PostgreSQL builds a PostgreSQL backend configuration.
//
// PostgreSQL 构造 PostgreSQL 后端配置。
func PostgreSQL(dsn string, opts ...Option) Config {
	cfg := PostgreSQLConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

// SQLiteConfig returns the default SQLite configuration.
//
// SQLiteConfig 返回 SQLite 后端的默认配置。
func SQLiteConfig(dsn string) Config {
	cfg := DefaultConfig(DriverSQLite, dsn)
	if cfg.DSN == "" {
		cfg.DSN = "file:metric?mode=memory&cache=shared"
	}
	// SQLite serializes writes, so a single connection is the safe default and
	// avoids "database is locked" contention. Callers can still override with
	// WithMaxOpenConns/WithMaxIdleConns. Setting it here (rather than inferring
	// it later from the 25/5 defaults) means an explicit WithMaxOpenConns(25)
	// is honored instead of being mistaken for "unset".
	cfg.MaxOpenConns = 1
	cfg.MaxIdleConns = 1
	return cfg
}

// MySQLConfig returns the default MySQL configuration.
//
// MySQLConfig 返回 MySQL 后端的默认配置。
func MySQLConfig(dsn string) Config {
	return DefaultConfig(DriverMySQL, dsn)
}

// PostgreSQLConfig returns the default PostgreSQL configuration.
//
// PostgreSQLConfig 返回 PostgreSQL 后端的默认配置。
func PostgreSQLConfig(dsn string) Config {
	return DefaultConfig(DriverPostgreSQL, dsn)
}

// WithDB sets a caller-owned database connection pool.
//
// WithDB 设置由调用方提供的数据库连接池。
func WithDB(db *sql.DB) Option {
	return func(c *Config) {
		c.DB = db
	}
}

// WithTablePrefix sets the table-name prefix used by metric tables.
//
// WithTablePrefix 设置 metric 私有表的表名前缀。
func WithTablePrefix(prefix string) Option {
	return func(c *Config) {
		c.TablePrefix = prefix
	}
}

// WithDefaultRetention sets the default metric retention in days.
//
// WithDefaultRetention 设置指标默认保留天数。
func WithDefaultRetention(days int) Option {
	return func(c *Config) {
		c.DefaultRetentionDays = days
	}
}

// WithAutoMigrate controls whether Open creates the schema automatically.
//
// WithAutoMigrate 控制 Open 时是否自动创建表结构。
func WithAutoMigrate(enabled bool) Option {
	return func(c *Config) {
		c.AutoMigrate = enabled
	}
}

// WithMaxOpenConns sets the maximum number of open database connections.
//
// WithMaxOpenConns 设置底层数据库连接池的最大打开连接数。
func WithMaxOpenConns(n int) Option {
	return func(c *Config) {
		c.MaxOpenConns = n
	}
}

// WithMaxIdleConns sets the maximum number of idle database connections.
//
// WithMaxIdleConns 设置底层数据库连接池的最大空闲连接数。
func WithMaxIdleConns(n int) Option {
	return func(c *Config) {
		c.MaxIdleConns = n
	}
}

// WithConnMaxLifetime sets the maximum lifetime for pooled connections.
//
// WithConnMaxLifetime 设置连接最大复用时间。
func WithConnMaxLifetime(d time.Duration) Option {
	return func(c *Config) {
		c.ConnMaxLifetime = d
	}
}

// WithConnectTimeout sets the timeout used while opening the store.
//
// WithConnectTimeout 设置打开 Store 时 ping 数据库的超时时间。
func WithConnectTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.ConnectTimeout = d
	}
}

// WithSQLiteProfile sets the SQLite durability and performance profile.
//
// WithSQLiteProfile 设置 SQLite 持久化性能预设。
func WithSQLiteProfile(profile SQLitePerformanceProfile) Option {
	return func(c *Config) {
		c.SQLite.PerformanceProfile = profile
	}
}

// WithSQLiteBusyTimeout sets SQLite busy_timeout.
//
// WithSQLiteBusyTimeout 设置 SQLite busy_timeout。
func WithSQLiteBusyTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.SQLite.BusyTimeout = d
	}
}

// WithSQLiteCacheSizeKB sets the SQLite page cache size in KB.
//
// WithSQLiteCacheSizeKB 设置 SQLite 页缓存大小，单位为 KB。
func WithSQLiteCacheSizeKB(kb int) Option {
	return func(c *Config) {
		c.SQLite.CacheSizeKB = kb
	}
}

// WithSQLiteMMapSize sets SQLite mmap_size.
//
// WithSQLiteMMapSize 设置 SQLite mmap_size。
func WithSQLiteMMapSize(bytes int64) Option {
	return func(c *Config) {
		c.SQLite.MMapSizeBytes = bytes
	}
}

// WithSQLitePageSize sets SQLite page_size.
//
// WithSQLitePageSize 设置 SQLite page_size。
func WithSQLitePageSize(bytes int) Option {
	return func(c *Config) {
		c.SQLite.PageSize = bytes
	}
}

// WithSQLiteTempStoreMemory controls whether SQLite uses memory for temporary storage.
//
// WithSQLiteTempStoreMemory 设置 SQLite 是否使用内存临时存储。
func WithSQLiteTempStoreMemory(enabled bool) Option {
	return func(c *Config) {
		c.SQLite.TempStoreMemory = enabled
	}
}

// WithSQLiteWALAutoCheckpoint sets the SQLite WAL auto-checkpoint page count.
//
// WithSQLiteWALAutoCheckpoint 设置 SQLite WAL 自动 checkpoint 页数。
func WithSQLiteWALAutoCheckpoint(pages int) Option {
	return func(c *Config) {
		c.SQLite.WALAutoCheckpoint = pages
	}
}

// WithSQLiteReadPool enables a dedicated read-only connection pool of n
// connections for SQLite. Writes stay on the single primary connection
// (SQLite serializes them); reads fan out across the pool, which WAL mode
// allows to run concurrently. Pass n <= 1 to disable (the default).
//
// WithSQLiteReadPool 为 SQLite 启用包含 n 个连接的独立只读连接池。写入仍走
// 单主连接（SQLite 会串行化写入）；读取会分散到该连接池中，WAL 模式允许它们
// 并发执行。传入 n <= 1 会禁用该功能（默认行为）。
func WithSQLiteReadPool(n int) Option {
	return func(c *Config) {
		c.SQLite.ReadPoolSize = n
	}
}

// WithRollupPolicy sets the downsampling/retention ladder. Compact uses it to
// build rollup tiers (each progressively coarser and longer-lived) and to age
// out raw points and expired tiers.
//
// WithRollupPolicy 设置降采样与保留时间阶梯；Compact 会据此构建逐级
// 更粗、保留更久的 rollup，并清理过期原始点和过期层级。
func WithRollupPolicy(p RollupPolicy) Option {
	return func(c *Config) {
		c.RollupPolicy = p
	}
}

// applyOptions applies configuration options in order.
//
// applyOptions 将一组选项依次应用到 Config。
func applyOptions(cfg *Config, opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
}

// Validate checks whether the value is well formed.
//
// Validate 检查 Config 的后端、表名前缀、保留时间和 rollup 策略是否合法。
func (c Config) Validate() error {
	switch c.Driver {
	case DriverSQLite, DriverMySQL, DriverPostgreSQL:
	default:
		return fmt.Errorf("%w: unsupported driver %q", ErrInvalidArgument, c.Driver)
	}
	if c.DB == nil && strings.TrimSpace(c.DSN) == "" {
		return fmt.Errorf("%w: dsn is required when db is not supplied", ErrInvalidArgument)
	}
	if c.TablePrefix == "" {
		return fmt.Errorf("%w: table prefix cannot be empty", ErrInvalidArgument)
	}
	for _, r := range c.TablePrefix {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z') {
			return fmt.Errorf("%w: table prefix must contain only letters, digits, and underscores", ErrInvalidArgument)
		}
	}
	if c.DefaultRetentionDays <= 0 {
		return fmt.Errorf("%w: default retention days must be positive", ErrInvalidArgument)
	}
	if err := c.RollupPolicy.Validate(); err != nil {
		return err
	}
	return nil
}

// driverName returns the database/sql driver name for the configured backend.
//
// driverName 返回 database/sql 注册使用的驱动名称。
func (c Config) driverName() string {
	switch c.Driver {
	case DriverSQLite:
		return "sqlite3"
	case DriverMySQL:
		return "mysql"
	case DriverPostgreSQL:
		return "pgx"
	default:
		return string(c.Driver)
	}
}

// sqliteFileDSN converts a filesystem path into a SQLite file DSN.
//
// sqliteFileDSN 将文件路径转换为 SQLite file: DSN。
func sqliteFileDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?cache=shared&mode=rwc"
}

// appendSQLiteDSNParam appends a query parameter to a SQLite DSN.
//
// appendSQLiteDSNParam 向 SQLite DSN 追加查询参数。
func appendSQLiteDSNParam(dsn, key, value string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	return dsn + "?" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}
