package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
)

func TestDefaultRollupPolicy(t *testing.T) {
	policy := defaultRollupPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("default rollup policy should validate: %v", err)
	}
	if policy.RawRetention != DefaultRollupRawRetention {
		t.Fatalf("raw retention = %s, want %s", policy.RawRetention, DefaultRollupRawRetention)
	}
	if len(policy.Tiers) != 3 {
		t.Fatalf("expected 3 rollup tiers, got %d", len(policy.Tiers))
	}

	wantIntervals := []time.Duration{time.Minute, 5 * time.Minute, time.Hour}
	wantRetentions := []time.Duration{48 * time.Hour, 14 * 24 * time.Hour, 14 * 24 * time.Hour}
	for i := range wantIntervals {
		if policy.Tiers[i].Interval != wantIntervals[i] {
			t.Fatalf("tier %d interval = %s, want %s", i, policy.Tiers[i].Interval, wantIntervals[i])
		}
		if policy.Tiers[i].Retention != wantRetentions[i] {
			t.Fatalf("tier %d retention = %s, want %s", i, policy.Tiers[i].Retention, wantRetentions[i])
		}
	}
}

func TestBuildMetricConfigEnablesDefaultRollupPolicy(t *testing.T) {
	cfg, err := buildMetricConfig(&MetricStoreConfig{
		Driver:              "sqlite",
		DSN:                 ":memory:",
		DownsamplingEnabled: true,
		TablePrefix:         "metric_",
	}, false)
	if err != nil {
		t.Fatalf("build metric config: %v", err)
	}
	if !cfg.RollupPolicy.Enabled() {
		t.Fatal("expected default rollup policy to be enabled")
	}
	if cfg.RollupPolicy.RawRetention != DefaultRollupRawRetention {
		t.Fatalf("raw retention = %s, want %s", cfg.RollupPolicy.RawRetention, DefaultRollupRawRetention)
	}
}

func TestBuildMetricConfigLeavesFinalRetentionToMetricDefinition(t *testing.T) {
	cfg, err := buildMetricConfig(&MetricStoreConfig{
		Driver:              "sqlite",
		DSN:                 ":memory:",
		DownsamplingEnabled: true,
	}, false)
	if err != nil {
		t.Fatalf("build metric config: %v", err)
	}
	wantRollupRetention := 14 * 24 * time.Hour
	lastTier := cfg.RollupPolicy.Tiers[len(cfg.RollupPolicy.Tiers)-1]
	if lastTier.Retention != wantRollupRetention {
		t.Fatalf("rollup retention = %s, want %s", lastTier.Retention, wantRollupRetention)
	}
}

func TestBuildMetricConfigCanDisableDownsampling(t *testing.T) {
	cfg, err := buildMetricConfig(&MetricStoreConfig{
		Driver:              "sqlite",
		DSN:                 ":memory:",
		DownsamplingEnabled: false,
	}, false)
	if err != nil {
		t.Fatalf("build metric config: %v", err)
	}
	if cfg.RollupPolicy.Enabled() {
		t.Fatal("expected rollup policy to be disabled")
	}
}

func TestGetPingRecordsReadsRollupsAfterRawCompaction(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()
	if err := s.UpsertMetric(ctx, metric.Definition{
		Name:          MetricPingLatency,
		Type:          metric.TypeGauge,
		RetentionDays: 30,
	}); err != nil {
		t.Fatalf("create ping metric: %v", err)
	}
	if err := s.WriteBatch(ctx, []metric.Point{
		{MetricName: MetricPingLatency, EntityID: "node-a", Timestamp: now.Add(-20 * time.Minute), Value: 20, Tags: map[string]string{"task_id": "7"}},
		{MetricName: MetricPingLatency, EntityID: "node-a", Timestamp: now.Add(-10 * time.Minute), Value: 10, Tags: map[string]string{"task_id": "7"}},
		{MetricName: MetricPingLatency, EntityID: "node-a", Timestamp: now.Add(-5 * time.Minute), Value: 5, Tags: map[string]string{"task_id": "7"}},
	}); err != nil {
		t.Fatalf("write ping points: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact ping points: %v", err)
	}

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
	}()

	records, err := GetPingRecords(ctx, "node-a", 7, now.Add(-30*time.Minute), now)
	if err != nil {
		t.Fatalf("get ping records: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 ping records across raw and rollup data, got %d: %#v", len(records), records)
	}
	if records[0].Value != 5 || records[1].Value != 10 || records[2].Value != 20 {
		t.Fatalf("unexpected ping values in descending order: %#v", records)
	}
}

func TestCreateMetricDefinitionsUsesExplicitRetentionAndPreservesOverrides(t *testing.T) {
	if defaultBuiltinMetricRetentionDays != 1 {
		t.Fatalf("default built-in metric retention = %d, want 1 day", defaultBuiltinMetricRetentionDays)
	}

	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()

	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("create definitions: %v", err)
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	if len(defs) != 25 {
		t.Fatalf("definition count = %d, want 25", len(defs))
	}
	for _, def := range defs {
		if def.RetentionDays != defaultBuiltinMetricRetentionDays {
			t.Fatalf("%s retention = %d, want %d", def.Name, def.RetentionDays, defaultBuiltinMetricRetentionDays)
		}
	}

	cpu, err := s.GetMetric(ctx, MetricCPU)
	if err != nil {
		t.Fatalf("get cpu definition: %v", err)
	}
	cpu.RetentionDays = 60
	if err := s.UpsertMetric(ctx, cpu); err != nil {
		t.Fatalf("override cpu retention: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("recreate definitions: %v", err)
	}
	cpu, err = s.GetMetric(ctx, MetricCPU)
	if err != nil {
		t.Fatalf("reload cpu definition: %v", err)
	}
	if cpu.RetentionDays != 60 {
		t.Fatalf("cpu retention = %d, want preserved override 60", cpu.RetentionDays)
	}
	if _, err := s.SetMetricRetention(ctx, MetricCPU, 0); err != nil {
		t.Fatalf("disable cpu retention: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("refresh disabled definition: %v", err)
	}
	cpu, err = s.GetMetric(ctx, MetricCPU)
	if err != nil {
		t.Fatalf("reload disabled cpu definition: %v", err)
	}
	if cpu.RetentionDays != 0 {
		t.Fatalf("cpu retention = %d, want preserved disabled state", cpu.RetentionDays)
	}
}

func TestGetRetentionSummaryUsesAllMetricDefinitions(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	defer s.Close()

	storeMu.Lock()
	oldStore := store
	store = s
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		storeMu.Unlock()
	}()

	empty, err := GetRetentionSummary(ctx)
	if err != nil {
		t.Fatalf("summarize empty store: %v", err)
	}
	if empty.AllPositive || empty.MaxDays != 0 {
		t.Fatalf("unexpected empty summary: %#v", empty)
	}
	for _, def := range []metric.Definition{
		{Name: "short", Type: metric.TypeGauge, RetentionDays: 7},
		{Name: "long", Type: metric.TypeGauge, RetentionDays: 60},
	} {
		if err := s.UpsertMetric(ctx, def); err != nil {
			t.Fatalf("upsert %s: %v", def.Name, err)
		}
	}
	summary, err := GetRetentionSummary(ctx)
	if err != nil {
		t.Fatalf("summarize definitions: %v", err)
	}
	if !summary.AllPositive || summary.MaxDays != 60 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if _, err := s.SetMetricRetention(ctx, "short", 0); err != nil {
		t.Fatalf("disable short metric: %v", err)
	}
	summary, err = GetRetentionSummary(ctx)
	if err != nil {
		t.Fatalf("summarize disabled metric: %v", err)
	}
	if summary.AllPositive || summary.MaxDays != 60 {
		t.Fatalf("unexpected disabled summary: %#v", summary)
	}
}

func TestSummarizeRetentionDefinitionsRequiresEveryMetricToBePositive(t *testing.T) {
	summary := summarizeRetentionDefinitions([]metric.Definition{
		{Name: "enabled", RetentionDays: 30},
		{Name: "disabled", RetentionDays: 0},
		{Name: "long", RetentionDays: 60},
	})
	if summary.AllPositive || summary.MaxDays != 60 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestCompactCleansExpiredRawPointsWhenDownsamplingDisabled(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:", metric.WithMaxOpenConns(1)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := s.UpsertMetric(ctx, metric.Definition{
		Name:          "raw.metric",
		Type:          metric.TypeGauge,
		RetentionDays: 1,
	}); err != nil {
		t.Fatalf("upsert metric: %v", err)
	}

	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	if err := s.WriteBatch(ctx, []metric.Point{
		{MetricName: "raw.metric", EntityID: "node", Timestamp: now.Add(-48 * time.Hour), Value: 1},
		{MetricName: "raw.metric", EntityID: "node", Timestamp: now.Add(-time.Hour), Value: 2},
	}); err != nil {
		t.Fatalf("write points: %v", err)
	}

	storeMu.Lock()
	oldStore := store
	oldCompactAt := compactAt
	store = s
	compactAt = 0
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		compactAt = oldCompactAt
		storeMu.Unlock()
		_ = s.Close()
	}()

	if _, err := Compact(ctx, now); err != nil {
		t.Fatalf("compact: %v", err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: "raw.metric",
		EntityID:   "node",
		Start:      now.Add(-72 * time.Hour),
		End:        now,
	})
	if err != nil {
		t.Fatalf("query points: %v", err)
	}
	if len(points) != 1 || points[0].Value != 2 {
		t.Fatalf("expected only the retained raw point, got %#v", points)
	}
}

func TestCompactKeepsRotatingCursorAfterFullCycle(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	for _, name := range []string{"a.metric", "b.metric", "c.metric"} {
		if err := s.UpsertMetric(ctx, metric.Definition{Name: name, Type: metric.TypeGauge}); err != nil {
			t.Fatalf("upsert metric %s: %v", name, err)
		}
	}

	storeMu.Lock()
	oldStore := store
	oldCompactAt := compactAt
	store = s
	compactAt = 1
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		compactAt = oldCompactAt
		storeMu.Unlock()
		_ = s.Close()
	}()

	if _, err := Compact(ctx, time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compactAt != 1 {
		t.Fatalf("compact cursor = %d, want 1 after a complete rotated cycle", compactAt)
	}
}

func TestGetRecordsByClientAndTimeReadsRollupsAfterRawCompaction(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy()),
	))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		t.Fatalf("create metric definitions: %v", err)
	}

	storeMu.Lock()
	oldStore := store
	oldCompactAt := compactAt
	store = s
	compactAt = 0
	storeMu.Unlock()
	defer func() {
		storeMu.Lock()
		store = oldStore
		compactAt = oldCompactAt
		storeMu.Unlock()
		_ = s.Close()
	}()

	now := time.Now().UTC().Truncate(time.Minute)
	ts := now.Add(-time.Hour)
	rec := models.Record{
		Client:         "node-a",
		Time:           models.FromTime(ts),
		Cpu:            42.5,
		Ram:            123456,
		RamTotal:       999999,
		Disk:           456789,
		DiskTotal:      777777,
		Load:           0.75,
		Connections:    321,
		ConnectionsUdp: 12,
	}
	if err := WriteRecord(ctx, rec); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact raw into rollup: %v", err)
	}
	raw, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   rec.Client,
		Start:      ts.Add(-time.Minute),
		End:        now,
	})
	if err != nil {
		t.Fatalf("query raw cpu: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected old raw cpu point to be deleted after compaction, got %d", len(raw))
	}

	got, err := GetRecordsByClientAndTime(ctx, rec.Client, ts.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("get records: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 reconstructed record from rollup, got %d: %#v", len(got), got)
	}
	if got[0].Cpu == 0 || got[0].Ram == 0 || got[0].Disk == 0 || got[0].Connections == 0 {
		t.Fatalf("record was not reconstructed from rollup: %#v", got[0])
	}

	all, err := GetRecordsByTime(ctx, ts.Add(-time.Minute), now)
	if err != nil {
		t.Fatalf("get all records: %v", err)
	}
	if len(all) != 1 || all[0].Client != rec.Client || all[0].Cpu == 0 {
		t.Fatalf("all-client records were not reconstructed from rollup: %#v", all)
	}
}
