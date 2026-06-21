package metric

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSQLiteStoreWriteQueryAggregate verifies SQLite write, query, aggregate, and stats.
//
// TestSQLiteStoreWriteQueryAggregate 验证 SQLite 写入、查询、聚合和统计。
func TestSQLiteStoreWriteQueryAggregate(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:test-metric?mode=memory&cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	if err := store.CreateMetric(ctx, Definition{Name: "cpu.usage", Type: TypeGauge, Unit: "%"}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	base := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
	points := []Point{
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base, Value: 10, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base.Add(time.Minute), Value: 20, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base.Add(2 * time.Minute), Value: 30, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-2", Timestamp: base.Add(time.Minute), Value: 99, Tags: map[string]string{"region": "eu"}},
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	got, err := store.Query(ctx, Query{
		MetricName: "cpu.usage",
		EntityID:   "server-1",
		Start:      base.Add(-time.Second),
		End:        base.Add(3 * time.Minute),
		Tags:       map[string]string{"region": "ap"},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 points, got %d", len(got))
	}
	if got[0].Value != 10 || got[2].Value != 30 {
		t.Fatalf("unexpected ordered values: %#v", got)
	}

	agg, err := store.Aggregate(ctx, AggregateQuery{
		Query: Query{
			MetricName: "cpu.usage",
			EntityID:   "server-1",
			Start:      base,
			End:        base.Add(3 * time.Minute),
		},
		Aggregation: AggAvg,
		Interval:    2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(agg) != 2 {
		t.Fatalf("expected 2 aggregate buckets, got %d", len(agg))
	}
	if agg[0].Value != 15 || agg[0].Count != 2 {
		t.Fatalf("unexpected first aggregate: %#v", agg[0])
	}

	stats, err := store.Stats(ctx, Query{
		MetricName: "cpu.usage",
		EntityID:   "server-1",
		Start:      base,
		End:        base.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Count != 3 || stats.Avg != 20 || stats.P95 != 29 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestWriteRejectsNonFiniteValues(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite("file:test-non-finite?mode=memory&cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		err := store.Write(ctx, Point{
			MetricName: "bad",
			EntityID:   "server-1",
			Timestamp:  time.Now(),
			Value:      value,
		})
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("expected ErrInvalidArgument for %v, got %v", value, err)
		}
	}
}

// TestSQLiteInDirCreatesDirectoryAndAppliesPragmas verifies SQLite file setup and PRAGMAs.
//
// TestSQLiteInDirCreatesDirectoryAndAppliesPragmas 验证 SQLite 文件初始化和 PRAGMA 设置。
func TestSQLiteInDirCreatesDirectoryAndAppliesPragmas(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "metrics")
	store, err := Open(ctx, SQLiteInDir(
		dir,
		WithSQLiteProfile(SQLiteProfilePerformance),
		WithSQLiteCacheSizeKB(32*1024),
	))
	if err != nil {
		t.Fatalf("open sqlite dir store: %v", err)
	}
	defer store.Close()

	if _, err := os.Stat(filepath.Join(dir, "metrics.db")); err != nil {
		t.Fatalf("expected sqlite database file to be created: %v", err)
	}

	var journalMode string
	if err := store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("expected WAL journal mode, got %q", journalMode)
	}

	var synchronous int
	if err := store.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("query synchronous: %v", err)
	}
	if synchronous != 0 {
		t.Fatalf("expected performance profile synchronous=OFF(0), got %d", synchronous)
	}
}
