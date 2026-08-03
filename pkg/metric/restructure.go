package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	restructureRollupReadBatchSize  = 5000
	restructureRollupWriteBatchSize = 500
)

// RestructureProgress is emitted after each bounded import batch.
type RestructureProgress struct {
	Phase        string
	Current      string
	RowsDone     int64
	RowsTotal    int64
	MetricsDone  int
	MetricsTotal int
}

// RestructureResult describes the logical data copied into the normalized
// schema. Physical before/after bytes are measured by the caller around the
// one required reclaim operation.
type RestructureResult struct {
	RowsCopied int64
	Metrics    int
}

type restructureRollupGroup struct {
	name     string
	interval time.Duration
	buckets  map[rollupKey]*rollupBucket
}

// metricSchemaShape describes the physical schema without assuming that one
// marker column implies that every normalized table was created successfully.
// A failed multi-table migration can leave a mixture of these shapes behind.
type metricSchemaShape struct {
	definitionsExists     bool
	definitionsNormalized bool
	definitionsLegacy     bool
	pointsExists          bool
	pointsLegacy          bool
	rollupsExists         bool
	rollupsNormalized     bool
	rollupsLegacy         bool
	seriesNormalized      bool
	labelsNormalized      bool
	resolutionsNormalized bool
	foreignKeysNormalized bool
	watermarksExists      bool
	mysqlBackupsExist     bool
}

var normalizedDefinitionColumns = []string{
	"name", "type", "unit", "description", "retention_days", "metadata",
	"created_at_milli", "updated_at_milli",
}

var legacyDefinitionColumns = []string{
	"name", "type", "unit", "description", "retention_days", "metadata",
	"created_at", "updated_at",
}

var legacyPointColumns = []string{
	"id", "metric_name", "entity_id", "tags_hash", "ts_nano", "value",
	"tags", "labels", "created_at",
}

var normalizedSeriesColumns = []string{"id", "metric_name", "entity_id", "tags_hash", "tags"}
var normalizedLabelColumns = []string{"id", "labels_hash", "labels"}
var normalizedResolutionColumns = []string{"id", "resolution_milli"}

var normalizedRollupSchemaColumns = []string{
	"series_id", "resolution_id", "label_id", "bucket_milli", "count", "sum",
	"sum_sq", "min_val", "max_val", "first_val", "first_ts_milli", "last_val",
	"last_ts_milli", "digest", "created_at_milli",
}

var legacyRollupSchemaColumns = []string{
	"id", "metric_name", "entity_id", "tags_hash", "tags", "resolution_nano",
	"bucket_nano", "count", "sum", "sum_sq", "min_val", "max_val", "first_val",
	"first_ts", "last_val", "last_ts", "digest", "created_at",
}

func (shape metricSchemaShape) hasAnyTable() bool {
	return shape.definitionsExists || shape.pointsExists || shape.rollupsExists ||
		shape.seriesNormalized || shape.labelsNormalized || shape.resolutionsNormalized ||
		shape.watermarksExists || shape.mysqlBackupsExist
}

func (shape metricSchemaShape) normalizedComplete() bool {
	return shape.normalizedColumnsComplete() && shape.foreignKeysNormalized
}

func (shape metricSchemaShape) normalizedColumnsComplete() bool {
	return shape.definitionsNormalized && shape.seriesNormalized && shape.labelsNormalized &&
		shape.resolutionsNormalized && shape.rollupsNormalized
}

func (shape metricSchemaShape) needsRestructure() bool {
	if shape.normalizedComplete() {
		return shape.pointsExists || shape.watermarksExists || shape.mysqlBackupsExist
	}
	return shape.hasAnyTable()
}

// inspectSchema probes all required columns in each managed table. CREATE TABLE
// IF NOT EXISTS cannot repair a table that exists with an older column layout,
// so table existence alone is not enough to select the fast path.
func (s *Store) inspectSchema(ctx context.Context) (metricSchemaShape, error) {
	var shape metricSchemaShape
	var err error
	if shape.definitionsExists, err = s.tableExists(ctx, s.tables.definitions); err != nil {
		return shape, err
	}
	if shape.definitionsExists {
		if shape.definitionsNormalized, err = s.columnsExist(ctx, s.tables.definitions, normalizedDefinitionColumns); err != nil {
			return shape, err
		}
		if shape.definitionsLegacy, err = s.columnsExist(ctx, s.tables.definitions, legacyDefinitionColumns); err != nil {
			return shape, err
		}
	}
	if shape.pointsExists, err = s.tableExists(ctx, s.tables.points); err != nil {
		return shape, err
	}
	if shape.pointsExists {
		if shape.pointsLegacy, err = s.columnsExist(ctx, s.tables.points, legacyPointColumns); err != nil {
			return shape, err
		}
	}
	if shape.rollupsExists, err = s.tableExists(ctx, s.tables.rollups); err != nil {
		return shape, err
	}
	if shape.rollupsExists {
		if shape.rollupsNormalized, err = s.columnsExist(ctx, s.tables.rollups, normalizedRollupSchemaColumns); err != nil {
			return shape, err
		}
		if shape.rollupsLegacy, err = s.columnsExist(ctx, s.tables.rollups, legacyRollupSchemaColumns); err != nil {
			return shape, err
		}
	}
	if shape.seriesNormalized, err = s.tableColumnsExist(ctx, s.tables.series, normalizedSeriesColumns); err != nil {
		return shape, err
	}
	if shape.labelsNormalized, err = s.tableColumnsExist(ctx, s.tables.labels, normalizedLabelColumns); err != nil {
		return shape, err
	}
	if shape.resolutionsNormalized, err = s.tableColumnsExist(ctx, s.tables.resolutions, normalizedResolutionColumns); err != nil {
		return shape, err
	}
	if shape.normalizedColumnsComplete() {
		if shape.foreignKeysNormalized, err = s.normalizedForeignKeysExist(ctx); err != nil {
			return shape, err
		}
	}
	if shape.watermarksExists, err = s.tableExists(ctx, s.tables.watermarks); err != nil {
		return shape, err
	}
	if s.cfg.Driver == DriverMySQL {
		for _, table := range s.mysqlLegacyBackupTables() {
			exists, err := s.tableExists(ctx, table)
			if err != nil {
				return shape, err
			}
			if exists {
				shape.mysqlBackupsExist = true
				break
			}
		}
	}
	return shape, nil
}

func (s *Store) mysqlLegacySourceTables() []string {
	return []string{
		s.tables.points,
		s.tables.watermarks,
		s.tables.rollups,
		s.tables.series,
		s.tables.labels,
		s.tables.resolutions,
		s.tables.definitions,
	}
}

func (s *Store) mysqlLegacyBackupTables() []string {
	sources := s.mysqlLegacySourceTables()
	backups := make([]string, len(sources))
	for i, source := range sources {
		backups[i] = source + "_legacy"
	}
	return backups
}

func (s *Store) tableColumnsExist(ctx context.Context, table string, columns []string) (bool, error) {
	exists, err := s.tableExists(ctx, table)
	if err != nil || !exists {
		return false, err
	}
	return s.columnsExist(ctx, table, columns)
}

func (s *Store) columnsExist(ctx context.Context, table string, columns []string) (bool, error) {
	if len(columns) == 0 {
		return true, nil
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE 1 = 0", strings.Join(columns, ", "), table))
	if err == nil {
		return true, nil
	}
	if isMissingColumnError(err) {
		return false, nil
	}
	return false, err
}

type normalizedForeignKey struct {
	table, column, referencedTable, referencedColumn string
}

func (s *Store) expectedNormalizedForeignKeys() []normalizedForeignKey {
	return []normalizedForeignKey{
		{table: s.tables.series, column: "metric_name", referencedTable: s.tables.definitions, referencedColumn: "name"},
		{table: s.tables.rollups, column: "series_id", referencedTable: s.tables.series, referencedColumn: "id"},
		{table: s.tables.rollups, column: "resolution_id", referencedTable: s.tables.resolutions, referencedColumn: "id"},
		{table: s.tables.rollups, column: "label_id", referencedTable: s.tables.labels, referencedColumn: "id"},
	}
}

func (s *Store) normalizedForeignKeysExist(ctx context.Context) (bool, error) {
	for _, expected := range s.expectedNormalizedForeignKeys() {
		found, err := s.normalizedForeignKeyExists(ctx, expected)
		if err != nil {
			return false, err
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func (s *Store) normalizedForeignKeyExists(ctx context.Context, expected normalizedForeignKey) (bool, error) {
	if s.cfg.Driver == DriverSQLite {
		rows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_list("+expected.table+")")
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, sequence int
			var referencedTable, column, referencedColumn, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &referencedTable, &column, &referencedColumn, &onUpdate, &onDelete, &match); err != nil {
				return false, err
			}
			if column == expected.column && referencedTable == expected.referencedTable && referencedColumn == expected.referencedColumn && strings.EqualFold(onDelete, "CASCADE") {
				return true, nil
			}
		}
		return false, rows.Err()
	}

	var query string
	var args []any
	switch s.cfg.Driver {
	case DriverMySQL:
		query = `SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE kcu
			JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
			  ON kcu.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA AND kcu.CONSTRAINT_NAME = rc.CONSTRAINT_NAME
			WHERE kcu.CONSTRAINT_SCHEMA = DATABASE() AND kcu.TABLE_NAME = ? AND kcu.COLUMN_NAME = ?
			AND kcu.REFERENCED_TABLE_NAME = ? AND kcu.REFERENCED_COLUMN_NAME = ? AND rc.DELETE_RULE = 'CASCADE'`
		args = []any{expected.table, expected.column, expected.referencedTable, expected.referencedColumn}
	case DriverPostgreSQL:
		query = `SELECT COUNT(*) FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON tc.constraint_schema = kcu.constraint_schema AND tc.constraint_name = kcu.constraint_name
			JOIN information_schema.constraint_column_usage ccu
			  ON tc.constraint_schema = ccu.constraint_schema AND tc.constraint_name = ccu.constraint_name
			WHERE tc.table_schema = current_schema() AND tc.constraint_type = 'FOREIGN KEY'
			  AND tc.table_name = $1 AND kcu.column_name = $2
			  AND ccu.table_name = $3 AND ccu.column_name = $4
			  AND tc.constraint_name IN (
				SELECT rc.constraint_name FROM information_schema.referential_constraints rc
				WHERE rc.constraint_schema = current_schema() AND rc.delete_rule = 'CASCADE'
			  )`
		// PostgreSQL folds every unquoted identifier used by this package to
		// lowercase, while Config permits uppercase characters in a prefix.
		args = []any{strings.ToLower(expected.table), expected.column, strings.ToLower(expected.referencedTable), expected.referencedColumn}
	default:
		return false, fmt.Errorf("%w: unsupported driver %q", ErrInvalidArgument, s.cfg.Driver)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// LegacyStorageSize measures every table that may exist before rebuilding. It
// is separate from StorageSize because transitional stores can contain both
// normalized dictionaries and obsolete point/watermark tables.
func (s *Store) LegacyStorageSize(ctx context.Context) (int64, error) {
	if s.cfg.Driver == DriverSQLite {
		return s.StorageSize(ctx)
	}
	names := []string{
		s.tables.definitions,
		s.tables.points,
		s.tables.series,
		s.tables.labels,
		s.tables.resolutions,
		s.tables.rollups,
		s.tables.watermarks,
	}
	if s.cfg.Driver == DriverMySQL {
		names = append(names, s.mysqlLegacyBackupTables()...)
	}
	placeholders := make([]string, len(names))
	args := make([]any, len(names))
	for i, name := range names {
		placeholders[i], args[i] = s.dialect.placeholder(i+1), name
	}
	var query string
	switch s.cfg.Driver {
	case DriverMySQL:
		query = `SELECT COALESCE(SUM(DATA_LENGTH + INDEX_LENGTH), 0) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME IN (` + strings.Join(placeholders, ", ") + `)`
	case DriverPostgreSQL:
		for i := range args {
			args[i] = strings.ToLower(names[i])
		}
		query = `SELECT COALESCE(SUM(pg_total_relation_size(c.oid)), 0) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema() AND c.relname IN (` + strings.Join(placeholders, ", ") + `)`
	default:
		return 0, fmt.Errorf("%w: unsupported driver %q", ErrInvalidArgument, s.cfg.Driver)
	}
	var size int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&size); err != nil {
		return 0, err
	}
	return size, nil
}

// NeedsRestructure reports whether an existing metric schema predates the
// normalized millisecond rollup layout. A database without metric tables is a
// fresh install and will be created normally by AutoMigrate.
func (s *Store) NeedsRestructure(ctx context.Context) (bool, error) {
	if err := s.ensureOpen(); err != nil {
		return false, err
	}
	shape, err := s.inspectSchema(ctx)
	if err != nil {
		return false, err
	}
	return shape.needsRestructure(), nil
}

// Restructure rebuilds an old store into normalized dictionary and rollup
// tables. It is intentionally explicit and intended for the
// authenticated upgrade guide; normal startup never mutates an existing schema.
func (s *Store) Restructure(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	if err := s.ensureOpen(); err != nil {
		return RestructureResult{}, err
	}
	shape, err := s.inspectSchema(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if !shape.needsRestructure() {
		return RestructureResult{}, nil
	}
	if shape.normalizedComplete() {
		return s.removeObsoleteRawTables(ctx, report, shape)
	}
	if shape.normalizedColumnsComplete() {
		return s.rebuildNormalizedSchema(ctx, report)
	}
	if err := s.validateRestructurePrefix(); err != nil {
		return RestructureResult{}, err
	}
	if !shape.definitionsNormalized && !shape.definitionsLegacy {
		return RestructureResult{}, fmt.Errorf("metric definitions schema is incomplete; history cannot be preserved safely")
	}
	if shape.pointsExists && !shape.pointsLegacy {
		return RestructureResult{}, fmt.Errorf("metric points schema is not a recognized legacy layout; history cannot be preserved safely")
	}
	if shape.rollupsExists && !shape.rollupsLegacy {
		return RestructureResult{}, fmt.Errorf("metric rollups schema is not a recognized legacy layout; history cannot be preserved safely")
	}

	definitions, err := s.readDefinitionsForShape(ctx, shape)
	if err != nil {
		return RestructureResult{}, err
	}
	rowsTotal, err := s.legacyRowCount(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	remaining, err := s.legacyMetricRowCounts(ctx, definitions)
	if err != nil {
		return RestructureResult{}, err
	}

	shadowPrefix := s.cfg.TablePrefix + "rebuild_"
	shadowCfg := s.cfg
	shadowCfg.DB = s.db
	shadowCfg.AutoMigrate = false
	shadowCfg.TablePrefix = shadowPrefix
	shadow, err := Open(ctx, shadowCfg)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("create rebuild schema: %w", err)
	}
	defer shadow.Close()
	if err := dropNormalizedTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.Migrate(ctx); err != nil {
		return RestructureResult{}, err
	}
	for _, def := range definitions {
		if err := shadow.UpsertMetric(ctx, def); err != nil {
			return RestructureResult{}, err
		}
	}

	progress := RestructureProgress{Phase: "copying", RowsTotal: rowsTotal, MetricsTotal: len(definitions)}
	for _, count := range remaining {
		if count == 0 {
			progress.MetricsDone++
		}
	}
	if report != nil {
		report(progress)
	}

	rowsCopied, err := s.copyLegacyPoints(ctx, shadow, definitions, remaining, &progress, report)
	if err != nil {
		return RestructureResult{}, err
	}
	rollupsCopied, err := s.copyLegacyRollups(ctx, shadow, remaining, &progress, report)
	if err != nil {
		return RestructureResult{}, err
	}
	rowsCopied += rollupsCopied
	if err := shadow.flushAllHotRollups(ctx); err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.rebuildDailyRollups(ctx); err != nil {
		return RestructureResult{}, err
	}
	if _, err := shadow.Compact(ctx, time.Now().UTC()); err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate rebuild schema before switch: %w", err)
	}

	progress.Phase = "switching"
	if report != nil {
		report(progress)
	}
	if err := s.replaceLegacyTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate rebuilt schema after switch: %w", err)
	}
	progress.Phase = "completed"
	progress.RowsDone = progress.RowsTotal
	progress.MetricsDone = progress.MetricsTotal
	if report != nil {
		report(progress)
	}
	return RestructureResult{RowsCopied: rowsCopied, Metrics: len(definitions)}, nil
}

func (s *Store) rebuildNormalizedSchema(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	if err := s.validateRestructurePrefix(); err != nil {
		return RestructureResult{}, err
	}
	definitions, err := s.ListMetrics(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate normalized data before rebuilding relationships: %w", err)
	}
	var rowsTotal int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.rollups)).Scan(&rowsTotal); err != nil {
		return RestructureResult{}, err
	}

	shadowCfg := s.cfg
	shadowCfg.DB = s.db
	shadowCfg.AutoMigrate = false
	shadowCfg.TablePrefix = s.cfg.TablePrefix + "rebuild_"
	shadow, err := Open(ctx, shadowCfg)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("create rebuild schema: %w", err)
	}
	defer shadow.Close()
	if err := dropNormalizedTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.Migrate(ctx); err != nil {
		return RestructureResult{}, err
	}
	for _, definition := range definitions {
		if err := shadow.UpsertMetric(ctx, definition); err != nil {
			return RestructureResult{}, err
		}
	}

	progress := RestructureProgress{Phase: "copying", RowsTotal: rowsTotal, MetricsTotal: len(definitions)}
	if report != nil {
		report(progress)
	}
	rowsCopied, err := s.copyNormalizedRollups(ctx, shadow, definitions, &progress, report)
	if err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate relationship rebuild before switch: %w", err)
	}
	progress.Phase = "switching"
	if report != nil {
		report(progress)
	}
	if err := s.replaceLegacyTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate relationship rebuild after switch: %w", err)
	}
	progress.Phase = "completed"
	progress.RowsDone = progress.RowsTotal
	progress.MetricsDone = progress.MetricsTotal
	if report != nil {
		report(progress)
	}
	return RestructureResult{RowsCopied: rowsCopied, Metrics: len(definitions)}, nil
}

func (s *Store) copyNormalizedRollups(ctx context.Context, shadow *Store, definitions []Definition, progress *RestructureProgress, report func(RestructureProgress)) (int64, error) {
	var copied int64
	for _, definition := range definitions {
		var offset int64
		for {
			rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT
				s.entity_id, s.tags, l.labels, d.resolution_milli, r.bucket_milli,
				r.count, r.sum, r.sum_sq, r.min_val, r.max_val,
				r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli,
				r.digest, r.created_at_milli
				FROM %s r
				JOIN %s s ON s.id = r.series_id
				JOIN %s d ON d.id = r.resolution_id
				JOIN %s l ON l.id = r.label_id
				WHERE s.metric_name = %s
				ORDER BY r.series_id, r.resolution_id, r.label_id, r.bucket_milli
				LIMIT %s OFFSET %s`,
				s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels,
				s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3)),
				definition.Name, restructureRollupWriteBatchSize, offset)
			if err != nil {
				return copied, err
			}
			batch := make([]PersistedRollup, 0, restructureRollupWriteBatchSize)
			for rows.Next() {
				var row PersistedRollup
				var tags, labels any
				var resolutionMilli, bucketMilli, firstMilli, lastMilli, createdMilli int64
				if err := rows.Scan(
					&row.EntityID, &tags, &labels, &resolutionMilli, &bucketMilli,
					&row.Count, &row.Sum, &row.SumSq, &row.Min, &row.Max,
					&row.FirstValue, &firstMilli, &row.LastValue, &lastMilli,
					&row.Digest, &createdMilli,
				); err != nil {
					_ = rows.Close()
					return copied, err
				}
				row.MetricName = definition.Name
				row.Resolution = time.Duration(resolutionMilli) * time.Millisecond
				row.Bucket = fromMillis(bucketMilli)
				row.FirstTime = fromMillis(firstMilli)
				row.LastTime = fromMillis(lastMilli)
				row.CreatedAt = fromMillis(createdMilli)
				row.Tags, err = decodeMap(tags)
				if err != nil {
					_ = rows.Close()
					return copied, err
				}
				row.Labels, err = decodeMap(labels)
				if err != nil {
					_ = rows.Close()
					return copied, err
				}
				row.Digest = append([]byte(nil), row.Digest...)
				batch = append(batch, row)
			}
			rowsErr := rows.Err()
			closeErr := rows.Close()
			if rowsErr != nil {
				return copied, rowsErr
			}
			if closeErr != nil {
				return copied, closeErr
			}
			if len(batch) == 0 {
				break
			}
			if err := shadow.ImportRollups(ctx, batch); err != nil {
				return copied, err
			}
			batchSize := int64(len(batch))
			copied += batchSize
			offset += batchSize
			progress.RowsDone += batchSize
			progress.Current = definition.Name
			if report != nil {
				report(*progress)
			}
			if len(batch) < restructureRollupWriteBatchSize {
				break
			}
		}
		progress.MetricsDone++
		if report != nil {
			report(*progress)
		}
	}
	return copied, nil
}

// DiscardHistory removes all historical samples. Legacy stores are upgraded to
// the normalized schema as part of the operation; already-normalized stores are
// cleared in place. Metric definitions and retention settings are preserved.
func (s *Store) DiscardHistory(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	if err := s.ensureOpen(); err != nil {
		return RestructureResult{}, err
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	if err := s.ensureOpen(); err != nil {
		return RestructureResult{}, err
	}

	shape, err := s.inspectSchema(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if shape.normalizedComplete() {
		return s.discardNormalizedHistory(ctx, report)
	}
	if !shape.hasAnyTable() {
		return RestructureResult{}, nil
	}
	if err := s.validateRestructurePrefix(); err != nil {
		return RestructureResult{}, err
	}

	definitions, err := s.readDefinitionsForShape(ctx, shape)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("preserve metric definitions before discarding history: %w", err)
	}
	rowsTotal, err := s.legacyRowCount(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if report != nil {
		report(RestructureProgress{Phase: "discarding", RowsTotal: rowsTotal, MetricsTotal: len(definitions)})
	}

	shadowCfg := s.cfg
	shadowCfg.DB = s.db
	shadowCfg.AutoMigrate = false
	shadowCfg.TablePrefix = s.cfg.TablePrefix + "rebuild_"
	shadow, err := Open(ctx, shadowCfg)
	if err != nil {
		return RestructureResult{}, fmt.Errorf("create rebuild schema: %w", err)
	}
	defer shadow.Close()
	if err := dropNormalizedTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := shadow.Migrate(ctx); err != nil {
		return RestructureResult{}, err
	}
	for _, definition := range definitions {
		if err := shadow.UpsertMetric(ctx, definition); err != nil {
			return RestructureResult{}, err
		}
	}
	if err := shadow.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate empty rebuild schema before switch: %w", err)
	}
	if report != nil {
		report(RestructureProgress{Phase: "discarding", RowsDone: rowsTotal, RowsTotal: rowsTotal, MetricsDone: len(definitions), MetricsTotal: len(definitions)})
		report(RestructureProgress{Phase: "switching", RowsDone: rowsTotal, RowsTotal: rowsTotal, MetricsDone: len(definitions), MetricsTotal: len(definitions)})
	}
	if err := s.replaceLegacyTables(ctx, shadow); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate empty rebuilt schema after switch: %w", err)
	}
	if report != nil {
		report(RestructureProgress{Phase: "completed", RowsDone: rowsTotal, RowsTotal: rowsTotal, MetricsDone: len(definitions), MetricsTotal: len(definitions)})
	}
	return RestructureResult{RowsCopied: rowsTotal, Metrics: len(definitions)}, nil
}

func (s *Store) discardNormalizedHistory(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()

	definitions, err := s.ListMetrics(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	rowsTotal, err := s.normalizedHistoryRowCount(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if report != nil {
		report(RestructureProgress{Phase: "discarding", RowsTotal: rowsTotal, MetricsTotal: len(definitions)})
	}
	deleteTables := make([]string, 0, 6)
	for _, table := range []string{s.tables.points, s.tables.watermarks, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return RestructureResult{}, err
		}
		if exists {
			deleteTables = append(deleteTables, table)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RestructureResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Delete rows in child-to-parent order. Keep obsolete-table DDL outside the
	// transaction: MySQL implicitly commits around DROP TABLE, which otherwise
	// makes a later FK failure impossible to roll back.
	for _, table := range deleteTables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return RestructureResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RestructureResult{}, err
	}
	var dropErr error
	obsoleteTables := []string{s.tables.points, s.tables.watermarks}
	if s.cfg.Driver == DriverMySQL {
		obsoleteTables = append(obsoleteTables, s.mysqlLegacyBackupTables()...)
	}
	for _, table := range obsoleteTables {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			dropErr = err
			break
		}
		if !exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			dropErr = err
			break
		}
	}
	s.rawMu.Lock()
	clear(s.raw)
	s.rawMu.Unlock()
	s.hotMu.Lock()
	clear(s.hot)
	clear(s.hotReplace)
	s.hotMu.Unlock()
	if err := s.validateNormalizedRestructure(ctx, len(definitions)); err != nil {
		return RestructureResult{}, fmt.Errorf("validate empty normalized schema: %w", err)
	}
	if dropErr != nil {
		return RestructureResult{}, fmt.Errorf("drop obsolete metric tables: %w", dropErr)
	}
	if report != nil {
		report(RestructureProgress{Phase: "discarding", RowsDone: rowsTotal, RowsTotal: rowsTotal, MetricsDone: len(definitions), MetricsTotal: len(definitions)})
		report(RestructureProgress{Phase: "completed", RowsDone: rowsTotal, RowsTotal: rowsTotal, MetricsDone: len(definitions), MetricsTotal: len(definitions)})
	}
	return RestructureResult{RowsCopied: rowsTotal, Metrics: len(definitions)}, nil
}

func (s *Store) normalizedHistoryRowCount(ctx context.Context) (int64, error) {
	var rows int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.rollups)).Scan(&rows); err != nil {
		return 0, err
	}
	pointsExist, err := s.tableExists(ctx, s.tables.points)
	if err != nil {
		return 0, err
	}
	if !pointsExist {
		return rows, nil
	}
	var points int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.points)).Scan(&points); err != nil {
		return 0, err
	}
	return rows + points, nil
}

func (s *Store) removeObsoleteRawTables(ctx context.Context, report func(RestructureProgress), shape metricSchemaShape) (RestructureResult, error) {
	var rows int64
	if shape.pointsExists {
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.points)).Scan(&rows); err != nil {
			return RestructureResult{}, err
		}
	}
	var definitions int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.definitions)).Scan(&definitions); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, definitions); err != nil {
		return RestructureResult{}, fmt.Errorf("validate normalized schema before removing obsolete tables: %w", err)
	}
	progress := RestructureProgress{Phase: "switching", RowsTotal: rows, MetricsTotal: definitions}
	if report != nil {
		report(progress)
	}
	for _, table := range []struct {
		name   string
		exists bool
	}{
		{name: s.tables.points, exists: shape.pointsExists},
		{name: s.tables.watermarks, exists: shape.watermarksExists},
	} {
		if !table.exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+table.name); err != nil {
			return RestructureResult{}, err
		}
	}
	if shape.mysqlBackupsExist {
		if err := s.finishInterruptedMySQLIndexes(ctx); err != nil {
			return RestructureResult{}, err
		}
		for _, table := range s.mysqlLegacyBackupTables() {
			if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
				return RestructureResult{}, err
			}
		}
	}
	if err := s.validateNormalizedRestructure(ctx, definitions); err != nil {
		return RestructureResult{}, fmt.Errorf("validate normalized schema after removing obsolete tables: %w", err)
	}
	progress.Phase = "completed"
	progress.RowsDone = rows
	progress.MetricsDone = definitions
	if report != nil {
		report(progress)
	}
	return RestructureResult{Metrics: definitions}, nil
}

func (s *Store) validateNormalizedRestructure(ctx context.Context, expectedDefinitions int) error {
	for _, table := range []string{s.tables.definitions, s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.rollups} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("required table %s is missing", table)
		}
	}
	var definitions int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.definitions)).Scan(&definitions); err != nil {
		return err
	}
	if definitions != expectedDefinitions {
		return fmt.Errorf("definition count = %d, want %d", definitions, expectedDefinitions)
	}
	var invalid int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s s
		LEFT JOIN %s d ON d.name = s.metric_name
		WHERE d.name IS NULL`, s.tables.series, s.tables.definitions)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("series contain %d missing metric definition references", invalid)
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r
		LEFT JOIN %s s ON s.id = r.series_id
		LEFT JOIN %s d ON d.id = r.resolution_id
		LEFT JOIN %s l ON l.id = r.label_id
		WHERE s.id IS NULL OR d.id IS NULL OR l.id IS NULL`, s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("rollups contain %d missing dictionary references", invalid)
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s
		WHERE count <= 0 OR min_val > max_val
		   OR (min_val = max_val AND digest IS NOT NULL)
		   OR (min_val <> max_val AND digest IS NULL)`, s.tables.rollups)).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("rollups contain %d invalid aggregate rows", invalid)
	}
	digestRows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT digest FROM %s WHERE digest IS NOT NULL", s.tables.rollups))
	if err != nil {
		return err
	}
	for digestRows.Next() {
		var blob []byte
		if err := digestRows.Scan(&blob); err != nil {
			_ = digestRows.Close()
			return err
		}
		digest, err := DecodeTDigest(blob)
		if err != nil {
			_ = digestRows.Close()
			return err
		}
		if digest.compression != s.cfg.RollupPolicy.compression() {
			_ = digestRows.Close()
			return fmt.Errorf("rollup t-digest compression = %v, want %v", digest.compression, s.cfg.RollupPolicy.compression())
		}
	}
	if err := digestRows.Err(); err != nil {
		_ = digestRows.Close()
		return err
	}
	if err := digestRows.Close(); err != nil {
		return err
	}
	allowed := make([]string, 0, len(s.cfg.RollupPolicy.Tiers))
	args := make([]any, 0, len(s.cfg.RollupPolicy.Tiers))
	for i, tier := range s.cfg.RollupPolicy.Tiers {
		allowed = append(allowed, s.dialect.placeholder(i+1))
		args = append(args, tier.Interval.Milliseconds())
	}
	if len(allowed) == 0 {
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.rollups)).Scan(&invalid); err != nil {
			return err
		}
	} else if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r
		JOIN %s d ON d.id = r.resolution_id
		WHERE d.resolution_milli NOT IN (%s)`, s.tables.rollups, s.tables.resolutions, strings.Join(allowed, ", ")), args...).Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return fmt.Errorf("rollups contain %d unsupported resolutions", invalid)
	}
	if s.cfg.Driver == DriverSQLite {
		var result string
		if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(result), "ok") {
			return fmt.Errorf("sqlite quick_check: %s", result)
		}
	}
	return nil
}

func (s *Store) validateRestructurePrefix() error {
	switch s.cfg.Driver {
	case DriverMySQL:
		if len(s.cfg.TablePrefix) > 27 {
			return fmt.Errorf("%w: table prefix is too long for MySQL rebuild identifiers", ErrInvalidArgument)
		}
	case DriverPostgreSQL:
		if len(s.cfg.TablePrefix) > 26 {
			return fmt.Errorf("%w: table prefix is too long for PostgreSQL rebuild identifiers", ErrInvalidArgument)
		}
	}
	return nil
}

func (s *Store) readDefinitionsForShape(ctx context.Context, shape metricSchemaShape) ([]Definition, error) {
	if shape.definitionsNormalized {
		return s.ListMetrics(ctx)
	}
	if shape.definitionsLegacy {
		return s.readLegacyDefinitions(ctx)
	}
	return nil, fmt.Errorf("metric definitions table is missing or has an unsupported column layout")
}

func (s *Store) readLegacyDefinitions(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT name, type, unit, description, retention_days, metadata, created_at, updated_at FROM %s ORDER BY name`, s.tables.definitions))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	defs := make([]Definition, 0)
	for rows.Next() {
		var def Definition
		var typ string
		var metadata any
		var created, updated int64
		if err := rows.Scan(&def.Name, &typ, &def.Unit, &def.Description, &def.RetentionDays, &metadata, &created, &updated); err != nil {
			return nil, err
		}
		values, err := decodeMap(metadata)
		if err != nil {
			return nil, err
		}
		def.Type, def.Metadata = MetricType(typ), values
		def.CreatedAt, def.UpdatedAt = time.Unix(0, created).UTC(), time.Unix(0, updated).UTC()
		defs = append(defs, def)
	}
	return defs, rows.Err()
}

func (s *Store) legacyRowCount(ctx context.Context) (int64, error) {
	var total int64
	for _, table := range []string{s.tables.points, s.tables.rollups} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return 0, err
		}
		if !exists {
			continue
		}
		var rows int64
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&rows); err != nil {
			return 0, err
		}
		total += rows
	}
	return total, nil
}

func (s *Store) legacyMetricRowCounts(ctx context.Context, definitions []Definition) (map[string]int64, error) {
	counts := make(map[string]int64, len(definitions))
	for _, def := range definitions {
		counts[def.Name] = 0
	}
	for _, table := range []string{s.tables.points, s.tables.rollups} {
		exists, err := s.tableExists(ctx, table)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT metric_name, COUNT(*) FROM %s GROUP BY metric_name", table))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			var count int64
			if err := rows.Scan(&name, &count); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if _, known := counts[name]; known {
				counts[name] += count
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return counts, nil
}

func advanceRestructureMetric(progress *RestructureProgress, remaining map[string]int64, name string) {
	count, known := remaining[name]
	if !known || count <= 0 {
		return
	}
	count--
	remaining[name] = count
	if count == 0 {
		progress.MetricsDone++
	}
}

func (s *Store) copyLegacyPoints(ctx context.Context, shadow *Store, definitions []Definition, remaining map[string]int64, progress *RestructureProgress, report func(RestructureProgress)) (int64, error) {
	exists, err := s.tableExists(ctx, s.tables.points)
	if err != nil || !exists {
		return 0, err
	}
	const batchSize = 1000
	known := make(map[string]struct{}, len(definitions))
	for _, def := range definitions {
		known[def.Name] = struct{}{}
	}
	var after, copied int64
	for {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, metric_name, entity_id, ts_nano, value, tags, labels FROM %s WHERE id > %s ORDER BY id ASC LIMIT %s`, s.tables.points, s.dialect.placeholder(1), s.dialect.placeholder(2)), after, batchSize)
		if err != nil {
			return copied, err
		}
		points := make([]Point, 0, batchSize)
		batchScanned := int64(0)
		for rows.Next() {
			var id, timestamp int64
			var name, entityID string
			var value float64
			var tags, labels any
			if err := rows.Scan(&id, &name, &entityID, &timestamp, &value, &tags, &labels); err != nil {
				_ = rows.Close()
				return copied, err
			}
			after = id
			batchScanned++
			advanceRestructureMetric(progress, remaining, name)
			if _, ok := known[name]; !ok {
				continue
			}
			tagMap, err := decodeMap(tags)
			if err != nil {
				_ = rows.Close()
				return copied, err
			}
			labelMap, err := decodeMap(labels)
			if err != nil {
				_ = rows.Close()
				return copied, err
			}
			points = append(points, Point{MetricName: name, EntityID: entityID, Timestamp: time.Unix(0, timestamp).UTC(), Value: value, Tags: tagMap, Labels: labelMap})
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return copied, err
		}
		if closeErr != nil {
			return copied, closeErr
		}
		if batchScanned == 0 {
			break
		}
		if len(points) > 0 {
			if err := shadow.WriteBatch(ctx, points); err != nil {
				return copied, err
			}
			copied += int64(len(points))
		}
		progress.RowsDone += batchScanned
		if len(points) > 0 {
			progress.Current = points[len(points)-1].MetricName
		}
		if report != nil {
			report(*progress)
		}
		if batchScanned < batchSize {
			break
		}
	}
	return copied, nil
}

func (s *Store) copyLegacyRollups(ctx context.Context, shadow *Store, remaining map[string]int64, progress *RestructureProgress, report func(RestructureProgress)) (int64, error) {
	exists, err := s.tableExists(ctx, s.tables.rollups)
	if err != nil || !exists {
		return 0, err
	}
	cache := newRollupDictionaryCache()
	var after, copied int64
	for {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest FROM %s WHERE id > %s ORDER BY id ASC LIMIT %s`, s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2)), after, restructureRollupReadBatchSize)
		if err != nil {
			return copied, err
		}
		groups := make(map[string]*restructureRollupGroup)
		batchScanned := int64(0)
		for rows.Next() {
			var id, resolution, bucket, count, firstTS, lastTS int64
			var name, entityID, tagsHash string
			var tags any
			var sum, sumSq, min, max, firstVal, lastVal float64
			var digest []byte
			if err := rows.Scan(&id, &name, &entityID, &tagsHash, &tags, &resolution, &bucket, &count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS, &digest); err != nil {
				_ = rows.Close()
				return copied, err
			}
			after = id
			batchScanned++
			advanceRestructureMetric(progress, remaining, name)
			if _, known := remaining[name]; !known {
				continue
			}
			tagsJSON, err := rawJSONToString(tags)
			if err != nil {
				_ = rows.Close()
				return copied, err
			}
			d, err := digestFromRollup(count, min, max, digest, shadow.cfg.RollupPolicy.compression())
			if err != nil {
				_ = rows.Close()
				return copied, err
			}
			compressedDigest := NewTDigest(shadow.cfg.RollupPolicy.compression())
			compressedDigest.Merge(d)
			key := rollupKey{entityID: entityID, tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: bucket / int64(time.Millisecond)}
			bucketData := &rollupBucket{count: count, sum: sum, sumSq: sumSq, min: min, max: max, firstVal: firstVal, firstTS: firstTS / int64(time.Millisecond), lastVal: lastVal, lastTS: lastTS / int64(time.Millisecond), digest: compressedDigest, tagsHash: tagsHash, tagsJSON: tagsJSON, labelsHash: emptyLabelsHash, labelsJSON: "{}"}
			groupKey := name + "\x00" + fmt.Sprint(resolution)
			item := groups[groupKey]
			if item == nil {
				item = &restructureRollupGroup{name: name, interval: time.Duration(resolution), buckets: make(map[rollupKey]*rollupBucket)}
				groups[groupKey] = item
			}
			item.buckets[key] = bucketData
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return copied, err
		}
		if closeErr != nil {
			return copied, closeErr
		}
		if batchScanned == 0 {
			break
		}
		if len(groups) > 0 {
			tx, err := shadow.db.BeginTx(ctx, nil)
			if err != nil {
				return copied, err
			}
			groupKeys := make([]string, 0, len(groups))
			for key := range groups {
				groupKeys = append(groupKeys, key)
			}
			sort.Strings(groupKeys)
			for _, key := range groupKeys {
				item := groups[key]
				if _, err := shadow.writeRestructureRollupGroupTx(ctx, item, cache, tx); err != nil {
					_ = tx.Rollback()
					return copied, err
				}
				progress.Current = item.name
			}
			if err := tx.Commit(); err != nil {
				return copied, err
			}
		}
		batchCopied := int64(0)
		for _, item := range groups {
			batchCopied += int64(len(item.buckets))
		}
		copied += batchCopied
		progress.RowsDone += batchScanned
		if report != nil {
			report(*progress)
		}
		if batchScanned < restructureRollupReadBatchSize {
			break
		}
	}
	return copied, nil
}

func (s *Store) writeRestructureRollupGroupTx(ctx context.Context, group *restructureRollupGroup, cache *rollupDictionaryCache, tx *sql.Tx) (int, error) {
	resolutionID, err := cache.resolutionID(ctx, s, tx, group.interval)
	if err != nil {
		return 0, err
	}
	keys := make([]rollupKey, 0, len(group.buckets))
	for key := range group.buckets {
		keys = append(keys, key)
	}
	sortRollupKeys(keys)
	createdAt := timeMillis(time.Now())
	written := 0
	batch := make([]normalizedRollupRow, 0, min(restructureRollupWriteBatchSize, len(keys)))
	flush := func() error {
		if err := s.upsertNormalizedRollupRowsTx(ctx, batch, tx); err != nil {
			return err
		}
		written += len(batch)
		batch = batch[:0]
		return nil
	}
	for _, key := range keys {
		bucket := group.buckets[key]
		key.bucket = normalizeBucketMillis(key.bucket)
		seriesID, err := cache.seriesID(ctx, s, tx, group.name, key, bucket.tagsJSON)
		if err != nil {
			return written, err
		}
		labelID, err := cache.labelID(ctx, s, tx, key.labelsHash, bucket.labelsJSON)
		if err != nil {
			return written, err
		}
		batch = append(batch, normalizedRollupRow{
			seriesID: seriesID, resolutionID: resolutionID, labelID: labelID,
			bucketMilli: key.bucket, count: bucket.count, sum: bucket.sum, sumSq: bucket.sumSq,
			min: bucket.min, max: bucket.max, firstVal: bucket.firstVal, firstTSMilli: bucket.firstTS,
			lastVal: bucket.lastVal, lastTSMilli: bucket.lastTS, digest: bucket.encodedDigest(),
			createdAtMilli: createdAt,
		})
		if len(batch) == restructureRollupWriteBatchSize {
			if err := flush(); err != nil {
				return written, err
			}
		}
	}
	if len(batch) > 0 {
		if err := flush(); err != nil {
			return written, err
		}
	}
	return written, nil
}

func (s *Store) rebuildDailyRollups(ctx context.Context) error {
	var hourly, daily time.Duration
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		switch tier.Interval {
		case time.Hour:
			hourly = tier.Interval
		case 24 * time.Hour:
			daily = tier.Interval
		}
	}
	if hourly == 0 || daily == 0 {
		return nil
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cache := newRollupDictionaryCache()
	for _, def := range defs {
		rows, err := s.scanRollupRows(ctx, tx, def.Name, hourly)
		if err != nil {
			return err
		}
		buckets := make(map[rollupKey]*rollupBucket)
		for _, row := range rows {
			key := rollupKey{entityID: row.entityID, tagsHash: row.bucketData.tagsHash, labelsHash: row.bucketData.labelsHash, bucket: bucketStartMillis(row.bucket, daily.Milliseconds())}
			bucket := buckets[key]
			if bucket == nil {
				bucket = newRollupBucket(s.cfg.RollupPolicy.compression())
				bucket.tagsHash, bucket.tagsJSON = row.bucketData.tagsHash, row.bucketData.tagsJSON
				bucket.labelsHash, bucket.labelsJSON = row.bucketData.labelsHash, row.bucketData.labelsJSON
				buckets[key] = bucket
			}
			bucket.mergeStored(row.bucketData)
		}

		// Replace only days backed by hourly rows. This removes any partial daily
		// contribution already cascaded from copied raw points, while preserving
		// older legacy daily buckets that no longer have hourly coverage.
		keys := make([]rollupKey, 0, len(buckets))
		for key := range buckets {
			keys = append(keys, key)
		}
		sortRollupKeys(keys)
		for _, key := range keys {
			if err := s.upsertRollupWithDictionaryTx(ctx, def.Name, daily, key, buckets[key], cache, tx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func dropNormalizedTables(ctx context.Context, s *Store) error {
	for _, name := range []string{s.tables.points, s.tables.watermarks, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.definitions, s.tables.state} {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) replaceLegacyTables(ctx context.Context, shadow *Store) error {
	if s.cfg.Driver == DriverMySQL {
		return s.replaceLegacyMySQLTables(ctx, shadow)
	}
	if err := shadow.dropNormalizedIndexes(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range []string{s.tables.points, s.tables.watermarks, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.definitions, s.tables.state} {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return err
		}
	}
	pairs := [][2]string{{shadow.tables.definitions, s.tables.definitions}, {shadow.tables.series, s.tables.series}, {shadow.tables.labels, s.tables.labels}, {shadow.tables.resolutions, s.tables.resolutions}, {shadow.tables.rollups, s.tables.rollups}, {shadow.tables.state, s.tables.state}}
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, renameTableSQL(s.cfg.Driver, pair[0], pair[1])); err != nil {
			return err
		}
	}
	if err := s.createPortableNormalizedIndexes(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// replaceLegacyMySQLTables keeps the old point-backed tables recoverable until
// every normalized table has become visible. MySQL DDL does not participate in
// transactions, but one multi-table RENAME TABLE statement is atomic.
func (s *Store) replaceLegacyMySQLTables(ctx context.Context, shadow *Store) error {
	backupTables := s.mysqlLegacyBackupTables()
	sourceTables := s.mysqlLegacySourceTables()
	legacy := make([][2]string, 0, len(sourceTables))
	for i, source := range sourceTables {
		backup := backupTables[i]
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+backup); err != nil {
			return err
		}
		exists, err := s.tableExists(ctx, source)
		if err != nil {
			return err
		}
		if exists {
			legacy = append(legacy, [2]string{source, backup})
		}
	}
	// The maintenance state belongs to the rebuilt normalized schema. Drop the
	// old cursor before the atomic rename so a rebuild starts with fresh state.
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+s.tables.state); err != nil {
		return err
	}
	pairs := append(legacy, [][2]string{
		{shadow.tables.definitions, s.tables.definitions},
		{shadow.tables.series, s.tables.series},
		{shadow.tables.labels, s.tables.labels},
		{shadow.tables.resolutions, s.tables.resolutions},
		{shadow.tables.rollups, s.tables.rollups},
		{shadow.tables.state, s.tables.state},
	}...)
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair[0]+" TO "+pair[1])
	}
	if _, err := s.db.ExecContext(ctx, "RENAME TABLE "+strings.Join(parts, ", ")); err != nil {
		return err
	}
	// Finish the live schema before removing backups. If backup cleanup is
	// interrupted, the normalized tables still retain their canonical indexes.
	if err := s.finishInterruptedMySQLIndexes(ctx); err != nil {
		return err
	}
	for _, pair := range legacy {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+pair[1]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) finishInterruptedMySQLIndexes(ctx context.Context) error {
	if err := s.renameMySQLRebuildIndexes(ctx, s.cfg.TablePrefix+"rebuild_"); err != nil {
		return err
	}
	return s.createNormalizedIndexes(ctx)
}

func (s *Store) renameMySQLRebuildIndexes(ctx context.Context, rebuildPrefix string) error {
	current := s.normalizedIndexes()
	rebuild := normalizedIndexesFor(rebuildPrefix, s.tables)
	for i := range current {
		if current[i].name == rebuild[i].name {
			continue
		}
		exists, err := s.mysqlIndexExists(ctx, current[i].table, rebuild[i].name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		canonicalExists, err := s.mysqlIndexExists(ctx, current[i].table, current[i].name)
		if err != nil {
			return err
		}
		if canonicalExists {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", current[i].table, rebuild[i].name)); err != nil {
				return err
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME INDEX %s TO %s", current[i].table, rebuild[i].name, current[i].name)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	_, err := s.db.ExecContext(ctx, "SELECT 1 FROM "+name+" WHERE 1 = 0")
	if err == nil {
		return true, nil
	}
	if isMissingTableError(err) {
		return false, nil
	}
	return false, err
}

func renameTableSQL(driver Driver, source, target string) string {
	if driver == DriverMySQL {
		return "RENAME TABLE " + source + " TO " + target
	}
	return "ALTER TABLE " + source + " RENAME TO " + target
}

var emptyLabelsHash = func() string { hash, _, _ := tagsFingerprint(map[string]string{}); return hash }()

func isMissingTableError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such table") || strings.Contains(text, "doesn't exist") ||
		(strings.Contains(text, "relation ") && strings.Contains(text, "does not exist")) ||
		strings.Contains(text, "sqlstate 42p01")
}

func isMissingColumnError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such column") || strings.Contains(text, "unknown column") ||
		(strings.Contains(text, "column ") && strings.Contains(text, "does not exist")) ||
		strings.Contains(text, "sqlstate 42703")
}
