package metric

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const (
	timescaleColumnstoreAlgorithm = "columnstore"
	timescaleChunkIntervalNanos   = int64(24 * 60 * 60 * 1_000_000_000)
)

type timescaleCompressionFlavor uint8

const (
	timescaleCompressionUnsupported timescaleCompressionFlavor = iota
	timescaleCompressionLegacy
	timescaleCompressionHypercore
)

type postgreSQLTimescaleState struct {
	installed         bool
	supported         bool
	active            bool
	compressionActive bool
}

type timescaleCompressionTable struct {
	name           string
	timeColumn     string
	segmentColumns []string
}

func (state postgreSQLTimescaleState) option() CompressionOption {
	option := CompressionOption{
		Mode:       CompressionModeTimescaleDB,
		Algorithms: []string{},
	}
	switch {
	case !state.installed:
		option.Reason = compressionReasonTimescaleDBNotInstalled
	case !state.supported:
		option.Reason = compressionReasonTimescaleDBVersionUnsupported
	default:
		option.Supported = true
		option.Algorithms = []string{timescaleColumnstoreAlgorithm}
		option.Warning = compressionWarningTimescaleDB
	}
	return option
}

func (s *Store) inspectPostgreSQLTimescaleCompression(ctx context.Context) (postgreSQLTimescaleState, error) {
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'`).Scan(&version)
	if err == sql.ErrNoRows {
		return postgreSQLTimescaleState{}, nil
	}
	if err != nil {
		return postgreSQLTimescaleState{}, fmt.Errorf("metric: inspect TimescaleDB extension: %w", err)
	}

	state := postgreSQLTimescaleState{installed: true}
	if timescaleCompressionFlavorForVersion(version) == timescaleCompressionUnsupported {
		return state, nil
	}
	state.supported = true

	tables := timescaleCompressionTables(s.tables)
	var hypertableCount, compressedCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(*) FILTER (WHERE compression_enabled)
FROM timescaledb_information.hypertables
WHERE hypertable_schema = current_schema()
  AND hypertable_name IN ($1, $2)`, tables[0].name, tables[1].name).Scan(&hypertableCount, &compressedCount); err != nil {
		return postgreSQLTimescaleState{}, fmt.Errorf("metric: inspect TimescaleDB hypertables: %w", err)
	}
	if hypertableCount != 0 && hypertableCount != len(tables) {
		return postgreSQLTimescaleState{}, fmt.Errorf("metric: TimescaleDB conversion is incomplete; managed metric tables require manual repair")
	}
	state.active = hypertableCount == len(tables)
	state.compressionActive = compressedCount == len(tables)
	return state, nil
}

func (s *Store) configureTimescaleDBCompression(ctx context.Context) error {
	var version string
	if err := s.db.QueryRowContext(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'`).Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: TimescaleDB is not installed", ErrInvalidArgument)
		}
		return fmt.Errorf("metric: inspect TimescaleDB version before conversion: %w", err)
	}
	flavor := timescaleCompressionFlavorForVersion(version)
	if flavor == timescaleCompressionUnsupported {
		return fmt.Errorf("%w: TimescaleDB version %q is unsupported; version 2.0 or newer is required", ErrInvalidArgument, version)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin TimescaleDB conversion: %w", err)
	}
	defer tx.Rollback()

	tables := timescaleCompressionTables(s.tables)
	for _, table := range tables {
		if err := dropPostgreSQLPrimaryKey(ctx, tx, table.name); err != nil {
			return err
		}
		if err := createTimescaleHypertable(ctx, tx, table, version); err != nil {
			return err
		}
	}

	nowFunction := strings.ToLower(s.cfg.TablePrefix) + "integer_now"
	createNowFunction := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s()
RETURNS BIGINT
LANGUAGE SQL
STABLE
AS $$ SELECT (extract(epoch FROM clock_timestamp()) * 1000000000)::BIGINT $$`, quoteMaintenanceIdentifier(DriverPostgreSQL, nowFunction))
	if _, err := tx.ExecContext(ctx, createNowFunction); err != nil {
		return fmt.Errorf("metric: create TimescaleDB integer now function: %w", err)
	}

	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, `SELECT set_integer_now_func(
  to_regclass(format('%I.%I', current_schema(), $1)),
  to_regproc(format('%I.%I', current_schema(), $2)),
  replace_if_exists => TRUE
)`, table.name, nowFunction); err != nil {
			return fmt.Errorf("metric: configure TimescaleDB integer time for %s: %w", table.name, err)
		}
		if err := enableTimescaleCompression(ctx, tx, table, flavor); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit TimescaleDB conversion: %w", err)
	}
	return nil
}

func dropPostgreSQLPrimaryKey(ctx context.Context, tx *sql.Tx, table string) error {
	var constraint string
	err := tx.QueryRowContext(ctx, `SELECT conname
FROM pg_catalog.pg_constraint
WHERE contype = 'p'
  AND conrelid = to_regclass(format('%I.%I', current_schema(), $1))`, table).Scan(&constraint)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("metric: inspect PostgreSQL primary key on %s: %w", table, err)
	}
	query := "ALTER TABLE " + quoteMaintenanceIdentifier(DriverPostgreSQL, table) +
		" DROP CONSTRAINT " + quoteMaintenanceIdentifier(DriverPostgreSQL, constraint)
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("metric: drop PostgreSQL primary key on %s: %w", table, err)
	}
	return nil
}

func createTimescaleHypertable(ctx context.Context, tx *sql.Tx, table timescaleCompressionTable, version string) error {
	query := `SELECT create_hypertable(
  to_regclass(format('%I.%I', current_schema(), $1)),
  $2,
  chunk_time_interval => $3::BIGINT,
  if_not_exists => TRUE,
  migrate_data => TRUE
)`
	if numericVersionAtLeast(version, 2, 13, 0) {
		query = `SELECT create_hypertable(
  to_regclass(format('%I.%I', current_schema(), $1)),
  by_range($2, $3::BIGINT),
  if_not_exists => TRUE,
  migrate_data => TRUE
)`
	}
	if _, err := tx.ExecContext(ctx, query, table.name, table.timeColumn, timescaleChunkIntervalNanos); err != nil {
		return fmt.Errorf("metric: convert %s to a TimescaleDB hypertable: %w", table.name, err)
	}
	return nil
}

func enableTimescaleCompression(ctx context.Context, tx *sql.Tx, table timescaleCompressionTable, flavor timescaleCompressionFlavor) error {
	quotedTable := quoteMaintenanceIdentifier(DriverPostgreSQL, table.name)
	segmentBy := strings.Join(table.segmentColumns, ",")
	var settings, policy string
	if flavor == timescaleCompressionHypercore {
		settings = fmt.Sprintf("ALTER TABLE %s SET (timescaledb.enable_columnstore, timescaledb.orderby = '%s DESC', timescaledb.segmentby = '%s')", quotedTable, table.timeColumn, segmentBy)
		policy = `CALL add_columnstore_policy(
  to_regclass(format('%I.%I', current_schema(), $1)),
  $2::BIGINT,
  if_not_exists => TRUE
)`
	} else {
		settings = fmt.Sprintf("ALTER TABLE %s SET (timescaledb.compress, timescaledb.compress_orderby = '%s DESC', timescaledb.compress_segmentby = '%s')", quotedTable, table.timeColumn, segmentBy)
		policy = `SELECT add_compression_policy(
  to_regclass(format('%I.%I', current_schema(), $1)),
  compress_after => $2::BIGINT,
  if_not_exists => TRUE
)`
	}
	if _, err := tx.ExecContext(ctx, settings); err != nil {
		return fmt.Errorf("metric: enable TimescaleDB compression on %s: %w", table.name, err)
	}
	if _, err := tx.ExecContext(ctx, policy, table.name, timescaleChunkIntervalNanos); err != nil {
		return fmt.Errorf("metric: add TimescaleDB compression policy on %s: %w", table.name, err)
	}
	return nil
}

func timescaleCompressionTables(t tables) []timescaleCompressionTable {
	return []timescaleCompressionTable{
		{
			name:           strings.ToLower(t.points),
			timeColumn:     "ts_nano",
			segmentColumns: []string{"metric_name", "entity_id", "tags_hash"},
		},
		{
			name:           strings.ToLower(t.rollups),
			timeColumn:     "bucket_nano",
			segmentColumns: []string{"metric_name", "entity_id", "tags_hash", "resolution_nano"},
		},
	}
}

func timescaleCompressionFlavorForVersion(version string) timescaleCompressionFlavor {
	if !numericVersionAtLeast(version, 2, 0, 0) {
		return timescaleCompressionUnsupported
	}
	if numericVersionAtLeast(version, 2, 18, 0) {
		return timescaleCompressionHypercore
	}
	return timescaleCompressionLegacy
}

func numericVersionAtLeast(raw string, major, minor, patch int) bool {
	parts := strings.SplitN(strings.TrimSpace(raw), ".", 4)
	if len(parts) < 2 {
		return false
	}
	parsed := [3]int{}
	for i := range parsed {
		if i >= len(parts) {
			break
		}
		digits := strings.TrimLeftFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
		if end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
			digits = digits[:end]
		}
		value, err := strconv.Atoi(digits)
		if err != nil {
			return false
		}
		parsed[i] = value
	}
	want := [3]int{major, minor, patch}
	for i := range parsed {
		if parsed[i] != want[i] {
			return parsed[i] > want[i]
		}
	}
	return true
}
