package metric

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Driver Driver
	DSN    string

	// DB can be supplied by the host app when it owns the connection pool.
	// When DB is set, DSN is ignored and Close leaves the supplied DB open.
	DB *sql.DB

	TablePrefix          string
	DefaultRetentionDays int
	AutoMigrate          bool
	MaxOpenConns         int
	MaxIdleConns         int
	ConnMaxLifetime      time.Duration
	ConnectTimeout       time.Duration
	SQLite               SQLiteOptions
}

type SQLiteOptions struct {
	PerformanceProfile SQLitePerformanceProfile
	BusyTimeout        time.Duration
	CacheSizeKB        int
	PageSize           int
	TempStoreMemory    bool
	MMapSizeBytes      int64
	WALAutoCheckpoint  int
	// ReadPoolSize, when > 0, opens a second read-only connection pool with
	// this many connections for SELECT-style calls. SQLite serializes writes,
	// but WAL lets readers run concurrently, so a read pool lifts read
	// throughput while keeping writes on the single primary connection.
	// 0 (default) means all calls share the single primary pool, preserving
	// the previous single-connection behavior.
	ReadPoolSize int
}

type SQLitePerformanceProfile string

const (
	SQLiteProfileDefault     SQLitePerformanceProfile = ""
	SQLiteProfileBalanced    SQLitePerformanceProfile = "balanced"
	SQLiteProfilePerformance SQLitePerformanceProfile = "performance"
	SQLiteProfileDurable     SQLitePerformanceProfile = "durable"
)

type Option func(*Config)

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

func Backend(driver Driver, dsn string, opts ...Option) Config {
	cfg := DefaultConfig(driver, dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

func SQLite(dsn string, opts ...Option) Config {
	cfg := SQLiteConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

func SQLiteInDir(dir string, opts ...Option) Config {
	cfg := SQLiteConfig(sqliteFileDSN(filepath.Join(dir, "metrics.db")))
	cfg.MaxOpenConns = 1
	cfg.MaxIdleConns = 1
	applyOptions(&cfg, opts...)
	return cfg
}

func MySQL(dsn string, opts ...Option) Config {
	cfg := MySQLConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

func PostgreSQL(dsn string, opts ...Option) Config {
	cfg := PostgreSQLConfig(dsn)
	applyOptions(&cfg, opts...)
	return cfg
}

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

func MySQLConfig(dsn string) Config {
	return DefaultConfig(DriverMySQL, dsn)
}

func PostgreSQLConfig(dsn string) Config {
	return DefaultConfig(DriverPostgreSQL, dsn)
}

func WithDB(db *sql.DB) Option {
	return func(c *Config) {
		c.DB = db
	}
}

func WithTablePrefix(prefix string) Option {
	return func(c *Config) {
		c.TablePrefix = prefix
	}
}

func WithDefaultRetention(days int) Option {
	return func(c *Config) {
		c.DefaultRetentionDays = days
	}
}

func WithAutoMigrate(enabled bool) Option {
	return func(c *Config) {
		c.AutoMigrate = enabled
	}
}

func WithMaxOpenConns(n int) Option {
	return func(c *Config) {
		c.MaxOpenConns = n
	}
}

func WithMaxIdleConns(n int) Option {
	return func(c *Config) {
		c.MaxIdleConns = n
	}
}

func WithConnMaxLifetime(d time.Duration) Option {
	return func(c *Config) {
		c.ConnMaxLifetime = d
	}
}

func WithConnectTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.ConnectTimeout = d
	}
}

func WithSQLiteProfile(profile SQLitePerformanceProfile) Option {
	return func(c *Config) {
		c.SQLite.PerformanceProfile = profile
	}
}

func WithSQLiteBusyTimeout(d time.Duration) Option {
	return func(c *Config) {
		c.SQLite.BusyTimeout = d
	}
}

func WithSQLiteCacheSizeKB(kb int) Option {
	return func(c *Config) {
		c.SQLite.CacheSizeKB = kb
	}
}

func WithSQLiteMMapSize(bytes int64) Option {
	return func(c *Config) {
		c.SQLite.MMapSizeBytes = bytes
	}
}

func WithSQLitePageSize(bytes int) Option {
	return func(c *Config) {
		c.SQLite.PageSize = bytes
	}
}

func WithSQLiteTempStoreMemory(enabled bool) Option {
	return func(c *Config) {
		c.SQLite.TempStoreMemory = enabled
	}
}

func WithSQLiteWALAutoCheckpoint(pages int) Option {
	return func(c *Config) {
		c.SQLite.WALAutoCheckpoint = pages
	}
}

// WithSQLiteReadPool enables a dedicated read-only connection pool of n
// connections for SQLite. Writes stay on the single primary connection
// (SQLite serializes them); reads fan out across the pool, which WAL mode
// allows to run concurrently. Pass n <= 1 to disable (the default).
func WithSQLiteReadPool(n int) Option {
	return func(c *Config) {
		c.SQLite.ReadPoolSize = n
	}
}

func applyOptions(cfg *Config, opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
}

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
	return nil
}

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

func sqliteFileDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?cache=shared&mode=rwc"
}

func appendSQLiteDSNParam(dsn, key, value string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	return dsn + "?" + url.QueryEscape(key) + "=" + url.QueryEscape(value)
}
