package metric

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestAggregateRollupExcludesPartialBucket verifies the rollup query no longer
// drags an out-of-window sample in through a partially-overlapping bucket.
//
// Two samples share one 1m rollup bucket [00:00:00, 00:01:00): 00:00:10=100 and
// 00:00:50=10. A query window of [00:00:40, 00:01:30] only overlaps part of that
// bucket. Because a rollup bucket is an indivisible summary, the old "any
// overlap" rule folded the whole bucket in and counted 00:00:10 (value 100),
// which lies OUTSIDE the window. Full containment excludes the straddling
// bucket entirely, so the out-of-window sample can no longer leak in.
//
// TestAggregateRollupExcludesPartialBucket 验证 rollup 查询不再通过部分重叠的桶
// 把窗口外的样本带进来。
func TestAggregateRollupExcludesPartialBucket(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "win", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	batch := []Point{
		{MetricName: "win", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 100}, // out of window
		{MetricName: "win", EntityID: "n1", Timestamp: base.Add(50 * time.Second), Value: 10},  // in window
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Window [00:00:40, 00:01:30] partially overlaps the single 1m bucket.
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "win", EntityID: "n1", Start: base.Add(40 * time.Second), End: base.Add(90 * time.Second)},
		Aggregation: AggSum,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("aggregate rollup: %v", err)
	}
	// The partially-overlapping bucket is excluded under full containment, so the
	// out-of-window 00:00:10 sample (value 100) must not appear. Concretely: no
	// returned bucket may carry the out-of-window value or a count of 2.
	for _, p := range res {
		if p.Count >= 2 || p.Value == 100 {
			t.Fatalf("partial bucket leaked out-of-window sample: %#v", p)
		}
	}
}

// TestAggregateRollupEndBoundaryInclusive verifies the rollup query end boundary
// is inclusive (matching the raw query's ts <= end) and that a window aligned to
// resolution boundaries returns every whole bucket it covers.
//
// Buckets at 00:00 and 00:01 are both whole-contained in [00:00:00, 00:02:00]:
// the second bucket's window is [00:01:00, 00:02:00), whose last covered nano is
// < 00:02:00, so an inclusive end keeps it. The previous half-open (< end) rule
// could drop a bucket sitting exactly on the end edge.
//
// TestAggregateRollupEndBoundaryInclusive 验证 rollup 查询 end 边界为闭区间
// （与 raw 的 ts <= end 一致），且对齐到分辨率边界的窗口会返回它覆盖的每个完整桶。
func TestAggregateRollupEndBoundaryInclusive(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "edge", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	batch := []Point{
		{MetricName: "edge", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 1},             // bucket 00:00
		{MetricName: "edge", EntityID: "n1", Timestamp: base.Add(time.Minute + 10*time.Second), Value: 2}, // bucket 00:01
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}

	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "edge", EntityID: "n1", Start: base, End: base.Add(2 * time.Minute)},
		Aggregation: AggSum,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("aggregate rollup: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("aligned window should return both whole buckets, got %d: %#v", len(res), res)
	}
	if res[0].Value != 1 || res[1].Value != 2 {
		t.Fatalf("unexpected bucket values: %#v", res)
	}
}

// TestSeriesHybridMergesStraddlingBucket verifies a query output bucket that
// straddles the raw-retention boundary aggregates BOTH its rollup half and its
// raw half, instead of letting raw replace the rollup half.
//
// With a 30m raw retention and now=12:00 the boundary is 11:30. A 1h output
// bucket [11:00, 12:00) therefore straddles it. An 11:05 sample (value 10, older
// than the boundary -> rolled up, raw deleted) and an 11:35 sample (value 100,
// within retention -> still raw) both belong to that 11:00 bucket. The correct
// answer folds both: count=2, avg=(10+100)/2=55. The old merge-by-bucket-time
// path kept only the raw half and reported count=1, avg=100.
//
// TestSeriesHybridMergesStraddlingBucket 验证跨越原始保留期边界的输出桶会同时
// 聚合 rollup 半边和 raw 半边，而不是让 raw 覆盖 rollup 半边。
func TestSeriesHybridMergesStraddlingBucket(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "straddle", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	// 11:05 is older than the 11:30 cutoff (rolled up, raw deleted on compact);
	// 11:35 is within retention (kept raw).
	old := now.Add(-55 * time.Minute)    // 11:05
	recent := now.Add(-25 * time.Minute) // 11:35
	batch := []Point{
		{MetricName: "straddle", EntityID: "n1", Timestamp: old, Value: 10},
		{MetricName: "straddle", EntityID: "n1", Timestamp: recent, Value: 100},
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Confirm the old raw point was deleted and the recent one survives, so the
	// two halves genuinely come from different sources.
	oldRaw, err := s.Query(ctx, Query{MetricName: "straddle", EntityID: "n1", Start: old.Add(-time.Minute), End: old.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query old raw: %v", err)
	}
	if len(oldRaw) != 0 {
		t.Fatalf("expected old raw point deleted by retention, got %d", len(oldRaw))
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "straddle", EntityID: "n1", Start: now.Add(-time.Hour), End: now},
		Aggregation: AggAvg,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}

	bucket := alignTime(now.Add(-time.Hour), time.Hour) // 11:00
	var found bool
	for _, p := range got {
		if p.Bucket.Equal(bucket) {
			found = true
			if p.Count != 2 {
				t.Fatalf("straddling bucket count: got %d want 2 (%#v)", p.Count, p)
			}
			if math.Abs(p.Value-55) > 1e-9 {
				t.Fatalf("straddling bucket avg: got %v want 55 (%#v)", p.Value, p)
			}
		}
	}
	if !found {
		t.Fatalf("expected a merged 11:00 bucket in result: %#v", got)
	}
}

// TestSeriesHybridSumAcrossBoundary is the AggSum analogue: the straddling 1h
// bucket must sum both halves (10 + 100 = 110), proving the merge is at the
// summary level rather than a bucket-time replacement.
//
// TestSeriesHybridSumAcrossBoundary 是 AggSum 版本：跨边界的 1h 桶必须把两半求和
// （10 + 100 = 110），证明合并发生在摘要层面，而不是按桶时间替换。
func TestSeriesHybridSumAcrossBoundary(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "sumstraddle", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	batch := []Point{
		{MetricName: "sumstraddle", EntityID: "n1", Timestamp: now.Add(-55 * time.Minute), Value: 10},
		{MetricName: "sumstraddle", EntityID: "n1", Timestamp: now.Add(-25 * time.Minute), Value: 100},
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "sumstraddle", EntityID: "n1", Start: now.Add(-time.Hour), End: now},
		Aggregation: AggSum,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	bucket := alignTime(now.Add(-time.Hour), time.Hour)
	var found bool
	for _, p := range got {
		if p.Bucket.Equal(bucket) {
			found = true
			if p.Count != 2 || math.Abs(p.Value-110) > 1e-9 {
				t.Fatalf("straddling sum bucket: got count=%d value=%v want count=2 value=110", p.Count, p.Value)
			}
		}
	}
	if !found {
		t.Fatalf("expected merged sum bucket: %#v", got)
	}
}

func TestSeriesHybridIncludesRawBetweenCutoffAndNextBucket(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "cutgap", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Date(2026, 6, 18, 12, 0, 30, 0, time.UTC)
	old := time.Date(2026, 6, 18, 11, 30, 10, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "cutgap", EntityID: "n1", Timestamp: old, Value: 10}); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	recentUncompacted := time.Date(2026, 6, 18, 11, 30, 45, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "cutgap", EntityID: "n1", Timestamp: recentUncompacted, Value: 100}); err != nil {
		t.Fatalf("write recent: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "cutgap", EntityID: "n1", Start: time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), End: now},
		Aggregation: AggCount,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(got) != 1 || got[0].Count != 2 {
		t.Fatalf("series should include rollup plus raw at the aligned cutoff, got %#v", got)
	}
}

func TestSeriesHybridEndAtCutoffIncludesRawBoundary(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "cutedge", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Date(2026, 6, 18, 12, 0, 30, 0, time.UTC)
	cutoff := policy.rawCutoff(now)
	if err := s.Write(ctx, Point{MetricName: "cutedge", EntityID: "n1", Timestamp: cutoff, Value: 7}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "cutedge", EntityID: "n1", Start: cutoff.Add(-time.Hour), End: cutoff},
		Aggregation: AggCount,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var total int
	for _, p := range got {
		total += p.Count
	}
	if total != 1 {
		t.Fatalf("series should include raw point exactly at cutoff/end, got %#v", got)
	}
}

func TestCompactDoesNotDoubleCountRawBucketAfterCutoffAdvances(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "nodup", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}

	now := time.Date(2026, 6, 18, 12, 0, 30, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "nodup", EntityID: "n1", Timestamp: time.Date(2026, 6, 18, 11, 30, 10, 0, time.UTC), Value: 10}); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact 1: %v", err)
	}
	if err := s.Write(ctx, Point{MetricName: "nodup", EntityID: "n1", Timestamp: time.Date(2026, 6, 18, 11, 30, 45, 0, time.UTC), Value: 100}); err != nil {
		t.Fatalf("write recent: %v", err)
	}
	now = now.Add(time.Minute)
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact 2: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "nodup", EntityID: "n1", Start: time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), End: now},
		Aggregation: AggCount,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var total int
	for _, p := range got {
		total += p.Count
	}
	if total != 2 {
		t.Fatalf("compaction should not double-count a bucket after cutoff advances, got %#v", got)
	}
}
