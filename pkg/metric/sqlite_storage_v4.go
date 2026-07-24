package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	sqliteStorageVersionV4 = 4
	sqliteV4HotWindow      = 5 * time.Minute
)

// migrateSQLiteStorageV4 first brings legacy layouts to the normalized V3
// schema, then atomically moves raw values and long-term rollups into verified
// lossless blocks. Databases created by the first V4 test build have point
// blocks but no rollup blocks; they are upgraded in place without re-encoding
// the already migrated points.
func (s *Store) migrateSQLiteStorageV4(ctx context.Context) error {
	if err := s.migrateSQLiteStorageV3(ctx); err != nil {
		return err
	}
	pointKind, err := sqliteObjectType(ctx, s.db, s.tables.pointBlocks)
	if err != nil {
		return err
	}
	rollupKind, err := sqliteObjectType(ctx, s.db, s.tables.rollupBlocks)
	if err != nil {
		return err
	}
	if pointKind != "" && pointKind != "table" {
		return fmt.Errorf("metric: SQLite V4 object %s has unexpected type %q", s.tables.pointBlocks, pointKind)
	}
	if rollupKind != "" && rollupKind != "table" {
		return fmt.Errorf("metric: SQLite V4 object %s has unexpected type %q", s.tables.rollupBlocks, rollupKind)
	}
	if pointKind == "table" && rollupKind == "table" {
		s.sqliteStorageV4 = true
		return s.ensureSQLiteStorageV4(ctx)
	}

	log.Printf("metric: migrating SQLite metric storage to V%d", sqliteStorageVersionV4)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin SQLite V4 migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.createSQLiteV4PointBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.createSQLiteV4RollupBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.validateSQLiteV4PointBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.validateSQLiteV4RollupBlocks(ctx, tx); err != nil {
		return err
	}
	var pointSourceCount, rollupSourceCount int64
	if pointKind == "" {
		pointSourceCount, err = sqliteTableRowCountTx(ctx, tx, s.tables.pointValues)
		if err != nil {
			return err
		}
		sealed, err := s.sealSQLiteV4PointsTx(ctx, tx, "", math.MaxInt64)
		if err != nil {
			return fmt.Errorf("metric: encode SQLite V4 point blocks: %w", err)
		}
		if sealed != pointSourceCount {
			return fmt.Errorf("metric: SQLite V4 point count mismatch: source=%d encoded=%d", pointSourceCount, sealed)
		}
	}
	if rollupKind == "" {
		rollupSourceCount, err = sqliteTableRowCountTx(ctx, tx, s.tables.rollupValues)
		if err != nil {
			return err
		}
		sealed, err := s.sealAllSQLiteV4RollupsTx(ctx, tx)
		if err != nil {
			return fmt.Errorf("metric: encode SQLite V4 rollup blocks: %w", err)
		}
		if sealed != rollupSourceCount {
			return fmt.Errorf("metric: SQLite V4 rollup count mismatch: source=%d encoded=%d", rollupSourceCount, sealed)
		}
	}
	var pointHotCount, pointBlockCount, rollupHotCount, rollupBlockCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.tables.pointValues).Scan(&pointHotCount); err != nil {
		return fmt.Errorf("metric: validate SQLite V4 hot points: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(point_count), 0) FROM `+s.tables.pointBlocks).Scan(&pointBlockCount); err != nil {
		return fmt.Errorf("metric: validate SQLite V4 point blocks: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.tables.rollupValues).Scan(&rollupHotCount); err != nil {
		return fmt.Errorf("metric: validate SQLite V4 hot rollups: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bucket_count), 0) FROM `+s.tables.rollupBlocks).Scan(&rollupBlockCount); err != nil {
		return fmt.Errorf("metric: validate SQLite V4 rollup blocks: %w", err)
	}
	if pointKind == "" && (pointHotCount != 0 || pointBlockCount != pointSourceCount) {
		return fmt.Errorf("metric: SQLite V4 point validation failed: source=%d hot=%d blocks=%d", pointSourceCount, pointHotCount, pointBlockCount)
	}
	if rollupKind == "" && (rollupHotCount != 0 || rollupBlockCount != rollupSourceCount) {
		return fmt.Errorf("metric: SQLite V4 rollup validation failed: source=%d hot=%d blocks=%d", rollupSourceCount, rollupHotCount, rollupBlockCount)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit SQLite V4 migration: %w", err)
	}
	s.sqliteStorageV4 = true
	if err := s.fullSQLiteVacuum(ctx); err != nil {
		// The V4 transaction is already fully validated and committed. A failed
		// physical rewrite does not invalidate the logical migration.
		log.Printf("metric: SQLite V4 post-migration vacuum skipped: %v", err)
	}
	log.Printf("metric: migrated SQLite metric storage to V%d (%d raw points and %d rollups preserved bit-for-bit)", sqliteStorageVersionV4, pointSourceCount, rollupSourceCount)
	return nil
}

func (s *Store) ensureSQLiteStorageV4(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metric: begin SQLite V4 verification: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.createSQLiteV4PointBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.createSQLiteV4RollupBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.validateSQLiteV4PointBlocks(ctx, tx); err != nil {
		return err
	}
	if err := s.validateSQLiteV4RollupBlocks(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metric: commit SQLite V4 verification: %w", err)
	}
	return nil
}

func (s *Store) validateSQLiteV4PointBlocks(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+s.tables.pointBlocks+`)`)
	if err != nil {
		return fmt.Errorf("metric: inspect SQLite V4 point block table: %w", err)
	}
	defer rows.Close()
	found := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range []string{"series_id", "start_nano", "end_nano", "point_count", "codec", "checksum", "payload"} {
		if !found[name] {
			return fmt.Errorf("metric: SQLite V4 point block table is missing column %s", name)
		}
	}
	return nil
}

func (s *Store) createSQLiteV4PointBlocks(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			series_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			start_nano BIGINT NOT NULL,
			end_nano BIGINT NOT NULL,
			point_count INTEGER NOT NULL,
			codec INTEGER NOT NULL,
			checksum INTEGER NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY(series_id, start_nano),
			CHECK(end_nano >= start_nano),
			CHECK(point_count > 0)
		) WITHOUT ROWID`, s.tables.pointBlocks, s.tables.series),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_point_blocks_time_idx ON %s (start_nano, end_nano)`, s.cfg.TablePrefix, s.tables.pointBlocks),
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("metric: create SQLite V4 point block table: %w", err)
		}
	}
	return nil
}

type sqliteV4Series struct {
	id         int64
	metricName string
	entityID   string
	tagsHash   string
	tagsJSON   string
	tags       map[string]string
}

type sqliteV4PointKey struct {
	seriesID int64
	tsNano   int64
}

type sqliteV4StoredPoint struct {
	series sqliteV4Series
	block  sqliteV4BlockPoint
}

func (s *Store) querySQLiteV4Snapshot(ctx context.Context, query Query) ([]Point, error) {
	tx, err := s.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	points, err := s.querySQLiteV4(ctx, tx, query)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return points, nil
}

func (s *Store) querySQLiteV4(ctx context.Context, q querier, query Query) ([]Point, error) {
	query = query.normalized()
	series, err := s.sqliteV4MatchingSeries(ctx, q, query.MetricName, query.EntityID, query.Tags)
	if err != nil || len(series) == 0 {
		return nil, err
	}
	seriesByID := make(map[int64]sqliteV4Series, len(series))
	for _, item := range series {
		seriesByID[item.id] = item
	}
	startNano, endNano := query.Start.UnixNano(), query.End.UnixNano()
	stored := make(map[sqliteV4PointKey]sqliteV4StoredPoint)

	seriesWhere, seriesArgs := sqliteV4SeriesIDClause(series)
	blockArgs := append(append([]any{}, seriesArgs...), startNano, endNano)
	blockRows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT series_id, start_nano, end_nano, point_count, codec, checksum, payload
		 FROM %s WHERE series_id IN (%s) AND end_nano >= ? AND start_nano <= ?`,
		s.tables.pointBlocks, seriesWhere,
	), blockArgs...)
	if err != nil {
		return nil, err
	}
	for blockRows.Next() {
		var seriesID, blockStart, blockEnd int64
		var count, codec int
		var checksum int64
		var payload []byte
		if err := blockRows.Scan(&seriesID, &blockStart, &blockEnd, &count, &codec, &checksum, &payload); err != nil {
			_ = blockRows.Close()
			return nil, err
		}
		points, err := decodeSQLiteV4Block(codec, count, uint32(checksum), payload)
		if err != nil {
			_ = blockRows.Close()
			return nil, fmt.Errorf("metric: decode SQLite V4 block series=%d start=%d: %w", seriesID, blockStart, err)
		}
		if len(points) == 0 || points[0].timestamp != blockStart || points[len(points)-1].timestamp != blockEnd {
			_ = blockRows.Close()
			return nil, fmt.Errorf("metric: SQLite V4 block boundary mismatch for series=%d start=%d", seriesID, blockStart)
		}
		for _, point := range points {
			if point.timestamp < startNano || point.timestamp > endNano {
				continue
			}
			stored[sqliteV4PointKey{seriesID: seriesID, tsNano: point.timestamp}] = sqliteV4StoredPoint{series: seriesByID[seriesID], block: point}
		}
	}
	if err := blockRows.Err(); err != nil {
		_ = blockRows.Close()
		return nil, err
	}
	if err := blockRows.Close(); err != nil {
		return nil, err
	}

	hotArgs := append(append([]any{}, seriesArgs...), startNano, endNano)
	hotRows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT series_id, ts_nano, value, labels, created_at FROM %s
		 WHERE series_id IN (%s) AND ts_nano >= ? AND ts_nano <= ?`,
		s.tables.pointValues, seriesWhere,
	), hotArgs...)
	if err != nil {
		return nil, err
	}
	for hotRows.Next() {
		var seriesID int64
		var point sqliteV4BlockPoint
		var value float64
		var rawLabels any
		if err := hotRows.Scan(&seriesID, &point.timestamp, &value, &rawLabels, &point.createdAt); err != nil {
			_ = hotRows.Close()
			return nil, err
		}
		point.valueBits = math.Float64bits(value)
		point.labels, err = rawTagsToJSON(rawLabels)
		if err != nil {
			_ = hotRows.Close()
			return nil, err
		}
		// Hot writes win over an older sealed value at the same series/timestamp.
		stored[sqliteV4PointKey{seriesID: seriesID, tsNano: point.timestamp}] = sqliteV4StoredPoint{series: seriesByID[seriesID], block: point}
	}
	if err := hotRows.Err(); err != nil {
		_ = hotRows.Close()
		return nil, err
	}
	if err := hotRows.Close(); err != nil {
		return nil, err
	}

	ordered := make([]sqliteV4StoredPoint, 0, len(stored))
	for _, point := range stored {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].block.timestamp != ordered[j].block.timestamp {
			if query.Order == OrderDesc {
				return ordered[i].block.timestamp > ordered[j].block.timestamp
			}
			return ordered[i].block.timestamp < ordered[j].block.timestamp
		}
		if ordered[i].series.entityID != ordered[j].series.entityID {
			return ordered[i].series.entityID < ordered[j].series.entityID
		}
		return ordered[i].series.tagsJSON < ordered[j].series.tagsJSON
	})
	start := query.Offset
	if start > len(ordered) {
		start = len(ordered)
	}
	end := len(ordered)
	if query.Limit > 0 && start+query.Limit < end {
		end = start + query.Limit
	}
	result := make([]Point, 0, end-start)
	for _, storedPoint := range ordered[start:end] {
		labels, err := decodeMapString(storedPoint.block.labels)
		if err != nil {
			return nil, err
		}
		result = append(result, Point{
			MetricName: storedPoint.series.metricName,
			EntityID:   storedPoint.series.entityID,
			Timestamp:  time.Unix(0, storedPoint.block.timestamp).UTC(),
			Value:      math.Float64frombits(storedPoint.block.valueBits),
			Tags:       cloneStringMap(storedPoint.series.tags),
			Labels:     labels,
		})
	}
	return result, nil
}

func (s *Store) sqliteV4MatchingSeries(ctx context.Context, q querier, metricName, entityID string, tags map[string]string) ([]sqliteV4Series, error) {
	var args []any
	var parts []string
	if strings.TrimSpace(metricName) != "" {
		args = append(args, metricName)
		parts = append(parts, "metric_name = ?")
	}
	if strings.TrimSpace(entityID) != "" {
		args = append(args, entityID)
		parts = append(parts, "entity_id = ?")
	}
	for _, key := range sortedKeys(tags) {
		args = append(args, tags[key])
		parts = append(parts, s.dialect.jsonExtractEquals("tags", key, "?"))
	}
	where := "1 = 1"
	if len(parts) > 0 {
		where = strings.Join(parts, " AND ")
	}
	rows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT id, metric_name, entity_id, tags_hash, tags FROM %s WHERE %s ORDER BY id`,
		s.tables.series, where,
	), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []sqliteV4Series
	for rows.Next() {
		var item sqliteV4Series
		var rawTags any
		if err := rows.Scan(&item.id, &item.metricName, &item.entityID, &item.tagsHash, &rawTags); err != nil {
			return nil, err
		}
		item.tagsJSON, err = rawTagsToJSON(rawTags)
		if err != nil {
			return nil, err
		}
		item.tags, err = decodeMapString(item.tagsJSON)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func sqliteV4SeriesIDClause(series []sqliteV4Series) (string, []any) {
	placeholders := make([]string, len(series))
	args := make([]any, len(series))
	for i, item := range series {
		placeholders[i] = "?"
		args[i] = item.id
	}
	return strings.Join(placeholders, ","), args
}

func (s *Store) sealSQLiteV4PointsTx(ctx context.Context, tx *sql.Tx, metricName string, beforeNano int64) (int64, error) {
	comparison := "<"
	if beforeNano == math.MaxInt64 {
		comparison = "<="
	}
	args := []any{beforeNano}
	metricFilter := ""
	if strings.TrimSpace(metricName) != "" {
		args = append(args, metricName)
		metricFilter = " AND s.metric_name = ?"
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT DISTINCT p.series_id FROM %s p JOIN %s s ON s.id = p.series_id
		 WHERE p.ts_nano %s ?%s ORDER BY p.series_id`,
		s.tables.pointValues, s.tables.series, comparison, metricFilter,
	), args...)
	if err != nil {
		return 0, err
	}
	var seriesIDs []int64
	for rows.Next() {
		var seriesID int64
		if err := rows.Scan(&seriesID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		seriesIDs = append(seriesIDs, seriesID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	var total int64
	for _, seriesID := range seriesIDs {
		points, err := s.loadAllSQLiteV4BlockPoints(ctx, tx, seriesID)
		if err != nil {
			return total, err
		}
		byTimestamp := make(map[int64]sqliteV4BlockPoint, len(points))
		for _, point := range points {
			byTimestamp[point.timestamp] = point
		}
		hotRows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT ts_nano, value, labels, created_at FROM %s WHERE series_id = ? AND ts_nano %s ? ORDER BY ts_nano`,
			s.tables.pointValues, comparison,
		), seriesID, beforeNano)
		if err != nil {
			return total, err
		}
		var hotCount int64
		for hotRows.Next() {
			var point sqliteV4BlockPoint
			var value float64
			var rawLabels any
			if err := hotRows.Scan(&point.timestamp, &value, &rawLabels, &point.createdAt); err != nil {
				_ = hotRows.Close()
				return total, err
			}
			point.valueBits = math.Float64bits(value)
			point.labels, err = rawTagsToJSON(rawLabels)
			if err != nil {
				_ = hotRows.Close()
				return total, err
			}
			byTimestamp[point.timestamp] = point
			hotCount++
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return total, err
		}
		if err := hotRows.Close(); err != nil {
			return total, err
		}
		merged := make([]sqliteV4BlockPoint, 0, len(byTimestamp))
		for _, point := range byTimestamp {
			merged = append(merged, point)
		}
		sort.Slice(merged, func(i, j int) bool { return merged[i].timestamp < merged[j].timestamp })
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.pointBlocks+` WHERE series_id = ?`, seriesID); err != nil {
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.pointValues+` WHERE series_id = ? AND ts_nano `+comparison+` ?`, seriesID, beforeNano); err != nil {
			return total, err
		}
		if err := s.writeSQLiteV4BlocksTx(ctx, tx, seriesID, merged); err != nil {
			return total, err
		}
		total += hotCount
	}
	return total, nil
}

func (s *Store) loadAllSQLiteV4BlockPoints(ctx context.Context, q querier, seriesID int64) ([]sqliteV4BlockPoint, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT start_nano, end_nano, point_count, codec, checksum, payload FROM %s WHERE series_id = ? ORDER BY start_nano`,
		s.tables.pointBlocks,
	), seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []sqliteV4BlockPoint
	for rows.Next() {
		var startNano, endNano, checksum int64
		var count, codec int
		var payload []byte
		if err := rows.Scan(&startNano, &endNano, &count, &codec, &checksum, &payload); err != nil {
			return nil, err
		}
		points, err := decodeSQLiteV4Block(codec, count, uint32(checksum), payload)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 block series=%d start=%d: %w", seriesID, startNano, err)
		}
		if len(points) == 0 || points[0].timestamp != startNano || points[len(points)-1].timestamp != endNano {
			return nil, fmt.Errorf("metric: SQLite V4 block boundary mismatch for series=%d start=%d", seriesID, startNano)
		}
		result = append(result, points...)
	}
	return result, rows.Err()
}

func (s *Store) writeSQLiteV4BlocksTx(ctx context.Context, tx *sql.Tx, seriesID int64, points []sqliteV4BlockPoint) error {
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i].timestamp < points[j].timestamp })
	for start := 0; start < len(points); start += sqliteV4BlockPointLimit {
		end := start + sqliteV4BlockPointLimit
		if end > len(points) {
			end = len(points)
		}
		encoded, err := encodeSQLiteV4Block(points[start:end])
		if err != nil {
			return err
		}
		decoded, err := decodeSQLiteV4Block(encoded.codec, encoded.count, encoded.checksum, encoded.payload)
		if err != nil || !sqliteV4PointsEqual(points[start:end], decoded) {
			if err == nil {
				err = errors.New("round-trip validation changed data")
			}
			return fmt.Errorf("metric: validate SQLite V4 point block: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (series_id, start_nano, end_nano, point_count, codec, checksum, payload)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`, s.tables.pointBlocks,
		), seriesID, encoded.startNano, encoded.endNano, encoded.count, encoded.codec, int64(encoded.checksum), encoded.payload); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteSQLiteV4PointsTx(ctx context.Context, tx *sql.Tx, filter Query, beforeNano *int64) (int64, error) {
	series, err := s.sqliteV4MatchingSeries(ctx, tx, filter.MetricName, filter.EntityID, filter.Tags)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, item := range series {
		blocks, err := s.loadAllSQLiteV4BlockPoints(ctx, tx, item.id)
		if err != nil {
			return total, err
		}
		kept := make([]sqliteV4BlockPoint, 0, len(blocks))
		deletedTimestamps := make(map[int64]struct{})
		for _, point := range blocks {
			if beforeNano == nil || point.timestamp < *beforeNano {
				deletedTimestamps[point.timestamp] = struct{}{}
			} else {
				kept = append(kept, point)
			}
		}
		hotSQL := fmt.Sprintf(`SELECT ts_nano FROM %s WHERE series_id = ?`, s.tables.pointValues)
		hotArgs := []any{item.id}
		if beforeNano != nil {
			hotSQL += ` AND ts_nano < ?`
			hotArgs = append(hotArgs, *beforeNano)
		}
		hotRows, err := tx.QueryContext(ctx, hotSQL, hotArgs...)
		if err != nil {
			return total, err
		}
		for hotRows.Next() {
			var timestamp int64
			if err := hotRows.Scan(&timestamp); err != nil {
				_ = hotRows.Close()
				return total, err
			}
			deletedTimestamps[timestamp] = struct{}{}
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return total, err
		}
		if err := hotRows.Close(); err != nil {
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.pointBlocks+` WHERE series_id = ?`, item.id); err != nil {
			return total, err
		}
		deleteHotSQL := `DELETE FROM ` + s.tables.pointValues + ` WHERE series_id = ?`
		if beforeNano != nil {
			deleteHotSQL += ` AND ts_nano < ?`
		}
		if _, err := tx.ExecContext(ctx, deleteHotSQL, hotArgs...); err != nil {
			return total, err
		}
		if err := s.writeSQLiteV4BlocksTx(ctx, tx, item.id, kept); err != nil {
			return total, err
		}
		total += int64(len(deletedTimestamps))
	}
	return total, nil
}

func (s *Store) sqliteV4EntityIDs(ctx context.Context, query Query) ([]string, error) {
	query = query.normalized()
	series, err := s.sqliteV4MatchingSeries(ctx, s.reader(), query.MetricName, query.EntityID, query.Tags)
	if err != nil {
		return nil, err
	}
	entities := make(map[string]struct{})
	for _, item := range series {
		points, err := s.querySQLiteV4Snapshot(ctx, Query{
			MetricName: item.metricName,
			EntityID:   item.entityID,
			Start:      query.Start,
			End:        query.End,
			Tags:       item.tags,
			Limit:      1,
		})
		if err != nil {
			return nil, err
		}
		if len(points) > 0 {
			entities[item.entityID] = struct{}{}
		}
	}
	for _, item := range series {
		if _, exists := entities[item.entityID]; exists {
			continue
		}
		hasRollup, err := s.sqliteV4SeriesHasRollupBetween(ctx, s.reader(), item.id, query.Start.UnixNano(), query.End.UnixNano())
		if err != nil {
			return nil, err
		}
		if hasRollup {
			entities[item.entityID] = struct{}{}
		}
	}
	result := make([]string, 0, len(entities))
	for entityID := range entities {
		if entityID != "" {
			result = append(result, entityID)
		}
	}
	sort.Strings(result)
	return result, nil
}
