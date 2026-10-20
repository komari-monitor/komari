package metric

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// rollupKey identifies a materialized bucket. The tags and labels themselves
// are interned in separate dictionaries; the hashes only keep hot maps small.
type rollupKey struct {
	entityID   string
	tagsHash   string
	labelsHash string
	bucket     int64 // Unix milliseconds
}

type storedRollup struct {
	entityID   string
	bucket     int64 // Unix milliseconds
	bucketData *rollupBucket
}

// Compact seals closed minute buckets, materializes due in-memory parents, and
// enforces retention for explicit callers. The server's frequent compact task
// uses Flush and FlushCoarse only; retention runs on its own hourly schedule.
func (s *Store) Compact(ctx context.Context, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	written, err := s.Flush(ctx, now)
	if err != nil {
		return 0, err
	}
	coarse, err := s.FlushCoarse(ctx, now)
	if err != nil {
		return written, err
	}
	written += coarse
	if _, err := s.CleanupExpired(ctx, now); err != nil {
		return written, err
	}
	return written, nil
}

// Flush seals closed in-memory minute buckets and trims the exact raw window,
// without running persisted retention or materializing coarse parents.
// It is used by the scheduled compactor so a series that stops reporting is
// still persisted even when no later report arrives to close its last minute.
func (s *Store) Flush(ctx context.Context, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.ingestMu.Lock()
	defer s.ingestMu.Unlock()
	now = now.UTC()
	s.trimRawWindow(now)
	written, err := s.flushClosedHotRollups(ctx, now)
	if err != nil {
		return 0, err
	}
	return written, nil
}

func (s *Store) CompactMetric(ctx context.Context, metricName string, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.enforceMetricRetentionTx(ctx, metricName, now.UTC(), tx); err != nil {
		return 0, err
	}
	return 0, tx.Commit()
}

// writeTierCascadeTx persists the durable minute tier. Coarser parents are
// accumulated in memory and materialized only after their late-arrival grace.
func (s *Store) writeTierCascadeTx(ctx context.Context, metricName string, policy RollupPolicy, minute map[rollupKey]*rollupBucket, tx *sql.Tx) (int, error) {
	if len(policy.Tiers) == 0 {
		return 0, nil
	}
	return s.mergeRollupBucketsWithDictionaryTx(ctx, metricName, policy.Tiers[0].Interval, minute, newRollupDictionaryCache(), tx)
}

// replaceMinuteRollupsTx overwrites rebuilt minute buckets, then recomputes
// only their affected ancestors from the stored finer tier. This keeps late
// raw upserts idempotent even though t-digests cannot remove observations.
func (s *Store) replaceMinuteRollupsTx(ctx context.Context, metricName string, policy RollupPolicy, replacements map[rollupKey]*rollupBucket, tx *sql.Tx) error {
	if len(replacements) == 0 || len(policy.Tiers) == 0 {
		return nil
	}
	cache := newRollupDictionaryCache()
	keys := make([]rollupKey, 0, len(replacements))
	for key := range replacements {
		keys = append(keys, key)
	}
	sortRollupKeys(keys)
	for _, key := range keys {
		bucket := replacements[key]
		if bucket == nil || bucket.count == 0 {
			if err := s.deleteRollupBucketTx(ctx, metricName, policy.Tiers[0].Interval, key, tx); err != nil {
				return err
			}
			continue
		}
		if err := s.upsertRollupWithDictionaryTx(ctx, metricName, policy.Tiers[0].Interval, key, bucket, cache, tx); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) rebuildRollupBucketTx(ctx context.Context, metricName string, fineInterval, coarseInterval time.Duration, key rollupKey, tx *sql.Tx) (*rollupBucket, error) {
	start := key.bucket
	end := start + coarseInterval.Milliseconds()
	columns := "s.entity_id, s.tags_hash, s.tags, l.labels_hash, l.labels, r.bucket_milli, r.count, r.sum, r.sum_sq, r.min_val, r.max_val, r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest"
	query := fmt.Sprintf(`SELECT %s FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		JOIN %s l ON l.id = r.label_id
		WHERE s.metric_name = %s AND s.entity_id = %s AND s.tags_hash = %s
			AND l.labels_hash = %s AND d.resolution_milli = %s
			AND r.bucket_milli >= %s AND r.bucket_milli < %s
		ORDER BY r.bucket_milli ASC`,
		columns, s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels,
		s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
		s.dialect.placeholder(4), s.dialect.placeholder(5), s.dialect.placeholder(6), s.dialect.placeholder(7))
	rows, err := tx.QueryContext(ctx, query, metricName, key.entityID, key.tagsHash, key.labelsHash, fineInterval.Milliseconds(), start, end)
	if err != nil {
		return nil, err
	}
	stored, err := scanStoredRollupsForMaintenance(rows, true, s.cfg.RollupPolicy.compression())
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(stored) == 0 {
		return nil, nil
	}
	bucket := newRollupBucket(s.cfg.RollupPolicy.compression())
	for _, row := range stored {
		if bucket.count == 0 {
			bucket.tagsHash, bucket.tagsJSON = row.bucketData.tagsHash, row.bucketData.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = row.bucketData.labelsHash, row.bucketData.labelsJSON
		}
		bucket.mergeStored(row.bucketData)
	}
	return bucket, nil
}

func (s *Store) deleteRollupBucketTx(ctx context.Context, metricName string, interval time.Duration, key rollupKey, tx *sql.Tx) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE bucket_milli = %s
		AND resolution_id IN (SELECT id FROM %s WHERE resolution_milli = %s)
		AND series_id IN (SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s AND tags_hash = %s)
		AND label_id IN (SELECT id FROM %s WHERE labels_hash = %s)`,
		s.tables.rollups, s.dialect.placeholder(1),
		s.tables.resolutions, s.dialect.placeholder(2),
		s.tables.series, s.dialect.placeholder(3), s.dialect.placeholder(4), s.dialect.placeholder(5),
		s.tables.labels, s.dialect.placeholder(6))
	_, err := tx.ExecContext(ctx, query, key.bucket, interval.Milliseconds(), metricName, key.entityID, key.tagsHash, key.labelsHash)
	return err
}

func buildCoarserBucketsFromDelta(delta map[rollupKey]*rollupBucket, interval time.Duration, compression float64) map[rollupKey]*rollupBucket {
	out := make(map[rollupKey]*rollupBucket, len(delta))
	for key, source := range delta {
		coarse := rollupKey{
			entityID: key.entityID, tagsHash: key.tagsHash, labelsHash: key.labelsHash,
			bucket: bucketStartMillis(key.bucket, interval.Milliseconds()),
		}
		bucket := out[coarse]
		if bucket == nil {
			bucket = newRollupBucket(compression)
			bucket.tagsHash, bucket.tagsJSON = source.tagsHash, source.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = source.labelsHash, source.labelsJSON
			out[coarse] = bucket
		}
		bucket.mergeStored(source)
	}
	return out
}

func (s *Store) mergeRollupBucketsWithDictionaryTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, cache *rollupDictionaryCache, tx *sql.Tx) (int, error) {
	if len(buckets) == 0 {
		return 0, nil
	}
	keys := make([]rollupKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sortRollupKeys(keys)
	// 批量读现有行并合并
	existing, err := s.readRollupBucketsBatchTx(ctx, metricName, interval, keys, buckets, cache, tx)
	if err != nil {
		return 0, err
	}
	rows := make([]normalizedRollupRow, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		key.bucket = normalizeBucketMillis(key.bucket)
		if old := existing[key]; old != nil {
			old.mergeStored(bucket)
			bucket = old
		}
		seriesID, err := cache.seriesID(ctx, s, tx, metricName, key, bucket.tagsJSON)
		if err != nil {
			return 0, err
		}
		labelID, err := cache.labelID(ctx, s, tx, key.labelsHash, bucket.labelsJSON)
		if err != nil {
			return 0, err
		}
		resolutionID, err := cache.resolutionID(ctx, s, tx, interval)
		if err != nil {
			return 0, err
		}
		rows = append(rows, normalizedRollupRow{
			seriesID: seriesID, resolutionID: resolutionID, labelID: labelID,
			bucketMilli: key.bucket, count: bucket.count, sum: bucket.sum, sumSq: bucket.sumSq,
			min: bucket.min, max: bucket.max, firstVal: bucket.firstVal, firstTSMilli: bucket.firstTS,
			lastVal: bucket.lastVal, lastTSMilli: bucket.lastTS, digest: bucket.encodedDigest(),
			createdAtMilli: timeMillis(time.Now()),
		})
	}
	for start := 0; start < len(rows); start += coarseRollupWriteBatchSize {
		end := start + coarseRollupWriteBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		if err := s.upsertNormalizedRollupRowsTx(ctx, rows[start:end], tx); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

// readRollupBucketsBatchTx 批量读出给定 keys 中已存在的行，按 key 返回。
func (s *Store) readRollupBucketsBatchTx(ctx context.Context, metricName string, interval time.Duration, keys []rollupKey, buckets map[rollupKey]*rollupBucket, cache *rollupDictionaryCache, tx *sql.Tx) (map[rollupKey]*rollupBucket, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	resolutionID, err := cache.resolutionID(ctx, s, tx, interval)
	if err != nil {
		return nil, err
	}
	// 按 bucket_milli 分组，每组收集 series_id
	type batchKey struct {
		bucket int64
		labels string
	}
	groups := make(map[batchKey][]rollupKey)
	for _, key := range keys {
		bk := batchKey{bucket: normalizeBucketMillis(key.bucket), labels: key.labelsHash}
		groups[bk] = append(groups[bk], key)
	}
	result := make(map[rollupKey]*rollupBucket)
	for bk, gkeys := range groups {
		seriesIDs := make([]int64, 0, len(gkeys))
		keyBySeries := make(map[int64]rollupKey, len(gkeys))
		for _, k := range gkeys {
			sid, err := cache.seriesID(ctx, s, tx, metricName, k, buckets[k].tagsJSON)
			if err != nil {
				return nil, err
			}
			seriesIDs = append(seriesIDs, sid)
			keyBySeries[sid] = k
		}
		labelID, err := cache.labelID(ctx, s, tx, bk.labels, buckets[gkeys[0]].labelsJSON)
		if err != nil {
			return nil, err
		}
		// IN 子句：每批最多 500 个 series_id
		for start := 0; start < len(seriesIDs); start += 500 {
			end := start + 500
			if end > len(seriesIDs) {
				end = len(seriesIDs)
			}
			batch := seriesIDs[start:end]
			placeholders := make([]string, len(batch))
			args := make([]any, 0, len(batch)+3)
			for i := range batch {
				placeholders[i] = s.dialect.placeholder(i + 1)
				args = append(args, batch[i])
			}
			args = append(args, resolutionID, bk.bucket, labelID)
			sqlText := fmt.Sprintf(`SELECT r.series_id, r.count, r.sum, r.sum_sq, r.min_val, r.max_val, r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest, s.tags, l.labels
				FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s l ON l.id = r.label_id
				WHERE r.series_id IN (%s) AND r.resolution_id = %s AND r.bucket_milli = %s AND r.label_id = %s`,
				s.tables.rollups, s.tables.series, s.tables.labels,
				joinSQL(placeholders), s.dialect.placeholder(len(batch)+1), s.dialect.placeholder(len(batch)+2), s.dialect.placeholder(len(batch)+3),
			)
			rows, err := tx.QueryContext(ctx, sqlText, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var seriesID, count, firstTS, lastTS int64
				var sum, sumSq, min, max, firstVal, lastVal float64
				var digest []byte
				var tags, labels any
				if err := rows.Scan(&seriesID, &count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS, &digest, &tags, &labels); err != nil {
					rows.Close()
					return nil, err
				}
				tagsJSON, err := rawJSONToString(tags)
				if err != nil {
					rows.Close()
					return nil, err
				}
				labelsJSON, err := rawJSONToString(labels)
				if err != nil {
					rows.Close()
					return nil, err
				}
				d, err := digestFromRollup(count, min, max, digest, s.cfg.RollupPolicy.compression())
				if err != nil {
					rows.Close()
					return nil, err
				}
				key := keyBySeries[seriesID]
				result[key] = &rollupBucket{count: count, sum: sum, sumSq: sumSq, min: min, max: max, firstVal: firstVal, firstTS: firstTS, lastVal: lastVal, lastTS: lastTS, digest: d, tagsHash: key.tagsHash, tagsJSON: tagsJSON, labelsHash: key.labelsHash, labelsJSON: labelsJSON}
			}
			rows.Close()
		}
	}
	return result, nil
}

// writeRollupBucketsTx is retained for package callers and tests. It uses the
// same merge-safe path as normal sealed-minute writes.
func (s *Store) writeRollupBucketsTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, tx *sql.Tx) (int, error) {
	return s.mergeRollupBucketsWithDictionaryTx(ctx, metricName, interval, buckets, newRollupDictionaryCache(), tx)
}

func (s *Store) readRollupBucketTx(ctx context.Context, metricName string, key rollupKey, interval time.Duration, tx *sql.Tx) (*rollupBucket, error) {
	sqlText := fmt.Sprintf(`SELECT r.count, r.sum, r.sum_sq, r.min_val, r.max_val, r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest, s.tags, l.labels
		FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s d ON d.id = r.resolution_id JOIN %s l ON l.id = r.label_id
		WHERE s.metric_name = %s AND s.entity_id = %s AND s.tags_hash = %s AND d.resolution_milli = %s AND r.bucket_milli = %s AND l.labels_hash = %s`,
		s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels,
		s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.placeholder(4), s.dialect.placeholder(5), s.dialect.placeholder(6),
	)
	var count, firstTS, lastTS int64
	var sum, sumSq, min, max, firstVal, lastVal float64
	var digest []byte
	var tags, labels any
	err := tx.QueryRowContext(ctx, sqlText, metricName, key.entityID, key.tagsHash, interval.Milliseconds(), key.bucket, key.labelsHash).Scan(
		&count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS, &digest, &tags, &labels,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tagsJSON, err := rawJSONToString(tags)
	if err != nil {
		return nil, err
	}
	labelsJSON, err := rawJSONToString(labels)
	if err != nil {
		return nil, err
	}
	d, err := digestFromRollup(count, min, max, digest, s.cfg.RollupPolicy.compression())
	if err != nil {
		return nil, err
	}
	return &rollupBucket{count: count, sum: sum, sumSq: sumSq, min: min, max: max, firstVal: firstVal, firstTS: firstTS, lastVal: lastVal, lastTS: lastTS, digest: d, tagsHash: key.tagsHash, tagsJSON: tagsJSON, labelsHash: key.labelsHash, labelsJSON: labelsJSON}, nil
}

func (s *Store) scanRollupRows(ctx context.Context, q querier, metricName string, interval time.Duration) ([]storedRollup, error) {
	return s.scanRollupRowsBetweenWith(ctx, q, metricName, "", nil, interval, -1<<62, 1<<62, true)
}

func (s *Store) enforceMetricRetentionTx(ctx context.Context, metricName string, now time.Time, tx *sql.Tx) error {
	var retentionDays int
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT retention_days FROM %s WHERE name = %s", s.tables.definitions, s.dialect.placeholder(1)), metricName).Scan(&retentionDays)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if retentionDays == 0 {
		return s.deleteRollupsForMetricTx(ctx, metricName, tx)
	}
	metricRetention := time.Duration(retentionDays) * 24 * time.Hour
	policy := s.cfg.RollupPolicy.withMetricRetention(metricRetention)
	retained := make(map[time.Duration]time.Duration, len(policy.Tiers))
	for _, tier := range policy.Tiers {
		retained[tier.Interval] = tier.Retention
	}
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		retention, keep := retained[tier.Interval]
		if !keep {
			if err := s.deleteRollupTierTx(ctx, metricName, tier.Interval, tx); err != nil {
				return err
			}
			continue
		}
		if err := s.deleteRollupsBeforeTx(ctx, metricName, tier.Interval, now.Add(-retention).UnixMilli(), tx); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteRollupTierTx(ctx context.Context, metricName string, interval time.Duration, tx *sql.Tx) error {
	sqlText := fmt.Sprintf(`DELETE FROM %s WHERE resolution_id IN (SELECT id FROM %s WHERE resolution_milli = %s) AND series_id IN (SELECT id FROM %s WHERE metric_name = %s)`,
		s.tables.rollups, s.tables.resolutions, s.dialect.placeholder(1), s.tables.series, s.dialect.placeholder(2))
	_, err := tx.ExecContext(ctx, sqlText, interval.Milliseconds(), metricName)
	return err
}

func (s *Store) deleteRollupsBeforeTx(ctx context.Context, metricName string, interval time.Duration, beforeMilli int64, tx *sql.Tx) error {
	sqlText := fmt.Sprintf(`DELETE FROM %s WHERE resolution_id IN (SELECT id FROM %s WHERE resolution_milli = %s) AND series_id IN (SELECT id FROM %s WHERE metric_name = %s) AND bucket_milli < %s`,
		s.tables.rollups, s.tables.resolutions, s.dialect.placeholder(1), s.tables.series, s.dialect.placeholder(2), s.dialect.placeholder(3))
	// Keep a bucket that straddles the cutoff because it can contain samples
	// still inside retention. At most one extra bucket per series is retained.
	beforeMilli = bucketStartMillis(beforeMilli, interval.Milliseconds())
	_, err := tx.ExecContext(ctx, sqlText, interval.Milliseconds(), metricName, beforeMilli)
	return err
}

func (s *Store) deleteRollupsForMetricTx(ctx context.Context, metricName string, tx *sql.Tx) error {
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE series_id IN (SELECT id FROM %s WHERE metric_name = %s)", s.tables.rollups, s.tables.series, s.dialect.placeholder(1))
	_, err := tx.ExecContext(ctx, sqlText, metricName)
	return err
}

func sortRollupKeys(keys []rollupKey) {
	slices.SortFunc(keys, func(a, b rollupKey) int {
		if c := cmp.Compare(a.bucket, b.bucket); c != 0 {
			return c
		}
		if c := cmp.Compare(a.entityID, b.entityID); c != 0 {
			return c
		}
		if c := cmp.Compare(a.tagsHash, b.tagsHash); c != 0 {
			return c
		}
		return cmp.Compare(a.labelsHash, b.labelsHash)
	})
}

func bucketStartMillis(ts, size int64) int64 {
	if size <= 0 {
		return ts
	}
	q := ts / size
	if ts < 0 && ts%size != 0 {
		q--
	}
	return q * size
}

func normalizeBucketMillis(bucket int64) int64 {
	if bucket > 10_000_000_000_000 || bucket < -10_000_000_000_000 {
		return bucket / int64(time.Millisecond)
	}
	return bucket
}
