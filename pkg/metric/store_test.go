package metric

import (
	"context"
	"database/sql"
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

	if err := store.CreateMetric(ctx, Definition{Name: "cpu.usage", Type: TypeGauge, Unit: "%", RetentionDays: 30}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Minute)
	points := []Point{
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base, Value: 10, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base.Add(10 * time.Second), Value: 20, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-1", Timestamp: base.Add(20 * time.Second), Value: 30, Tags: map[string]string{"region": "ap"}},
		{MetricName: "cpu.usage", EntityID: "server-2", Timestamp: base.Add(10 * time.Second), Value: 99, Tags: map[string]string{"region": "eu"}},
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	got, err := store.Query(ctx, Query{
		MetricName: "cpu.usage",
		EntityID:   "server-1",
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Minute),
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
			End:        base.Add(time.Minute),
		},
		Aggregation: AggAvg,
		Interval:    20 * time.Second,
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
		End:        base.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Count != 3 || stats.Avg != 20 || math.Abs(stats.P95-29) > 1 {
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
	if err := store.CreateMetric(ctx, Definition{Name: "bad", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}

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

func TestWriteBatchRequiresMetricDefinition(t *testing.T) {
	ctx := context.Background()
	store := newMemStore(t)
	point := Point{MetricName: "custom.metric", EntityID: "server-1", Timestamp: time.Now().UTC(), Value: 1}

	if err := store.Write(ctx, point); !errors.Is(err, ErrNotFound) {
		t.Fatalf("write undefined metric error = %v, want ErrNotFound", err)
	}
	if err := store.CreateMetric(ctx, Definition{Name: point.MetricName, Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("register dynamic metric: %v", err)
	}
	if err := store.Write(ctx, point); err != nil {
		t.Fatalf("write registered dynamic metric: %v", err)
	}
}

func TestUpdateMetricRetentionDefersDisabledMetricCleanup(t *testing.T) {
	ctx := context.Background()
	store := newMemStore(t)
	const metricName = "deferred.cleanup"
	if err := store.CreateMetric(ctx, Definition{Name: metricName, Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	point := Point{MetricName: metricName, EntityID: "server-1", Timestamp: time.Now().UTC(), Value: 1}
	if err := store.Write(ctx, point); err != nil {
		t.Fatalf("write point: %v", err)
	}

	if _, err := store.UpdateMetricRetention(ctx, metricName, 0); err != nil {
		t.Fatalf("disable metric retention: %v", err)
	}
	points, err := store.Query(ctx, Query{MetricName: metricName, EntityID: point.EntityID, Start: point.Timestamp.Add(-time.Second), End: point.Timestamp.Add(time.Second)})
	if err != nil {
		t.Fatalf("query retained point before cleanup: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("points before cleanup = %d, want 1", len(points))
	}

	deleted, err := store.DeleteMetricDataIfDisabled(ctx, metricName)
	if err != nil {
		t.Fatalf("delete disabled metric data: %v", err)
	}
	if !deleted {
		t.Fatal("expected disabled metric data to be deleted")
	}
	points, err = store.Query(ctx, Query{MetricName: metricName, EntityID: point.EntityID, Start: point.Timestamp.Add(-time.Second), End: point.Timestamp.Add(time.Second)})
	if err != nil {
		t.Fatalf("query cleaned metric: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("points after cleanup = %d, want 0", len(points))
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
	var autoCheckpoint int
	if err := store.db.QueryRowContext(ctx, "PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil {
		t.Fatalf("query wal autocheckpoint: %v", err)
	}
	if autoCheckpoint != 4000 {
		t.Fatalf("wal autocheckpoint = %d, want 4000", autoCheckpoint)
	}
	var journalSizeLimit int64
	if err := store.db.QueryRowContext(ctx, "PRAGMA journal_size_limit").Scan(&journalSizeLimit); err != nil {
		t.Fatalf("query journal size limit: %v", err)
	}
	if journalSizeLimit != 4*1024*1024 {
		t.Fatalf("journal size limit = %d, want %d", journalSizeLimit, 4*1024*1024)
	}
}

// TestSQLiteConnectionHookConfiguresExpandedAndRotatedReaders verifies that
// connection-local PRAGMAs do not disappear after the reader pool grows or
// database/sql replaces expired connections.
func TestSQLiteConnectionHookConfiguresExpandedAndRotatedReaders(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "metrics.db")) + "?mode=rwc&_txlock=immediate"
	store, err := Open(ctx, SQLite(
		dsn,
		WithSQLiteProfile(SQLiteProfileDurable),
		WithSQLiteCacheSizeKB(8*1024),
		WithSQLiteMMapSize(8*1024*1024),
		WithSQLiteTempStoreMemory(false),
		WithSQLiteWALAutoCheckpoint(128),
		WithSQLiteJournalSizeLimit(512*1024),
		WithSQLiteReadPool(2),
	))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()
	if store.readDB == nil {
		t.Fatal("expected dedicated SQLite read pool")
	}

	first, err := store.readDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire first reader: %v", err)
	}
	defer first.Close()
	second, err := store.readDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire second reader: %v", err)
	}
	defer second.Close()
	assertSQLiteConnectionTuning(t, ctx, first)
	assertSQLiteConnectionTuning(t, ctx, second)

	if err := first.Close(); err != nil {
		t.Fatalf("release first reader: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("release second reader: %v", err)
	}
	statsBefore := store.readDB.Stats()
	store.readDB.SetConnMaxLifetime(time.Nanosecond)
	time.Sleep(time.Millisecond)

	rotated, err := store.readDB.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire rotated reader: %v", err)
	}
	defer rotated.Close()
	assertSQLiteConnectionTuning(t, ctx, rotated)
	if statsAfter := store.readDB.Stats(); statsAfter.MaxLifetimeClosed <= statsBefore.MaxLifetimeClosed {
		t.Fatalf("reader pool did not replace an expired connection: before=%d after=%d", statsBefore.MaxLifetimeClosed, statsAfter.MaxLifetimeClosed)
	}
}

func assertSQLiteConnectionTuning(t *testing.T, ctx context.Context, conn *sql.Conn) {
	t.Helper()
	checks := []struct {
		pragma string
		want   any
	}{
		{pragma: "foreign_keys", want: 1},
		{pragma: "journal_mode", want: "wal"},
		{pragma: "synchronous", want: 2},
		{pragma: "busy_timeout", want: 5000},
		{pragma: "cache_size", want: -8 * 1024},
		{pragma: "mmap_size", want: int64(8 * 1024 * 1024)},
		{pragma: "temp_store", want: 1},
		{pragma: "wal_autocheckpoint", want: 128},
		{pragma: "journal_size_limit", want: int64(512 * 1024)},
	}
	for _, check := range checks {
		switch want := check.want.(type) {
		case int:
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(&got); err != nil {
				t.Fatalf("read PRAGMA %s: %v", check.pragma, err)
			}
			if got != want {
				t.Fatalf("PRAGMA %s = %d, want %d", check.pragma, got, want)
			}
		case int64:
			var got int64
			if err := conn.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(&got); err != nil {
				t.Fatalf("read PRAGMA %s: %v", check.pragma, err)
			}
			if got != want {
				t.Fatalf("PRAGMA %s = %d, want %d", check.pragma, got, want)
			}
		case string:
			var got string
			if err := conn.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(&got); err != nil {
				t.Fatalf("read PRAGMA %s: %v", check.pragma, err)
			}
			if got != want {
				t.Fatalf("PRAGMA %s = %q, want %q", check.pragma, got, want)
			}
		default:
			t.Fatalf("unsupported expected type for PRAGMA %s: %T", check.pragma, want)
		}
	}

	var cacheSpill int
	if err := conn.QueryRowContext(ctx, "PRAGMA cache_spill").Scan(&cacheSpill); err != nil {
		t.Fatalf("read PRAGMA cache_spill: %v", err)
	}
	if cacheSpill == 0 {
		t.Fatal("PRAGMA cache_spill is disabled")
	}
}
