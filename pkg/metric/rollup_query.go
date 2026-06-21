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

	// Read rollup buckets at this resolution that are FULLY contained in the
	// inclusive window [Start, End] (entity and tag filters pushed into SQL),
	// then fold them into query.Interval-wide output buckets. Full containment
	// (rather than mere overlap) is what keeps a partially-overlapping bucket's
	// out-of-window samples from leaking into the result: a rollup bucket is an
	// indivisible summary, so a bucket straddling a window edge cannot be
	// trimmed to the window and is therefore excluded. Callers that need every
	// sample in a sub-bucket window must align the window to resolution
	// boundaries (or query raw points).
	rows, err := s.scanRollupRowsContained(ctx, q.MetricName, q.EntityID, q.Tags, resolution, q.Start, q.End)
	if err != nil {
		return nil, err
	}
	groups := foldRollupRows(nil, rows, query.Interval, comp)

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

// scanRollupRowsContained loads rollup rows for one resolution whose whole
// bucket window [bucket, bucket+resolution) lies inside the inclusive query
// window [start, end], with optional entity and tag filters pushed into SQL.
//
// Full containment (not mere overlap) is the boundary rule that keeps a rollup
// query from over-counting: a rollup bucket is an indivisible summary, so a
// bucket that only partially overlaps the window would drag its out-of-window
// samples in if it were included. The end boundary is inclusive to match the
// raw query semantics (raw uses ts <= end); a bucket whose last covered nano is
// exactly end is therefore still contained. A degenerate zero-width window
// (start == end) contains no whole bucket and yields an empty result — callers
// that need sub-bucket precision must query raw points.
//
// scanRollupRowsContained 读取某分辨率下整桶窗口 [bucket, bucket+resolution)
// 完整落在闭区间 [start, end] 内的 rollup 行，并可把实体和标签过滤下推到 SQL。
//
// 采用“完整包含”（而非仅重叠）作为边界规则，避免 rollup 查询过度计数：rollup
// 桶是不可分割的摘要，只与窗口部分重叠的桶若被纳入，会把窗口外的样本一起带入。
// end 边界为闭区间，以匹配原始查询语义（raw 使用 ts <= end）；因此最后覆盖纳秒
// 恰为 end 的桶仍算被包含。零宽窗口（start == end）不包含任何完整桶，返回空结果，
// 需要亚桶精度的调用方应查询原始点。
func (s *Store) scanRollupRowsContained(ctx context.Context, metricName, entityID string, tags map[string]string, resolution time.Duration, start, end time.Time) ([]storedRollup, error) {
	resNano := resolution.Nanoseconds()
	startNano := start.UTC().UnixNano()
	endNano := end.UTC().UnixNano()
	// A fully-contained bucket has start >= startNano and end (inclusive,
	// bucket+resNano-1) <= endNano, i.e. bucket in [startNano, endNano-resNano+1].
	// Push that exact closed range into SQL; no post-filter is then required.
	lower := startNano
	upper := endNano - resNano + 1 // inclusive upper bound for bucket_nano
	if upper < lower {
		return nil, nil // window narrower than one bucket: nothing is contained
	}
	rows, err := s.scanRollupRowsBetween(ctx, metricName, entityID, tags, resNano, lower, upper)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// scanRollupRowsBetween loads rollup rows for one resolution whose bucket start
// falls in the inclusive nano range [lowerBucket, upperBucket], with optional
// entity and tag filters pushed into SQL. It is the shared SQL primitive behind
// the containment and hybrid scans; the bucket-window semantics are imposed by
// the caller through the bounds it passes.
//
// scanRollupRowsBetween 读取某分辨率下桶起点落在闭区间
// [lowerBucket, upperBucket] 内的 rollup 行，并可把实体和标签过滤下推到 SQL。
// 它是包含扫描和混合扫描共用的 SQL 原语；桶窗口语义由调用方通过传入的边界决定。
func (s *Store) scanRollupRowsBetween(ctx context.Context, metricName, entityID string, tags map[string]string, resNano, lowerBucket, upperBucket int64) ([]storedRollup, error) {
	args := []any{metricName, resNano, lowerBucket, upperBucket}
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

// foldRollupRows folds stored rollup rows into interval-wide output buckets,
// merging summaries (including the t-digest) per bucket. groups may be nil, in
// which case a fresh map is allocated; passing an existing map lets a caller
// accumulate rollup and raw contributions into the same output buckets.
//
// foldRollupRows 将存储的 rollup 行折叠进 interval 宽的输出桶，按桶合并摘要
// （包括 t-digest）。groups 可为 nil，此时会分配新 map；传入已有 map 可让调用方
// 把 rollup 和 raw 的贡献累加到同一批输出桶中。
func foldRollupRows(groups map[int64]*rollupBucket, rows []storedRollup, interval time.Duration, comp float64) map[int64]*rollupBucket {
	if groups == nil {
		groups = make(map[int64]*rollupBucket)
	}
	size := interval.Nanoseconds()
	for _, r := range rows {
		bkt := floorDivNano(r.bucket, size)
		b := groups[bkt]
		if b == nil {
			b = newRollupBucket(comp)
			groups[bkt] = b
		}
		b.mergeStored(r.bucketData)
	}
	return groups
}

// foldRawPoints folds raw points into interval-wide output buckets, adding each
// observation into the matching bucket's accumulator. It shares the bucket map
// with foldRollupRows so a hybrid query can combine an old rollup half and a
// recent raw half into the same output buckets and aggregate them together
// (correct count/avg/percentile across the boundary).
//
// foldRawPoints 将原始点折叠进 interval 宽的输出桶，把每个观测值加入对应桶的
// 累加器。它与 foldRollupRows 共用桶 map，因此混合查询可以把旧 rollup 半边和
// 近期 raw 半边合并到同一批输出桶中并一起聚合（跨边界的 count/avg/百分位正确）。
func foldRawPoints(groups map[int64]*rollupBucket, points []Point, interval time.Duration, comp float64) map[int64]*rollupBucket {
	if groups == nil {
		groups = make(map[int64]*rollupBucket)
	}
	size := interval.Nanoseconds()
	for _, p := range points {
		ts := p.Timestamp.UTC().UnixNano()
		bkt := floorDivNano(ts, size)
		b := groups[bkt]
		if b == nil {
			b = newRollupBucket(comp)
			groups[bkt] = b
		}
		b.addPoint(p.Value, ts)
	}
	return groups
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
//   - If the query spans the raw retention boundary, it uses a hybrid approach:
//     read rollups for the old part and raw for the recent part, then merge
//     the results to avoid losing uncompacted recent data.
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
//   - 如果查询跨越原始数据保留期边界，它使用混合方式：读取旧部分的 rollup
//     和最近部分的原始点，然后合并结果，避免丢掉未 compact 的最近数据。
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

	rawCutoff := policy.rawCutoff(now)

	// Whole window inside raw retention (or raw kept forever) -> raw.
	if rawCutoff.IsZero() || !q.Start.Before(rawCutoff) {
		return s.Aggregate(ctx, query)
	}
	// Rate is raw-only; the caller asked for something rollups can't provide, so
	// answer from raw regardless of age.
	if query.Aggregation == AggRate {
		return s.Aggregate(ctx, query)
	}

	if q.Start.Before(rawCutoff) && !q.End.Before(rawCutoff) {
		for _, tier := range policy.Tiers { // finest-first
			if query.Interval < tier.Interval || query.Interval%tier.Interval != 0 {
				continue
			}
			if now.Add(-tier.Retention).After(q.Start) {
				continue
			}
			return s.seriesHybrid(ctx, query, rawCutoff, &tier)
		}
	}

	// Find the finest tier that can serve the entire window from the start.
	var servicingTier *RollupTier
	for _, tier := range policy.Tiers { // finest-first
		if query.Interval < tier.Interval || query.Interval%tier.Interval != 0 {
			continue
		}
		if now.Add(-tier.Retention).After(q.Start) {
			continue // tier doesn't reach back to the window start
		}
		servicingTier = &tier
		break
	}

	if servicingTier == nil {
		return s.Aggregate(ctx, query)
	}

	return s.AggregateRollup(ctx, query, servicingTier.Interval)
}

// seriesHybrid answers a query that spans the raw-retention boundary by reading
// rollups for the old part and raw for the recent part and folding BOTH into the
// same query.Interval-wide output buckets before reducing them.
//
// The old lossy approach reduced each half to AggregatePoints independently and
// then deduplicated by bucket time, letting raw fully replace a rollup point in
// any output bucket they shared. That dropped the rollup half of a bucket that
// straddles the boundary: e.g. with a 1h output bucket and the boundary mid-hour,
// an 11:05 rollup sample and an 11:35 raw sample both belong to the 11:00 bucket,
// yet the result kept only the raw sample (count/avg wrong). Folding both halves
// into one rollupBucket per output bucket fixes that: count, sum/avg, min/max and
// percentiles are computed over the union of the two halves.
//
// To avoid double counting or gaps, the caller passes a boundary already aligned
// to a rollup-resolution bucket. The rollup half supplies whole buckets strictly
// before the boundary and the raw half supplies points at or after it.
//
// seriesHybrid 回答跨越原始保留期边界的查询：读取旧部分的 rollup 和近期部分的
// 原始点，并在归约前把两者折叠进同一批 query.Interval 宽的输出桶。
//
// 旧的有损做法会把两半各自归约成 AggregatePoint 再按桶时间去重，让 raw 在二者
// 共有的输出桶里完全覆盖 rollup 点，从而丢掉跨边界桶的 rollup 半边（例如 1h 输出
// 桶且边界落在小时中间时，11:05 的 rollup 样本和 11:35 的 raw 样本都属于 11:00 桶，
// 结果却只剩 raw 样本，count/avg 错误）。把两半折叠进同一个 rollupBucket 即可修复：
// count、sum/avg、min/max 和百分位都在两半的并集上计算。
//
// 为避免重复计数或留空，调用方传入的边界已经对齐到 rollup 分辨率桶。rollup 半边
// 提供严格早于该边界的完整桶，raw 半边提供该边界及之后的点。
func (s *Store) seriesHybrid(ctx context.Context, query AggregateQuery, boundary time.Time, tier *RollupTier) ([]AggregatePoint, error) {
	q := query.Query.normalized()
	comp := s.cfg.RollupPolicy.compression()
	resNano := tier.Interval.Nanoseconds()
	startNano := q.Start.UnixNano()
	endNano := q.End.UnixNano()
	splitAt := boundary.UTC().UnixNano()

	groups := make(map[int64]*rollupBucket)

	// Old half: rollup buckets fully contained in [Start, min(splitAt, End]].
	// "Fully before splitAt" is bucket <= splitAt-resNano (splitAt is aligned);
	// also clamp to the query's own inclusive end so a bucket past End is never
	// pulled in when splitAt overshoots a short window.
	upperBucket := splitAt - resNano
	if clamp := endNano - resNano + 1; clamp < upperBucket {
		upperBucket = clamp
	}
	if upperBucket >= startNano {
		rows, err := s.scanRollupRowsBetween(ctx, q.MetricName, q.EntityID, q.Tags, resNano, startNano, upperBucket)
		if err != nil {
			return nil, err
		}
		foldRollupRows(groups, rows, query.Interval, comp)
	}

	// Recent half: raw points in [splitAt, End]. Reuse the raw Query path so the
	// entity and tag filters match the rollup half exactly, then fold the points
	// into the SAME output buckets so a bucket straddling splitAt aggregates both
	// halves. Skip when splitAt is past the window end (no recent raw to add).
	if splitAt <= endNano {
		rawQuery := q
		rawQuery.Start = time.Unix(0, splitAt).UTC()
		rawQuery.Limit = 0
		rawQuery.Offset = 0
		points, err := s.Query(ctx, rawQuery)
		if err != nil {
			return nil, err
		}
		foldRawPoints(groups, points, query.Interval, comp)
	}

	out, err := rollupGroupsToPoints(groups, query)
	if err != nil {
		return nil, err
	}
	return pageBuckets(out, query.BucketLimit, query.BucketOffset), nil
}
