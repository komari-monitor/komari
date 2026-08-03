package metric

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRollupTransferRoundTrip(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 10 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 10 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 50 * time.Hour},
			{Interval: time.Hour, Retention: 600 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	source := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "source.db"), policy)
	target := openRollupTransferTestStore(t, filepath.Join(t.TempDir(), "target.db"), policy)

	definition := Definition{
		Name: "load.average", Description: "system load", Type: TypeGauge,
		Unit: "load", RetentionDays: 365, Metadata: map[string]string{"scope": "system"},
	}
	for _, store := range []*Store{source, target} {
		if err := store.UpsertMetric(ctx, definition); err != nil {
			t.Fatalf("upsert metric: %v", err)
		}
	}

	base := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	variableDigest := NewTDigest(policy.Compression)
	for _, value := range []float64{1, 4, 9} {
		variableDigest.Add(value, 1)
	}
	constantDigest := NewTDigest(policy.Compression)
	constantDigest.Add(7, 2)
	createdAt := base.Add(48 * time.Hour)
	input := []PersistedRollup{
		{
			MetricName: definition.Name, EntityID: "node-a",
			Tags: map[string]string{"core": "0", "region": "ap"}, Labels: map[string]string{"rack": "r1"},
			Resolution: 30 * time.Second, Bucket: base, Count: 2, Sum: 14, SumSq: 98,
			Min: 7, Max: 7, FirstValue: 7, FirstTime: base.Add(time.Second),
			LastValue: 7, LastTime: base.Add(20 * time.Second), Digest: constantDigest.Encode(), CreatedAt: createdAt,
		},
		{
			MetricName: definition.Name, EntityID: "node-a",
			Tags: map[string]string{"core": "0", "region": "ap"}, Labels: map[string]string{"rack": "r1"},
			Resolution: time.Minute, Bucket: base, Count: 3, Sum: 14, SumSq: 98,
			Min: 1, Max: 9, FirstValue: 1, FirstTime: base.Add(time.Second),
			LastValue: 9, LastTime: base.Add(40 * time.Second), Digest: variableDigest.Encode(), CreatedAt: createdAt,
		},
		constantTransferRollup(definition.Name, "node-a", base, 5*time.Minute, 11, createdAt),
		constantTransferRollup(definition.Name, "node-b", base, time.Hour, 12, createdAt),
		constantTransferRollup(definition.Name, "node-b", base, 24*time.Hour, 13, createdAt),
	}
	if err := source.ImportRollups(ctx, input); err != nil {
		t.Fatalf("import source rollups: %v", err)
	}

	exported, batches := exportAllTestRollups(t, source, definition.Name, 2)
	if len(exported) != len(input) {
		t.Fatalf("exported rollups = %d, want %d", len(exported), len(input))
	}
	if !reflect.DeepEqual(batches, []int{2, 2, 1}) {
		t.Fatalf("export batch sizes = %v, want [2 2 1]", batches)
	}
	for _, rollup := range exported {
		if rollup.Min == rollup.Max && len(rollup.Digest) != 0 {
			t.Fatalf("constant rollup at %s retained a digest", rollup.Resolution)
		}
	}
	if len(exported[1].Digest) == 0 {
		t.Fatal("non-constant minute rollup did not preserve its digest")
	}
	exportedDigest, err := DecodeTDigest(exported[1].Digest)
	if err != nil || math.Abs(exportedDigest.Quantile(0.95)-variableDigest.Quantile(0.95)) > 1e-12 {
		t.Fatalf("non-constant minute rollup changed digest semantics: err=%v", err)
	}

	if err := target.ImportRollups(ctx, exported); err != nil {
		t.Fatalf("import target rollups: %v", err)
	}
	if err := target.ImportRollups(ctx, exported); err != nil {
		t.Fatalf("repeat target import: %v", err)
	}
	roundTrip, _ := exportAllTestRollups(t, target, definition.Name, 20)
	if !reflect.DeepEqual(roundTrip, exported) {
		t.Fatalf("round-trip rollups differ:\n got: %#v\nwant: %#v", roundTrip, exported)
	}

	digest, err := DecodeTDigest(variableDigest.Encode())
	if err != nil {
		t.Fatalf("decode expected digest: %v", err)
	}
	points, err := target.AggregateRollup(ctx, AggregateQuery{
		Query: Query{
			MetricName: definition.Name, EntityID: "node-a", Tags: map[string]string{"core": "0", "region": "ap"},
			Start: base, End: base.Add(time.Minute - time.Millisecond),
		},
		Aggregation: AggP95, Interval: time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("query transferred percentile: %v", err)
	}
	if len(points) != 1 || points[0].Count != 3 || math.Abs(points[0].Value-digest.Quantile(0.95)) > 1e-12 {
		t.Fatalf("transferred percentile = %#v, want count 3 value %v", points, digest.Quantile(0.95))
	}
}

func openRollupTransferTestStore(t *testing.T, path string, policy RollupPolicy) *Store {
	t.Helper()
	store, err := Open(context.Background(), SQLite(path,
		WithMaxOpenConns(1),
		WithMaxIdleConns(1),
		WithRollupPolicy(policy),
	))
	if err != nil {
		t.Fatalf("open rollup transfer store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func constantTransferRollup(metricName, entityID string, bucket time.Time, resolution time.Duration, value float64, createdAt time.Time) PersistedRollup {
	return PersistedRollup{
		MetricName: metricName, EntityID: entityID,
		Tags: map[string]string{"region": "eu"}, Labels: map[string]string{"rack": "r2"},
		Resolution: resolution, Bucket: bucket, Count: 4, Sum: value * 4, SumSq: value * value * 4,
		Min: value, Max: value, FirstValue: value, FirstTime: bucket.Add(time.Second),
		LastValue: value, LastTime: bucket.Add(2 * time.Second), CreatedAt: createdAt,
	}
}

func exportAllTestRollups(t *testing.T, store *Store, metricName string, batchSize int) ([]PersistedRollup, []int) {
	t.Helper()
	var (
		rollups []PersistedRollup
		batches []int
	)
	total, err := store.ExportRollups(context.Background(), metricName, batchSize, func(batch []PersistedRollup) error {
		batches = append(batches, len(batch))
		rollups = append(rollups, batch...)
		return nil
	})
	if err != nil {
		t.Fatalf("export rollups: %v", err)
	}
	if total != int64(len(rollups)) {
		t.Fatalf("export total = %d, want %d", total, len(rollups))
	}
	return rollups, batches
}
