package metric

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestRollupKeepsTagSeriesSeparate is the regression test for the bug where the
// rollup dropped the tag dimension: two points in the SAME metric/entity/bucket
// but with different tags must become two independent rollup series, and a tag
// filter must return that tag's own value rather than a cross-tag merge.
//
// TestRollupKeepsTagSeriesSeparate 是针对 rollup 曾丢失标签维度这一问题的
// 回归测试：同一个指标、实体和桶内带有不同标签的两个点，必须成为两条独立的
// rollup 序列；标签过滤必须返回该标签自己的值，而不是跨标签合并结果。
func TestRollupKeepsTagSeriesSeparate(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "util", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// Same entity, same 1-minute bucket; two GPUs distinguished only by tag.
	// device 0 carries values 10..19, device 1 carries 80..89.
	var batch []Point
	for i := 0; i < 10; i++ {
		batch = append(batch,
			Point{MetricName: "util", EntityID: "host-1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(10 + i), Tags: map[string]string{"device_index": "0"}},
			Point{MetricName: "util", EntityID: "host-1", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(80 + i), Tags: map[string]string{"device_index": "1"}},
		)
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	written, err := s.Compact(ctx, base.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Two distinct tag series in the one bucket => two rollup rows.
	if written != 2 {
		t.Fatalf("expected 2 rollup series (one per device_index), got %d", written)
	}

	q := func(dev string) AggregateQuery {
		return AggregateQuery{
			Query: Query{
				MetricName: "util", EntityID: "host-1",
				Start: base, End: base.Add(10 * time.Minute),
				Tags: map[string]string{"device_index": dev},
			},
			Aggregation: AggAvg,
			Interval:    time.Minute,
		}
	}
	// device 0: avg of 10..19 = 14.5
	res0, err := s.AggregateRollup(ctx, q("0"), time.Minute)
	if err != nil {
		t.Fatalf("rollup dev0: %v", err)
	}
	if len(res0) != 1 || res0[0].Count != 10 || math.Abs(res0[0].Value-14.5) > 1e-9 {
		t.Fatalf("device 0 should be its own series avg=14.5 count=10, got %#v", res0)
	}
	// device 1: avg of 80..89 = 84.5
	res1, err := s.AggregateRollup(ctx, q("1"), time.Minute)
	if err != nil {
		t.Fatalf("rollup dev1: %v", err)
	}
	if len(res1) != 1 || res1[0].Count != 10 || math.Abs(res1[0].Value-84.5) > 1e-9 {
		t.Fatalf("device 1 should be its own series avg=84.5 count=10, got %#v", res1)
	}

	// No tag filter => both series merge (count 20, avg of all 20 values).
	all, err := s.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "util", EntityID: "host-1", Start: base, End: base.Add(10 * time.Minute)},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup all: %v", err)
	}
	wantAvg := (145.0 + 845.0) / 20.0 // sum(10..19)=145, sum(80..89)=845
	if len(all) != 1 || all[0].Count != 20 || math.Abs(all[0].Value-wantAvg) > 1e-9 {
		t.Fatalf("no-filter should merge both tags count=20 avg=%.2f, got %#v", wantAvg, all)
	}
}

// TestRollupTagPercentileSurvivesRetention proves the headline TSDB property
// still holds per tag: after raw is deleted, a tag-filtered percentile is still
// answerable from that tag's surviving rollup series (and not contaminated by
// the other tag).
//
// TestRollupTagPercentileSurvivesRetention 验证核心 TSDB 特性在每个标签上
// 仍然成立：原始点删除后，按标签过滤的百分位仍能从该标签保留下来的 rollup 序列
// 回答，并且不会被另一个标签污染。
func TestRollupTagPercentileSurvivesRetention(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 2 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 365 * 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "lat", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	// One 1m bucket. region=ap: values 0..99; region=eu: values 1000..1099.
	var batch []Point
	for i := 0; i < 100; i++ {
		ts := base.Add(time.Duration(i) * time.Millisecond * 500)
		batch = append(batch,
			Point{MetricName: "lat", EntityID: "n1", Timestamp: ts, Value: float64(i), Tags: map[string]string{"region": "ap"}},
			Point{MetricName: "lat", EntityID: "n1", Timestamp: ts, Value: float64(1000 + i), Tags: map[string]string{"region": "eu"}},
		)
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(48*time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Raw gone.
	raw, err := s.Query(ctx, Query{MetricName: "lat", EntityID: "n1", Start: base, End: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected raw deleted, got %d", len(raw))
	}
	// p90 of region=ap must be near the ap-only exact (≈89), NOT pulled up toward
	// the eu values (which would happen if tags were merged).
	res, err := s.AggregateRollup(ctx, AggregateQuery{
		Query: Query{
			MetricName: "lat", EntityID: "n1",
			Start: base, End: base.Add(time.Hour),
			Tags: map[string]string{"region": "ap"},
		},
		Aggregation: Pxx(90),
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup ap p90: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 ap bucket, got %d", len(res))
	}
	exactAP := percentileSortedRange(0, 99, 0.90)
	if math.Abs(res[0].Value-exactAP)/exactAP > 0.05 {
		t.Fatalf("ap p90 %v contaminated or wrong (exact ap ≈ %v)", res[0].Value, exactAP)
	}
	if res[0].Value > 500 {
		t.Fatalf("ap p90 %v is contaminated by eu series (>500)", res[0].Value)
	}
}

// TestSeriesTagFilterRoutesPerTag checks the auto-routing read path honors tags
// on both the raw branch (recent) and the rollup branch (old).
//
// TestSeriesTagFilterRoutesPerTag 检查自动路由读取路径在原始分支（近期数据）和
// rollup 分支（旧数据）上都遵守标签。
func TestSeriesTagFilterRoutesPerTag(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: time.Hour,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "m", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	// Old bucket: env=prod values 0..9, env=stage values 500..509.
	var batch []Point
	for i := 0; i < 10; i++ {
		ts := old.Add(time.Duration(i) * time.Second)
		batch = append(batch,
			Point{MetricName: "m", EntityID: "n1", Timestamp: ts, Value: float64(i), Tags: map[string]string{"env": "prod"}},
			Point{MetricName: "m", EntityID: "n1", Timestamp: ts, Value: float64(500 + i), Tags: map[string]string{"env": "stage"}},
		)
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// Old window, raw aged out -> Series must use rollup, filtered to env=prod
	// (avg of 0..9 = 4.5), not the merged 252.
	got, err := s.Series(ctx, AggregateQuery{
		Query: Query{
			MetricName: "m", EntityID: "n1",
			Start: old.Add(-time.Minute), End: old.Add(2 * time.Minute),
			Tags: map[string]string{"env": "prod"},
		},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("series old prod: %v", err)
	}
	var found bool
	for _, p := range got {
		if p.Count == 10 && math.Abs(p.Value-4.5) < 1e-9 {
			found = true
		}
		if p.Count > 10 {
			t.Fatalf("env=prod bucket contaminated by stage: %#v", p)
		}
	}
	if !found {
		t.Fatalf("env=prod old series not served correctly from rollup: %#v", got)
	}
}

// TestDeleteSeriesRemovesTaggedTaskAcrossAgents verifies a task_id tag can be
// deleted across every agent, including stored rollups.
//
// TestDeleteSeriesRemovesTaggedTaskAcrossAgents 验证可以跨所有 agent 删除某个
// task_id 标签，包括已存储的 rollup。
func TestDeleteSeriesRemovesTaggedTaskAcrossAgents(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 2 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "ping.latency_ms", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	var batch []Point
	for _, agent := range []string{"agent-001", "agent-002"} {
		for i := 0; i < 60; i++ {
			ts := base.Add(time.Duration(i) * time.Second)
			batch = append(batch,
				Point{MetricName: "ping.latency_ms", EntityID: agent, Timestamp: ts, Value: float64(20 + i), Tags: map[string]string{"task_id": "pingtask1"}},
				Point{MetricName: "ping.latency_ms", EntityID: agent, Timestamp: ts, Value: float64(60 + i), Tags: map[string]string{"task_id": "pingtask2"}},
			)
		}
	}
	if err := s.WriteBatch(ctx, batch); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if _, err := s.DeleteSeries(ctx, Query{
		MetricName: "ping.latency_ms",
		Tags:       map[string]string{"task_id": "pingtask2"},
	}); err != nil {
		t.Fatalf("delete series: %v", err)
	}

	task2, err := s.AggregateRollup(ctx, AggregateQuery{
		Query: Query{
			MetricName: "ping.latency_ms",
			Start:      base, End: base.Add(time.Minute),
			Tags: map[string]string{"task_id": "pingtask2"},
		},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup task2: %v", err)
	}
	if len(task2) != 0 {
		t.Fatalf("deleted task still has rollup buckets: %#v", task2)
	}
	task1, err := s.AggregateRollup(ctx, AggregateQuery{
		Query: Query{
			MetricName: "ping.latency_ms",
			Start:      base, End: base.Add(time.Minute),
			Tags: map[string]string{"task_id": "pingtask1"},
		},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("rollup task1: %v", err)
	}
	if len(task1) != 1 || task1[0].Count != 120 {
		t.Fatalf("remaining task should keep both agents, got %#v", task1)
	}
}

// TestDeleteMetricRemovesRollups verifies metric deletion clears rollup rows.
//
// TestDeleteMetricRemovesRollups 验证删除指标会清理 rollup 行。
func TestDeleteMetricRemovesRollups(t *testing.T) {
	ctx := context.Background()
	s := newRollupStore(t, RollupPolicy{
		Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	})
	if err := s.CreateMetric(ctx, Definition{Name: "gone", Type: TypeGauge}); err != nil {
		t.Fatalf("create: %v", err)
	}
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	if err := s.Write(ctx, Point{MetricName: "gone", EntityID: "agent-001", Timestamp: base, Value: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.Compact(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := s.DeleteMetric(ctx, "gone"); err != nil {
		t.Fatalf("delete metric: %v", err)
	}
	rows, err := s.scanRollupRows(ctx, s.reader(), "gone", time.Minute)

	if err != nil {
		t.Fatalf("scan rollups: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("deleted metric still has rollups: %#v", rows)
	}
}

// TestTagsFingerprintStable verifies equal tag maps fingerprint identically
// regardless of construction order, and different maps differ.
//
// TestTagsFingerprintStable 验证相同标签 map 无论构造顺序如何都会生成相同指纹，
// 而不同 map 会得到不同指纹。
func TestTagsFingerprintStable(t *testing.T) {
	a := map[string]string{"region": "ap", "device_index": "3"}
	b := map[string]string{"device_index": "3", "region": "ap"}
	ha, _, err := tagsFingerprint(a)
	if err != nil {
		t.Fatalf("fp a: %v", err)
	}
	hb, _, err := tagsFingerprint(b)
	if err != nil {
		t.Fatalf("fp b: %v", err)
	}
	if ha != hb {
		t.Fatalf("equal tag maps must fingerprint identically: %s vs %s", ha, hb)
	}
	hc, _, _ := tagsFingerprint(map[string]string{"region": "eu", "device_index": "3"})
	if ha == hc {
		t.Fatalf("different tag maps must fingerprint differently")
	}
	// nil and empty map agree.
	hn, _, _ := tagsFingerprint(nil)
	he, _, _ := tagsFingerprint(map[string]string{})
	if hn != he {
		t.Fatalf("nil and empty tag maps must agree: %s vs %s", hn, he)
	}
}
