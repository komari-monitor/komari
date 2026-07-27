package metric

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRawMemoryUpsertOrderingPagingAndLabels(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "raw", EntityID: "n2", Timestamp: base, Value: 2, Tags: map[string]string{"zone": "a"}, Labels: map[string]string{"source": "old"}},
		{MetricName: "raw", EntityID: "n1", Timestamp: base, Value: 1, Tags: map[string]string{"zone": "a"}},
		{MetricName: "raw", EntityID: "n1", Timestamp: base.Add(time.Second), Value: 3, Tags: map[string]string{"zone": "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, Point{MetricName: "raw", EntityID: "n2", Timestamp: base, Value: 22, Tags: map[string]string{"zone": "a"}, Labels: map[string]string{"source": "new"}}); err != nil {
		t.Fatal(err)
	}

	points, err := s.Query(ctx, Query{MetricName: "raw", Start: base.Add(-time.Second), End: base.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 || points[0].EntityID != "n1" || points[1].EntityID != "n2" || points[1].Value != 22 || points[1].Labels["source"] != "new" {
		t.Fatalf("ordered upserted points = %#v", points)
	}
	paged, err := s.Query(ctx, Query{MetricName: "raw", Start: base.Add(-time.Second), End: base.Add(time.Minute), Order: OrderDesc, Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 1 || paged[0].EntityID != "n2" || paged[0].Value != 22 {
		t.Fatalf("paged descending points = %#v", paged)
	}

	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "raw", EntityID: "n2", Start: base.Add(-time.Second), End: base.Add(time.Minute)},
		Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
	}, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Count != 1 || series[0].Value != 1 {
		t.Fatalf("rollup counted overwritten sample more than once: %#v", series)
	}
}

func TestConcurrentRawUpsertKeepsOneRollupObservation(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-race", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-10 * time.Second)
	var writers sync.WaitGroup
	for i := 0; i < 16; i++ {
		writers.Add(1)
		go func(value float64) {
			defer writers.Done()
			if err := s.Write(ctx, Point{MetricName: "raw-race", EntityID: "n1", Timestamp: at, Value: value}); err != nil {
				t.Errorf("write: %v", err)
			}
		}(float64(i))
	}
	writers.Wait()
	raw, err := s.Query(ctx, Query{MetricName: "raw-race", EntityID: "n1", Start: at.Add(-time.Second), End: at.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("raw upsert count = %d, want 1", len(raw))
	}
	rollup, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "raw-race", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(rollup) != 1 || rollup[0].Count != 1 || rollup[0].Value != 1 {
		t.Fatalf("rollup upsert count = %#v", rollup)
	}
}

func TestRawMemoryExpiresAfterOneMinute(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-expiry", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "raw-expiry", EntityID: "n1", Timestamp: now.Add(-time.Minute - time.Millisecond), Value: 1},
		{MetricName: "raw-expiry", EntityID: "n1", Timestamp: now.Add(-30 * time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	points, err := s.Query(ctx, Query{MetricName: "raw-expiry", Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].Value != 2 {
		t.Fatalf("exact raw window = %#v", points)
	}
}

func TestRestartDropsRawButKeepsRollup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	policy := RollupPolicy{RawRetention: time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: time.Hour}}, Compression: 30}
	s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "restart", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := s.Write(ctx, Point{MetricName: "restart", EntityID: "n1", Timestamp: at, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, err := s.Query(ctx, Query{MetricName: "restart", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("raw survived restart: %#v", raw)
	}
	rollup, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "restart", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(rollup) != 1 || rollup[0].Value != 7 || rollup[0].Count != 1 {
		t.Fatalf("persisted rollup after restart = %#v", rollup)
	}
}
