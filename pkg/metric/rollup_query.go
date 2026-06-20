package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// AggregateRollup answers an AggregateQuery from a stored rollup tier instead of
// raw points. resolution names which tier to read (it must match a tier
// Interval that Compact has materialized). The query Interval must be a positive
// integer multiple of resolution, so each output bucket is composed of whole
// rollup buckets.
//
// query.Tags is honored: only rollup series whose stored tag set matches the
// filter are folded in, so a tag filter selects the same data it would over raw
// points (each distinct tag combination is its own rollup series). With no tag
// filter, all tag series within a bucket merge together, matching the raw path's
// cross-tag aggregation.
//
// Every aggregation works except AggRate, which needs the ordered raw series and
// is therefore raw-only. Percentiles (p50, p95, p99, and arbitrary pXX) are
// answered by merging the per-bucket t-digests, so they survive downsampling
// with bounded error.
//
// AggregateRollup 从已存储的 rollup 层回答 AggregateQuery，而不是读取原始点。
// resolution 指定要读取的层级（必须匹配 Compact 已物化的某个层级 Interval）。
// 查询的 Interval 必须是 resolution 的正整数倍，因此每个输出桶都由完整的
// rollup 桶组成。
//
// query.Tags 会被遵守：只有存储标签集合匹配过滤条件的 rollup 序列会被合入，
// 因此标签过滤会选中与原始点查询相同的数据（每个不同标签组合都是自己的
// rollup 序列）。没有标签过滤时，同一桶内的所有标签序列会合并在一起，
// 与原始路径的跨标签聚合一致。
//
// 除 AggRate 外，每种聚合都可用；AggRate 需要有序原始序列，因此只能基于原始点。
// 百分位（p50、p95、p99 和任意 pXX）通过合并每桶 t-digest 回答，因此能在
// 降采样后以有界误差保留下来。
func (s *Store) AggregateRollup(ctx context.Context, query AggregateQuery, resolution time.Duration) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if resolution <= 0 {
		return nil, fmt.Errorf("%w: rollup resolution must be positive", ErrInvalidArgument)
	}
	if query.Interval < resolution || query.Interval%resolution != 0 {
		return nil, fmt.Errorf("%w: query interval must be a positive multiple of the rollup resolution", ErrInvalidArgument)
	}
	if query.Aggregation == AggRate {
		return nil, fmt.Errorf("%w: rate is not derivable from rollups (raw only)", ErrInvalidArgument)
	}

	q := query.Query.normalized()
	comp := s.cfg.RollupPolicy.compression()
	size := query.Interval.Nanoseconds()

	// Read every rollup bucket at this resolution overlapping the window (with
	// the entity and tag filters pushed into SQL), then fold them into
	// query.Interval-wide output buckets.
	rows, err := s.scanRollupRowsRange(ctx, q.MetricName, q.EntityID, q.Tags, resolution, q.Start, q.End)
	if err != nil {
		return nil, err
	}
	groups := make(map[int64]*rollupBucket)
	for _, r := range rows {
		bkt := floorDivNano(r.bucket, size)
		b := groups[bkt]
		if b == nil {
			b = newRollupBucket(comp)
			groups[bkt] = b
		}
		b.mergeStored(r.bucketData)
	}

	out, err := rollupGroupsToPoints(groups, query)
	if err != nil {
		return nil, err
	}
	return pageBuckets(out, query.BucketLimit, query.BucketOffset), nil
}

// rollupGroupsToPoints turns the merged output buckets into ordered
// AggregatePoints, computing the requested aggregation from each bucket's
// summaries/digest. FillEmpty emits zero-count buckets for gaps, mirroring the
// raw AggregatePoints behavior.
//
// rollupGroupsToPoints 将合并后的输出桶转换为有序 AggregatePoint，并根据
// 每个桶的摘要或 digest 计算请求的聚合。FillEmpty 会为空洞输出零计数桶，
// 与原始 AggregatePoints 行为一致。
func rollupGroupsToPoints(groups map[int64]*rollupBucket, query AggregateQuery) ([]AggregatePoint, error) {
	var bucketStarts []int64
	if query.FillEmpty {
		for t := alignTime(query.Start, query.Interval); !t.After(query.End); t = t.Add(query.Interval) {
			bucketStarts = append(bucketStarts, t.UnixNano())
		}
	} else {
		for k := range groups {
			bucketStarts = append(bucketStarts, k)
		}
		sort.Slice(bucketStarts, func(i, j int) bool { return bucketStarts[i] < bucketStarts[j] })
	}

	out := make([]AggregatePoint, 0, len(bucketStarts))
	for _, start := range bucketStarts {
		b := groups[start]
		if b == nil {
			out = append(out, AggregatePoint{
				MetricName: query.MetricName,
				EntityID:   query.EntityID,
				Bucket:     time.Unix(0, start).UTC(),
			})
			continue
		}
		v, ok := b.value(query.Aggregation)
		if !ok {
			return nil, fmt.Errorf("%w: aggregation %q not supported over rollups", ErrInvalidArgument, query.Aggregation)
		}
		out = append(out, AggregatePoint{
			MetricName: query.MetricName,
			EntityID:   query.EntityID,
			Bucket:     time.Unix(0, start).UTC(),
			Value:      v,
			Count:      int(b.count),
		})
	}
	return out, nil
}

// scanRollupRowsRange loads rollup rows for one resolution within [start, end]
// (by bucket start), with optional entity and tag filters pushed into SQL. The
// lower bound is aligned down to the resolution so buckets straddling start are
// still included. Tag filtering uses the same dialect JSON accessor as the raw
// Query path, so rollup and raw tag semantics match exactly.
//
// scanRollupRowsRange 读取一个分辨率在 [start, end] 内的 rollup 行
// （按桶起点判断），并可把实体和标签过滤下推到 SQL。下界会向下对齐到
// resolution，因此跨越 start 的桶仍会被包含。标签过滤使用与原始 Query 路径
// 相同的方言 JSON 访问器，所以 rollup 和原始标签语义完全一致。
func (s *Store) scanRollupRowsRange(ctx context.Context, metricName, entityID string, tags map[string]string, resolution time.Duration, start, end time.Time) ([]storedRollup, error) {
	resNano := resolution.Nanoseconds()
	lower := floorDivNano(start.UTC().UnixNano(), resNano)
	args := []any{metricName, resNano, lower, end.UTC().UnixNano()}
	parts := []string{
		"metric_name = " + s.dialect.placeholder(1),
		"resolution_nano = " + s.dialect.placeholder(2),
		"bucket_nano >= " + s.dialect.placeholder(3),
		"bucket_nano <= " + s.dialect.placeholder(4),
	}
	if strings.TrimSpace(entityID) != "" {
		args = append(args, entityID)
		parts = append(parts, "entity_id = "+s.dialect.placeholder(len(args)))
	}
	for _, k := range sortedKeys(tags) {
		args = append(args, tags[k])
		parts = append(parts, s.dialect.jsonExtractEquals("tags", k, s.dialect.placeholder(len(args))))
	}
	sqlText := fmt.Sprintf(
		`SELECT entity_id, tags_hash, tags, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest
		 FROM %s WHERE %s ORDER BY bucket_nano ASC`,
		s.tables.rollups, strings.Join(parts, " AND "),
	)
	rows, err := s.reader().QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStoredRollups(rows)
}

// scanStoredRollups reconstructs storedRollup rows (including tag identity and
// the decoded t-digest) from a result set whose columns are, in order:
// entity_id, tags_hash, tags, bucket_nano, count, sum, sum_sq, min_val, max_val,
// first_val, first_ts, last_val, last_ts, digest.
//
// scanStoredRollups 从结果集中还原 storedRollup 行（包括标签身份和解码后的
// t-digest）。结果集的列顺序必须是：entity_id、tags_hash、tags、bucket_nano、
// count、sum、sum_sq、min_val、max_val、first_val、first_ts、last_val、
// last_ts、digest。
func scanStoredRollups(rows *sql.Rows) ([]storedRollup, error) {
	var out []storedRollup
	for rows.Next() {
		var (
			eid                                   string
			tagsHash                              string
			rawTags                               any
			bucketNano                            int64
			count                                 int64
			sum, sumSq, minV, maxV, firstV, lastV float64
			firstTS, lastTS                       int64
			digestBlob                            []byte
		)
		if err := rows.Scan(&eid, &tagsHash, &rawTags, &bucketNano, &count, &sum, &sumSq, &minV, &maxV, &firstV, &firstTS, &lastV, &lastTS, &digestBlob); err != nil {
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
		out = append(out, storedRollup{
			entityID: eid,
			bucket:   bucketNano,
			bucketData: &rollupBucket{
				count: count, sum: sum, sumSq: sumSq,
				min: minV, max: maxV,
				firstVal: firstV, firstTS: firstTS,
				lastVal: lastV, lastTS: lastTS,
				digest:   td,
				tagsHash: tagsHash,
				tagsJSON: tagsJSON,
			},
		})
	}
	return out, rows.Err()
}

// rawTagsToJSON normalizes a scanned tags column (string or []byte) into the
// canonical JSON string used when the bucket is re-written by a coarser tier.
//
// rawTagsToJSON 将扫描出的 tags 列（string 或 []byte）规范化为 JSON 字符串，
// 供更粗层级重写桶时复用。
func rawTagsToJSON(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "{}", nil
	case string:
		if x == "" {
			return "{}", nil
		}
		return x, nil
	case []byte:
		if len(x) == 0 {
			return "{}", nil
		}
		return string(x), nil
	default:
		return "", fmt.Errorf("unsupported tags column type %T", v)
	}
}

// Series answers an AggregateQuery by transparently choosing the best data
// source for the requested window, given `now`:
//
//   - If rollups are disabled, or the whole window still lies within raw
//     retention, it reads raw points (Aggregate) for full fidelity.
//   - Otherwise it picks the FINEST rollup tier that both (a) has an Interval
//     dividing query.Interval and (b) whose retention reaches back to the start
//     of the window, and serves the query from that tier.
//   - If no tier qualifies, it falls back to raw (which may be incomplete for
//     data already aged out) so the call still returns its best answer.
//
// query.Tags is honored on both branches: the raw path already filters by tag,
// and the rollup path filters by the stored tag set, so a tag filter selects the
// same series regardless of which source answers. This is the "downsampling
// TSDB" read path: recent ranges answer from raw at full resolution, older
// ranges answer from progressively coarser rollups.
//
// Series 会在给定 `now` 的情况下，通过透明选择最佳数据源来回答 AggregateQuery：
//
//   - 如果 rollup 已禁用，或整个窗口仍在原始保留期内，它会读取原始点
//     （Aggregate）以获得完整保真度。
//   - 否则它会选择最细的 rollup 层，该层必须同时满足 (a) Interval 能整除
//     query.Interval，且 (b) 保留时间能覆盖到窗口起点，然后从该层服务查询。
//   - 如果没有层级符合条件，它会回退到原始点（对于已经过期的数据可能不完整），
//     让调用仍返回当前能给出的最佳答案。
//
// query.Tags 在两条分支上都会被遵守：原始路径已按标签过滤，rollup 路径会按
// 存储的标签集合过滤，因此无论由哪个数据源回答，标签过滤都会选择相同序列。
// 这是“降采样 TSDB”的读取路径：近期范围以完整分辨率从原始点回答，旧范围从
// 逐级更粗的 rollup 回答。
func (s *Store) Series(ctx context.Context, query AggregateQuery, now time.Time) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	policy := s.cfg.RollupPolicy
	if !policy.Enabled() {
		return s.Aggregate(ctx, query)
	}
	q := query.Query.normalized()
	now = now.UTC()

	// Whole window inside raw retention (or raw kept forever) -> raw.
	if policy.RawRetention == 0 || !q.Start.Before(now.Add(-policy.RawRetention)) {
		return s.Aggregate(ctx, query)
	}
	// Rate is raw-only; the caller asked for something rollups can't provide, so
	// answer from raw regardless of age.
	if query.Aggregation == AggRate {
		return s.Aggregate(ctx, query)
	}

	for _, tier := range policy.Tiers { // finest-first
		if query.Interval < tier.Interval || query.Interval%tier.Interval != 0 {
			continue
		}
		if now.Add(-tier.Retention).After(q.Start) {
			continue // tier doesn't reach back to the window start
		}
		return s.AggregateRollup(ctx, query, tier.Interval)
	}
	// No tier reaches back far enough at a compatible resolution.
	return s.Aggregate(ctx, query)
}
