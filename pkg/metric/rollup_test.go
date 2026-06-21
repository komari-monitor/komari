package metric

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

// newRollupStore opens an in-memory SQLite store with the given policy. A unique
// DSN per test keeps the shared-cache in-memory databases isolated.
//
// newRollupStore 使用给定策略打开内存 SQLite store；每个测试使用唯一 DSN，
// 避免 shared-cache 内存数据库相互影响。
func newRollupStore(t *testing.T, policy RollupPolicy) *Store {
	t.Helper()
	dsn := fmt.Sprintf("file:rollup-%d?mode=memory&cache=shared", time.Now().UnixNano())
	store, err := Open(context.Background(), SQLite(dsn, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open rollup store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// TestArbitraryPercentileOverRaw verifies arbitrary percentile (pxx)
// aggregation end-to-end over raw points.
//
// TestArbitraryPercentileOverRaw 验证任意百分位（pxx）聚合在原始点路径上的
// 端到端行为。
func TestArbitraryPercentileOverRaw(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "lat", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 1; i <= 100; i++ { // values 1..100 in one bucket
		batch = append(batch, Point{MetricName: "lat", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, tc := range []struct {
		agg  Aggregation
		want float64
	}{
		{Pxx(50), percentileSortedRange(1, 100, 0.50)},
		{Pxx(90), percentileSortedRange(1, 100, 0.90)},
		{Pxx(99), percentileSortedRange(1, 100, 0.99)},
		{Pxx(99.9), percentileSortedRange(1, 100, 0.999)},
		{Pxx(75), percentileSortedRange(1, 100, 0.75)},
	} {
		res, err := s.Aggregate(ctx, AggregateQuery{
			Query:       Query{MetricName: "lat", EntityID: "n1", Start: base, End: base.Add(time.Hour)},
			Aggregation: tc.agg,
			Interval:    time.Hour,
		})
		if err != nil {
			t.Fatalf("aggregate %s: %v", tc.agg, err)
		}
		if len(res) != 1 {
			t.Fatalf("%s: expected 1 bucket, got %d", tc.agg, len(res))
		}
		if math.Abs(res[0].Value-tc.want) > 1e-9 {
			t.Fatalf("%s: got %v want %v", tc.agg, res[0].Value, tc.want)
		}
	}
}

// percentileSortedRange computes an exact percentile for an integer range.
//
// percentileSortedRange 为整数范围计算精确百分位。
func percentileSortedRange(lo, hi int, q float64) float64 {
	vals := make([]float64, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		vals = append(vals, float64(i))
	}
	return percentileSorted(vals, q)
}

// TestPxxStringForms verifies arbitrary percentile aggregation names.
//
// TestPxxStringForms 验证任意百分位聚合名的字符串形式。
func TestPxxStringForms(t *testing.T) {
	if Pxx(95) != AggP95 {
		t.Fatalf("Pxx(95)=%q, want %q", Pxx(95), AggP95)
	}
	if Pxx(50) != AggP50 {
		t.Fatalf("Pxx(50)=%q, want %q", Pxx(50), AggP50)
	}
	if Pxx(99.9) != Aggregation("p99.9") {
		t.Fatalf("Pxx(99.9)=%q", Pxx(99.9))
	}
	// Out-of-range percentiles must be rejected by validation.
	q := AggregateQuery{Query: Query{MetricName: "m", Start: time.Now(), End: time.Now().Add(time.Hour)}, Aggregation: Pxx(100), Interval: time.Minute}
	if err := q.Validate(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("p100 should be invalid, got %v", err)
	}
}

// TestCompactBuildsFinestTier verifies Compact builds correct finest-tier
// rollup statistics from raw points.
//
// TestCompactBuildsFinestTier 验证 Compact 能从原始点构建正确的最细层
// rollup 统计。
func TestCompactBuildsFinestTier(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "m", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// Two 1-minute buckets: bucket A has values 0..59, bucket B has 60..119.
	var batch []Point
	for i := 0; i < 120; i++ {
		batch = append(batch, Point{MetricName: "m", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	now := base.Add(10 * time.Minute)
	written, err := s.Compact(ctx, now)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if written != 2 {
		t.Fatalf("expected 2 rollup buckets written, got %d", written)
	}
	// AggregateRollup at the 1m resolution should reproduce per-bucket stats.
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "m", EntityID: "n1", Start: base, End: base.Add(10 * time.Minute)},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("aggregate rollup: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(res))
	}
	if res[0].Value != 29.5 || res[0].Count != 60 {
		t.Fatalf("bucket A avg/count wrong: %#v", res[0])
	}
	if res[1].Value != 89.5 || res[1].Count != 60 {
		t.Fatalf("bucket B avg/count wrong: %#v", res[1])
	}
	// Sum/min/max derived from rollup must match raw.
	for _, tc := range []struct {
		agg   Aggregation
		wantA float64
		wantB float64
	}{
		{AggSum, 1770, 5370}, // sum 0..59 and 60..119
		{AggMin, 0, 60},
		{AggMax, 59, 119},
	} {
		r, err := s.AggregateRollup(ctx, AggregateQuery{
			Query:       Query{MetricName: "m", EntityID: "n1", Start: base, End: base.Add(10 * time.Minute)},
			Aggregation: tc.agg,
			Interval:    time.Minute,
		}, time.Minute)
		if err != nil {
			t.Fatalf("rollup %s: %v", tc.agg, err)
		}
		if r[0].Value != tc.wantA || r[1].Value != tc.wantB {
			t.Fatalf("%s: got %v,%v want %v,%v", tc.agg, r[0].Value, r[1].Value, tc.wantA, tc.wantB)
		}
	}
}

// TestCompactCascadeFineToCoarse verifies a coarse tier composed from a fine
// tier matches a direct raw rollup.
//
// TestCompactCascadeFineToCoarse 验证由细层合成的粗层与直接从原始点计算的
// rollup 一致。
func TestCompactCascadeFineToCoarse(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: time.Hour},
			{Interval: 5 * time.Minute, Retention: 24 * time.Hour},
		},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "c", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// 5 minutes of data, one point per second, value = second index 0..299.
	var batch []Point
	for i := 0; i < 300; i++ {
		batch = append(batch, Point{MetricName: "c", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// The single 5m coarse bucket should cover all 300 points.
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "c", EntityID: "n1", Start: base, End: base.Add(5 * time.Minute)},
		Aggregation: AggAvg,
		Interval:    5 * time.Minute,
	}, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if len(res) != 1 || res[0].Count != 300 {
		t.Fatalf("expected 1 coarse bucket covering 300 points, got %#v", res)
	}
	if math.Abs(res[0].Value-149.5) > 1e-9 { // avg of 0..299
		t.Fatalf("coarse avg wrong: %v", res[0].Value)
	}
	// Percentile from the cascaded coarse digest should be close to the exact.
	pres, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "c", EntityID: "n1", Start: base, End: base.Add(5 * time.Minute)},
		Aggregation: Pxx(95),
		Interval:    5 * time.Minute,
	}, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup p95: %v", err)
	}
	exact := percentileSortedRange(0, 299, 0.95)
	if math.Abs(pres[0].Value-exact)/exact > 0.02 {
		t.Fatalf("cascaded p95 %v too far from exact %v", pres[0].Value, exact)
	}
}

func TestCompactDoesNotOverwriteCoarseRollupWithPartialFineRows(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 10 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 11 * time.Minute},
			{Interval: 5 * time.Minute, Retention: time.Hour},
		},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "partial", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 300; i++ {
		batch = append(batch, Point{MetricName: "partial", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: 1})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(15*time.Minute)); err != nil {
		t.Fatalf("compact initial: %v", err)
	}
	query := AggregateQuery{
		Query:       Query{MetricName: "partial", EntityID: "n1", Start: base, End: base.Add(5*time.Minute - time.Nanosecond)},
		Aggregation: AggCount,
		Interval:    5 * time.Minute,
	}
	before, err := s.AggregateRollup(ctx, query, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup before: %v", err)
	}
	if len(before) != 1 || before[0].Count != 300 {
		t.Fatalf("expected initial complete coarse bucket, got %#v", before)
	}
	if _, err := s.Compact(ctx, base.Add(18*time.Minute)); err != nil {
		t.Fatalf("compact after fine retention moves: %v", err)
	}
	after, err := s.AggregateRollup(ctx, query, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup after: %v", err)
	}
	if len(after) != 1 || after[0].Count != 300 {
		t.Fatalf("coarse bucket was overwritten from partial fine rows: %#v", after)
	}
	if err := s.Write(ctx, Point{MetricName: "partial", EntityID: "n1", Timestamp: base.Add(4*time.Minute + 30*time.Second), Value: 1}); err != nil {
		t.Fatalf("write late: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(19*time.Minute)); err != nil {
		t.Fatalf("compact late: %v", err)
	}
	late, err := s.AggregateRollup(ctx, query, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup after late: %v", err)
	}
	if len(late) != 1 || late[0].Count != 301 {
		t.Fatalf("late fine delta should merge into retained coarse bucket: %#v", late)
	}
	if _, err := s.Compact(ctx, base.Add(20*time.Minute)); err != nil {
		t.Fatalf("compact late again: %v", err)
	}
	again, err := s.AggregateRollup(ctx, query, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup after repeated compact: %v", err)
	}
	if len(again) != 1 || again[0].Count != 301 {
		t.Fatalf("repeated compact should not merge the same late delta twice: %#v", again)
	}
}

func TestCompactMergesLateFineDeltaLargerThanCoarseBucket(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 10 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 11 * time.Minute},
			{Interval: 5 * time.Minute, Retention: time.Hour},
		},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "latebig", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "latebig", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 1}); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(15*time.Minute)); err != nil {
		t.Fatalf("compact initial: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(18*time.Minute)); err != nil {
		t.Fatalf("compact expire fine: %v", err)
	}
	for _, ts := range []time.Time{base.Add(20 * time.Second), base.Add(30 * time.Second)} {
		if err := s.Write(ctx, Point{MetricName: "latebig", EntityID: "n1", Timestamp: ts, Value: 1}); err != nil {
			t.Fatalf("write late: %v", err)
		}
	}
	if _, err := s.Compact(ctx, base.Add(19*time.Minute)); err != nil {
		t.Fatalf("compact late: %v", err)
	}
	got, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "latebig", EntityID: "n1", Start: base, End: base.Add(5*time.Minute - time.Nanosecond)},
		Aggregation: AggCount,
		Interval:    5 * time.Minute,
	}, 5*time.Minute)
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if len(got) != 1 || got[0].Count != 3 {
		t.Fatalf("late delta should merge even when larger than existing bucket, got %#v", got)
	}
}

// TestRetentionDropsRawButPercentileSurvives verifies the TSDB property that
// raw data can age out while percentiles remain answerable from rollups.
//
// TestRetentionDropsRawButPercentileSurvives 验证 TSDB 特性：原始数据可以过期，
// 但百分位仍能从 rollup 中回答。
func TestRetentionDropsRawButPercentileSurvives(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 2 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 365 * 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "old", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 60; i++ { // one minute bucket, values 0..59
		batch = append(batch, Point{MetricName: "old", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Compact far in the future: raw (older than 2m) is deleted, rollup kept.
	now := base.Add(48 * time.Hour)
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Raw must be gone.
	raw, err := s.Query(ctx, Query{MetricName: "old", EntityID: "n1", Start: base, End: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected raw points deleted by retention, got %d", len(raw))
	}
	// Percentile must still be answerable from the surviving rollup.
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "old", EntityID: "n1", Start: base, End: base.Add(time.Hour)},
		Aggregation: Pxx(90),
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup p90 after retention: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 surviving rollup bucket, got %d", len(res))
	}
	exact := percentileSortedRange(0, 59, 0.90)
	if math.Abs(res[0].Value-exact)/exact > 0.05 {
		t.Fatalf("surviving p90 %v too far from exact %v", res[0].Value, exact)
	}
}

// TestCompactMergesLateRawIntoExpiredRollup verifies a late-arriving raw point
// for an already-retained rollup bucket is folded into the stored rollup instead
// of replacing the bucket with only the late sample.
//
// TestCompactMergesLateRawIntoExpiredRollup 验证已过原始保留期的 rollup 桶收到
// 迟到 raw 点时，会把迟到样本合入已有 rollup，而不是用迟到样本覆盖整桶。
func TestCompactMergesLateRawIntoExpiredRollup(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 2 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "late", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 60; i++ {
		batch = append(batch, Point{MetricName: "late", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact initial: %v", err)
	}
	if err := s.Write(ctx, Point{
		MetricName: "late",
		EntityID:   "n1",
		Timestamp:  base.Add(30*time.Second + 500*time.Millisecond),
		Value:      1000,
	}); err != nil {
		t.Fatalf("write late: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("compact late: %v", err)
	}
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "late", EntityID: "n1", Start: base, End: base.Add(time.Minute)},
		Aggregation: AggSum,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup sum: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 rollup bucket, got %d", len(res))
	}
	if res[0].Count != 61 || res[0].Value != 2770 {
		t.Fatalf("late point should merge into existing bucket count=61 sum=2770, got %#v", res[0])
	}
}

// TestSeriesRoutesByAge verifies Series auto-routes between raw and rollup data
// based on the query window's age.
//
// TestSeriesRoutesByAge 验证 Series 会根据查询窗口的新旧程度在原始数据和
// rollup 数据之间自动路由。
func TestSeriesRoutesByAge(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: time.Hour,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "sr", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	// Recent window (10 min ago) stays raw; old window (2 days ago) uses rollup.
	recent := now.Add(-10 * time.Minute)
	old := now.Add(-48 * time.Hour)
	var batch []Point
	for i := 0; i < 60; i++ {
		batch = append(batch, Point{MetricName: "sr", EntityID: "n1", Timestamp: recent.Add(time.Duration(i) * time.Second), Value: float64(i)})
		batch = append(batch, Point{MetricName: "sr", EntityID: "n1", Timestamp: old.Add(time.Duration(i) * time.Second), Value: float64(100 + i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}

	// Recent window: Series should match raw Aggregate exactly (full fidelity).
	recentQ := AggregateQuery{
		Query:       Query{MetricName: "sr", EntityID: "n1", Start: recent.Add(-time.Minute), End: recent.Add(2 * time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute,
	}
	got, err := s.Series(ctx, recentQ, now)
	if err != nil {
		t.Fatalf("series recent: %v", err)
	}
	want, err := s.Aggregate(ctx, recentQ)
	if err != nil {
		t.Fatalf("aggregate recent: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("recent: series=%d raw=%d buckets", len(got), len(want))
	}
	for i := range got {
		if !got[i].Bucket.Equal(want[i].Bucket) || got[i].Value != want[i].Value {
			t.Fatalf("recent bucket %d differs: series=%#v raw=%#v", i, got[i], want[i])
		}
	}

	// Old window: raw is gone, so Series must answer from the rollup tier.
	oldRaw, err := s.Query(ctx, Query{MetricName: "sr", EntityID: "n1", Start: old, End: old.Add(time.Hour)})
	if err != nil {
		t.Fatalf("query old raw: %v", err)
	}
	if len(oldRaw) != 0 {
		t.Fatalf("expected old raw aged out, got %d", len(oldRaw))
	}
	oldQ := AggregateQuery{
		Query:       Query{MetricName: "sr", EntityID: "n1", Start: old.Add(-time.Minute), End: old.Add(2 * time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute,
	}
	oldGot, err := s.Series(ctx, oldQ, now)
	if err != nil {
		t.Fatalf("series old: %v", err)
	}
	var found bool
	for _, p := range oldGot {
		if p.Count == 60 && math.Abs(p.Value-129.5) < 1e-9 { // avg of 100..159
			found = true
		}
	}
	if !found {
		t.Fatalf("old window not served from rollup correctly: %#v", oldGot)
	}
}

// TestSeriesAcrossRetentionIncludesUncompactedRecentRaw verifies a query that
// spans the raw-retention boundary uses rollups for old buckets and raw data for
// recent buckets that may not have been compacted yet.
//
// TestSeriesAcrossRetentionIncludesUncompactedRecentRaw 验证跨原始保留期边界的查询
// 会用 rollup 回答旧桶，并包含可能尚未 compact 的近期 raw 桶。
func TestSeriesAcrossRetentionIncludesUncompactedRecentRaw(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: time.Hour,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "hybrid", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * time.Hour)
	var batch []Point
	for i := 0; i < 60; i++ {
		batch = append(batch, Point{MetricName: "hybrid", EntityID: "n1", Timestamp: old.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact old: %v", err)
	}

	recent := now.Add(-10 * time.Minute)
	if err := s.Write(ctx, Point{MetricName: "hybrid", EntityID: "n1", Timestamp: recent, Value: 500}); err != nil {
		t.Fatalf("write recent: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query: Query{
			MetricName: "hybrid",
			EntityID:   "n1",
			Start:      old.Add(-time.Minute),
			End:        recent.Add(time.Minute),
		},
		Aggregation: AggCount,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var oldFound, recentFound bool
	for _, p := range got {
		if p.Bucket.Equal(alignTime(old, time.Minute)) && p.Count == 60 {
			oldFound = true
		}
		if p.Bucket.Equal(alignTime(recent, time.Minute)) && p.Count == 1 {
			recentFound = true
		}
	}
	if !oldFound || !recentFound {
		t.Fatalf("series should include old rollup and recent raw buckets (old=%v recent=%v): %#v", oldFound, recentFound, got)
	}
}

// TestRollupPolicyValidate verifies rollup policy validation rules.
//
// TestRollupPolicyValidate 验证 rollup 策略校验规则。
func TestRollupPolicyValidate(t *testing.T) {
	ok := RollupPolicy{
		RawRetention: 10 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: time.Hour},
			{Interval: 5 * time.Minute, Retention: 24 * time.Hour},
		},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	bad := []RollupPolicy{
		{Tiers: []RollupTier{{Interval: 0, Retention: time.Hour}}},                                                                  // zero interval
		{Tiers: []RollupTier{{Interval: time.Minute, Retention: 0}}},                                                                // zero retention
		{Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}, {Interval: 90 * time.Second, Retention: time.Hour}}},    // not a multiple
		{Tiers: []RollupTier{{Interval: time.Minute, Retention: 2 * time.Hour}, {Interval: 5 * time.Minute, Retention: time.Hour}}}, // coarse retention < fine
		{RawRetention: 30 * time.Second, Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}}},                        // raw retention < 2x finest
	}
	for i, p := range bad {
		if err := p.Validate(); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad policy %d should be invalid, got %v", i, err)
		}
	}
}

// TestCompactIdempotent verifies Compact is idempotent for unchanged windows.
//
// TestCompactIdempotent 验证 Compact 对未变化窗口保持幂等。
func TestCompactIdempotent(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "idem", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for i := 0; i < 120; i++ {
		batch = append(batch, Point{MetricName: "idem", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)})
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	now := base.Add(10 * time.Minute)
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact 1: %v", err)
	}
	r1, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "idem", EntityID: "n1", Start: base, End: base.Add(10 * time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup 1: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact 2: %v", err)
	}
	r2, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "idem", EntityID: "n1", Start: base, End: base.Add(10 * time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup 2: %v", err)
	}
	if len(r1) != len(r2) {
		t.Fatalf("idempotency broken: %d vs %d buckets", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].Value != r2[i].Value || r1[i].Count != r2[i].Count {
			t.Fatalf("idempotency broken at %d: %#v vs %#v", i, r1[i], r2[i])
		}
	}
}
