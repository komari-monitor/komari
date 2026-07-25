package metric

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// SQLiteMigrationSummary describes pending work without modifying the
// database. SourceRows is the number of raw points and rollup buckets that may
// need to be rewritten; LegacyBlocks covers the earlier V4 rollup codec.
type SQLiteMigrationSummary struct {
	Required     bool   `json:"required"`
	Layout       string `json:"layout"`
	SourceRows   int64  `json:"source_rows"`
	LegacyBlocks int64  `json:"legacy_blocks"`
}

// InspectSQLiteMigration reports whether opening cfg with AutoMigrate would
// perform a potentially long SQLite storage migration.
func InspectSQLiteMigration(ctx context.Context, cfg Config) (SQLiteMigrationSummary, error) {
	if cfg.Driver != DriverSQLite {
		return SQLiteMigrationSummary{}, nil
	}
	if cfg.TablePrefix == "" {
		cfg.TablePrefix = "metric_"
	}
	if err := cfg.Validate(); err != nil {
		return SQLiteMigrationSummary{}, err
	}

	var db *sql.DB
	if cfg.DB != nil {
		db = cfg.DB
	} else {
		path := sqliteFilePath(cfg.DSN)
		if path != "" && path != ":memory:" && !isMemoryDSN(cfg.DSN) {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return SQLiteMigrationSummary{Layout: "empty"}, nil
			} else if err != nil {
				return SQLiteMigrationSummary{}, fmt.Errorf("metric: inspect SQLite storage file: %w", err)
			}
		}
		var err error
		db, err = sql.Open(cfg.driverName(), cfg.DSN)
		if err != nil {
			return SQLiteMigrationSummary{}, err
		}
		defer db.Close()
	}
	if err := db.PingContext(ctx); err != nil {
		return SQLiteMigrationSummary{}, err
	}

	t := tables{
		points:       tableName(cfg.TablePrefix, "points"),
		rollups:      tableName(cfg.TablePrefix, "rollups"),
		series:       tableName(cfg.TablePrefix, "series"),
		pointValues:  tableName(cfg.TablePrefix, "point_values"),
		pointBlocks:  tableName(cfg.TablePrefix, "point_blocks"),
		rollupValues: tableName(cfg.TablePrefix, "rollup_values"),
		rollupBlocks: tableName(cfg.TablePrefix, "rollup_blocks"),
	}
	pointKind, err := sqliteObjectTypeDB(ctx, db, t.points)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}
	rollupKind, err := sqliteObjectTypeDB(ctx, db, t.rollups)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}
	if pointKind == "" && rollupKind == "" {
		return SQLiteMigrationSummary{Layout: "empty"}, nil
	}
	if pointKind != rollupKind || (pointKind != "table" && pointKind != "view") {
		return SQLiteMigrationSummary{}, fmt.Errorf("metric: inconsistent SQLite storage objects: %s=%q %s=%q", t.points, pointKind, t.rollups, rollupKind)
	}

	summary := SQLiteMigrationSummary{Required: true}
	if pointKind == "table" {
		summary.Layout = "legacy"
		summary.SourceRows, err = sumSQLiteRows(ctx, db, t.points, t.rollups)
		return summary, err
	}

	for _, table := range []string{t.series, t.pointValues, t.rollupValues} {
		kind, inspectErr := sqliteObjectTypeDB(ctx, db, table)
		if inspectErr != nil {
			return SQLiteMigrationSummary{}, inspectErr
		}
		if kind != "table" {
			return SQLiteMigrationSummary{}, fmt.Errorf("metric: normalized SQLite table %s is missing", table)
		}
	}
	summary.Layout = "normalized"
	summary.SourceRows, err = sumSQLiteRows(ctx, db, t.pointValues, t.rollupValues)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}

	pointBlockKind, err := sqliteObjectTypeDB(ctx, db, t.pointBlocks)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}
	rollupBlockKind, err := sqliteObjectTypeDB(ctx, db, t.rollupBlocks)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}
	if (pointBlockKind != "" && pointBlockKind != "table") || (rollupBlockKind != "" && rollupBlockKind != "table") {
		return SQLiteMigrationSummary{}, fmt.Errorf("metric: invalid SQLite V4 block objects: %s=%q %s=%q", t.pointBlocks, pointBlockKind, t.rollupBlocks, rollupBlockKind)
	}
	if pointBlockKind != "table" || rollupBlockKind != "table" {
		return summary, nil
	}

	summary.Layout = "v4"
	columns, err := sqliteColumns(ctx, db, t.rollupBlocks)
	if err != nil {
		return SQLiteMigrationSummary{}, err
	}
	for _, name := range []string{"digest_codec", "digest_checksum", "digest_payload"} {
		if !columns[name] {
			return summary, nil
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.rollupBlocks+` WHERE codec = ?`, sqliteV4LegacyRollupBlockCodec).Scan(&summary.LegacyBlocks); err != nil {
		return SQLiteMigrationSummary{}, fmt.Errorf("metric: count legacy SQLite V4 blocks: %w", err)
	}
	var autoVacuum int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&autoVacuum); err != nil {
		return SQLiteMigrationSummary{}, fmt.Errorf("metric: inspect SQLite auto-vacuum mode: %w", err)
	}
	summary.Required = summary.LegacyBlocks > 0 || autoVacuum != 2
	if !summary.Required {
		summary.Layout = "current"
	}
	return summary, nil
}

func sqliteObjectTypeDB(ctx context.Context, db *sql.DB, name string) (string, error) {
	var kind string
	err := db.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name = ? AND type IN ('table', 'view')`, name).Scan(&kind)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("metric: inspect SQLite object %s: %w", name, err)
	}
	return kind, nil
}

func sumSQLiteRows(ctx context.Context, db *sql.DB, names ...string) (int64, error) {
	var total int64
	for _, name := range names {
		var count int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+name).Scan(&count); err != nil {
			return 0, fmt.Errorf("metric: count SQLite rows in %s: %w", name, err)
		}
		total += count
	}
	return total, nil
}

func sqliteColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
