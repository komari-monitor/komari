package metric

import (
	"context"
	"fmt"
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

func TestExactRawWindowKeepsOnlyRecentMinute(t *testing.T) {
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
	if len(raw) != 2 || raw[0].Value != 2 || raw[1].Value != 3 {
		t.Fatalf("retained raw = %#v, want the two recent samples", raw)
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

func TestCoveringSeriesResolutionUsesFinestTierCoveringStart(t *testing.T) {
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

	resolution, err := s.coveringSeriesResolution(ctx, Query{MetricName: "coverage-tier", EntityID: "n1", Start: now.Add(-6 * time.Hour), End: now}, time.Minute)
	if err != nil {
		t.Fatalf("select covering tier: %v", err)
	}
	if resolution != 5*time.Minute {
		t.Fatalf("covering tier = %s, want 5m", resolution)
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
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "retention-boundary", EntityID: "n1", Timestamp: day.Add(12 * time.Hour), Value: 10},
		{MetricName: "retention-boundary", EntityID: "n1", Timestamp: day.Add(20 * time.Hour), Value: 20},
	}); err != nil {
		t.Fatalf("write daily boundary points: %v", err)
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
	points := make([]Point, 0, 60*24*60)
	for at := now.Add(-60 * 24 * time.Hour); at.Before(now); at = at.Add(time.Minute) {
		points = append(points, Point{MetricName: "coverage", EntityID: "n1", Timestamp: at, Value: 1})
	}
	if err := s.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact history: %v", err)
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
	}
}
