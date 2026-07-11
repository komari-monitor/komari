package metricstore

import (
	"context"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
)

func TestDefaultRollupPolicy(t *testing.T) {
	policy := defaultRollupPolicy(30)
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
	wantRetentions := []time.Duration{48 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour}
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
		Driver:        "sqlite",
		DSN:           ":memory:",
		RetentionDays: 30,
		TablePrefix:   "metric_",
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

func TestCompactKeepsRotatingCursorAfterFullCycle(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:",
		metric.WithMaxOpenConns(1),
		metric.WithRollupPolicy(defaultRollupPolicy(30)),
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
		metric.WithRollupPolicy(defaultRollupPolicy(30)),
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
