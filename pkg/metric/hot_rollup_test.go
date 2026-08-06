package metric

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWriteBatchKeepsRawInMemoryAndBuildsMinuteRollup(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "hot", RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "hot", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 10},
		{MetricName: "hot", EntityID: "n1", Timestamp: base.Add(20 * time.Second), Value: 20},
	}); err != nil {
		t.Fatalf("write samples: %v", err)
	}
	var rawTableCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, s.tables.points).Scan(&rawTableCount); err != nil {
		t.Fatalf("inspect points table: %v", err)
	}
	if rawTableCount != 0 {
		t.Fatalf("metric_points table count = %d, want 0", rawTableCount)
	}
	points, err := s.Query(ctx, Query{MetricName: "hot", EntityID: "n1", Start: base, End: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query minute rollup: %v", err)
	}
	if len(points) != 2 || points[0].Value != 10 || points[1].Value != 20 {
		t.Fatalf("points = %#v, want both raw samples", points)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "hot", EntityID: "n1", Start: base, End: base.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("query minute rollup: %v", err)
	}
	if len(series) != 1 || series[0].Count != 2 || series[0].Value != 15 {
		t.Fatalf("minute rollup = %#v, want avg=15 count=2", series)
	}
}

func TestClosedMinutePersistsAndAcceptsExactUpsert(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "late-upsert", RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	now := time.Date(2026, 7, 27, 12, 0, 10, 0, time.UTC)
	at := now.Add(-20 * time.Second)
	for _, value := range []float64{1, 2} {
		prepared, err := prepareMetricPoints([]Point{{MetricName: "late-upsert", EntityID: "n1", Timestamp: at, Value: value}})
		if err != nil {
			t.Fatalf("prepare point: %v", err)
		}
		rebuild := s.writeRawPointsAt(prepared, now)
		if err := s.writePreparedHotRollups(ctx, prepared, now, rebuild); err != nil {
			t.Fatalf("write hot rollup: %v", err)
		}
	}

	var persisted int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tables.rollups).Scan(&persisted); err != nil {
		t.Fatalf("count early persisted rollups: %v", err)
	}
	if persisted != 1 {
		t.Fatalf("persisted closed-minute rollups = %d, want 1", persisted)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "late-upsert", EntityID: "n1", Start: at.Add(-time.Minute), End: now},
		Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
	}, now)
	if err != nil {
		t.Fatalf("query hot rollup: %v", err)
	}
	if len(series) != 1 || series[0].Count != 1 || series[0].Value != 1 {
		t.Fatalf("late upsert was counted twice: %#v", series)
	}

	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count FROM "+s.tables.rollups).Scan(&count); err != nil {
		t.Fatalf("read persisted rollup: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted rollup count = %d, want 1", count)
	}
}

func TestRawWindowKeepsCompressedAndDirectSamples(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "raw-window", RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "raw-window", EntityID: "n1", Timestamp: now.Add(-70 * time.Second), Value: 1},
		{MetricName: "raw-window", EntityID: "n1", Timestamp: now.Add(-50 * time.Second), Value: 2},
		{MetricName: "raw-window", EntityID: "n1", Timestamp: now.Add(-40 * time.Second), Value: 3},
	}); err != nil {
		t.Fatalf("write samples: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	raw, err := s.Query(ctx, Query{MetricName: "raw-window", EntityID: "n1", Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if len(raw) != 3 || raw[0].Value != 1 || raw[1].Value != 2 || raw[2].Value != 3 {
		t.Fatalf("retained raw = %#v, want all ten-minute samples", raw)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "raw-window", EntityID: "n1", Start: now.Add(-time.Hour), End: now},
		Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
	}, now)
	if err != nil {
		t.Fatalf("query rollup: %v", err)
	}
	var count int
	for _, point := range series {
		count += point.Count
	}
	if count != 3 {
		t.Fatalf("rollups lost samples after raw cleanup: %#v", series)
	}
}

func TestOldestRawMinuteRebuildKeepsHiddenPrefix(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: rawMemoryRetention,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression:  30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "oldest-minute", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 12, 10, 30, 0, time.UTC)
	oldestMinute := now.Add(-rawMemoryRetention).Truncate(time.Minute)
	write := func(points ...Point) {
		t.Helper()
		prepared, err := prepareMetricPoints(points)
		if err != nil {
			t.Fatal(err)
		}
		rebuild := s.writeRawPointsAt(prepared, now)
		if err := s.writePreparedHotRollups(ctx, prepared, now, rebuild); err != nil {
			t.Fatal(err)
		}
	}
	write(
		Point{MetricName: "oldest-minute", EntityID: "n1", Timestamp: oldestMinute.Add(5 * time.Second), Value: 1},
		Point{MetricName: "oldest-minute", EntityID: "n1", Timestamp: oldestMinute.Add(40 * time.Second), Value: 2},
	)
	write(Point{MetricName: "oldest-minute", EntityID: "n1", Timestamp: oldestMinute.Add(40 * time.Second), Value: 4})

	rows, err := s.scanRollupRowsBetween(ctx, "oldest-minute", "n1", nil, time.Minute.Milliseconds(), oldestMinute.UnixMilli(), oldestMinute.UnixMilli(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].bucketData.count != 2 || rows[0].bucketData.sum != 5 {
		t.Fatalf("rebuilt oldest minute = %#v, want count=2 sum=5", rows)
	}
}

func TestLateLabelReplacementRebuildsPersistedCascade(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: rawMemoryRetention,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 24 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
			{Interval: time.Hour, Retention: 30 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "cascade-replace", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 12, 2, 10, 0, time.UTC)
	at := now.Add(-90 * time.Second)
	write := func(point Point) {
		t.Helper()
		prepared, err := prepareMetricPoints([]Point{point})
		if err != nil {
			t.Fatal(err)
		}
		rebuild := s.writeRawPointsAt(prepared, now)
		if err := s.writePreparedHotRollups(ctx, prepared, now, rebuild); err != nil {
			t.Fatal(err)
		}
	}
	write(Point{MetricName: "cascade-replace", EntityID: "n1", Timestamp: at, Value: 1, Labels: map[string]string{"source": "old"}})
	write(Point{MetricName: "cascade-replace", EntityID: "n1", Timestamp: at, Value: 7, Labels: map[string]string{"source": "new"}})

	if _, err := s.FlushCoarse(ctx, at.Truncate(24*time.Hour).Add(24*time.Hour+coarseRollupGrace)); err != nil {
		t.Fatalf("seal replacement parents: %v", err)
	}
	for _, tier := range policy.Tiers[:3] {
		start := bucketStartMillis(at.UnixMilli(), tier.Interval.Milliseconds())
		rows, err := s.scanRollupRowsBetween(ctx, "cascade-replace", "n1", nil, tier.Interval.Milliseconds(), start, start, true)
		if err != nil {
			t.Fatalf("scan %s tier: %v", tier.Interval, err)
		}
		if len(rows) != 1 || rows[0].bucketData.count != 1 || rows[0].bucketData.sum != 7 {
			t.Fatalf("%s replacement rows = %#v, want one count=1 sum=7 row", tier.Interval, rows)
		}
		labels, err := decodeMapString(rows[0].bucketData.labelsJSON)
		if err != nil || labels["source"] != "new" {
			t.Fatalf("%s replacement labels = %#v, %v", tier.Interval, labels, err)
		}
	}
}

func TestOneDayRetentionStagesOnlyMinuteAndFiveMinute(t *testing.T) {
	ctx := context.Background()
	policy := DefaultConfig(DriverSQLite, ":memory:").RollupPolicy
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "one-day", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	prepared, err := prepareMetricPoints([]Point{{MetricName: "one-day", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 7}})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := s.writeRawPointsAt(prepared, base.Add(2*time.Minute))
	if err := s.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
		t.Fatal(err)
	}

	for _, tier := range policy.Tiers {
		rows, err := s.scanRollupRows(ctx, s.reader(), "one-day", tier.Interval)
		if err != nil {
			t.Fatalf("scan %s before parent seal: %v", tier.Interval, err)
		}
		want := 0
		if tier.Interval == time.Minute {
			want = 1
		}
		if len(rows) != want {
			t.Fatalf("%s rows before parent seal = %d, want %d", tier.Interval, len(rows), want)
		}
	}
	if written, err := s.FlushCoarse(ctx, base.Add(15*time.Minute)); err != nil || written != 1 {
		t.Fatalf("seal five-minute parent = %d, %v; want 1, nil", written, err)
	}
	for _, tier := range policy.Tiers {
		rows, err := s.scanRollupRows(ctx, s.reader(), "one-day", tier.Interval)
		if err != nil {
			t.Fatalf("scan %s after parent seal: %v", tier.Interval, err)
		}
		want := 0
		switch tier.Interval {
		case time.Minute, 5 * time.Minute:
			want = 1
		}
		if len(rows) != want {
			t.Fatalf("%s rows after parent seal = %d, want %d", tier.Interval, len(rows), want)
		}
	}
}

func TestLongRetentionCoarseRollupsSealOnce(t *testing.T) {
	ctx := context.Background()
	policy := DefaultConfig(DriverSQLite, ":memory:").RollupPolicy
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "long-lived", RetentionDays: 120}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	prepared, err := prepareMetricPoints([]Point{{MetricName: "long-lived", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 7}})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := s.writeRawPointsAt(prepared, base.Add(2*time.Minute))
	if err := s.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
		t.Fatal(err)
	}
	if written, err := s.FlushCoarse(ctx, base.Add(15*time.Minute)); err != nil || written != 1 {
		t.Fatalf("seal five-minute parent = %d, %v; want 1, nil", written, err)
	}
	if written, err := s.FlushCoarse(ctx, base.Truncate(24*time.Hour).Add(24*time.Hour+coarseRollupGrace)); err != nil || written != 2 {
		t.Fatalf("seal hourly and daily parents = %d, %v; want 2, nil", written, err)
	}
	if written, err := s.FlushCoarse(ctx, base.Add(48*time.Hour)); err != nil || written != 0 {
		t.Fatalf("repeat coarse seal = %d, %v; want 0, nil", written, err)
	}
	for _, tier := range policy.Tiers {
		rows, err := s.scanRollupRows(ctx, s.reader(), "long-lived", tier.Interval)
		if err != nil {
			t.Fatalf("scan %s: %v", tier.Interval, err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s rows = %d, want one sealed row", tier.Interval, len(rows))
		}
	}
}

func TestLateReplacementChangesSealedPercentile(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	s := newRollupStore(t, policy)
	if err := s.CreateMetric(ctx, Definition{Name: "late-percentile", RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	write := func(points ...Point) {
		t.Helper()
		prepared, err := prepareMetricPoints(points)
		if err != nil {
			t.Fatal(err)
		}
		rebuild := s.writeRawPointsAt(prepared, base.Add(2*time.Minute))
		if err := s.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
			t.Fatal(err)
		}
	}
	write(
		Point{MetricName: "late-percentile", EntityID: "n1", Timestamp: base.Add(5 * time.Second), Value: 1},
		Point{MetricName: "late-percentile", EntityID: "n1", Timestamp: base.Add(20 * time.Second), Value: 20},
		Point{MetricName: "late-percentile", EntityID: "n1", Timestamp: base.Add(30 * time.Second), Value: 50},
	)
	write(Point{MetricName: "late-percentile", EntityID: "n1", Timestamp: base.Add(30 * time.Second), Value: 100})
	if _, err := s.FlushCoarse(ctx, base.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "late-percentile", EntityID: "n1", Start: base, End: base.Add(5*time.Minute - time.Millisecond)},
		Aggregation: AggP95,
		Interval:    5 * time.Minute,
	}, base.Add(16*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Count != 3 || series[0].Value < 90 {
		t.Fatalf("late percentile replacement = %#v, want count=3 p95 near 100", series)
	}
}

func TestRestartDropsUnsealedCoarseParents(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{Tiers: []RollupTier{
		{Interval: time.Minute, Retention: 24 * time.Hour},
		{Interval: 5 * time.Minute, Retention: 7 * 24 * time.Hour},
	}, Compression: 30}
	path := filepath.Join(t.TempDir(), "restart.db")
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	s, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMetric(ctx, Definition{Name: "restart-parent", RetentionDays: 30}); err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	prepared, err := prepareMetricPoints([]Point{{MetricName: "restart-parent", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 7}})
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	rebuild := s.writeRawPointsAt(prepared, base.Add(2*time.Minute))
	if err := s.writePreparedHotRollups(ctx, prepared, base.Add(2*time.Minute), rebuild); err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, SQLite(path, WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if written, err := reopened.FlushCoarse(ctx, base.Add(15*time.Minute)); err != nil || written != 0 {
		t.Fatalf("reopened coarse flush = %d, %v; want 0, nil", written, err)
	}
	rows, err := reopened.scanRollupRows(ctx, reopened.reader(), "restart-parent", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("unsealed parent survived restart: %#v", rows)
	}
}

func TestFlushTrimsInactiveRawWindow(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "idle-raw", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prepared, err := prepareMetricPoints([]Point{{MetricName: "idle-raw", EntityID: "n1", Timestamp: now.Add(-time.Minute), Value: 1}})
	if err != nil {
		t.Fatal(err)
	}
	rebuild := s.writeRawPointsAt(prepared, now)
	if err := s.writePreparedHotRollups(ctx, prepared, now, rebuild); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(ctx, now.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.rawMu.RLock()
	defer s.rawMu.RUnlock()
	if len(s.raw) != 0 {
		t.Fatalf("inactive raw series retained after scheduled flush: %#v", s.raw)
	}
}

func TestConcurrentHotPercentileQueriesUseDigestSnapshots(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "hot-percentile", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	if err := s.Write(ctx, Point{MetricName: "hot-percentile", EntityID: "n1", Timestamp: base, Value: 1}); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 1; i <= 200; i++ {
			if err := s.Write(ctx, Point{MetricName: "hot-percentile", EntityID: "n1", Timestamp: base.Add(time.Duration(i) * time.Millisecond), Value: float64(i)}); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 200; i++ {
			points, err := s.Series(ctx, AggregateQuery{
				Query:       Query{MetricName: "hot-percentile", EntityID: "n1", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
				Aggregation: AggP95, Interval: time.Minute, PreserveSeries: true,
			}, base)
			if err != nil {
				errCh <- err
				return
			}
			if len(points) == 0 {
				errCh <- fmt.Errorf("percentile query returned no points")
				return
			}
		}
	}()
	workers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestConcurrentPersistedReplacementQueriesStayConsistent(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: rawMemoryRetention,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression:  30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "replacement-view", RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	at := now.Add(-90 * time.Second)
	if err := s.Write(ctx, Point{MetricName: "replacement-view", EntityID: "n1", Timestamp: at, Value: 1}); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := 2; i <= 100; i++ {
			if err := s.Write(ctx, Point{MetricName: "replacement-view", EntityID: "n1", Timestamp: at, Value: float64(i)}); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		for i := 0; i < 300; i++ {
			points, err := s.Series(ctx, AggregateQuery{
				Query:       Query{MetricName: "replacement-view", EntityID: "n1", Start: at.Add(-time.Minute), End: now},
				Aggregation: AggCount, Interval: time.Minute, PreserveSeries: true,
			}, now)
			if err != nil {
				errCh <- err
				return
			}
			if len(points) != 1 || points[0].Count != 1 || points[0].Value != 1 {
				errCh <- fmt.Errorf("replacement query observed a split view: %#v", points)
				return
			}
		}
	}()
	workers.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

func TestRawQueryOffsetWithoutLimit(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "raw-page", RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "raw-page", EntityID: "n1", Timestamp: base, Value: 1},
		{MetricName: "raw-page", EntityID: "n1", Timestamp: base.Add(time.Second), Value: 2},
		{MetricName: "raw-page", EntityID: "n1", Timestamp: base.Add(2 * time.Second), Value: 3},
	}); err != nil {
		t.Fatalf("write samples: %v", err)
	}
	points, err := s.Query(ctx, Query{MetricName: "raw-page", Start: base, End: base.Add(2 * time.Second), Offset: 1})
	if err != nil {
		t.Fatalf("query offset: %v", err)
	}
	if len(points) != 2 || points[0].Value != 2 || points[1].Value != 3 {
		t.Fatalf("offset raw points = %#v", points)
	}
}

func TestSeriesMergesLabelsWithoutCollapsingRawShape(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(RollupPolicy{
		Tiers:       []RollupTier{{Interval: time.Minute, Retention: time.Hour}},
		Compression: 30,
	})))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "labels", RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	base := time.Now().UTC().Add(-30 * time.Second)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "labels", EntityID: "n1", Timestamp: base.Add(10 * time.Second), Value: 10, Tags: map[string]string{"disk": "sda"}, Labels: map[string]string{"source": "a"}},
		{MetricName: "labels", EntityID: "n1", Timestamp: base.Add(20 * time.Second), Value: 20, Tags: map[string]string{"disk": "sda"}, Labels: map[string]string{"source": "b"}},
	}); err != nil {
		t.Fatalf("write labeled samples: %v", err)
	}

	series, err := s.Series(ctx, AggregateQuery{
		Query:          Query{MetricName: "labels", EntityID: "n1", Start: base, End: base.Add(time.Minute)},
		Aggregation:    AggAvg,
		Interval:       time.Minute,
		PreserveSeries: true,
	}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("aggregate labels: %v", err)
	}
	var count int
	var sum float64
	for _, point := range series {
		count += point.Count
		sum += point.Value * float64(point.Count)
	}
	if count != 2 || sum/float64(count) != 15 {
		t.Fatalf("label sets split the public series: %#v", series)
	}

	points, err := s.Query(ctx, Query{MetricName: "labels", EntityID: "n1", Start: base, End: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query labeled raw samples: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("query lost independently indexed label sets: %#v", points)
	}
}

func TestSeriesResolutionDoesNotInspectStoredCoverage(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 25 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "coverage-tier", RetentionDays: 60}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatalf("fingerprint tags: %v", err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	for _, seed := range []struct {
		interval time.Duration
		at       time.Time
	}{
		{interval: time.Minute, at: now.Add(-30 * time.Minute)},
		{interval: 5 * time.Minute, at: now.Add(-7 * time.Hour)},
		{interval: 5 * time.Minute, at: now.Add(-5 * time.Hour)},
		{interval: time.Hour, at: now.Add(-30 * 24 * time.Hour)},
	} {
		bucket := newRollupBucket(policy.compression())
		bucket.tagsHash, bucket.tagsJSON = tagsHash, tagsJSON
		bucket.labelsHash, bucket.labelsJSON = emptyLabelsHash, "{}"
		bucket.addPoint(1, seed.at.UnixMilli())
		key := rollupKey{entityID: "n1", tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: bucketStartMillis(seed.at.UnixMilli(), seed.interval.Milliseconds())}
		if _, err := s.writeRollupBucketsTx(ctx, "coverage-tier", seed.interval, map[rollupKey]*rollupBucket{key: bucket}, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed %s tier: %v", seed.interval, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tiers: %v", err)
	}

	resolution := seriesResolutionForPolicy(now.Add(-6*time.Hour), time.Minute, now, policy)
	if resolution != time.Minute {
		t.Fatalf("selected tier = %s, want 1m", resolution)
	}
}

func TestSeriesReturnsEmptyWhenSelectedTierHasNoRows(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: time.Hour, Retention: 25 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "missing-fine-tier", RetentionDays: 60}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	now := time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC)
	at := now.Add(-2 * time.Hour)
	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatalf("fingerprint tags: %v", err)
	}
	bucket := newRollupBucket(policy.compression())
	bucket.tagsHash, bucket.tagsJSON = tagsHash, tagsJSON
	bucket.labelsHash, bucket.labelsJSON = emptyLabelsHash, "{}"
	bucket.addPoint(42, at.UnixMilli())
	key := rollupKey{entityID: "n1", tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: bucketStartMillis(at.UnixMilli(), time.Hour.Milliseconds())}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	if _, err := s.writeRollupBucketsTx(ctx, "missing-fine-tier", time.Hour, map[rollupKey]*rollupBucket{key: bucket}, tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("seed hourly tier: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit hourly tier: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "missing-fine-tier", EntityID: "n1", Start: now.Add(-3 * time.Hour), End: now},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("query series: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("series read a non-selected hourly tier: %#v", got)
	}
}

func TestSeriesDoesNotFallBackWhenPreferredTierCoversWindow(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "new-series", RetentionDays: 60}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatalf("fingerprint tags: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	for _, seed := range []struct {
		interval time.Duration
		at       time.Time
		value    float64
	}{
		{interval: 24 * time.Hour, at: now.Add(-30 * 24 * time.Hour), value: 10},
		{interval: time.Minute, at: now.Add(-5 * time.Minute), value: 20},
	} {
		bucket := newRollupBucket(policy.compression())
		bucket.tagsHash, bucket.tagsJSON = tagsHash, tagsJSON
		bucket.labelsHash, bucket.labelsJSON = emptyLabelsHash, "{}"
		bucket.addPoint(seed.value, seed.at.UnixMilli())
		key := rollupKey{entityID: "n1", tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: bucketStartMillis(seed.at.UnixMilli(), seed.interval.Milliseconds())}
		if _, err := s.writeRollupBucketsTx(ctx, "new-series", seed.interval, map[rollupKey]*rollupBucket{key: bucket}, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed %s tier: %v", seed.interval, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed tiers: %v", err)
	}

	got, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "new-series", EntityID: "n1", Start: now.Add(-time.Hour), End: now},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		t.Fatalf("query recent series: %v", err)
	}
	if len(got) != 1 || got[0].Bucket.Before(now.Add(-time.Hour)) || got[0].Value != 20 {
		t.Fatalf("recent query fell back to an older coarse tier: %#v", got)
	}
}

func TestDeleteBeforeKeepsBucketsThatStraddleCutoff(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 25 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "retention-boundary", RetentionDays: 60}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(-48 * time.Hour)
	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatal(err)
	}
	daily := newRollupBucket(policy.compression())
	daily.tagsHash, daily.tagsJSON = tagsHash, tagsJSON
	daily.labelsHash, daily.labelsJSON = emptyLabelsHash, "{}"
	daily.addPoint(10, day.Add(12*time.Hour).UnixMilli())
	daily.addPoint(20, day.Add(20*time.Hour).UnixMilli())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := rollupKey{entityID: "n1", tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: day.UnixMilli()}
	if _, err := s.writeRollupBucketsTx(ctx, "retention-boundary", 24*time.Hour, map[rollupKey]*rollupBucket{key: daily}, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBefore(ctx, "retention-boundary", day.Add(18*time.Hour)); err != nil {
		t.Fatalf("delete before cutoff: %v", err)
	}
	var dailyCount int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT r.count FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "retention-boundary", (24 * time.Hour).Milliseconds()).Scan(&dailyCount); err != nil {
		t.Fatalf("read straddling daily bucket: %v", err)
	}
	if dailyCount != 2 {
		t.Fatalf("straddling daily bucket count = %d, want 2", dailyCount)
	}

	futureMinute := time.Now().UTC().Truncate(time.Minute).Add(2 * time.Minute)
	if err := s.Write(ctx, Point{MetricName: "retention-boundary", EntityID: "n1", Timestamp: futureMinute.Add(40 * time.Second), Value: 30}); err != nil {
		t.Fatalf("write active boundary point: %v", err)
	}
	if _, err := s.DeleteBefore(ctx, "retention-boundary", futureMinute.Add(30*time.Second)); err != nil {
		t.Fatalf("delete before active-minute cutoff: %v", err)
	}
	if len(s.hot) != 1 {
		t.Fatalf("active minute was deleted across a partial cutoff: %#v", s.hot)
	}
}

func TestCompatibleIntervalsCoverDashboardRanges(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	policy := RollupPolicy{
		RawRetention: time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 25 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
	}
	s := &Store{cfg: Config{RollupPolicy: policy}}
	for _, test := range []struct {
		rangeDuration time.Duration
		interval      time.Duration
	}{
		{time.Hour, time.Minute},
		{6 * time.Hour, time.Minute},
		{7 * 24 * time.Hour, 15 * time.Minute},
		{30 * 24 * time.Hour, time.Hour},
		{60 * 24 * time.Hour, time.Hour},
	} {
		got := s.CompatibleSeriesInterval(now.Add(-test.rangeDuration), now, test.interval)
		if got <= 0 {
			t.Fatalf("range %s returned invalid interval %s", test.rangeDuration, got)
		}
		if got < time.Minute {
			t.Fatalf("range %s returned %s below persisted minute precision", test.rangeDuration, got)
		}
	}
}

func TestSeriesHasNoTrailingEmptyBucketAcrossDashboardRanges(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 25 * 24 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(":memory:", WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateMetric(ctx, Definition{Name: "coverage", RetentionDays: 90}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatalf("fingerprint tags: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin history transaction: %v", err)
	}
	metricPolicy := policy.withMetricRetention(90 * 24 * time.Hour)
	for _, tierIndex := range []int{0, 2, 3} {
		tier := metricPolicy.Tiers[tierIndex]
		buckets := make(map[rollupKey]*rollupBucket)
		start := bucketStartMillis(now.Add(-tier.Retention).UnixMilli(), tier.Interval.Milliseconds())
		for bucketStart := start; bucketStart < now.UnixMilli(); bucketStart += tier.Interval.Milliseconds() {
			bucket := newRollupBucket(policy.compression())
			bucket.tagsHash, bucket.tagsJSON = tagsHash, tagsJSON
			bucket.labelsHash, bucket.labelsJSON = emptyLabelsHash, "{}"
			bucket.addPoint(1, bucketStart)
			buckets[rollupKey{entityID: "n1", tagsHash: tagsHash, labelsHash: emptyLabelsHash, bucket: bucketStart}] = bucket
		}
		if _, err := s.writeRollupBucketsTx(ctx, "coverage", tier.Interval, buckets, tx); err != nil {
			_ = tx.Rollback()
			t.Fatalf("seed %s history: %v", tier.Interval, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit history: %v", err)
	}
	for _, duration := range []time.Duration{time.Hour, 6 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 60 * 24 * time.Hour} {
		interval := s.CompatibleSeriesInterval(now.Add(-duration), now, time.Minute)
		series, err := s.Series(ctx, AggregateQuery{
			Query:       Query{MetricName: "coverage", EntityID: "n1", Start: now.Add(-duration), End: now},
			Aggregation: AggAvg,
			Interval:    interval,
		}, now)
		if err != nil {
			t.Fatalf("query %s: %v", duration, err)
		}
		if len(series) == 0 {
			t.Fatalf("query %s returned no retained buckets", duration)
		}
		if series[len(series)-1].Count == 0 {
			t.Fatalf("query %s ended with an empty bucket: %#v", duration, series[len(series)-1])
		}
		for i := 1; i < len(series); i++ {
			if delta := series[i].Bucket.Sub(series[i-1].Bucket); delta != interval {
				t.Fatalf("query %s has a break between %s and %s: got %s, want %s", duration, series[i-1].Bucket, series[i].Bucket, delta, interval)
			}
		}
	}
}
