package metric

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPostgreSQLIntegration runs the PostgreSQL integration test when configured.
//
// TestPostgreSQLIntegration 在配置 DSN 后运行 PostgreSQL 集成测试。
func TestPostgreSQLIntegration(t *testing.T) {
	dsn := os.Getenv("METRIC_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("METRIC_POSTGRES_DSN is not set")
	}

	runSQLIntegration(t, "postgres", PostgreSQL(dsn), true)
}

// TestMySQLIntegration runs the MySQL integration test when configured.
//
// TestMySQLIntegration 在配置 DSN 后运行 MySQL 集成测试。
func TestMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("METRIC_MYSQL_DSN")
	if dsn == "" {
		t.Skip("METRIC_MYSQL_DSN is not set")
	}

	runSQLIntegration(t, "mysql", MySQL(dsn), false)
}

// runSQLIntegration exercises the SQL store against an external database.
//
// runSQLIntegration 在外部数据库上执行通用 SQL 集成测试流程。
func runSQLIntegration(t *testing.T, name string, cfg Config, expectSQLPercentile bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prefix := fmt.Sprintf("it_%d_", time.Now().UnixNano())
	cfg.TablePrefix = prefix
	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open %s store: %v", name, err)
	}
	defer store.Close()
	defer dropIntegrationTables(t, store, prefix)

	if err := store.CreateMetric(ctx, Definition{
		Name:        "http.latency",
		Type:        TypeGauge,
		Unit:        "ms",
		Description: "HTTP latency",
		Metadata:    map[string]string{"source": "integration"},
	}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	if err := store.CreateMetric(ctx, Definition{Name: "http.latency", Type: TypeGauge}); err == nil {
		t.Fatalf("duplicate CreateMetric should fail")
	}
	if err := store.UpsertMetric(ctx, Definition{Name: "http.latency", Type: TypeGauge, Unit: "milliseconds"}); err != nil {
		t.Fatalf("upsert metric: %v", err)
	}
	def, err := store.GetMetric(ctx, "http.latency")
	if err != nil {
		t.Fatalf("get metric: %v", err)
	}
	if def.Unit != "milliseconds" {
		t.Fatalf("upsert did not update unit: %#v", def)
	}

	base := time.Date(2026, 6, 20, 1, 0, 0, 0, time.UTC)
	points := []Point{
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base, Value: 10, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(time.Minute), Value: 20, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(2 * time.Minute), Value: 30, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(3 * time.Minute), Value: 40, Tags: map[string]string{"region.zone": "eu-1", "route-name": "/v1/nodes"}},
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	got, err := store.Query(ctx, Query{
		MetricName: "http.latency",
		EntityID:   "api-1",
		Start:      base.Add(-time.Second),
		End:        base.Add(5 * time.Minute),
		Tags:       map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"},
	})
	if err != nil {
		t.Fatalf("query with json tags: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 ap-1 points, got %d: %#v", len(got), got)
	}

	stats, err := store.Stats(ctx, Query{
		MetricName: "http.latency",
		EntityID:   "api-1",
		Start:      base,
		End:        base.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Count != 4 || stats.Avg != 25 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	avgBuckets, err := store.Aggregate(ctx, AggregateQuery{
		Query: Query{
			MetricName: "http.latency",
			EntityID:   "api-1",
			Start:      base,
			End:        base.Add(5 * time.Minute),
		},
		Aggregation:  AggAvg,
		Interval:     2 * time.Minute,
		BucketLimit:  1,
		BucketOffset: 1,
	})
	if err != nil {
		t.Fatalf("avg aggregate: %v", err)
	}
	if len(avgBuckets) != 1 || avgBuckets[0].Value != 35 || avgBuckets[0].Count != 2 {
		t.Fatalf("unexpected bucket-paged avg aggregate: %#v", avgBuckets)
	}

	p95Buckets, err := store.Aggregate(ctx, AggregateQuery{
		Query: Query{
			MetricName: "http.latency",
			EntityID:   "api-1",
			Start:      base,
			End:        base.Add(5 * time.Minute),
		},
		Aggregation: AggP95,
		Interval:    10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("p95 aggregate: %v", err)
	}
	if len(p95Buckets) != 1 || p95Buckets[0].Count != 4 || p95Buckets[0].Value <= 38 {
		t.Fatalf("unexpected p95 aggregate: %#v", p95Buckets)
	}
	if _, ok := sqlAggValueExpr(cfg.Driver, AggP95); ok != expectSQLPercentile {
		t.Fatalf("%s p95 pushdown expectation mismatch: got %v want %v", name, ok, expectSQLPercentile)
	}

	latest, err := store.Latest(ctx, "http.latency", "api-1", 1)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if len(latest) != 1 || latest[0].Value != 40 {
		t.Fatalf("unexpected latest point: %#v", latest)
	}

	deleted, err := store.DeleteBefore(ctx, "http.latency", base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("delete before: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted points, got %d", deleted)
	}
}

// dropIntegrationTables drops integration-test tables.
//
// dropIntegrationTables 删除集成测试创建的表。
func dropIntegrationTables(t *testing.T, store *Store, prefix string) {
	t.Helper()
	if strings.TrimSpace(prefix) == "" {
		t.Fatal("refusing to drop tables with empty prefix")
	}
	for _, name := range []string{prefix + "rollups", prefix + "points", prefix + "definitions"} {
		if _, err := store.db.Exec("DROP TABLE IF EXISTS " + name); err != nil {
			t.Fatalf("drop integration table %s: %v", name, err)
		}
	}
}
