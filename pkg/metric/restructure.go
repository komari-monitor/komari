package metric

import (
	"context"
	"fmt"
	"strings"
	"time"
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

// LegacyStorageSize measures the pre-rebuild tables. It is intentionally kept
// separate from StorageSize because the old schema includes metric_points.
func (s *Store) LegacyStorageSize(ctx context.Context) (int64, error) {
	if s.cfg.Driver == DriverSQLite {
		return s.StorageSize(ctx)
	}
	names := []string{s.tables.definitions, s.tables.points, s.tables.rollups, s.tables.watermarks}
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
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("SELECT created_at_milli FROM %s WHERE 1 = 0", s.tables.definitions))
	if err == nil {
		return s.tableExists(ctx, s.tables.points)
	}
	if isMissingTableError(err) {
		return false, nil
	}
	if isMissingColumnError(err) {
		return true, nil
	}
	return false, err
}

// Restructure rebuilds an old store into normalized dictionary and rollup
// tables. It is intentionally explicit and intended for the
// authenticated upgrade guide; normal startup never mutates an existing schema.
func (s *Store) Restructure(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	if err := s.ensureOpen(); err != nil {
		return RestructureResult{}, err
	}
	needs, err := s.NeedsRestructure(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if !needs {
		return RestructureResult{}, nil
	}
	normalized, err := s.hasMillisecondDefinitions(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	if normalized {
		return s.removeObsoleteRawTable(ctx, report)
	}
	if err := s.validateRestructurePrefix(); err != nil {
		return RestructureResult{}, err
	}

	shadowPrefix := s.cfg.TablePrefix + "rebuild_"
	shadowCfg := s.cfg
	shadowCfg.DB = s.db
	shadowCfg.AutoMigrate = true
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

	definitions, err := s.readLegacyDefinitions(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	for _, def := range definitions {
		if err := shadow.UpsertMetric(ctx, def); err != nil {
			return RestructureResult{}, err
		}
	}

	rowsTotal, err := s.legacyRowCount(ctx)
	if err != nil {
		return RestructureResult{}, err
	}
	remaining, err := s.legacyMetricRowCounts(ctx, definitions)
	if err != nil {
		return RestructureResult{}, err
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

func (s *Store) hasMillisecondDefinitions(ctx context.Context) (bool, error) {
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("SELECT created_at_milli FROM %s WHERE 1 = 0", s.tables.definitions))
	if err == nil {
		return true, nil
	}
	if isMissingColumnError(err) || isMissingTableError(err) {
		return false, nil
	}
	return false, err
}

func (s *Store) removeObsoleteRawTable(ctx context.Context, report func(RestructureProgress)) (RestructureResult, error) {
	var rows int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.points)).Scan(&rows); err != nil {
		return RestructureResult{}, err
	}
	var definitions int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.definitions)).Scan(&definitions); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, definitions); err != nil {
		return RestructureResult{}, fmt.Errorf("validate normalized schema before removing raw table: %w", err)
	}
	progress := RestructureProgress{Phase: "switching", RowsTotal: rows, MetricsTotal: definitions}
	if report != nil {
		report(progress)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.points); err != nil {
		return RestructureResult{}, err
	}
	if err := s.validateNormalizedRestructure(ctx, definitions); err != nil {
		return RestructureResult{}, fmt.Errorf("validate normalized schema after removing raw table: %w", err)
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
	var points, rollups int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.points)).Scan(&points); err != nil {
		return 0, err
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", s.tables.rollups)).Scan(&rollups); err != nil {
		return 0, err
	}
	return points + rollups, nil
}

func (s *Store) legacyMetricRowCounts(ctx context.Context, definitions []Definition) (map[string]int64, error) {
	counts := make(map[string]int64, len(definitions))
	for _, def := range definitions {
		counts[def.Name] = 0
	}
	for _, table := range []string{s.tables.points, s.tables.rollups} {
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
	type group struct {
		name     string
		interval time.Duration
		buckets  map[rollupKey]*rollupBucket
	}
	const batchSize = 1000
	var after, copied int64
	for {
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest FROM %s WHERE id > %s ORDER BY id ASC LIMIT %s`, s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2)), after, batchSize)
		if err != nil {
			return copied, err
		}
		groups := make(map[string]*group)
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
				item = &group{name: name, interval: time.Duration(resolution), buckets: make(map[rollupKey]*rollupBucket)}
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
			for _, item := range groups {
				if _, err := shadow.writeRollupBucketsTx(ctx, item.name, item.interval, item.buckets, tx); err != nil {
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
		if batchScanned < batchSize {
			break
		}
	}
	return copied, nil
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
			if err := s.upsertRollupTx(ctx, def.Name, daily, key, buckets[key], tx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func dropNormalizedTables(ctx context.Context, s *Store) error {
	for _, name := range []string{s.tables.points, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.definitions} {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) replaceLegacyTables(ctx context.Context, shadow *Store) error {
	if err := shadow.dropNormalizedIndexes(ctx); err != nil {
		return err
	}
	if s.cfg.Driver == DriverMySQL {
		return s.replaceLegacyMySQLTables(ctx, shadow)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range []string{s.tables.watermarks, s.tables.points, s.tables.rollups, s.tables.definitions} {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+name); err != nil {
			return err
		}
	}
	pairs := [][2]string{{shadow.tables.definitions, s.tables.definitions}, {shadow.tables.series, s.tables.series}, {shadow.tables.labels, s.tables.labels}, {shadow.tables.resolutions, s.tables.resolutions}, {shadow.tables.rollups, s.tables.rollups}}
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, renameTableSQL(s.cfg.Driver, pair[0], pair[1])); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.createNormalizedIndexes(ctx)
}

// replaceLegacyMySQLTables keeps the old point-backed tables recoverable until
// every normalized table has become visible. MySQL DDL does not participate in
// transactions, but one multi-table RENAME TABLE statement is atomic.
func (s *Store) replaceLegacyMySQLTables(ctx context.Context, shadow *Store) error {
	legacy := [][2]string{
		{s.tables.definitions, s.tables.definitions + "_legacy"},
		{s.tables.points, s.tables.points + "_legacy"},
		{s.tables.rollups, s.tables.rollups + "_legacy"},
	}
	if exists, err := s.tableExists(ctx, s.tables.watermarks); err != nil {
		return err
	} else if exists {
		legacy = append(legacy, [2]string{s.tables.watermarks, s.tables.watermarks + "_legacy"})
	}
	for _, pair := range legacy {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+pair[1]); err != nil {
			return err
		}
	}
	pairs := append(legacy, [][2]string{
		{shadow.tables.definitions, s.tables.definitions},
		{shadow.tables.series, s.tables.series},
		{shadow.tables.labels, s.tables.labels},
		{shadow.tables.resolutions, s.tables.resolutions},
		{shadow.tables.rollups, s.tables.rollups},
	}...)
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, pair[0]+" TO "+pair[1])
	}
	if _, err := s.db.ExecContext(ctx, "RENAME TABLE "+strings.Join(parts, ", ")); err != nil {
		return err
	}
	for _, pair := range legacy {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+pair[1]); err != nil {
			return err
		}
	}
	return s.createNormalizedIndexes(ctx)
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
	return strings.Contains(text, "no such table") || strings.Contains(text, "doesn't exist") || strings.Contains(text, "does not exist")
}

func isMissingColumnError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such column") || strings.Contains(text, "unknown column") || strings.Contains(text, "does not exist")
}
