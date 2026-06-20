package metric

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Compact runs the store's RollupPolicy over every metric: it rebuilds the
// rollup tiers up to `now` and enforces every retention window (raw points and
// each tier). It is the explicit maintenance entry point — call it from a cron
// job or scheduler. It is safe to call repeatedly and is idempotent for
// unchanged windows.
//
// Returns the number of rollup buckets written across all tiers and metrics.
//
// Compact 会对所有指标执行 Store 的 RollupPolicy：它会重建截至 `now` 的
// rollup 层，并执行每个保留窗口（原始点和各层级）。这是显式维护入口，
// 应从 cron 任务或调度器调用。它可以重复安全调用，未变化窗口保持幂等。
//
// 返回所有指标、所有层级中写入的 rollup 桶数量。
func (s *Store) Compact(ctx context.Context, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if !s.cfg.RollupPolicy.Enabled() {
		return 0, nil
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

// CompactMetric compacts a single metric. It builds the finest tier from raw
// points, then each coarser tier from the tier below it, upserts the resulting
// buckets, and finally deletes data that has aged out of each retention window
// (raw points first, then each tier).
//
// Rollups are keyed by tag set as well as entity, so each distinct tag
// combination (e.g. a GPU device_index) is summarized into its own series and
// can be queried independently after the raw points are gone.
//
// CompactMetric 会压缩单个指标。它先由原始点构建最细层，再由下一层之下的
// 层级逐层合成更粗层，upsert 生成的桶，最后删除每个保留窗口中过期的数据
// （先删除原始点，再删除各层级）。
//
// Rollup 会同时按标签集合和实体作为 key，因此每个不同标签组合（例如 GPU 的
// device_index）都会汇总成自己的序列，并且在原始点删除后仍可独立查询。
func (s *Store) CompactMetric(ctx context.Context, metricName string, now time.Time) (int, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	policy := s.cfg.RollupPolicy
	if !policy.Enabled() {
		return 0, nil
	}
	now = now.UTC()
	comp := policy.compression()

	written := 0
	var prevInterval time.Duration
	for tierIdx, tier := range policy.Tiers {
		var buckets map[rollupKey]*rollupBucket
		var err error
		if tierIdx == 0 {
			buckets, err = s.buildFinestTier(ctx, metricName, tier.Interval, comp)
		} else {
			buckets, err = s.buildCoarserTier(ctx, metricName, prevInterval, tier.Interval, comp)
		}
		if err != nil {
			return written, err
		}
		n, err := s.writeRollupBuckets(ctx, metricName, tier.Interval, buckets)
		if err != nil {
			return written, err
		}
		written += n
		prevInterval = tier.Interval
	}

	// Enforce retention windows after all tiers are materialized, so a coarser
	// tier is always built before the finer source it depends on is trimmed.
	if policy.RawRetention > 0 {
		cutoff := now.Add(-policy.RawRetention)
		if _, err := s.DeleteBefore(ctx, metricName, cutoff); err != nil {
			return written, err
		}
	}
	for _, tier := range policy.Tiers {
		cutoff := now.Add(-tier.Retention)
		if err := s.deleteRollupsBefore(ctx, metricName, tier.Interval, cutoff); err != nil {
			return written, err
		}
	}
	return written, nil
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
func (s *Store) buildFinestTier(ctx context.Context, metricName string, interval time.Duration, comp float64) (map[rollupKey]*rollupBucket, error) {
	size := interval.Nanoseconds()
	out := make(map[rollupKey]*rollupBucket)
	sqlText := fmt.Sprintf(
		`SELECT entity_id, ts_nano, value, tags FROM %s WHERE metric_name = %s ORDER BY ts_nano ASC`,
		s.tables.points, s.dialect.placeholder(1),
	)
	rows, err := s.reader().QueryContext(ctx, sqlText, metricName)
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
func (s *Store) buildCoarserTier(ctx context.Context, metricName string, fineInterval, coarseInterval time.Duration, comp float64) (map[rollupKey]*rollupBucket, error) {
	coarseSize := coarseInterval.Nanoseconds()
	out := make(map[rollupKey]*rollupBucket)
	fineRows, err := s.scanRollupRows(ctx, metricName, fineInterval)
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
func (s *Store) scanRollupRows(ctx context.Context, metricName string, interval time.Duration) ([]storedRollup, error) {
	sqlText := fmt.Sprintf(
		`SELECT entity_id, tags_hash, tags, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest
		 FROM %s WHERE metric_name = %s AND resolution_nano = %s ORDER BY bucket_nano ASC`,
		s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2),
	)
	rows, err := s.reader().QueryContext(ctx, sqlText, metricName, interval.Nanoseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStoredRollups(rows)
}

// writeRollupBuckets upserts a set of computed buckets for one resolution.
//
// writeRollupBuckets 将某个分辨率下计算出的 rollup 桶批量 upsert 到数据库。
func (s *Store) writeRollupBuckets(ctx context.Context, metricName string, interval time.Duration, buckets map[rollupKey]*rollupBucket) (int, error) {
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

	run := func(ex execer) error {
		for _, k := range keys {
			b := buckets[k]
			tagsJSON := b.tagsJSON
			if tagsJSON == "" {
				tagsJSON = "{}"
			}
			// Column order must match rollupColumns in dialect_rollup.go:
			// metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano,
			// count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val,
			// last_ts, digest, created_at.
			_, err := ex.ExecContext(ctx, stmt,
				metricName, k.entityID, k.tagsHash, tagsJSON, resNano, k.bucket,
				b.count, b.sum, b.sumSq, b.min, b.max,
				b.firstVal, b.firstTS, b.lastVal, b.lastTS,
				b.digest.Encode(), now,
			)
			if err != nil {
				return err
			}
		}
		return nil
	}

	if len(keys) == 1 {
		if err := run(s.db); err != nil {
			return 0, err
		}
		return len(keys), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := run(tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(keys), nil
}

// deleteRollupsBefore deletes stored rollup rows older than a cutoff.
//
// deleteRollupsBefore 删除早于指定时间的 rollup 行。
func (s *Store) deleteRollupsBefore(ctx context.Context, metricName string, interval time.Duration, before time.Time) error {
	sqlText := fmt.Sprintf(
		`DELETE FROM %s WHERE metric_name = %s AND resolution_nano = %s AND bucket_nano < %s`,
		s.tables.rollups, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
	)
	_, err := s.db.ExecContext(ctx, sqlText, metricName, interval.Nanoseconds(), before.UTC().UnixNano())
	return err
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
