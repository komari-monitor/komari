package metricstore

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

func TestMigrateBetweenStoresCopiesPersistedRollupsAfterReopen(t *testing.T) {
	ctx := context.Background()
	policy := defaultRollupPolicy()
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source, err := metric.Open(ctx, metric.SQLite(sourcePath,
		metric.WithMaxOpenConns(1),
		metric.WithMaxIdleConns(1),
		metric.WithRollupPolicy(policy),
	))
	if err != nil {
		t.Fatalf("open source: %v", err)
	}

	definitions := []metric.Definition{
		{Name: "a.metric", Description: "migrated definition", Type: metric.TypeGauge, Unit: "%", RetentionDays: 30, Metadata: map[string]string{"owner": "test"}},
		{Name: "z.empty", Type: metric.TypeCounter, Unit: "bytes", RetentionDays: 7, Metadata: map[string]string{}},
	}
	for _, definition := range definitions {
		if err := source.UpsertMetric(ctx, definition); err != nil {
			t.Fatalf("upsert source metric %q: %v", definition.Name, err)
		}
	}

	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	digest := metric.NewTDigest(policy.Compression)
	for _, value := range []float64{10, 20, 30} {
		digest.Add(value, 1)
	}
	sourceRollups := []metric.PersistedRollup{
		{
			MetricName: "a.metric", EntityID: "agent-1", Tags: map[string]string{"device": "cpu0"}, Labels: map[string]string{"zone": "ap"},
			Resolution: time.Minute, Bucket: base, Count: 3, Sum: 60, SumSq: 1400, Min: 10, Max: 30,
			FirstValue: 10, FirstTime: base.Add(time.Second), LastValue: 30, LastTime: base.Add(40 * time.Second),
			Digest: digest.Encode(), CreatedAt: base.Add(time.Hour),
		},
		{
			MetricName: "a.metric", EntityID: "agent-2", Tags: map[string]string{}, Labels: map[string]string{"zone": "eu"},
			Resolution: time.Hour, Bucket: base, Count: 12, Sum: 60, SumSq: 300, Min: 5, Max: 5,
			FirstValue: 5, FirstTime: base.Add(time.Minute), LastValue: 5, LastTime: base.Add(50 * time.Minute),
			CreatedAt: base.Add(time.Hour),
		},
	}
	if err := source.ImportRollups(ctx, sourceRollups); err != nil {
		t.Fatalf("seed source rollups: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	source, err = metric.Open(ctx, metric.SQLite(sourcePath,
		metric.WithMaxOpenConns(1),
		metric.WithMaxIdleConns(1),
		metric.WithRollupPolicy(policy),
	))
	if err != nil {
		t.Fatalf("reopen source: %v", err)
	}
	defer source.Close()
	raw, err := source.Query(ctx, metric.Query{
		MetricName: "a.metric", Start: base.Add(-time.Hour), End: base.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("query reopened raw window: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("reopened source raw points = %d, want 0", len(raw))
	}

	target, err := metric.Open(ctx, metric.SQLite(filepath.Join(t.TempDir(), "target.db"),
		metric.WithMaxOpenConns(1),
		metric.WithMaxIdleConns(1),
		metric.WithRollupPolicy(policy),
	))
	if err != nil {
		t.Fatalf("open target: %v", err)
	}
	defer target.Close()

	type progressEvent struct {
		metricName                string
		metricIndex, totalMetrics int
		added                     int64
	}
	var events []progressEvent
	total, err := migrateBetweenStores(ctx, source, target, func(name string, index, definitions int, added int64) {
		events = append(events, progressEvent{name, index, definitions, added})
	})
	if err != nil {
		t.Fatalf("migrate stores: %v", err)
	}
	if total != int64(len(sourceRollups)) {
		t.Fatalf("migrated rollups = %d, want %d", total, len(sourceRollups))
	}
	wantEvents := []progressEvent{
		{metricName: "a.metric", metricIndex: 0, totalMetrics: 2},
		{metricName: "a.metric", metricIndex: 0, totalMetrics: 2, added: 2},
		{metricName: "z.empty", metricIndex: 1, totalMetrics: 2},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("migration events = %#v, want %#v", events, wantEvents)
	}

	for _, want := range definitions {
		got, err := target.GetMetric(ctx, want.Name)
		if err != nil {
			t.Fatalf("get target metric %q: %v", want.Name, err)
		}
		if got.Name != want.Name || got.Description != want.Description || got.Type != want.Type || got.Unit != want.Unit || got.RetentionDays != want.RetentionDays || !reflect.DeepEqual(got.Metadata, want.Metadata) {
			t.Fatalf("target definition = %#v, want mutable fields from %#v", got, want)
		}
	}

	exported := exportMigrationTestRollups(t, target, "a.metric")
	if !reflect.DeepEqual(exported, sourceRollups) {
		t.Fatalf("target rollups differ:\n got: %#v\nwant: %#v", exported, sourceRollups)
	}
	secondTotal, err := MigrateBetweenStores(ctx, source, target)
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if secondTotal != total {
		t.Fatalf("repeat migrated rollups = %d, want %d", secondTotal, total)
	}
	if got := exportMigrationTestRollups(t, target, "a.metric"); !reflect.DeepEqual(got, sourceRollups) {
		t.Fatalf("repeat migration changed target rollups: %#v", got)
	}
}

func exportMigrationTestRollups(t *testing.T, store *metric.Store, metricName string) []metric.PersistedRollup {
	t.Helper()
	var result []metric.PersistedRollup
	_, err := store.ExportRollups(context.Background(), metricName, 1, func(batch []metric.PersistedRollup) error {
		result = append(result, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("export %q rollups: %v", metricName, err)
	}
	return result
}
