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
	runSQLRestructureIntegration(t, "postgres", PostgreSQL(dsn))
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
	runSQLRestructureIntegration(t, "mysql", MySQL(dsn))
}

// runSQLIntegration exercises the SQL store against an external database.
//
// runSQLIntegration 在外部数据库上执行通用 SQL 集成测试流程。
func runSQLIntegration(t *testing.T, name string, cfg Config, expectSQLPercentile bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	prefix := fmt.Sprintf("it_%d_", time.Now().UnixNano())
	cfg.TablePrefix = prefix
	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open %s store: %v", name, err)
	}
	defer store.Close()
	defer dropIntegrationTables(t, store, prefix)
	assertIntegrationForeignKeys(t, store)

	if err := store.CreateMetric(ctx, Definition{
		Name:          "http.latency",
		Type:          TypeGauge,
		Unit:          "ms",
		Description:   "HTTP latency",
		Metadata:      map[string]string{"source": "integration"},
		RetentionDays: 30}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	if err := store.CreateMetric(ctx, Definition{Name: "http.latency", Type: TypeGauge, RetentionDays: 30}); err == nil {
		t.Fatalf("duplicate CreateMetric should fail")
	}
	if err := store.UpsertMetric(ctx, Definition{Name: "http.latency", Type: TypeGauge, Unit: "milliseconds", RetentionDays: 30}); err != nil {
		t.Fatalf("upsert metric: %v", err)
	}
	def, err := store.GetMetric(ctx, "http.latency")
	if err != nil {
		t.Fatalf("get metric: %v", err)
	}
	if def.Unit != "milliseconds" {
		t.Fatalf("upsert did not update unit: %#v", def)
	}

	now := time.Now().UTC()
	base := now.Truncate(time.Minute)
	if elapsed := now.Sub(base); elapsed < time.Second {
		time.Sleep(time.Second - elapsed)
	}
	points := []Point{
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base, Value: 10, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(100 * time.Millisecond), Value: 20, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(200 * time.Millisecond), Value: 30, Tags: map[string]string{"region.zone": "ap-1", "route-name": "/v1/nodes"}},
		{MetricName: "http.latency", EntityID: "api-1", Timestamp: base.Add(300 * time.Millisecond), Value: 40, Tags: map[string]string{"region.zone": "eu-1", "route-name": "/v1/nodes"}},
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	got, err := store.Query(ctx, Query{
		MetricName: "http.latency",
		EntityID:   "api-1",
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Second),
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
		End:        base.Add(time.Second),
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
			End:        base.Add(time.Second),
			Tags:       map[string]string{"region.zone": "ap-1"},
		},
		Aggregation:  AggAvg,
		Interval:     200 * time.Millisecond,
		BucketLimit:  1,
		BucketOffset: 1,
	})
	if err != nil {
		t.Fatalf("avg aggregate: %v", err)
	}
	if len(avgBuckets) != 1 || avgBuckets[0].Value != 30 || avgBuckets[0].Count != 1 {
		t.Fatalf("unexpected bucket-paged avg aggregate: %#v", avgBuckets)
	}

	p95Buckets, err := store.Aggregate(ctx, AggregateQuery{
		Query: Query{
			MetricName: "http.latency",
			EntityID:   "api-1",
			Start:      base,
			End:        base.Add(time.Second),
			Tags:       map[string]string{"region.zone": "ap-1"},
		},
		Aggregation: AggP95,
		Interval:    10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("p95 aggregate: %v", err)
	}
	if len(p95Buckets) != 1 || p95Buckets[0].Count != 3 || p95Buckets[0].Value <= 28 {
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
	if err := store.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush rollups: %v", err)
	}
	assertContiguousIntegrationDictionaryIDs(t, store)

	deleted, err := store.DeleteBefore(ctx, "http.latency", base.Add(200*time.Millisecond))
	if err != nil {
		t.Fatalf("delete before: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted points, got %d", deleted)
	}

	storageSize, err := store.StorageSize(ctx)
	if err != nil {
		t.Fatalf("storage size: %v", err)
	}
	if storageSize <= 0 {
		t.Fatalf("storage size = %d, want a positive value", storageSize)
	}
	if err := store.ReclaimSpace(ctx); err != nil {
		t.Fatalf("reclaim storage space: %v", err)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("store unusable after space reclaim: %v", err)
	}
}

func runSQLRestructureIntegration(t *testing.T, name string, cfg Config) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	prefix := fmt.Sprintf("rit_%d_", time.Now().UnixNano())
	cfg.TablePrefix = prefix
	cfg.AutoMigrate = false
	store, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open legacy %s store: %v", name, err)
	}
	defer store.Close()
	defer dropIntegrationTables(t, store, prefix)

	d := store.dialect
	legacyDDL := []string{
		fmt.Sprintf(`CREATE TABLE %s (
			name VARCHAR(191) PRIMARY KEY, type VARCHAR(32) NOT NULL, unit VARCHAR(64) NOT NULL,
			description TEXT NOT NULL, retention_days INTEGER NOT NULL, metadata %s NOT NULL,
			created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`, store.tables.definitions, d.jsonType()),
		fmt.Sprintf(`CREATE TABLE %s (
			id BIGINT PRIMARY KEY, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL, ts_nano BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
			tags %s NOT NULL, labels %s NOT NULL, created_at BIGINT NOT NULL)`, store.tables.points, d.jsonType(), d.jsonType()),
		fmt.Sprintf(`CREATE TABLE %s (
			id BIGINT PRIMARY KEY, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
			tags_hash VARCHAR(64) NOT NULL, tags %s NOT NULL, resolution_nano BIGINT NOT NULL,
			bucket_nano BIGINT NOT NULL, count BIGINT NOT NULL, sum DOUBLE PRECISION NOT NULL,
			sum_sq DOUBLE PRECISION NOT NULL, min_val DOUBLE PRECISION NOT NULL, max_val DOUBLE PRECISION NOT NULL,
			first_val DOUBLE PRECISION NOT NULL, first_ts BIGINT NOT NULL, last_val DOUBLE PRECISION NOT NULL,
			last_ts BIGINT NOT NULL, digest %s, created_at BIGINT NOT NULL)`, store.tables.rollups, d.jsonType(), d.blobType()),
		fmt.Sprintf(`CREATE TABLE %s (metric_name VARCHAR(191) PRIMARY KEY, watermark_nano BIGINT NOT NULL, updated_at BIGINT NOT NULL)`, store.tables.watermarks),
	}
	for _, statement := range legacyDDL {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy %s schema: %v", name, err)
		}
	}

	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	created := at.UnixNano()
	definitionSQL := fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s)`, store.tables.definitions,
		d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4),
		d.placeholder(5), d.jsonPlaceholder(6), d.placeholder(7), d.placeholder(8))
	if _, err := store.db.ExecContext(ctx, definitionSQL, "legacy.metric", TypeGauge, "ms", "legacy fixture", 30, "{}", created, created); err != nil {
		t.Fatalf("seed legacy %s definition: %v", name, err)
	}
	tagsHash, tagsJSON, err := tagsFingerprint(map[string]string{"source": "legacy"})
	if err != nil {
		t.Fatalf("fingerprint legacy tags: %v", err)
	}
	pointSQL := fmt.Sprintf(`INSERT INTO %s
		(id, metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s)`, store.tables.points,
		d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4), d.placeholder(5),
		d.placeholder(6), d.jsonPlaceholder(7), d.jsonPlaceholder(8), d.placeholder(9))
	for i, value := range []float64{10, 20} {
		pointAt := at.Add(time.Duration(i) * 10 * time.Second)
		if _, err := store.db.ExecContext(ctx, pointSQL, i+1, "legacy.metric", "node-1", tagsHash, pointAt.UnixNano(), value, tagsJSON, "{}", created); err != nil {
			t.Fatalf("seed legacy %s point %d: %v", name, i, err)
		}
	}
	hourAt := at.Add(-2 * time.Hour).Truncate(time.Hour)
	rollupSQL := fmt.Sprintf(`INSERT INTO %s
		(id, metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano, count,
		 sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, created_at)
		VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`, store.tables.rollups,
		d.placeholder(1), d.placeholder(2), d.placeholder(3), d.placeholder(4), d.jsonPlaceholder(5), d.placeholder(6),
		d.placeholder(7), d.placeholder(8), d.placeholder(9), d.placeholder(10), d.placeholder(11), d.placeholder(12),
		d.placeholder(13), d.placeholder(14), d.placeholder(15), d.placeholder(16), d.placeholder(17), d.placeholder(18))
	if _, err := store.db.ExecContext(ctx, rollupSQL,
		1, "legacy.metric", "node-1", tagsHash, tagsJSON, time.Hour.Nanoseconds(), hourAt.UnixNano(),
		1, 7, 49, 7, 7, 7, hourAt.UnixNano(), 7, hourAt.UnixNano(), nil, created,
	); err != nil {
		t.Fatalf("seed legacy %s rollup: %v", name, err)
	}

	result, err := store.Restructure(ctx, nil)
	if err != nil {
		t.Fatalf("restructure legacy %s store: %v", name, err)
	}
	if result.RowsCopied != 3 || result.Metrics != 1 {
		t.Fatalf("legacy %s restructure result = %#v", name, result)
	}
	if needs, err := store.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("legacy %s still needs restructure: needs=%v err=%v", name, needs, err)
	}
	if exists, err := store.tableExists(ctx, store.tables.points); err != nil || exists {
		t.Fatalf("legacy %s points table remains: exists=%v err=%v", name, exists, err)
	}
	assertIntegrationForeignKeys(t, store)
	minute, err := store.AggregateRollup(ctx, AggregateQuery{
		Query:       Query{MetricName: "legacy.metric", EntityID: "node-1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg,
		Interval:    time.Minute,
	}, time.Minute)
	if err != nil {
		t.Fatalf("query restructured %s minute rollup: %v", name, err)
	}
	if len(minute) != 1 || minute[0].Count != 2 || minute[0].Value != 15 {
		t.Fatalf("restructured %s minute rollup = %#v", name, minute)
	}
	assertContiguousIntegrationDictionaryIDs(t, store)
}

func assertIntegrationForeignKeys(t *testing.T, store *Store) {
	t.Helper()
	var query string
	switch store.cfg.Driver {
	case DriverMySQL:
		query = `SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS
			WHERE CONSTRAINT_SCHEMA = DATABASE() AND CONSTRAINT_TYPE = 'FOREIGN KEY'
			AND TABLE_NAME IN (?, ?)`
	case DriverPostgreSQL:
		query = `SELECT COUNT(*) FROM information_schema.table_constraints
			WHERE table_schema = current_schema() AND constraint_type = 'FOREIGN KEY'
			AND table_name IN ($1, $2)`
	default:
		t.Fatalf("unexpected integration driver %q", store.cfg.Driver)
	}
	var count int
	if err := store.db.QueryRow(query, store.tables.series, store.tables.rollups).Scan(&count); err != nil {
		t.Fatalf("query foreign key metadata: %v", err)
	}
	if count != 4 {
		t.Fatalf("foreign key count = %d, want 4", count)
	}
}

func assertContiguousIntegrationDictionaryIDs(t *testing.T, store *Store) {
	t.Helper()
	for _, table := range []string{store.tables.series, store.tables.labels, store.tables.resolutions} {
		var count, minID, maxID int64
		if err := store.db.QueryRow(fmt.Sprintf("SELECT COUNT(*), COALESCE(MIN(id), 0), COALESCE(MAX(id), 0) FROM %s", table)).Scan(&count, &minID, &maxID); err != nil {
			t.Fatalf("query dictionary ids from %s: %v", table, err)
		}
		if count == 0 || minID != 1 || maxID != count {
			t.Fatalf("dictionary ids in %s: count=%d min=%d max=%d, want a fresh contiguous range", table, count, minID, maxID)
		}
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
	for _, name := range []string{
		prefix + "rebuild_points",
		prefix + "rebuild_rollups",
		prefix + "rebuild_series",
		prefix + "rebuild_label_sets",
		prefix + "rebuild_resolutions",
		prefix + "rebuild_definitions",
		prefix + "compaction_watermarks_legacy",
		prefix + "points_legacy",
		prefix + "rollups_legacy",
		prefix + "definitions_legacy",
		prefix + "compaction_watermarks",
		prefix + "points",
		prefix + "rollups",
		prefix + "series",
		prefix + "label_sets",
		prefix + "resolutions",
		prefix + "definitions",
	} {
		if _, err := store.db.Exec("DROP TABLE IF EXISTS " + name); err != nil {
			t.Fatalf("drop integration table %s: %v", name, err)
		}
	}
}
