package metric

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestSeriesHybridKeepsUncompactedRawNearCutoff verifies the hybrid read does
// not lose raw points that fall between the (finest-aligned) raw cutoff and the
// next bucket boundary, including a point written AFTER the last compaction.
//
// Reproduction: now=12:00:30, RawRetention=30m so the cutoff is 11:30:30, which
// the policy floors to 11:30:00 (finest = 1m). A point at 11:30:15 written
// before compaction is kept (>= aligned cutoff) and NOT rolled up; a point at
// 11:30:45 written after compaction is also raw. Both belong to the 11:00 1h
// output bucket. The old code aligned the split UP to 11:31:00 and read raw from
// there, dropping both boundary points (count=1, the stale rollup half only).
// Serving raw from the aligned cutoff fixes it (count=2).
//
// TestSeriesHybridKeepsUncompactedRawNearCutoff 验证混合读取不会丢失落在
// （最细对齐后的）原始 cutoff 与下一个桶边界之间的 raw 点，包括上次 compact 之后
// 才写入的点。
func TestSeriesHybridKeepsUncompactedRawNearCutoff(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "nearcut", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 30, 0, time.UTC)
	// 11:30:15 is >= the finest-aligned cutoff (11:30:00) -> survives compaction
	// as raw and is not rolled up.
	before := time.Date(2026, 6, 18, 11, 30, 15, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "nearcut", EntityID: "n1", Timestamp: before, Value: 10}); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Written after compaction: only ever lives in raw.
	after := time.Date(2026, 6, 18, 11, 30, 45, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "nearcut", EntityID: "n1", Timestamp: after, Value: 100}); err != nil {
		t.Fatalf("write after: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "nearcut", EntityID: "n1", Start: time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), End: now},
		Aggregation: AggAvg,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	bucket := alignTime(time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), time.Hour)
	var found bool
	for _, p := range got {
		if p.Bucket.Equal(bucket) {
			found = true
			if p.Count != 2 {
				t.Fatalf("near-cutoff bucket count: got %d want 2 (%#v)", p.Count, p)
			}
			if math.Abs(p.Value-55) > 1e-9 {
				t.Fatalf("near-cutoff bucket avg: got %v want 55 (%#v)", p.Value, p)
			}
		}
	}
	if !found {
		t.Fatalf("expected 11:00 bucket with both raw points: %#v", got)
	}
}

// TestSeriesHybridEndEqualsCutoff verifies a query whose End lands exactly on the
// raw cutoff still includes the raw point sitting at the cutoff, instead of
// returning empty.
//
// Reproduction: now=12:00:00, RawRetention=30m -> cutoff 11:30:00. A point at
// exactly 11:30:00 is kept by compaction (not < cutoff) and lives in raw. An old
// point at 11:00:10 is rolled up and its raw deleted. Querying [11:00, 11:30:00]
// (End == cutoff) must combine the rolled-up old point and the raw cutoff point.
// The old entry gate required End strictly AFTER the cutoff, so this fell through
// to the pure-rollup path and returned the cutoff point's bucket empty.
//
// TestSeriesHybridEndEqualsCutoff 验证 End 恰好等于原始 cutoff 的查询仍会包含
// 落在 cutoff 上的 raw 点，而不是返回空。
func TestSeriesHybridEndEqualsCutoff(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 30 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "endcut", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 6, 18, 11, 30, 0, 0, time.UTC)
	old := time.Date(2026, 6, 18, 11, 0, 10, 0, time.UTC) // rolled up, raw deleted
	if err := s.Write(ctx, Point{MetricName: "endcut", EntityID: "n1", Timestamp: old, Value: 10}); err != nil {
		t.Fatalf("write old: %v", err)
	}
	// Point exactly at the cutoff: kept as raw (not < cutoff) after compaction.
	if err := s.Write(ctx, Point{MetricName: "endcut", EntityID: "n1", Timestamp: cutoff, Value: 100}); err != nil {
		t.Fatalf("write cutoff: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "endcut", EntityID: "n1", Start: time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), End: cutoff},
		Aggregation: AggSum,
		Interval:    time.Hour,
	}, now)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	bucket := alignTime(time.Date(2026, 6, 18, 11, 0, 0, 0, time.UTC), time.Hour)
	var found bool
	for _, p := range got {
		if p.Bucket.Equal(bucket) {
			found = true
			if p.Count != 2 || math.Abs(p.Value-110) > 1e-9 {
				t.Fatalf("End==cutoff bucket: got count=%d value=%v want count=2 value=110", p.Count, p.Value)
			}
		}
	}
	if !found {
		t.Fatalf("expected 11:00 bucket including the cutoff raw point: %#v", got)
	}
}
