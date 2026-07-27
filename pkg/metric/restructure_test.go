package metric

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRestructureRebuildsLegacyPointsIntoNormalizedSchema(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false), WithRollupPolicy(RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}, {Interval: 5 * time.Minute, Retention: 5 * time.Hour}, {Interval: time.Hour, Retention: 24 * time.Hour}, {Interval: 24 * time.Hour, Retention: 365 * 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	legacy := []string{
		fmt.Sprintf(`CREATE TABLE %s (name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL, retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, ts_nano INTEGER NOT NULL, value REAL NOT NULL, tags TEXT NOT NULL, labels TEXT NOT NULL, created_at INTEGER NOT NULL)`, s.tables.points),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, tags TEXT NOT NULL, resolution_nano INTEGER NOT NULL, bucket_nano INTEGER NOT NULL, count INTEGER NOT NULL, sum REAL NOT NULL, sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL, first_val REAL NOT NULL, first_ts INTEGER NOT NULL, last_val REAL NOT NULL, last_ts INTEGER NOT NULL, digest BLOB, created_at INTEGER NOT NULL)`, s.tables.rollups),
		fmt.Sprintf(`CREATE TABLE %s (metric_name TEXT PRIMARY KEY, watermark_nano INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.watermarks),
	}
	for _, statement := range legacy {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy: %v", err)
		}
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	oldDaily := base.Add(-20 * 24 * time.Hour).Truncate(24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.definitions), "cpu", "gauge", "", "", 30, "{}", base.UnixNano(), base.UnixNano()); err != nil {
		t.Fatalf("definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.points), 1, "cpu", "node-a", "hash", base.UnixNano(), 42.0, `{"host":"a"}`, `{"source":"test"}`, base.UnixNano()); err != nil {
		t.Fatalf("point: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollups),
		1, "cpu", "node-a", "hash", `{"host":"a"}`, (24 * time.Hour).Nanoseconds(), oldDaily.UnixNano(),
		1, 7.0, 49.0, 7.0, 7.0, 7.0, oldDaily.UnixNano(), 7.0, oldDaily.UnixNano(), nil, base.UnixNano()); err != nil {
		t.Fatalf("old daily rollup: %v", err)
	}
	needs, err := s.NeedsRestructure(ctx)
	if err != nil || !needs {
		t.Fatalf("needs restructure = %v, %v", needs, err)
	}
	if _, err := s.Restructure(ctx, nil); err != nil {
		t.Fatalf("restructure: %v", err)
	}
	var labelsHash string
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT labels_hash FROM %s LIMIT 1", s.tables.labels)).Scan(&labelsHash); err != nil {
		t.Fatalf("read normalized label hash: %v", err)
	}
	if labelsHash == "" {
		t.Fatal("normalized label hash is empty")
	}
	var rebuildIndexes int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE ?`, s.cfg.TablePrefix+"rebuild_%").Scan(&rebuildIndexes); err != nil {
		t.Fatalf("count rebuild indexes: %v", err)
	}
	if rebuildIndexes != 0 {
		t.Fatalf("rebuild indexes left after table switch: %d", rebuildIndexes)
	}
	for _, index := range s.normalizedIndexes() {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index.name).Scan(&count); err != nil {
			t.Fatalf("find normalized index %s: %v", index.name, err)
		}
		if count != 1 {
			t.Fatalf("normalized index %s count = %d, want 1", index.name, count)
		}
	}
	var dailyCount int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT r.count FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds()).Scan(&dailyCount); err != nil {
		t.Fatalf("read rebuilt daily rollup: %v", err)
	}
	if dailyCount != 1 {
		t.Fatalf("rebuilt daily rollup count = %d, want 1", dailyCount)
	}
	var oldDailyValue float64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT r.last_val FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ? AND r.bucket_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds(), oldDaily.UnixMilli()).Scan(&oldDailyValue); err != nil {
		t.Fatalf("read preserved old daily rollup: %v", err)
	}
	if oldDailyValue != 7 {
		t.Fatalf("old daily rollup value = %v, want 7", oldDailyValue)
	}
	var dailyRows int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds()).Scan(&dailyRows); err != nil {
		t.Fatalf("count rebuilt daily rollups: %v", err)
	}
	if dailyRows != 2 {
		t.Fatalf("rebuilt daily rollup rows = %d, want old and recent days", dailyRows)
	}
	var rawTables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, s.tables.points).Scan(&rawTables); err != nil {
		t.Fatalf("inspect obsolete raw table: %v", err)
	}
	if rawTables != 0 {
		t.Fatalf("metric_points table count = %d, want 0", rawTables)
	}
	points, err := s.Query(ctx, Query{MetricName: "cpu", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("restarted raw window should be empty, got %#v", points)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "cpu", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("query rebuilt rollup: %v", err)
	}
	if len(series) != 1 || series[0].Value != 42 || series[0].Tags["host"] != "a" {
		t.Fatalf("rebuilt rollup = %#v", series)
	}
}

func TestRestructureDropsObsoleteRawTableFromMillisecondSchema(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "normalized", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "normalized", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "normalized", EntityID: "n1", Timestamp: at.Add(10 * time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		series_id BIGINT NOT NULL, label_id BIGINT NOT NULL,
		ts_milli BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
		PRIMARY KEY(series_id, ts_milli))`, s.tables.points)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (series_id, label_id, ts_milli, value)
		SELECT r.series_id, r.label_id, r.first_ts_milli, r.first_val FROM %s r LIMIT 1`, s.tables.points, s.tables.rollups)); err != nil {
		t.Fatal(err)
	}
	needs, err := s.NeedsRestructure(ctx)
	if err != nil || !needs {
		t.Fatalf("NeedsRestructure() = %v, %v", needs, err)
	}
	result, err := s.Restructure(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics != 1 || result.RowsCopied != 0 {
		t.Fatalf("result = %#v", result)
	}
	if exists, err := s.tableExists(ctx, s.tables.points); err != nil || exists {
		t.Fatalf("obsolete raw table exists = %v, err = %v", exists, err)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "normalized", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Count != 2 || series[0].Value != 1.5 {
		t.Fatalf("rollup changed while removing raw table: %#v", series)
	}
}

func TestValidateNormalizedRestructureRejectsMissingDigest(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "invalid", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "invalid", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "invalid", EntityID: "n1", Timestamp: at.Add(time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = NULL WHERE min_val <> max_val", s.tables.rollups)); err != nil {
		t.Fatal(err)
	}
	err := s.validateNormalizedRestructure(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "invalid aggregate rows") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateNormalizedRestructureRejectsWrongDigestCompression(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "invalid-compression", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "invalid-compression", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "invalid-compression", EntityID: "n1", Timestamp: at.Add(time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := NewTDigest(100)
	legacy.Add(1, 1)
	legacy.Add(2, 1)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = ? WHERE min_val <> max_val", s.tables.rollups), legacy.Encode()); err != nil {
		t.Fatal(err)
	}
	err := s.validateNormalizedRestructure(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "compression = 100") {
		t.Fatalf("validation error = %v", err)
	}
}

// TestRestructureDump is opt-in because it rewrites a complete legacy SQLite
// fixture in place. It provides a repeatable full-data migration and size check
// for an externally supplied MariaDB-to-SQLite conversion.
func TestRestructureDump(t *testing.T) {
	path := os.Getenv("KOMARI_METRIC_RESTRUCTURE_DUMP")
	if path == "" {
		t.Skip("set KOMARI_METRIC_RESTRUCTURE_DUMP to run the full-dump migration")
	}
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 600 * time.Minute},
			{Interval: 5 * time.Minute, Retention: 600 * 5 * time.Minute},
			{Interval: time.Hour, Retention: 600 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(path, WithAutoMigrate(false), WithMaxOpenConns(1), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open converted dump: %v", err)
	}
	defer s.Close()
	before, err := s.StorageSize(ctx)
	if err != nil {
		t.Fatalf("measure source storage: %v", err)
	}
	result, err := s.Restructure(ctx, func(progress RestructureProgress) {
		if progress.RowsDone > 0 && progress.RowsDone%100_000 == 0 {
			t.Logf("%s: %d/%d", progress.Phase, progress.RowsDone, progress.RowsTotal)
		}
	})
	if err != nil {
		t.Fatalf("restructure converted dump: %v", err)
	}
	if err := s.ReclaimSpace(ctx); err != nil {
		t.Fatalf("reclaim rebuilt store: %v", err)
	}
	assertRestructuredDumpQueryContinuity(t, ctx, s)
	after, err := s.StorageSize(ctx)
	if err != nil {
		t.Fatalf("measure rebuilt storage: %v", err)
	}
	saved := before - after
	if saved < 0 {
		saved = 0
	}
	percent := 0.0
	if before > 0 {
		percent = float64(saved) / float64(before) * 100
	}
	t.Logf("restructured rows=%d metrics=%d before=%d after=%d saved=%d percent=%.2f", result.RowsCopied, result.Metrics, before, after, saved, percent)
}

func assertRestructuredDumpQueryContinuity(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	var metricName, entityID string
	var latestMilli int64
	err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT series.metric_name, series.entity_id,
		(SELECT MAX(recent.last_ts_milli) FROM %s recent
		 JOIN %s recent_series ON recent_series.id = recent.series_id
		 WHERE recent_series.metric_name = series.metric_name AND recent_series.entity_id = series.entity_id)
		FROM %s daily
		JOIN %s series ON series.id = daily.series_id
		JOIN %s daily_resolution ON daily_resolution.id = daily.resolution_id
		WHERE daily_resolution.resolution_milli = ?
		GROUP BY series.metric_name, series.entity_id
		ORDER BY COUNT(DISTINCT daily.bucket_milli) DESC, series.metric_name, series.entity_id LIMIT 1`,
		s.tables.rollups, s.tables.series,
		s.tables.rollups, s.tables.series, s.tables.resolutions),
		(24*time.Hour).Milliseconds()).Scan(&metricName, &entityID, &latestMilli)
	if err != nil {
		t.Fatalf("select migrated series: %v", err)
	}
	now := fromMillis(latestMilli).Add(time.Millisecond)
	for _, duration := range []time.Duration{time.Hour, 6 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 60 * 24 * time.Hour} {
		start := now.Add(-duration)
		interval := s.CompatibleSeriesInterval(start, now, time.Minute)
		resolution, err := s.coveringSeriesResolution(ctx, Query{MetricName: metricName, EntityID: entityID, Start: start, End: now}, interval)
		if err != nil {
			t.Fatalf("select %s backing tier: %v", duration, err)
		}
		series, err := s.Series(ctx, AggregateQuery{
			Query:       Query{MetricName: metricName, EntityID: entityID, Start: start, End: now},
			Aggregation: AggAvg,
			Interval:    interval,
		}, now)
		if err != nil {
			t.Fatalf("query %s: %v", duration, err)
		}
		if len(series) == 0 || series[len(series)-1].Count == 0 {
			t.Fatalf("query %s has no final data bucket: %#v", duration, series)
		}
		expectedStep := max(interval, resolution)
		maxGap := time.Duration(0)
		for i := 1; i < len(series); i++ {
			gap := series[i].Bucket.Sub(series[i-1].Bucket)
			if gap > maxGap {
				maxGap = gap
			}
		}
		if maxGap > 2*expectedStep {
			t.Fatalf("query %s has a %s discontinuity larger than two %s buckets", duration, maxGap, expectedStep)
		}
		tailGap := now.Sub(series[len(series)-1].Bucket)
		if tailGap < 0 || tailGap > 2*expectedStep {
			t.Fatalf("query %s final data is %s from the query end, expected at most %s", duration, tailGap, 2*expectedStep)
		}
		t.Logf("range=%s metric=%s entity=%s requested=%s backing=%s points=%d first=%s last=%s max_gap=%s tail_gap=%s",
			duration, metricName, entityID, interval, resolution, len(series), series[0].Bucket, series[len(series)-1].Bucket, maxGap, tailGap)
	}
}

func TestRestructuredDumpQueryContinuity(t *testing.T) {
	path := os.Getenv("KOMARI_METRIC_RESTRUCTURE_RESULT")
	if path == "" {
		t.Skip("set KOMARI_METRIC_RESTRUCTURE_RESULT to validate a rebuilt dump")
	}
	ctx := context.Background()
	s, err := Open(ctx, SQLite(path, WithAutoMigrate(false), WithMaxOpenConns(1), WithRollupPolicy(RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 600 * time.Minute},
			{Interval: 5 * time.Minute, Retention: 600 * 5 * time.Minute},
			{Interval: time.Hour, Retention: 600 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	})))
	if err != nil {
		t.Fatalf("open rebuilt dump: %v", err)
	}
	defer s.Close()
	assertRestructuredDumpQueryContinuity(t, ctx, s)
}
