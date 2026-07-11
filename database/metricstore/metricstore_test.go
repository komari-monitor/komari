package metricstore

import (
	"context"
	"testing"
	"time"

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
