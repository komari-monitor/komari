// Package sqlitetune applies a consistent SQLite baseline to every physical
// connection opened through database/sql.
package sqlitetune

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"math"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// SynchronousMode selects SQLite's synchronous durability mode.
type SynchronousMode string

const (
	// SynchronousNormal balances write throughput and durability in WAL mode.
	SynchronousNormal SynchronousMode = "NORMAL"
	// SynchronousOff favors throughput over durability.
	SynchronousOff SynchronousMode = "OFF"
	// SynchronousFull favors durability over write throughput.
	SynchronousFull SynchronousMode = "FULL"
)

// Options configures the SQLite PRAGMAs applied to every physical connection.
// WAL mode is always enabled. Zero mmap size explicitly disables memory
// mapping; it is not treated as an unset value.
type Options struct {
	PageSize              int
	ForeignKeys           bool
	BusyTimeout           time.Duration
	CacheSizeKB           int
	MMapSizeBytes         int64
	TempStoreMemory       bool
	CacheSpill            bool
	WALAutoCheckpoint     int
	JournalSizeLimitBytes int64
	Synchronous           SynchronousMode
}

// Open returns a database/sql pool whose new SQLite connections are configured
// before they are handed to callers.
func Open(dsn string, options Options) (*sql.DB, error) {
	options, err := normalize(options)
	if err != nil {
		return nil, err
	}

	driver := &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return applyDriverConn(conn, options)
		},
	}
	return sql.OpenDB(&connector{driver: driver, dsn: dsn}), nil
}

// Apply configures one physical connection from a caller-owned pool. Future
// connections in that pool remain the caller's responsibility; use Open when
// this package owns the pool and every physical connection must be configured.
func Apply(ctx context.Context, db *sql.DB, options Options) error {
	options, err := normalize(options)
	if err != nil {
		return err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("sqlite tune: acquire connection: %w", err)
	}
	defer conn.Close()

	for _, pragma := range pragmas(options) {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("sqlite tune: apply %q: %w", pragma, err)
		}
	}
	return nil
}

type connector struct {
	driver *sqlite3.SQLiteDriver
	dsn    string
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c *connector) Driver() driver.Driver {
	return c.driver
}

func applyDriverConn(conn *sqlite3.SQLiteConn, options Options) error {
	for _, pragma := range pragmas(options) {
		if _, err := conn.Exec(pragma, nil); err != nil {
			return fmt.Errorf("sqlite tune: apply %q: %w", pragma, err)
		}
	}
	return nil
}

func normalize(options Options) (Options, error) {
	if options.Synchronous == "" {
		options.Synchronous = SynchronousNormal
	}
	switch options.Synchronous {
	case SynchronousNormal, SynchronousOff, SynchronousFull:
	default:
		return options, fmt.Errorf("sqlite tune: unsupported synchronous mode %q", options.Synchronous)
	}
	if options.PageSize < 0 {
		return options, fmt.Errorf("sqlite tune: page size cannot be negative")
	}
	if options.BusyTimeout < 0 {
		return options, fmt.Errorf("sqlite tune: busy timeout cannot be negative")
	}
	if options.CacheSizeKB <= 0 {
		return options, fmt.Errorf("sqlite tune: cache size must be positive")
	}
	if options.MMapSizeBytes < 0 {
		return options, fmt.Errorf("sqlite tune: mmap size cannot be negative")
	}
	if options.WALAutoCheckpoint <= 0 {
		return options, fmt.Errorf("sqlite tune: WAL auto-checkpoint must be positive")
	}
	// SQLite uses -1 to disable the post-checkpoint WAL size limit. Preserve
	// that documented value for callers that explicitly opt out of the cap.
	if options.JournalSizeLimitBytes < -1 {
		return options, fmt.Errorf("sqlite tune: journal size limit cannot be less than -1")
	}
	return options, nil
}

func pragmas(options Options) []string {
	pragmas := make([]string, 0, 11)
	if options.PageSize > 0 {
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA page_size = %d", options.PageSize))
	}
	pragmas = append(pragmas, "PRAGMA journal_mode = WAL")
	if options.ForeignKeys {
		pragmas = append(pragmas, "PRAGMA foreign_keys = ON")
	}
	pragmas = append(pragmas,
		fmt.Sprintf("PRAGMA synchronous = %s", options.Synchronous),
		fmt.Sprintf("PRAGMA busy_timeout = %d", durationMillis(options.BusyTimeout)),
		fmt.Sprintf("PRAGMA cache_size = -%d", options.CacheSizeKB),
		fmt.Sprintf("PRAGMA mmap_size = %d", options.MMapSizeBytes),
	)
	if options.TempStoreMemory {
		pragmas = append(pragmas, "PRAGMA temp_store = MEMORY")
	} else {
		pragmas = append(pragmas, "PRAGMA temp_store = FILE")
	}
	if options.CacheSpill {
		pragmas = append(pragmas, "PRAGMA cache_spill = ON")
	} else {
		pragmas = append(pragmas, "PRAGMA cache_spill = OFF")
	}
	return append(pragmas,
		fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", options.WALAutoCheckpoint),
		fmt.Sprintf("PRAGMA journal_size_limit = %d", options.JournalSizeLimitBytes),
	)
}

func durationMillis(d time.Duration) int {
	return int(math.Ceil(float64(d) / float64(time.Millisecond)))
}
