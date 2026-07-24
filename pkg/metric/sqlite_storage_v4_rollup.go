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

func (s *Store) migrateSQLiteV4RollupBlocksToSplit(ctx context.Context) (int64, int64, error) {
	var blockCount int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.tables.rollupBlocks+` WHERE codec = ?`, sqliteV4LegacyRollupBlockCodec).Scan(&blockCount); err != nil {
		return 0, 0, fmt.Errorf("metric: count legacy SQLite V4 rollup blocks: %w", err)
	}
	if blockCount == 0 {
		return 0, 0, nil
	}
	log.Printf("metric: migrating %d SQLite V4 rollup blocks to split summary/digest storage", blockCount)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("metric: begin split SQLite V4 rollup migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	type key struct {
		seriesID, resolution, startNano int64
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT series_id, resolution_nano, start_nano FROM %s WHERE codec = ? ORDER BY series_id, resolution_nano, start_nano`,
		s.tables.rollupBlocks,
	), sqliteV4LegacyRollupBlockCodec)
	if err != nil {
		return 0, 0, err
	}
	keys := make([]key, 0, blockCount)
	for rows.Next() {
		var item key
		if err := rows.Scan(&item.seriesID, &item.resolution, &item.startNano); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		keys = append(keys, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}

	var migratedBlocks, migratedBuckets int64
	for _, item := range keys {
		var endNano, checksum int64
		var count, codec int
		var payload []byte
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT end_nano, bucket_count, codec, checksum, payload FROM %s
			 WHERE series_id = ? AND resolution_nano = ? AND start_nano = ?`, s.tables.rollupBlocks,
		), item.seriesID, item.resolution, item.startNano).Scan(&endNano, &count, &codec, &checksum, &payload); err != nil {
			return migratedBlocks, migratedBuckets, err
		}
		records, err := decodeSQLiteV4LegacyRollupBlock(codec, count, uint32(checksum), payload)
		if err != nil {
			return migratedBlocks, migratedBuckets, fmt.Errorf("metric: decode legacy SQLite V4 rollup block series=%d resolution=%d start=%d: %w", item.seriesID, item.resolution, item.startNano, err)
		}
		if len(records) == 0 || records[0].bucketNano != item.startNano || records[len(records)-1].bucketNano != endNano {
			return migratedBlocks, migratedBuckets, fmt.Errorf("metric: legacy SQLite V4 rollup block boundary mismatch for series=%d resolution=%d start=%d", item.seriesID, item.resolution, item.startNano)
		}
		encoded, err := encodeSQLiteV4RollupBlock(records)
		if err != nil {
			return migratedBlocks, migratedBuckets, err
		}
		decoded, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload,
			encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, true)
		if err != nil || !sqliteV4RollupRecordsEqual(records, decoded) {
			if err == nil {
				err = errors.New("split round-trip validation changed data")
			}
			return migratedBlocks, migratedBuckets, fmt.Errorf("metric: validate split SQLite V4 rollup block: %w", err)
		}
		result, err := tx.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET codec = ?, checksum = ?, payload = ?, digest_codec = ?, digest_checksum = ?, digest_payload = ?
			 WHERE series_id = ? AND resolution_nano = ? AND start_nano = ? AND codec = ?`, s.tables.rollupBlocks,
		), encoded.codec, int64(encoded.checksum), encoded.payload, encoded.digestCodec, int64(encoded.digestChecksum), encoded.digestPayload,
			item.seriesID, item.resolution, item.startNano, sqliteV4LegacyRollupBlockCodec)
		if err != nil {
			return migratedBlocks, migratedBuckets, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return migratedBlocks, migratedBuckets, fmt.Errorf("metric: inspect split SQLite V4 rollup update: %w", err)
		}
		if affected != 1 {
			return migratedBlocks, migratedBuckets, fmt.Errorf("metric: split SQLite V4 rollup update affected %d rows, want 1", affected)
		}
		migratedBlocks++
		migratedBuckets += int64(len(records))
	}
	if err := tx.Commit(); err != nil {
		return migratedBlocks, migratedBuckets, fmt.Errorf("metric: commit split SQLite V4 rollup migration: %w", err)
	}
	return migratedBlocks, migratedBuckets, nil
}

func (s *Store) createSQLiteV4RollupBlocks(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			series_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
			resolution_nano BIGINT NOT NULL,
			start_nano BIGINT NOT NULL,
			end_nano BIGINT NOT NULL,
			bucket_count INTEGER NOT NULL,
			codec INTEGER NOT NULL,
			checksum INTEGER NOT NULL,
			payload BLOB NOT NULL,
			digest_codec INTEGER NOT NULL DEFAULT 0,
			digest_checksum INTEGER NOT NULL DEFAULT 0,
			digest_payload BLOB,
			PRIMARY KEY(series_id, resolution_nano, start_nano),
			CHECK(end_nano >= start_nano),
			CHECK(bucket_count > 0)
		) WITHOUT ROWID`, s.tables.rollupBlocks, s.tables.series),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s_rollup_blocks_time_idx ON %s (resolution_nano, start_nano, end_nano)`,
			s.cfg.TablePrefix, s.tables.rollupBlocks),
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("metric: create SQLite V4 rollup block table: %w", err)
		}
	}
	return s.ensureSQLiteV4RollupDigestColumns(ctx, tx)
}

func (s *Store) ensureSQLiteV4RollupDigestColumns(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+s.tables.rollupBlocks+`)`)
	if err != nil {
		return fmt.Errorf("metric: inspect SQLite V4 rollup digest columns: %w", err)
	}
	found := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	additions := []struct {
		name string
		sql  string
	}{
		{"digest_codec", "INTEGER NOT NULL DEFAULT 0"},
		{"digest_checksum", "INTEGER NOT NULL DEFAULT 0"},
		{"digest_payload", "BLOB"},
	}
	for _, column := range additions {
		if found[column.name] {
			continue
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, s.tables.rollupBlocks, column.name, column.sql)); err != nil {
			return fmt.Errorf("metric: add SQLite V4 rollup column %s: %w", column.name, err)
		}
	}
	return nil
}

func (s *Store) validateSQLiteV4RollupBlocks(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+s.tables.rollupBlocks+`)`)
	if err != nil {
		return fmt.Errorf("metric: inspect SQLite V4 rollup block table: %w", err)
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
	for _, name := range []string{"series_id", "resolution_nano", "start_nano", "end_nano", "bucket_count", "codec", "checksum", "payload", "digest_codec", "digest_checksum", "digest_payload"} {
		if !found[name] {
			return fmt.Errorf("metric: SQLite V4 rollup block table is missing column %s", name)
		}
	}
	return nil
}

func (s *Store) sealAllSQLiteV4RollupsTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT DISTINCT series_id, resolution_nano FROM %s ORDER BY series_id, resolution_nano`,
		s.tables.rollupValues,
	))
	if err != nil {
		return 0, err
	}
	type group struct{ seriesID, resolution int64 }
	var groups []group
	for rows.Next() {
		var item group
		if err := rows.Scan(&item.seriesID, &item.resolution); err != nil {
			_ = rows.Close()
			return 0, err
		}
		groups = append(groups, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	var sealed int64
	for _, item := range groups {
		records, err := s.loadAllSQLiteV4RollupBlockRecords(ctx, tx, item.seriesID, item.resolution)
		if err != nil {
			return sealed, err
		}
		byBucket := make(map[int64]sqliteV4RollupRecord, len(records))
		for _, record := range records {
			byBucket[record.bucketNano] = record
		}
		hotRows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts,
			        last_val, last_ts, digest, created_at
			 FROM %s WHERE series_id = ? AND resolution_nano = ? ORDER BY bucket_nano`,
			s.tables.rollupValues,
		), item.seriesID, item.resolution)
		if err != nil {
			return sealed, err
		}
		for hotRows.Next() {
			record, err := scanSQLiteV4RollupRecord(hotRows)
			if err != nil {
				_ = hotRows.Close()
				return sealed, err
			}
			byBucket[record.bucketNano] = record
			sealed++
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return sealed, err
		}
		if err := hotRows.Close(); err != nil {
			return sealed, err
		}
		merged := make([]sqliteV4RollupRecord, 0, len(byBucket))
		for _, record := range byBucket {
			merged = append(merged, record)
		}
		sort.Slice(merged, func(i, j int) bool { return merged[i].bucketNano < merged[j].bucketNano })
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupBlocks+` WHERE series_id = ? AND resolution_nano = ?`, item.seriesID, item.resolution); err != nil {
			return sealed, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupValues+` WHERE series_id = ? AND resolution_nano = ?`, item.seriesID, item.resolution); err != nil {
			return sealed, err
		}
		if err := s.writeSQLiteV4RollupBlocksTx(ctx, tx, item.seriesID, item.resolution, merged); err != nil {
			return sealed, err
		}
	}
	return sealed, nil
}

func scanSQLiteV4RollupRecord(scanner interface{ Scan(dest ...any) error }) (sqliteV4RollupRecord, error) {
	var record sqliteV4RollupRecord
	var sum, sumSq, minValue, maxValue, firstValue, lastValue float64
	if err := scanner.Scan(
		&record.bucketNano, &record.count, &sum, &sumSq, &minValue, &maxValue,
		&firstValue, &record.firstTS, &lastValue, &record.lastTS, &record.digest, &record.createdAt,
	); err != nil {
		return sqliteV4RollupRecord{}, err
	}
	record.sumBits = math.Float64bits(sum)
	record.sumSqBits = math.Float64bits(sumSq)
	record.minBits = math.Float64bits(minValue)
	record.maxBits = math.Float64bits(maxValue)
	record.firstBits = math.Float64bits(firstValue)
	record.lastBits = math.Float64bits(lastValue)
	record.digest = append([]byte(nil), record.digest...)
	return record, nil
}

func (s *Store) loadAllSQLiteV4RollupBlockRecords(ctx context.Context, q querier, seriesID, resolution int64) ([]sqliteV4RollupRecord, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT start_nano, end_nano, bucket_count, codec, checksum, payload,
		        digest_codec, digest_checksum, digest_payload FROM %s
		 WHERE series_id = ? AND resolution_nano = ? ORDER BY start_nano`,
		s.tables.rollupBlocks,
	), seriesID, resolution)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []sqliteV4RollupRecord
	for rows.Next() {
		var startNano, endNano, checksum, digestChecksum int64
		var count, codec, digestCodec int
		var payload, digestPayload []byte
		if err := rows.Scan(&startNano, &endNano, &count, &codec, &checksum, &payload, &digestCodec, &digestChecksum, &digestPayload); err != nil {
			return nil, err
		}
		records, err := decodeSQLiteV4RollupBlock(codec, count, uint32(checksum), payload, digestCodec, uint32(digestChecksum), digestPayload, true)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 rollup block series=%d resolution=%d start=%d: %w", seriesID, resolution, startNano, err)
		}
		if len(records) == 0 || records[0].bucketNano != startNano || records[len(records)-1].bucketNano != endNano {
			return nil, fmt.Errorf("metric: SQLite V4 rollup block boundary mismatch for series=%d resolution=%d start=%d", seriesID, resolution, startNano)
		}
		result = append(result, records...)
	}
	return result, rows.Err()
}

func (s *Store) writeSQLiteV4RollupBlocksTx(ctx context.Context, tx *sql.Tx, seriesID, resolution int64, records []sqliteV4RollupRecord) error {
	if len(records) == 0 {
		return nil
	}
	sort.Slice(records, func(i, j int) bool { return records[i].bucketNano < records[j].bucketNano })
	for start := 0; start < len(records); start += sqliteV4RollupBlockLimit {
		end := min(start+sqliteV4RollupBlockLimit, len(records))
		encoded, err := encodeSQLiteV4RollupBlock(records[start:end])
		if err != nil {
			return err
		}
		decoded, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload, encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, true)
		if err != nil || !sqliteV4RollupRecordsEqual(records[start:end], decoded) {
			if err == nil {
				err = errors.New("round-trip validation changed data")
			}
			return fmt.Errorf("metric: validate SQLite V4 rollup block: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(
			`INSERT INTO %s (series_id, resolution_nano, start_nano, end_nano, bucket_count, codec, checksum, payload,
			                 digest_codec, digest_checksum, digest_payload)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollupBlocks,
		), seriesID, resolution, encoded.startNano, encoded.endNano, encoded.count, encoded.codec, int64(encoded.checksum), encoded.payload,
			encoded.digestCodec, int64(encoded.digestChecksum), encoded.digestPayload); err != nil {
			return err
		}
	}
	return nil
}

func sqliteV4RollupFlushMinimum(resolution int64) int {
	if resolution <= 0 {
		return 1
	}
	eligibleWindow := sqliteV4RollupFlushWindow - sqliteV4HotWindow
	minimum := int(eligibleWindow.Nanoseconds() / resolution)
	if minimum < 1 {
		return 1
	}
	if minimum > sqliteV4RollupBlockLimit {
		return sqliteV4RollupBlockLimit
	}
	return minimum
}

// appendSQLiteV4RollupTailTx moves eligible hot rows into the last compressed
// block. A partial tail is replaced in place, so frequent sealing does not
// create more long-term blocks or make historical reads touch smaller chunks.
func (s *Store) appendSQLiteV4RollupTailTx(ctx context.Context, tx *sql.Tx, seriesID, resolution, beforeNano int64) (int, error) {
	sealed := 0
	for {
		var startNano, endNano, checksum, digestChecksum int64
		var count, codec, digestCodec int
		var payload, digestPayload []byte
		err := tx.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT start_nano, end_nano, bucket_count, codec, checksum, payload,
			        digest_codec, digest_checksum, digest_payload FROM %s
			 WHERE series_id = ? AND resolution_nano = ? ORDER BY start_nano DESC LIMIT 1`,
			s.tables.rollupBlocks,
		), seriesID, resolution).Scan(&startNano, &endNano, &count, &codec, &checksum, &payload,
			&digestCodec, &digestChecksum, &digestPayload)

		var tail []sqliteV4RollupRecord
		lower := int64(math.MinInt64)
		limit := sqliteV4RollupBlockLimit
		replaceTail := false
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return sealed, err
		default:
			records, decodeErr := decodeSQLiteV4RollupBlock(codec, count, uint32(checksum), payload,
				digestCodec, uint32(digestChecksum), digestPayload, true)
			if decodeErr != nil {
				return sealed, fmt.Errorf("metric: decode SQLite V4 rollup tail series=%d resolution=%d start=%d: %w", seriesID, resolution, startNano, decodeErr)
			}
			if len(records) == 0 || records[0].bucketNano != startNano || records[len(records)-1].bucketNano != endNano {
				return sealed, fmt.Errorf("metric: SQLite V4 rollup tail boundary mismatch for series=%d resolution=%d start=%d", seriesID, resolution, startNano)
			}
			lower = endNano
			if len(records) < sqliteV4RollupBlockLimit {
				tail = records
				limit = sqliteV4RollupBlockLimit - len(records)
				replaceTail = true
			}
		}

		hotRows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts,
			        last_val, last_ts, digest, created_at FROM %s
			 WHERE series_id = ? AND resolution_nano = ? AND bucket_nano > ? AND bucket_nano < ?
			 ORDER BY bucket_nano LIMIT ?`, s.tables.rollupValues,
		), seriesID, resolution, lower, beforeNano, limit)
		if err != nil {
			return sealed, err
		}
		var hot []sqliteV4RollupRecord
		for hotRows.Next() {
			record, scanErr := scanSQLiteV4RollupRecord(hotRows)
			if scanErr != nil {
				_ = hotRows.Close()
				return sealed, scanErr
			}
			hot = append(hot, record)
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return sealed, err
		}
		if err := hotRows.Close(); err != nil {
			return sealed, err
		}
		if len(hot) == 0 {
			return sealed, nil
		}

		if replaceTail {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupBlocks+` WHERE series_id = ? AND resolution_nano = ? AND start_nano = ?`, seriesID, resolution, startNano); err != nil {
				return sealed, err
			}
		}
		merged := make([]sqliteV4RollupRecord, 0, len(tail)+len(hot))
		merged = append(merged, tail...)
		merged = append(merged, hot...)
		if err := s.writeSQLiteV4RollupBlocksTx(ctx, tx, seriesID, resolution, merged); err != nil {
			return sealed, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupValues+` WHERE series_id = ? AND resolution_nano = ? AND bucket_nano >= ? AND bucket_nano <= ?`,
			seriesID, resolution, hot[0].bucketNano, hot[len(hot)-1].bucketNano); err != nil {
			return sealed, err
		}
		sealed += len(hot)
	}
}

func (s *Store) querySQLiteV4Rollups(ctx context.Context, q querier, metricName, entityID string, tags map[string]string, resolution, lower, upper int64, needDigest bool) ([]storedRollup, error) {
	series, err := s.sqliteV4MatchingSeries(ctx, q, metricName, entityID, tags)
	if err != nil {
		return nil, err
	}
	var result []storedRollup
	for _, item := range series {
		byBucket := make(map[int64]sqliteV4RollupRecord)
		blockColumns := "start_nano, end_nano, bucket_count, codec, checksum, payload"
		if needDigest {
			blockColumns += ", digest_codec, digest_checksum, digest_payload"
		}
		blockRows, err := q.QueryContext(ctx, fmt.Sprintf(
			`SELECT %s FROM %s
			 WHERE series_id = ? AND resolution_nano = ? AND end_nano >= ? AND start_nano <= ? ORDER BY start_nano`,
			blockColumns, s.tables.rollupBlocks,
		), item.id, resolution, lower, upper)
		if err != nil {
			return nil, err
		}
		for blockRows.Next() {
			var startNano, endNano, checksum, digestChecksum int64
			var count, codec, digestCodec int
			var payload, digestPayload []byte
			destinations := []any{&startNano, &endNano, &count, &codec, &checksum, &payload}
			if needDigest {
				destinations = append(destinations, &digestCodec, &digestChecksum, &digestPayload)
			}
			if err := blockRows.Scan(destinations...); err != nil {
				_ = blockRows.Close()
				return nil, err
			}
			records, err := decodeSQLiteV4RollupBlock(codec, count, uint32(checksum), payload, digestCodec, uint32(digestChecksum), digestPayload, needDigest)
			if err != nil {
				_ = blockRows.Close()
				return nil, fmt.Errorf("metric: decode SQLite V4 rollup block series=%d start=%d: %w", item.id, startNano, err)
			}
			if len(records) == 0 || records[0].bucketNano != startNano || records[len(records)-1].bucketNano != endNano {
				_ = blockRows.Close()
				return nil, fmt.Errorf("metric: SQLite V4 rollup block boundary mismatch for series=%d start=%d", item.id, startNano)
			}
			for _, record := range records {
				if record.bucketNano >= lower && record.bucketNano <= upper {
					byBucket[record.bucketNano] = record
				}
			}
		}
		if err := blockRows.Err(); err != nil {
			_ = blockRows.Close()
			return nil, err
		}
		if err := blockRows.Close(); err != nil {
			return nil, err
		}
		hotRows, err := q.QueryContext(ctx, fmt.Sprintf(
			`SELECT bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts,
			        last_val, last_ts, digest, created_at
			 FROM %s WHERE series_id = ? AND resolution_nano = ? AND bucket_nano >= ? AND bucket_nano <= ?`,
			s.tables.rollupValues,
		), item.id, resolution, lower, upper)
		if err != nil {
			return nil, err
		}
		for hotRows.Next() {
			record, err := scanSQLiteV4RollupRecord(hotRows)
			if err != nil {
				_ = hotRows.Close()
				return nil, err
			}
			byBucket[record.bucketNano] = record
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return nil, err
		}
		if err := hotRows.Close(); err != nil {
			return nil, err
		}
		buckets := make([]int64, 0, len(byBucket))
		for bucket := range byBucket {
			buckets = append(buckets, bucket)
		}
		sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
		for _, bucket := range buckets {
			record := byBucket[bucket]
			var digest *TDigest
			if needDigest {
				digest, err = DecodeTDigest(record.digest)
				if err != nil {
					return nil, err
				}
			}
			result = append(result, storedRollup{
				entityID: item.entityID,
				bucket:   record.bucketNano,
				bucketData: &rollupBucket{
					count: record.count,
					sum:   math.Float64frombits(record.sumBits), sumSq: math.Float64frombits(record.sumSqBits),
					min: math.Float64frombits(record.minBits), max: math.Float64frombits(record.maxBits),
					firstVal: math.Float64frombits(record.firstBits), firstTS: record.firstTS,
					lastVal: math.Float64frombits(record.lastBits), lastTS: record.lastTS,
					digest: digest, tagsHash: item.tagsHash, tagsJSON: item.tagsJSON,
				},
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].bucket != result[j].bucket {
			return result[i].bucket < result[j].bucket
		}
		if result[i].entityID != result[j].entityID {
			return result[i].entityID < result[j].entityID
		}
		return result[i].bucketData.tagsHash < result[j].bucketData.tagsHash
	})
	return result, nil
}

func (s *Store) readSQLiteV4RollupBucketTx(ctx context.Context, tx *sql.Tx, metricName, entityID, tagsHash string, resolution, bucketNano int64) (*rollupBucket, error) {
	series, err := s.sqliteV4MatchingSeries(ctx, tx, metricName, entityID, nil)
	if err != nil {
		return nil, err
	}
	for _, item := range series {
		if item.tagsHash != tagsHash {
			continue
		}
		rows, err := s.querySQLiteV4Rollups(ctx, tx, metricName, entityID, item.tags, resolution, bucketNano, bucketNano, true)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return rows[0].bucketData, nil
		}
	}
	return nil, nil
}

func (s *Store) sqliteV4SeriesHasRollupBetween(ctx context.Context, q querier, seriesID, lower, upper int64) (bool, error) {
	var hotExists int
	if err := q.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT EXISTS(SELECT 1 FROM %s WHERE series_id = ? AND bucket_nano >= ? AND bucket_nano <= ?)`,
		s.tables.rollupValues,
	), seriesID, lower, upper).Scan(&hotExists); err != nil {
		return false, err
	}
	if hotExists != 0 {
		return true, nil
	}
	rows, err := q.QueryContext(ctx, fmt.Sprintf(
		`SELECT start_nano, end_nano, bucket_count, codec, checksum, payload FROM %s
		 WHERE series_id = ? AND end_nano >= ? AND start_nano <= ? ORDER BY start_nano`,
		s.tables.rollupBlocks,
	), seriesID, lower, upper)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var startNano, endNano, checksum int64
		var count, codec int
		var payload []byte
		if err := rows.Scan(&startNano, &endNano, &count, &codec, &checksum, &payload); err != nil {
			return false, err
		}
		records, err := decodeSQLiteV4RollupBlock(codec, count, uint32(checksum), payload, 0, 0, nil, false)
		if err != nil {
			return false, fmt.Errorf("metric: decode SQLite V4 rollup block series=%d start=%d: %w", seriesID, startNano, err)
		}
		if len(records) == 0 || records[0].bucketNano != startNano || records[len(records)-1].bucketNano != endNano {
			return false, fmt.Errorf("metric: SQLite V4 rollup block boundary mismatch for series=%d start=%d", seriesID, startNano)
		}
		for _, record := range records {
			if record.bucketNano >= lower && record.bucketNano <= upper {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

func (s *Store) latestSQLiteV4RollupBefore(ctx context.Context, q querier, metricName, entityID string, resolution, beforeNano int64) (Point, bool, error) {
	series, err := s.sqliteV4MatchingSeries(ctx, q, metricName, entityID, nil)
	if err != nil {
		return Point{}, false, err
	}
	var latest Point
	found := false
	for _, item := range series {
		var hotValue float64
		var hotTimestamp int64
		err := q.QueryRowContext(ctx, fmt.Sprintf(
			`SELECT last_val, last_ts FROM %s WHERE series_id = ? AND resolution_nano = ? AND last_ts < ?
			 ORDER BY last_ts DESC LIMIT 1`, s.tables.rollupValues,
		), item.id, resolution, beforeNano).Scan(&hotValue, &hotTimestamp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Point{}, false, err
		}
		if err == nil && (!found || hotTimestamp > latest.Timestamp.UnixNano()) {
			latest = Point{MetricName: metricName, EntityID: entityID, Timestamp: time.Unix(0, hotTimestamp).UTC(), Value: hotValue, Tags: cloneStringMap(item.tags)}
			found = true
		}

		rows, err := q.QueryContext(ctx, fmt.Sprintf(
			`SELECT start_nano, end_nano, bucket_count, codec, checksum, payload FROM %s
			 WHERE series_id = ? AND resolution_nano = ? AND start_nano < ? ORDER BY start_nano DESC`,
			s.tables.rollupBlocks,
		), item.id, resolution, beforeNano)
		if err != nil {
			return Point{}, false, err
		}
		matchedSeries := false
		for rows.Next() {
			var startNano, endNano, checksum int64
			var count, codec int
			var payload []byte
			if err := rows.Scan(&startNano, &endNano, &count, &codec, &checksum, &payload); err != nil {
				_ = rows.Close()
				return Point{}, false, err
			}
			records, err := decodeSQLiteV4RollupBlock(codec, count, uint32(checksum), payload, 0, 0, nil, false)
			if err != nil {
				_ = rows.Close()
				return Point{}, false, fmt.Errorf("metric: decode SQLite V4 rollup block series=%d start=%d: %w", item.id, startNano, err)
			}
			if len(records) == 0 || records[0].bucketNano != startNano || records[len(records)-1].bucketNano != endNano {
				_ = rows.Close()
				return Point{}, false, fmt.Errorf("metric: SQLite V4 rollup block boundary mismatch for series=%d start=%d", item.id, startNano)
			}
			for index := len(records) - 1; index >= 0; index-- {
				record := records[index]
				if record.lastTS >= beforeNano {
					continue
				}
				if !found || record.lastTS > latest.Timestamp.UnixNano() {
					latest = Point{
						MetricName: metricName,
						EntityID:   entityID,
						Timestamp:  time.Unix(0, record.lastTS).UTC(),
						Value:      math.Float64frombits(record.lastBits),
						Tags:       cloneStringMap(item.tags),
					}
					found = true
				}
				matchedSeries = true
				break
			}
			if matchedSeries {
				break
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return Point{}, false, err
		}
		if err := rows.Close(); err != nil {
			return Point{}, false, err
		}
	}
	return latest, found, nil
}

func (s *Store) deleteSQLiteV4RollupsTx(ctx context.Context, tx *sql.Tx, filter Query, resolutions []int64, beforeNano *int64) (int64, error) {
	series, err := s.sqliteV4MatchingSeries(ctx, tx, filter.MetricName, filter.EntityID, filter.Tags)
	if err != nil {
		return 0, err
	}
	resolutionSet := make(map[int64]struct{}, len(resolutions))
	for _, resolution := range resolutions {
		resolutionSet[resolution] = struct{}{}
	}
	var total int64
	for _, item := range series {
		type rollupKey struct{ resolution, bucket int64 }
		deleted := make(map[rollupKey]struct{})
		args := []any{item.id}
		where := "series_id = ?"
		if len(resolutionSet) > 0 {
			placeholders := make([]string, 0, len(resolutionSet))
			for resolution := range resolutionSet {
				placeholders = append(placeholders, "?")
				args = append(args, resolution)
			}
			where += " AND resolution_nano IN (" + strings.Join(placeholders, ",") + ")"
		}
		blockWhere := where
		blockArgs := append([]any(nil), args...)
		if beforeNano != nil {
			blockWhere += " AND start_nano < ?"
			blockArgs = append(blockArgs, *beforeNano)
		}
		blockRows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT resolution_nano, start_nano, end_nano, bucket_count, codec, checksum, payload,
			        digest_codec, digest_checksum, digest_payload FROM %s WHERE %s`,
			s.tables.rollupBlocks, blockWhere,
		), blockArgs...)
		if err != nil {
			return total, err
		}
		type block struct {
			resolution, start, end, checksum, digestChecksum int64
			count, codec, digestCodec                        int
			payload, digestPayload                           []byte
		}
		var blocks []block
		for blockRows.Next() {
			var value block
			if err := blockRows.Scan(&value.resolution, &value.start, &value.end, &value.count, &value.codec, &value.checksum, &value.payload,
				&value.digestCodec, &value.digestChecksum, &value.digestPayload); err != nil {
				_ = blockRows.Close()
				return total, err
			}
			value.payload = append([]byte(nil), value.payload...)
			value.digestPayload = append([]byte(nil), value.digestPayload...)
			blocks = append(blocks, value)
		}
		if err := blockRows.Err(); err != nil {
			_ = blockRows.Close()
			return total, err
		}
		if err := blockRows.Close(); err != nil {
			return total, err
		}
		for _, value := range blocks {
			records, err := decodeSQLiteV4RollupBlock(value.codec, value.count, uint32(value.checksum), value.payload,
				value.digestCodec, uint32(value.digestChecksum), value.digestPayload, true)
			if err != nil {
				return total, err
			}
			if len(records) == 0 || records[0].bucketNano != value.start || records[len(records)-1].bucketNano != value.end {
				return total, fmt.Errorf("metric: SQLite V4 rollup block boundary mismatch for series=%d start=%d", item.id, value.start)
			}
			kept := make([]sqliteV4RollupRecord, 0, len(records))
			for _, record := range records {
				if beforeNano == nil || record.bucketNano < *beforeNano {
					deleted[rollupKey{resolution: value.resolution, bucket: record.bucketNano}] = struct{}{}
				} else {
					kept = append(kept, record)
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupBlocks+` WHERE series_id = ? AND resolution_nano = ? AND start_nano = ?`, item.id, value.resolution, value.start); err != nil {
				return total, err
			}
			if err := s.writeSQLiteV4RollupBlocksTx(ctx, tx, item.id, value.resolution, kept); err != nil {
				return total, err
			}
		}
		hotWhere := where
		hotArgs := append([]any(nil), args...)
		if beforeNano != nil {
			hotWhere += " AND bucket_nano < ?"
			hotArgs = append(hotArgs, *beforeNano)
		}
		hotRows, err := tx.QueryContext(ctx, `SELECT resolution_nano, bucket_nano FROM `+s.tables.rollupValues+` WHERE `+hotWhere, hotArgs...)
		if err != nil {
			return total, err
		}
		for hotRows.Next() {
			var key rollupKey
			if err := hotRows.Scan(&key.resolution, &key.bucket); err != nil {
				_ = hotRows.Close()
				return total, err
			}
			deleted[key] = struct{}{}
		}
		if err := hotRows.Err(); err != nil {
			_ = hotRows.Close()
			return total, err
		}
		if err := hotRows.Close(); err != nil {
			return total, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupValues+` WHERE `+hotWhere, hotArgs...); err != nil {
			return total, err
		}
		total += int64(len(deleted))
	}
	return total, nil
}

func (s *Store) sealSQLiteV4RollupHotTx(ctx context.Context, tx *sql.Tx, metricName string, beforeNano int64) error {
	series, err := s.sqliteV4MatchingSeries(ctx, tx, metricName, "", nil)
	if err != nil {
		return err
	}
	for _, item := range series {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT DISTINCT resolution_nano FROM %s WHERE series_id = ? AND bucket_nano < ?`,
			s.tables.rollupValues,
		), item.id, beforeNano)
		if err != nil {
			return err
		}
		var resolutions []int64
		for rows.Next() {
			var resolution int64
			if err := rows.Scan(&resolution); err != nil {
				_ = rows.Close()
				return err
			}
			resolutions = append(resolutions, resolution)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		for _, resolution := range resolutions {
			var maxEnd sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT MAX(end_nano) FROM `+s.tables.rollupBlocks+` WHERE series_id = ? AND resolution_nano = ?`, item.id, resolution).Scan(&maxEnd); err != nil {
				return err
			}
			if maxEnd.Valid {
				var lateCount int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.tables.rollupValues+` WHERE series_id = ? AND resolution_nano = ? AND bucket_nano <= ? AND bucket_nano < ?`, item.id, resolution, maxEnd.Int64, beforeNano).Scan(&lateCount); err != nil {
					return err
				}
				if lateCount > 0 {
					if err := s.rewriteSQLiteV4RollupGroupTx(ctx, tx, item.id, resolution, beforeNano); err != nil {
						return err
					}
					continue
				}
			}
			minimum := sqliteV4RollupFlushMinimum(resolution)
			lower := int64(math.MinInt64)
			if maxEnd.Valid {
				lower = maxEnd.Int64
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+s.tables.rollupValues+` WHERE series_id = ? AND resolution_nano = ? AND bucket_nano > ? AND bucket_nano < ?`, item.id, resolution, lower, beforeNano).Scan(&count); err != nil {
				return err
			}
			if count < minimum {
				continue
			}
			if _, err := s.appendSQLiteV4RollupTailTx(ctx, tx, item.id, resolution, beforeNano); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) rewriteSQLiteV4RollupGroupTx(ctx context.Context, tx *sql.Tx, seriesID, resolution, beforeNano int64) error {
	records, err := s.loadAllSQLiteV4RollupBlockRecords(ctx, tx, seriesID, resolution)
	if err != nil {
		return err
	}
	byBucket := make(map[int64]sqliteV4RollupRecord, len(records))
	for _, record := range records {
		byBucket[record.bucketNano] = record
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(
		`SELECT bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts,
		        last_val, last_ts, digest, created_at FROM %s
		 WHERE series_id = ? AND resolution_nano = ? AND bucket_nano < ? ORDER BY bucket_nano`,
		s.tables.rollupValues,
	), seriesID, resolution, beforeNano)
	if err != nil {
		return err
	}
	for rows.Next() {
		record, err := scanSQLiteV4RollupRecord(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		byBucket[record.bucketNano] = record
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	merged := make([]sqliteV4RollupRecord, 0, len(byBucket))
	for _, record := range byBucket {
		merged = append(merged, record)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].bucketNano < merged[j].bucketNano })
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupBlocks+` WHERE series_id = ? AND resolution_nano = ?`, seriesID, resolution); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.tables.rollupValues+` WHERE series_id = ? AND resolution_nano = ? AND bucket_nano < ?`, seriesID, resolution, beforeNano); err != nil {
		return err
	}
	return s.writeSQLiteV4RollupBlocksTx(ctx, tx, seriesID, resolution, merged)
}
