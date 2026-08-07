package metric

import (
	"context"
	"math"
	"path/filepath"
	"strconv"
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

func TestRawMemoryKeepsTenMinutes(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-expiry", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "raw-expiry", EntityID: "n1", Timestamp: now.Add(-11 * time.Minute), Value: 0},
		{MetricName: "raw-expiry", EntityID: "n1", Timestamp: now.Add(-9 * time.Minute), Value: 1},
		{MetricName: "raw-expiry", EntityID: "n1", Timestamp: now.Add(-30 * time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	points, err := s.Query(ctx, Query{MetricName: "raw-expiry", Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Value != 1 || points[1].Value != 2 {
		t.Fatalf("ten-minute raw window = %#v", points)
	}
}

func TestCompressedRawSamplesRoundTripExactly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	samples := []rawSample{
		{timestamp: now.Add(-9*time.Minute - 7*time.Millisecond).UnixMilli(), value: math.Pi, labelID: 1},
		{timestamp: now.Add(-5*time.Minute - 3*time.Millisecond).UnixMilli(), value: math.Copysign(0, -1), labelID: 0},
		{timestamp: now.Add(-time.Minute - time.Millisecond).UnixMilli(), value: math.SmallestNonzeroFloat64, labelID: 2},
	}
	var compressed compressedRawSamples
	compressed.append(samples)
	decoder := newRawSampleDecoder(compressed)
	for i := range samples {
		decoded, more := decoder.next()
		if !more {
			t.Fatalf("decoded sample count = %d, want %d", i, len(samples))
		}
		if decoded.timestamp != samples[i].timestamp || decoded.labelID != samples[i].labelID || math.Float64bits(decoded.value) != math.Float64bits(samples[i].value) {
			t.Fatalf("decoded sample %d = %#v, want %#v", i, decoded, samples[i])
		}
	}
	if _, more := decoder.next(); more {
		t.Fatal("decoder returned extra sample")
	}
}

func TestCompressedRawSamplesMergeInsertionAndReplacement(t *testing.T) {
	base := time.Now().UTC().Add(-5 * time.Minute).UnixMilli()
	var compressed compressedRawSamples
	compressed.append([]rawSample{
		{timestamp: base, value: 1, labelID: 0},
		{timestamp: base + 2000, value: 3, labelID: 2},
	})
	compressed.append([]rawSample{
		{timestamp: base + 1000, value: math.Copysign(0, -1), labelID: 1},
		{timestamp: base + 2000, value: 33, labelID: 3},
	})
	want := []rawSample{
		{timestamp: base, value: 1, labelID: 0},
		{timestamp: base + 1000, value: math.Copysign(0, -1), labelID: 1},
		{timestamp: base + 2000, value: 33, labelID: 3},
	}
	decoder := newRawSampleDecoder(compressed)
	for i := range want {
		got, more := decoder.next()
		if !more || got.timestamp != want[i].timestamp || got.labelID != want[i].labelID || math.Float64bits(got.value) != math.Float64bits(want[i].value) {
			t.Fatalf("sample %d = %#v, want %#v", i, got, want[i])
		}
	}
	if _, more := decoder.next(); more {
		t.Fatal("decoder returned extra merged sample")
	}
}

func TestRawQueryCompressedDirectBoundaryClosedRange(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-boundary", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	compressedAt := now.Add(-time.Minute)
	directAt := compressedAt.Add(time.Millisecond)
	series := &rawSeries{
		tagsJSON: "{}", labelIDs: map[string]uint32{emptyLabelsHash: 0},
		labelHashes: []string{emptyLabelsHash}, labelsJSON: []string{"{}"},
		samples: []rawSample{{timestamp: directAt.UnixMilli(), value: 2}},
	}
	series.compressed.append([]rawSample{{timestamp: compressedAt.UnixMilli(), value: 1}})
	s.raw[rawSeriesKey{metricName: "raw-boundary", entityID: "n1", tagsHash: emptyLabelsHash}] = series

	ascending, err := s.Query(ctx, Query{MetricName: "raw-boundary", Start: compressedAt, End: directAt, Order: OrderAsc})
	if err != nil {
		t.Fatal(err)
	}
	if len(ascending) != 2 || ascending[0].Value != 1 || ascending[1].Value != 2 {
		t.Fatalf("closed ascending range = %#v", ascending)
	}
	descending, err := s.Query(ctx, Query{MetricName: "raw-boundary", Start: compressedAt, End: directAt, Order: OrderDesc, Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(descending) != 1 || descending[0].Value != 1 {
		t.Fatalf("paged descending range = %#v", descending)
	}
}

func TestRawPruneStreamsAndReclaimsLabels(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-prune", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	cutoff := now.Add(-3 * time.Minute)
	series := &rawSeries{
		tagsJSON:    "{}",
		labelIDs:    map[string]uint32{"old": 0, "middle": 1, "keep": 2},
		labelHashes: []string{"old", "middle", "keep"},
		labelsJSON:  []string{`{"label":"old"}`, `{"label":"middle"}`, `{"label":"keep"}`},
		samples:     []rawSample{{timestamp: now.Add(-30 * time.Second).UnixMilli(), value: 4, labelID: 2}},
	}
	series.compressed.append([]rawSample{
		{timestamp: now.Add(-5 * time.Minute).UnixMilli(), value: 1, labelID: 0},
		{timestamp: now.Add(-4 * time.Minute).UnixMilli(), value: 2, labelID: 1},
		{timestamp: cutoff.UnixMilli(), value: 3, labelID: 2},
	})
	s.raw[rawSeriesKey{metricName: "raw-prune", entityID: "n1", tagsHash: emptyLabelsHash}] = series

	if deleted := s.deleteRawBefore("raw-prune", cutoff.UnixMilli()); deleted != 2 {
		t.Fatalf("deleted %d samples, want 2", deleted)
	}
	if len(series.labelsJSON) != 1 || series.labelsJSON[0] != `{"label":"keep"}` || series.compressed.count != 1 || series.samples[0].labelID != 0 {
		t.Fatalf("compacted series = %#v", series)
	}
	decoder := newRawSampleDecoder(series.compressed)
	kept, more := decoder.next()
	if !more || kept.timestamp != cutoff.UnixMilli() || kept.labelID != 0 || kept.value != 3 {
		t.Fatalf("kept compressed sample = %#v", kept)
	}
	points, err := s.Query(ctx, Query{MetricName: "raw-prune", Start: cutoff, End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || points[0].Labels["label"] != "keep" || points[1].Labels["label"] != "keep" {
		t.Fatalf("pruned query = %#v", points)
	}
}

func TestCompressedRawQueryAndLateReplacementPreserveSamples(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-compressed", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	older := now.Add(-5 * time.Minute)
	recent := now.Add(-30 * time.Second)
	for _, batch := range [][]Point{
		{
			{MetricName: "raw-compressed", EntityID: "n1", Timestamp: older, Value: 1.25, Labels: map[string]string{"source": "old"}},
			{MetricName: "raw-compressed", EntityID: "n1", Timestamp: recent, Value: 2.5, Labels: map[string]string{"source": "recent"}},
			{MetricName: "raw-compressed", EntityID: "n2", Timestamp: older.Add(time.Second), Value: 3.75},
		},
		{{MetricName: "raw-compressed", EntityID: "n1", Timestamp: older, Value: math.Pi, Labels: map[string]string{"source": "replacement"}}},
	} {
		prepared, err := prepareMetricPoints(batch)
		if err != nil {
			t.Fatal(err)
		}
		rebuild := s.writeRawPointsAt(prepared, now)
		if err := s.writePreparedHotRollups(ctx, prepared, now, rebuild); err != nil {
			t.Fatal(err)
		}
	}

	points, err := s.Query(ctx, Query{MetricName: "raw-compressed", EntityID: "n1", Start: now.Add(-10 * time.Minute), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 || math.Float64bits(points[0].Value) != math.Float64bits(math.Pi) || points[0].Labels["source"] != "replacement" || points[1].Value != 2.5 {
		t.Fatalf("compressed and direct query = %#v", points)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "raw-compressed", EntityID: "n1", Start: older.Truncate(time.Minute), End: older.Add(time.Minute)},
		Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Count != 1 || series[0].Value != 1 {
		t.Fatalf("late compressed replacement counted twice: %#v", series)
	}
	latest, err := s.Latest(ctx, "raw-compressed", "n2", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 1 || latest[0].Value != 3.75 {
		t.Fatalf("latest compressed sample = %#v", latest)
	}
	ids, err := s.EntityIDs(ctx, Query{MetricName: "raw-compressed", Start: now.Add(-10 * time.Minute), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "n1" || ids[1] != "n2" {
		t.Fatalf("compressed entity ids = %#v", ids)
	}
	if deleted, err := s.DeleteSeries(ctx, Query{MetricName: "raw-compressed", EntityID: "n2"}); err != nil || deleted == 0 {
		t.Fatalf("delete compressed series = %d, %v", deleted, err)
	}
	deletedPoints, err := s.Query(ctx, Query{MetricName: "raw-compressed", EntityID: "n2", Start: now.Add(-10 * time.Minute), End: now})
	if err != nil || len(deletedPoints) != 0 {
		t.Fatalf("deleted compressed points = %#v, %v", deletedPoints, err)
	}
}

func TestRawReplacementDropsUnreferencedLabels(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-label-replace", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	points := make([]Point, 100)
	for i := range points {
		points[i] = Point{
			MetricName: "raw-label-replace",
			EntityID:   "n1",
			Timestamp:  at,
			Value:      float64(i),
			Labels:     map[string]string{"attempt": strconv.Itoa(i)},
		}
	}
	if err := s.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	tagsHash, _, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatal(err)
	}

	s.rawMu.RLock()
	series := s.raw[rawSeriesKey{metricName: "raw-label-replace", entityID: "n1", tagsHash: tagsHash}]
	if series == nil {
		s.rawMu.RUnlock()
		t.Fatal("raw series was not retained")
	}
	labelCount := len(series.labelsJSON)
	s.rawMu.RUnlock()
	if labelCount != 1 {
		t.Fatalf("retained %d labels for one replaced sample, want 1", labelCount)
	}

	got, err := s.Query(ctx, Query{MetricName: "raw-label-replace", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != 99 || got[0].Labels["attempt"] != "99" {
		t.Fatalf("replacement result = %#v", got)
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
