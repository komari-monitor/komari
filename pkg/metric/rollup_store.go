package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Compact runs the store's RollupPolicy over every metric: it advances the
// rollup tiers to `now` and enforces every retention window (raw points and each
// tier). With finite raw retention, only newly eligible and late raw points are
// propagated. It is safe to call repeatedly and is idempotent for unchanged
// windows.
//
// Returns the number of rollup buckets written across all tiers and metrics.
//
// Compact 会对所有指标执行 Store 的 RollupPolicy：它会把 rollup 层推进到
// `now`，并执行每个保留窗口（原始点和各层级）。原始点保留期有限时，只会级联
// 本轮新到期及迟到的原始点。它可以重复安全调用，未变化窗口保持幂等。
//
// 返回所有指标、所有层级中写入的 rollup 桶数量。
func (s *Store) Compact(ctx context.Context, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, def := range defs {
		n, err := s.CompactMetric(ctx, def.Name, now)
		if err != nil {
			return total, fmt.Errorf("compact metric %q: %w", def.Name, err)
		}
		total += n
	}
	return total, nil
}

// CompactMetric compacts a single metric. With finite raw retention it consumes
// newly eligible raw points as a delta and propagates that delta through every
// tier. When raw is retained forever it falls back to rebuilding tiers. It then
// deletes data that has aged out of each retention window.
//
// The entire compaction is performed within a SERIALIZABLE transaction to
// guarantee that the raw scan and the raw deletion observe the same snapshot.
// Without this, the default isolation level on PostgreSQL/MySQL lets a point
// inserted between the scan and the delete be deleted by the cutoff DELETE
// without ever being folded into a rollup: the scan never saw it (it ran on an
// earlier read), yet the DELETE (a fresh read) does. SERIALIZABLE makes the
// scan acquire predicate/range protection so a concurrent write that would
// otherwise slip into that gap either is excluded from the delete or forces a
// serialization failure that we retry on a fresh snapshot. SQLite serializes
// writes on a single connection, so its default isolation already provides this
// guarantee and needs no escalation.
//
// Rollups are keyed by tag set as well as entity, so each distinct tag
// combination (e.g. a GPU device_index) is summarized into its own series and
// can be queried independently after the raw points are gone.
//
// CompactMetric 会压缩单个指标。原始点保留期有限时，它把本轮新到期的原始点
// 作为增量逐层传播；原始点永久保留时则回退到全量重建。最后删除各保留窗口中
// 已过期的数据。
//
// 整个 compaction 在一个 SERIALIZABLE 事务内执行，以保证 raw 扫描和 raw 删除
// 看到同一个快照。否则在 PostgreSQL/MySQL 的默认隔离级别下，扫描之后、删除
// 之前写入的旧时间点仍可能被 cutoff 删除却没有进入 rollup：扫描（较早的读）
// 没看到它，而删除（一次新的读）看到了。SERIALIZABLE 让扫描获得谓词/范围
// 保护，使这种并发写要么被删除排除在外，要么触发可重试的序列化失败，由我们
// 在新快照上重试。SQLite 在单连接上串行化写入，其默认隔离已提供该保证，
// 无需提升隔离级别。
func (s *Store) CompactMetric(ctx context.Context, metricName string, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	policy := s.cfg.RollupPolicy
	def, err := s.GetMetric(ctx, metricName)
	if errors.Is(err, ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if def.RetentionDays == 0 {
		_, err := s.DeleteSeries(ctx, Query{MetricName: metricName})
		return 0, err
	}
	if !policy.Enabled() {
		return 0, nil
	}
	effectivePolicy := policy.withMetricRetention(time.Duration(def.RetentionDays) * 24 * time.Hour)
	obsoleteIntervals := rollupIntervalsOutsidePolicy(policy.Tiers, effectivePolicy.Tiers)
	policy = effectivePolicy
	now = now.UTC()

	// Retry the whole compaction on a transient serialization/deadlock failure.
	// Under SERIALIZABLE, a concurrent writer touching the same raw range can
	// abort our transaction; re-running on a fresh snapshot is the correct and
	// expected recovery, and Compact is idempotent so a retry is safe.
	const maxAttempts = 5
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		written, err := s.compactMetricOnce(ctx, metricName, now, policy, obsoleteIntervals)
		if err == nil {
			return written, nil
		}
		if !isRetryableSerializationError(err) {
			return written, err
		}
		lastErr = err
	}
	return 0, fmt.Errorf("compact metric %q: exhausted retries after serialization failures: %w", metricName, lastErr)
}

// compactMetricOnce runs a single compaction attempt inside one transaction.
//
// compactMetricOnce 在单个事务内执行一次 compaction 尝试。
func (s *Store) compactMetricOnce(ctx context.Context, metricName string, now time.Time, policy RollupPolicy, obsoleteIntervals []time.Duration) (int, error) {
	// Use a transaction to ensure consistency between raw scan, rollup write, and
	// raw deletion. The isolation level is backend-specific (SERIALIZABLE on
	// PostgreSQL/MySQL, default on SQLite) so late-arriving points cannot be
	// deleted without first being rolled up.
	tx, err := s.db.BeginTx(ctx, s.dialect.compactTxOptions())
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.deleteRollupsForIntervalsTx(ctx, metricName, obsoleteIntervals, tx); err != nil {
		return 0, err
	}
	written, err := s.compactMetricWithinTx(ctx, metricName, now, policy, tx)
	if err != nil {
		return written, err
	}
	if err := s.persistCompactionWatermarkTx(ctx, metricName, policy.rawCutoff(now), tx); err != nil {
		return written, err
	}

	if err := tx.Commit(); err != nil {
		return written, err
	}
	return written, nil
}

func rollupIntervalsOutsidePolicy(configured, effective []RollupTier) []time.Duration {
	active := make(map[time.Duration]struct{}, len(effective))
	for _, tier := range effective {
		active[tier.Interval] = struct{}{}
	}
	obsolete := make([]time.Duration, 0, len(configured))
	for _, tier := range configured {
		if _, ok := active[tier.Interval]; !ok {
			obsolete = append(obsolete, tier.Interval)
		}
	}
	return obsolete
}

// isRetryableSerializationError reports whether err is a transient
// serialization failure or deadlock that should be retried on a fresh
// transaction. It matches on portable SQLSTATE codes and driver error text so
// the package stays free of driver-specific error type imports:
//
//   - PostgreSQL: 40001 serialization_failure, 40P01 deadlock_detected.
//   - MySQL: 1213 deadlock, 1205 lock wait timeout.
//   - SQLite: SQLITE_BUSY / database is locked (only relevant with >1 conn).
//
// isRetryableSerializationError 判断 err 是否为应在新事务上重试的瞬时序列化
// 失败或死锁。它通过可移植的 SQLSTATE 码和驱动错误文本匹配，使包不需要导入
// 驱动专用错误类型。
func isRetryableSerializationError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "40001"): // postgres serialization_failure
		return true
	case strings.Contains(msg, "40p01"): // postgres deadlock_detected
		return true
	case strings.Contains(msg, "serialization failure"):
		return true
	case strings.Contains(msg, "could not serialize"):
		return true
	case strings.Contains(msg, "deadlock"): // mysql 1213, postgres text
		return true
	case strings.Contains(msg, "lock wait timeout"): // mysql 1205
		return true
	case strings.Contains(msg, "database is locked"): // sqlite busy
		return true
	case strings.Contains(msg, "database table is locked"): // sqlite busy
		return true
	default:
		return false
	}
}

// compactMetricWithinTx is the actual compaction logic, meant to be called
// within a transaction. The tx parameter is passed explicitly to all operations
// that need transactional consistency.
func (s *Store) compactMetricWithinTx(ctx context.Context, metricName string, now time.Time, policy RollupPolicy, tx *sql.Tx) (int, error) {
	if !policy.Enabled() {
		return 0, nil
	}
	if policy.RawRetention > 0 {
		return s.compactMetricIncrementalWithinTx(ctx, metricName, now, policy, tx)
	}
	return s.compactMetricFullWithinTx(ctx, metricName, now, policy, tx)
}

// compactMetricIncrementalWithinTx rolls only raw points crossing the hot-data
// cutoff and propagates that delta through every coarser tier. Because those raw
// points are deleted in the same transaction, each sample is merged exactly
// once, including late samples for buckets that already exist.
func (s *Store) compactMetricIncrementalWithinTx(ctx context.Context, metricName string, now time.Time, policy RollupPolicy, tx *sql.Tx) (int, error) {
	comp := policy.compression()
	rawCutoff := policy.rawCutoff(now)
	delta, err := s.buildFinestTierBefore(ctx, tx, metricName, policy.Tiers[0].Interval, comp, rawCutoff)
	if err != nil {
		return 0, err
	}

	written := 0
	for tierIdx, tier := range policy.Tiers {
		if tierIdx > 0 {
			delta = buildCoarserBucketsFromDelta(delta, tier.Interval, comp)
		}
		n, err := s.mergeRollupBucketsTx(ctx, metricName, tier.Interval, delta, tx)
		if err != nil {
			return written, err
		}
		written += n
	}

	if err := s.enforceRetentionWithinTx(ctx, metricName, now, policy, tx); err != nil {
		return written, err
	}
	return written, nil
}

// compactMetricFullWithinTx retains the rebuild behavior required when raw
// points are kept forever and therefore cannot act as a consumed delta queue.
func (s *Store) compactMetricFullWithinTx(ctx context.Context, metricName string, now time.Time, policy RollupPolicy, tx *sql.Tx) (int, error) {
	comp := policy.compression()

	written := 0
	var prevInterval time.Duration
	var prevDelta map[rollupKey]*rollupBucket
	rawCutoff := policy.rawCutoff(now)

	for tierIdx, tier := range policy.Tiers {
		var buckets map[rollupKey]*rollupBucket
		var currentDelta map[rollupKey]*rollupBucket
		var err error
		// Reads must run on the transaction itself, not s.reader(): with a
		// single-connection pool (SQLite MaxOpenConns=1) the tx already holds
		// the only connection, so a pool read here would deadlock.
		if tierIdx == 0 {
			buckets, err = s.buildFinestTier(ctx, tx, metricName, tier.Interval, comp)
			if err == nil && !rawCutoff.IsZero() {
				cutoffNano := rawCutoff.UnixNano()
				for k := range buckets {
					if k.bucket >= cutoffNano {
						delete(buckets, k)
					}
				}
			}
			if !rawCutoff.IsZero() {
				currentDelta = buckets
			}
		} else {
			buckets, err = s.buildCoarserTier(ctx, tx, metricName, prevInterval, tier.Interval, comp)
			if prevDelta != nil {
				currentDelta = buildCoarserBucketsFromDelta(prevDelta, tier.Interval, comp)
			}
		}

		if err != nil {
			return written, err
		}
		// For the finest tier only, merge buckets older than the raw cutoff:
		// those buckets may already have a retained rollup while only late raw
		// samples remain in the raw table.
		var n int
		if tierIdx == 0 && !rawCutoff.IsZero() {
			n, err = s.writeRollupBucketsWithMergePointTx(ctx, metricName, tier.Interval, buckets, rawCutoff, time.Time{}, nil, tx)
		} else if tierIdx > 0 {
			deltaCutoff := alignRollupRetentionCutoff(now.Add(-policy.Tiers[tierIdx-1].Retention), tier.Interval)
			n, err = s.writeRollupBucketsWithMergePointTx(ctx, metricName, tier.Interval, buckets, time.Time{}, deltaCutoff, currentDelta, tx)
		} else {
			n, err = s.writeRollupBucketsTx(ctx, metricName, tier.Interval, buckets, tx)
		}
		if err != nil {
			return written, err
		}
		written += n
		prevInterval = tier.Interval
		prevDelta = currentDelta
	}

	if err := s.enforceRetentionWithinTx(ctx, metricName, now, policy, tx); err != nil {
		return written, err
	}
	return written, nil
}

// enforceRetentionWithinTx trims raw and rollup tiers after coarser data has
// been materialized. Cutoffs retain a bucket that straddles the boundary.
func (s *Store) enforceRetentionWithinTx(ctx context.Context, metricName string, now time.Time, policy RollupPolicy, tx *sql.Tx) error {
	if policy.RawRetention > 0 {
		if _, err := s.DeleteBeforeTx(ctx, metricName, policy.rawCutoff(now), tx); err != nil {
			return err
		}
	}
	for i, tier := range policy.Tiers {
		alignment := tier.Interval
		if i+1 < len(policy.Tiers) {
			alignment = policy.Tiers[i+1].Interval
		}
		cutoff := alignRollupRetentionCutoff(now.Add(-tier.Retention), alignment)
		if err := s.deleteRollupsBeforeTx(ctx, metricName, tier.Interval, cutoff, tx); err != nil {
			return err
		}
	}
	return nil
}

// deleteRollupsForIntervalsTx removes rows from resolutions that are no longer
// part of a metric's effective retention policy.
func (s *Store) deleteRollupsForIntervalsTx(ctx context.Context, metricName string, intervals []time.Duration, tx *sql.Tx) error {
	if len(intervals) == 0 {
		return nil
	}
	args := make([]any, 1, len(intervals)+1)
	args[0] = metricName
	placeholders := make([]string, len(intervals))
	for i, interval := range intervals {
		placeholders[i] = s.dialect.placeholder(i + 2)
		args = append(args, interval.Nanoseconds())
	}
	sqlText := fmt.Sprintf(
		`DELETE FROM %s WHERE metric_name = %s AND resolution_nano IN (%s)`,
		s.tables.rollups, s.dialect.placeholder(1), strings.Join(placeholders, ", "),
	)
	_, err := tx.ExecContext(ctx, sqlText, args...)
	return err
}

func alignRollupRetentionCutoff(cutoff time.Time, nextInterval time.Duration) time.Time {
	if nextInterval <= 0 {
		return cutoff.UTC()
	}
	return time.Unix(0, floorDivNano(cutoff.UTC().UnixNano(), nextInterval.Nanoseconds())).UTC()
}

// rollupKey identifies one rollup cell. The tag dimension (tagsHash) is part of
// the key so points carrying different tags never collapse into the same bucket.
//
// rollupKey 标识一个 rollup 单元；tagsHash 是 key 的一部分，确保不同标签
// 的点不会落入同一个桶。
type rollupKey struct {
	// entityID is the entity dimension of the rollup cell.
	//
	// entityID 是 rollup 单元的实体维度。
	entityID string
	// tagsHash is the stable fingerprint of the tag set.
	//
	// tagsHash 是标签集合的稳定指纹。
	tagsHash string
	// bucket is the bucket start timestamp in nanoseconds.
	//
	// bucket 是桶起始时间的纳秒时间戳。
	bucket int64
}

// buildFinestTier scans raw points for the metric and groups them into buckets
// of the given interval, keyed by (entity, tag set, bucket-start). Each point's
// tag map determines which series it belongs to.
//
// buildFinestTier 扫描某指标的原始点，并按给定 interval 分桶，key 为
// （实体、标签集合、桶起点）。每个点的标签 map 决定它属于哪条序列。
func (s *Store) buildFinestTier(ctx context.Context, q querier, metricName string, interval time.Duration, comp float64) (map[rollupKey]*rollupBucket, error) {
	return s.buildFinestTierBefore(ctx, q, metricName, interval, comp, time.Time{})
}

// buildFinestTierBefore aggregates only raw points older than before. A zero
// cutoff preserves the full-rebuild behavior used when raw is retained forever.
func (s *Store) buildFinestTierBefore(ctx context.Context, q querier, metricName string, interval time.Duration, comp float64, before time.Time) (map[rollupKey]*rollupBucket, error) {
	size := interval.Nanoseconds()
	out := make(map[rollupKey]*rollupBucket)
	args := []any{metricName}
	where := "metric_name = " + s.dialect.placeholder(1)
	if !before.IsZero() {
		args = append(args, before.UTC().UnixNano())
		where += " AND ts_nano < " + s.dialect.placeholder(2)
	}
	sqlText := fmt.Sprintf(
		`SELECT entity_id, ts_nano, value, tags FROM %s WHERE %s ORDER BY ts_nano ASC`,
		s.tables.points, where,
	)
	rows, err := q.QueryContext(ctx, sqlText, args...)

	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var entityID string
		var ts int64
		var value float64
		var rawTags any
		if err := rows.Scan(&entityID, &ts, &value, &rawTags); err != nil {
			return nil, err
		}
		tags, err := decodeMap(rawTags)
		if err != nil {
			return nil, err
		}
		hash, canonical, err := tagsFingerprint(tags)
		if err != nil {
			return nil, err
		}
		bucket := floorDivNano(ts, size)
		k := rollupKey{entityID: entityID, tagsHash: hash, bucket: bucket}
		b := out[k]
		if b == nil {
			b = newRollupBucket(comp)
			b.tagsHash = hash
			b.tagsJSON = canonical
			out[k] = b
		}
		b.addPoint(value, ts)
	}
	return out, rows.Err()
}

// buildCoarserTier composes a coarser tier from the already-stored finer tier:
// it reads the finer rollup rows and merges every finer bucket into the coarse
// bucket that shares its (entity, tag set). Tag identity is preserved end to
// end, so a coarse series only ever merges finer buckets of the same tag set.
//
// buildCoarserTier 基于已存储的细层 rollup 合成更粗层：它读取细层 rollup 行，
// 并把每个细桶合并进共享相同（实体、标签集合）的粗桶。标签身份会端到端保留，
// 因此一条粗粒度序列只会合并相同标签集合的细桶。
func (s *Store) buildCoarserTier(ctx context.Context, q querier, metricName string, fineInterval, coarseInterval time.Duration, comp float64) (map[rollupKey]*rollupBucket, error) {
	coarseSize := coarseInterval.Nanoseconds()
	out := make(map[rollupKey]*rollupBucket)
	fineRows, err := s.scanRollupRows(ctx, q, metricName, fineInterval)

	if err != nil {
		return nil, err
	}
	for _, fr := range fineRows {
		bucket := floorDivNano(fr.bucket, coarseSize)
		k := rollupKey{entityID: fr.entityID, tagsHash: fr.bucketData.tagsHash, bucket: bucket}
		b := out[k]
		if b == nil {
			b = newRollupBucket(comp)
			b.tagsHash = fr.bucketData.tagsHash
			b.tagsJSON = fr.bucketData.tagsJSON
			out[k] = b
		}
		b.mergeStored(fr.bucketData)
	}
	return out, nil
}

func buildCoarserBucketsFromDelta(delta map[rollupKey]*rollupBucket, coarseInterval time.Duration, comp float64) map[rollupKey]*rollupBucket {
	out := make(map[rollupKey]*rollupBucket)
	if len(delta) == 0 {
		return out
	}
	coarseSize := coarseInterval.Nanoseconds()
	for k, src := range delta {
		bucket := floorDivNano(k.bucket, coarseSize)
		ck := rollupKey{entityID: k.entityID, tagsHash: k.tagsHash, bucket: bucket}
		b := out[ck]
		if b == nil {
			b = newRollupBucket(comp)
			b.tagsHash = src.tagsHash
			b.tagsJSON = src.tagsJSON
			out[ck] = b
		}
		b.mergeStored(src)
	}
	return out
}

// storedRollup represents a rollup row reconstructed from storage.
//
// storedRollup 表示从存储中还原的一行 rollup 数据。
type storedRollup struct {
	// entityID is the entity stored on the rollup row.
	//
	// entityID 是 rollup 行中保存的实体。
	entityID string
	// bucket is the stored bucket start timestamp in nanoseconds.
	//
	// bucket 是存储的桶起始纳秒时间戳。
	bucket int64
	// bucketData is the reconstructed in-memory accumulator for the row.
	//
	// bucketData 是该行还原出的内存累加器。
	bucketData *rollupBucket
}

// scanRollupRows loads all rollup rows for a metric at a given resolution and
// reconstructs their in-memory accumulators (including tag identity and the
// decoded t-digest).
//
// scanRollupRows 读取某指标在给定分辨率下的所有 rollup 行，并还原它们的
// 内存累加器（包括标签身份和解码后的 t-digest）。
func (s *Store) scanRollupRows(ctx context.Context, q querier, metricName string, interval time.Duration) ([]storedRollup, error) {
	sqlText := fmt.Sprintf(
		`SELECT entity_id, tags_hash, tags, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest
		 FROM %s WHERE metric_name = %s AND resolution_nano = %s ORDER BY bucket_nano ASC`,
		s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2),
	)
	rows, err := q.QueryContext(ctx, sqlText, metricName, interval.Nanoseconds())

	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStoredRollups(rows)
}

// mergeRollupBucketsTx merges a consumed raw-data delta into the affected
// rollup buckets. Existing buckets are read by their indexed primary key, so a
// compact run performs work proportional to new/late data rather than to the
// complete retained history.
func (s *Store) mergeRollupBucketsTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, tx *sql.Tx) (int, error) {
	if len(buckets) == 0 {
		return 0, nil
	}

	keys := make([]rollupKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].entityID != keys[j].entityID {
			return keys[i].entityID < keys[j].entityID
		}
		if keys[i].tagsHash != keys[j].tagsHash {
			return keys[i].tagsHash < keys[j].tagsHash
		}
		return keys[i].bucket < keys[j].bucket
	})

	stmt := s.dialect.upsertRollupSQL(s.tables)
	resolution := interval.Nanoseconds()
	createdAt := time.Now().UTC().UnixNano()
	for _, key := range keys {
		bucket := buckets[key]
		existing, err := s.readRollupBucketTx(ctx, metricName, key.entityID, key.tagsHash, interval, key.bucket, tx)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			existing.mergeStored(bucket)
			bucket = existing
		}
		tagsJSON := bucket.tagsJSON
		if tagsJSON == "" {
			tagsJSON = "{}"
		}
		if _, err := tx.ExecContext(ctx, stmt,
			metricName, key.entityID, key.tagsHash, tagsJSON, resolution, key.bucket,
			bucket.count, bucket.sum, bucket.sumSq, bucket.min, bucket.max,
			bucket.firstVal, bucket.firstTS, bucket.lastVal, bucket.lastTS,
			bucket.digest.Encode(), createdAt,
		); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

// writeRollupBucketsTx upserts buckets within an existing transaction.
func (s *Store) writeRollupBucketsTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, tx *sql.Tx) (int, error) {
	return s.writeRollupBucketsWithMergePointTx(ctx, metricName, interval, buckets, time.Time{}, time.Time{}, nil, tx)
}

// writeRollupBucketsWithMergePointTx is the transactional version that executes
// all operations within an existing transaction. deltaCutoff marks coarser
// buckets whose source tier may already have aged out; a smaller recomputed
// bucket in that region is treated as late-arriving delta data and merged into
// the retained coarse bucket instead of replacing it.
func (s *Store) writeRollupBucketsWithMergePointTx(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket, mergeCutoff, protectCutoff time.Time, deltaBuckets map[rollupKey]*rollupBucket, tx *sql.Tx) (int, error) {
	if len(buckets) == 0 {
		return 0, nil
	}
	keys := make([]rollupKey, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].entityID != keys[j].entityID {
			return keys[i].entityID < keys[j].entityID
		}
		if keys[i].tagsHash != keys[j].tagsHash {
			return keys[i].tagsHash < keys[j].tagsHash
		}
		return keys[i].bucket < keys[j].bucket
	})

	stmt := s.dialect.upsertRollupSQL(s.tables)
	resNano := interval.Nanoseconds()
	now := time.Now().UTC().UnixNano()
	mergeCutoffNano := mergeCutoff.UnixNano()
	deltaCutoffNano := protectCutoff.UnixNano()
	written := 0

	for _, k := range keys {
		b := buckets[k]
		tagsJSON := b.tagsJSON
		if tagsJSON == "" {
			tagsJSON = "{}"
		}

		// If mergeCutoff is set and this bucket is older than the cutoff,
		// merge with existing data instead of replacing it.
		if !mergeCutoff.IsZero() && k.bucket < mergeCutoffNano {
			// Read existing row if it exists
			existing, err := s.readRollupBucketTx(ctx, metricName, k.entityID, k.tagsHash, interval, k.bucket, tx)
			if err != nil {
				return 0, err
			}
			if existing != nil {
				// Merge the new bucket into the existing one
				existing.mergeStored(b)
				b = existing
			}
		}
		if !protectCutoff.IsZero() && k.bucket < deltaCutoffNano {
			existing, err := s.readRollupBucketTx(ctx, metricName, k.entityID, k.tagsHash, interval, k.bucket, tx)
			if err != nil {
				return 0, err
			}
			if existing != nil {
				if deltaBuckets == nil {
					// Without delta tracking (raw is retained forever), the
					// source tier is rebuilt from full raw data and can replace.
				} else if delta := deltaBuckets[k]; delta != nil && delta.count > 0 {
					existing.mergeStored(delta)
					b = existing
				} else {
					continue
				}
			}
		}

		// Column order must match rollupColumns in dialect_rollup.go
		_, err := tx.ExecContext(ctx, stmt,
			metricName, k.entityID, k.tagsHash, tagsJSON, resNano, k.bucket,
			b.count, b.sum, b.sumSq, b.min, b.max,
			b.firstVal, b.firstTS, b.lastVal, b.lastTS,
			b.digest.Encode(), now,
		)
		if err != nil {
			return 0, err
		}
		written++
	}
	return written, nil
}

// deleteRollupsBeforeTx deletes stored rollup rows within a transaction.
func (s *Store) deleteRollupsBeforeTx(ctx context.Context, metricName string, interval time.Duration, before time.Time, tx *sql.Tx) error {
	sqlText := fmt.Sprintf(
		`DELETE FROM %s WHERE metric_name = %s AND resolution_nano = %s AND bucket_nano < %s`,
		s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
	)
	_, err := tx.ExecContext(ctx, sqlText, metricName, interval.Nanoseconds(), before.UTC().UnixNano())
	return err
}

// DeleteBeforeTx deletes raw points before a cutoff within a transaction.
func (s *Store) DeleteBeforeTx(ctx context.Context, metricName string, before time.Time, tx *sql.Tx) (int64, error) {
	sqlText := fmt.Sprintf(
		`DELETE FROM %s WHERE metric_name = %s AND ts_nano < %s`,
		s.tables.points, s.dialect.placeholder(1), s.dialect.placeholder(2),
	)
	result, err := tx.ExecContext(ctx, sqlText, metricName, before.UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// floorDivNano floors ts to the start of its size-wide bucket, handling
// negative timestamps (pre-epoch) toward negative infinity so buckets align
// consistently. Mirrors alignTime but operates on raw nanos.
//
// floorDivNano 将 ts 向下对齐到 size 宽桶的起点，并把负时间戳（Unix epoch 前）
// 朝负无穷取整，让桶保持一致对齐。它与 alignTime 逻辑一致，但直接操作纳秒值。
func floorDivNano(ts, size int64) int64 {
	rem := ((ts % size) + size) % size
	return ts - rem
}

// readRollupBucketTx reads a single rollup bucket from a transaction.
func (s *Store) readRollupBucketTx(ctx context.Context, metricName, entityID, tagsHash string, interval time.Duration, bucketNano int64, tx *sql.Tx) (*rollupBucket, error) {
	sqlText := fmt.Sprintf(
		`SELECT count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, tags
		 FROM %s WHERE metric_name = %s AND resolution_nano = %s AND entity_id = %s
		 AND tags_hash = %s AND bucket_nano = %s`,
		s.tables.rollups,
		s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
		s.dialect.placeholder(4), s.dialect.placeholder(5),
	)
	row := tx.QueryRowContext(ctx, sqlText, metricName, interval.Nanoseconds(), entityID, tagsHash, bucketNano)

	var count int64
	var sum, sumSq, minV, maxV, firstV, lastV float64
	var firstTS, lastTS int64
	var digestBlob []byte
	var rawTags any

	err := row.Scan(&count, &sum, &sumSq, &minV, &maxV, &firstV, &firstTS, &lastV, &lastTS, &digestBlob, &rawTags)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	td, err := DecodeTDigest(digestBlob)
	if err != nil {
		return nil, err
	}
	tagsJSON, err := rawTagsToJSON(rawTags)
	if err != nil {
		return nil, err
	}

	return &rollupBucket{
		count: count, sum: sum, sumSq: sumSq,
		min: minV, max: maxV,
		firstVal: firstV, firstTS: firstTS,
		lastVal: lastV, lastTS: lastTS,
		digest:   td,
		tagsHash: tagsHash,
		tagsJSON: tagsJSON,
	}, nil
}
