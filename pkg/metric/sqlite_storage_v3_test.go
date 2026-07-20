package metric

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStorageV3MigratesLegacyDataAndPreservesQueries(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	tags := map[string]string{"task_id": "7"}
	tagsHash, tagsJSON, err := tagsFingerprint(tags)
	if err != nil {
		t.Fatalf("fingerprint tags: %v", err)
	}
	digest := NewTDigest(defaultTDigestCompression)
	for i := 0; i < 60; i++ {
		digest.Add(float64(i%10), 1)
	}
	legacyDigest := digest.encodeRaw()

	legacy, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open legacy SQLite database: %v", err)
	}
	createLegacySQLiteMetricSchema(t, ctx, legacy)
	createdAt := base.Add(-time.Hour).UnixNano()
	if _, err := legacy.ExecContext(ctx, `INSERT INTO metric_definitions
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"latency", TypeGauge, "ms", "legacy metric", 30, `{"source":"legacy"}`, createdAt, createdAt,
	); err != nil {
		t.Fatalf("insert legacy definition: %v", err)
	}
	rawTime := base.Add(30 * time.Second)
	if _, err := legacy.ExecContext(ctx, `INSERT INTO metric_points
		(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"latency", "node-a", tagsHash, rawTime.UnixNano(), 12.5, tagsJSON, `{"origin":"old-db"}`, createdAt,
	); err != nil {
		t.Fatalf("insert legacy point: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO metric_rollups
		(metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano,
		 count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"latency", "node-a", tagsHash, tagsJSON, time.Minute.Nanoseconds(), base.UnixNano(),
		60, 270.0, 1710.0, 0.0, 9.0, 0.0, base.UnixNano(),
		9.0, base.Add(59*time.Second).UnixNano(), legacyDigest, createdAt,
	); err != nil {
		t.Fatalf("insert legacy rollup: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy SQLite database: %v", err)
	}

	policy := RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	store, err := Open(ctx, SQLite(dsn, WithRollupPolicy(policy), WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("open and migrate legacy SQLite database: %v", err)
	}
	if store.readDB == nil || !store.sqliteStorageV3 {
		t.Fatal("SQLite V3/read pool was not enabled after migration")
	}
	assertSQLiteV3Schema(t, ctx, store.db)
	assertSQLiteQueryPlanUses(t, sqliteQueryPlan(t, ctx, store.db,
		`SELECT ts_nano, value FROM metric_points WHERE metric_name = ? AND entity_id = ? AND ts_nano BETWEEN ? AND ? ORDER BY ts_nano`,
		"latency", "node-a", base.UnixNano(), base.Add(time.Minute).UnixNano()),
		"SEARCH s USING COVERING INDEX", "SEARCH p USING PRIMARY KEY")
	assertSQLiteQueryPlanUses(t, sqliteQueryPlan(t, ctx, store.db,
		`SELECT bucket_nano, count FROM metric_rollups WHERE metric_name = ? AND entity_id = ? AND resolution_nano = ? AND bucket_nano BETWEEN ? AND ? ORDER BY bucket_nano`,
		"latency", "node-a", time.Minute.Nanoseconds(), base.UnixNano(), base.Add(time.Minute).UnixNano()),
		"SEARCH s USING COVERING INDEX", "SEARCH r USING PRIMARY KEY")

	points, err := store.Query(ctx, Query{
		MetricName: "latency", EntityID: "node-a", Start: base, End: base.Add(time.Minute), Tags: tags,
	})
	if err != nil {
		t.Fatalf("query migrated raw point: %v", err)
	}
	if len(points) != 1 || points[0].Timestamp != rawTime || points[0].Value != 12.5 ||
		points[0].Tags["task_id"] != "7" || points[0].Labels["origin"] != "old-db" {
		t.Fatalf("migrated raw point changed: %#v", points)
	}

	series, err := store.Series(ctx, AggregateQuery{
		Query: Query{
			MetricName: "latency", EntityID: "node-a", Start: base,
			End: base.Add(time.Minute - time.Nanosecond), Tags: tags,
		},
		Aggregation: AggP99,
		Interval:    time.Minute,
	}, base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("query migrated percentile rollup: %v", err)
	}
	if len(series) != 1 || series[0].Count != 60 || math.Float64bits(series[0].Value) != math.Float64bits(digest.Quantile(0.99)) {
		t.Fatalf("migrated percentile rollup changed: %#v", series)
	}

	var migratedDigest []byte
	if err := store.db.QueryRowContext(ctx, `SELECT digest FROM metric_rollup_values LIMIT 1`).Scan(&migratedDigest); err != nil {
		t.Fatalf("read migrated digest: %v", err)
	}
	if len(migratedDigest) >= len(legacyDigest) || migratedDigest[1] != tdigestCompressedMagic1 {
		t.Fatalf("legacy digest was not compressed: old=%d new=%d", len(legacyDigest), len(migratedDigest))
	}

	rootPage := sqliteRootPage(t, ctx, store.db, "metric_series")
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	store, err = Open(ctx, SQLite(dsn, WithRollupPolicy(policy), WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("reopen migrated SQLite database: %v", err)
	}
	defer store.Close()
	if got := sqliteRootPage(t, ctx, store.db, "metric_series"); got != rootPage {
		t.Fatalf("SQLite V3 storage was rebuilt on reopen: root page %d -> %d", rootPage, got)
	}

	updated := Point{
		MetricName: "latency", EntityID: "node-a", Timestamp: rawTime,
		Value: 99, Tags: tags, Labels: map[string]string{"origin": "new-write"},
	}
	if err := store.Write(ctx, updated); err != nil {
		t.Fatalf("upsert point through V3 view: %v", err)
	}
	points, err = store.Query(ctx, Query{MetricName: "latency", EntityID: "node-a", Start: base, End: base.Add(time.Minute)})
	if err != nil || len(points) != 1 || points[0].Value != 99 || points[0].Labels["origin"] != "new-write" {
		t.Fatalf("V3 point upsert result: points=%#v err=%v", points, err)
	}
	deleted, err := store.DeleteEntity(ctx, "node-a")
	if err != nil {
		t.Fatalf("delete migrated entity: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted rows = %d, want raw+rollup = 2", deleted)
	}
	var remainingSeries int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_series`).Scan(&remainingSeries); err != nil || remainingSeries != 0 {
		t.Fatalf("unused series after entity delete = %d, err=%v", remainingSeries, err)
	}
}

func TestSQLiteStorageV3UsedForNewDatabase(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatalf("open new SQLite database: %v", err)
	}
	defer store.Close()
	assertSQLiteV3Schema(t, ctx, store.db)
}

func TestSQLiteStorageV3SupportsCustomTablePrefix(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLite(
		sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db")),
		WithTablePrefix("custom_"),
	))
	if err != nil {
		t.Fatalf("open custom-prefix SQLite database: %v", err)
	}
	defer store.Close()
	for name, wantKind := range map[string]string{
		"custom_points": "view", "custom_rollups": "view", "custom_series": "table",
		"custom_point_values": "table", "custom_rollup_values": "table",
	} {
		kind, err := sqliteObjectType(ctx, store.db, name)
		if err != nil || kind != wantKind {
			t.Fatalf("custom-prefix object %s: kind=%q want=%q err=%v", name, kind, wantKind, err)
		}
	}
}

func TestSQLiteStorageV3DetectedWithoutAutoMigration(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	store, err := Open(ctx, SQLite(dsn))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMetric(ctx, Definition{Name: "existing", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	store, err = Open(ctx, SQLite(dsn, WithAutoMigrate(false)))
	if err != nil {
		t.Fatalf("open V3 database without auto migration: %v", err)
	}
	defer store.Close()
	if !store.sqliteStorageV3 {
		t.Fatal("V3 storage was not detected without auto migration")
	}
	point := Point{MetricName: "existing", EntityID: "node-a", Timestamp: time.Now().UTC(), Value: 1}
	if err := store.Write(ctx, point); err != nil {
		t.Fatalf("write V3 database without auto migration: %v", err)
	}
}

func TestSQLiteStorageV3IncrementalVacuumReclaimsDeletedPages(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, SQLiteInDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateMetric(ctx, Definition{Name: "vacuum", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	points := make([]Point, 5000)
	for i := range points {
		points[i] = Point{MetricName: "vacuum", EntityID: "node-a", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i)}
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMetricData(ctx, "vacuum"); err != nil {
		t.Fatal(err)
	}
	beforePages := sqlitePragmaInt(t, ctx, store.db, "page_count")
	beforeFree := sqlitePragmaInt(t, ctx, store.db, "freelist_count")
	if beforeFree == 0 {
		t.Fatal("expected deleted V3 data to leave reclaimable pages")
	}
	if err := store.incrementalSQLiteVacuum(ctx, beforeFree); err != nil {
		t.Fatal(err)
	}
	afterPages := sqlitePragmaInt(t, ctx, store.db, "page_count")
	afterFree := sqlitePragmaInt(t, ctx, store.db, "freelist_count")
	if afterPages > beforePages || afterFree >= beforeFree {
		t.Fatalf("incremental vacuum did not reclaim pages: pages %d->%d free %d->%d", beforePages, afterPages, beforeFree, afterFree)
	}
}

func TestSQLiteStorageV3MigrationFailureKeepsLegacyTables(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	legacy, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open legacy SQLite database: %v", err)
	}
	createLegacySQLiteMetricSchema(t, ctx, legacy)
	if _, err := legacy.ExecContext(ctx, `ALTER TABLE metric_rollups DROP COLUMN digest`); err != nil {
		t.Fatalf("make legacy rollup schema incompatible: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO metric_points
		(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES ('latency', 'node-a', 'hash', 1, 12.5, '{}', '{}', 1)`); err != nil {
		t.Fatalf("insert legacy point: %v", err)
	}
	_ = legacy.Close()

	if store, err := Open(ctx, SQLite(dsn)); err == nil {
		_ = store.Close()
		t.Fatal("migration unexpectedly succeeded for incompatible legacy schema")
	}
	legacy, err = sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("reopen legacy database: %v", err)
	}
	defer legacy.Close()
	for _, table := range []string{"metric_points", "metric_rollups"} {
		kind, err := sqliteObjectType(ctx, legacy, table)
		if err != nil || kind != "table" {
			t.Fatalf("legacy object %s after rollback: kind=%q err=%v", table, kind, err)
		}
	}
	var rows int
	if err := legacy.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_points`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("legacy rows after rollback = %d, err=%v", rows, err)
	}
}

func TestSQLiteStorageV3ReducesRepresentativeFileSize(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	dsn := sqliteFileDSN(path)
	legacy, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	createLegacySQLiteMetricSchema(t, ctx, legacy)
	tagsHash, tagsJSON, err := tagsFingerprint(nil)
	if err != nil {
		t.Fatal(err)
	}
	digest := NewTDigest(defaultTDigestCompression)
	for i := 0; i < 60; i++ {
		digest.Add(float64(i%10), 1)
	}
	legacyDigest := digest.encodeRaw()
	tx, err := legacy.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	pointStmt, err := tx.PrepareContext(ctx, `INSERT INTO metric_points
		(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
		VALUES (?, ?, ?, ?, ?, ?, '{}', ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if _, err := pointStmt.ExecContext(ctx, fmt.Sprintf("metric.%02d", i%12), fmt.Sprintf("node-%03d", i%100),
			tagsHash, int64(i)*time.Second.Nanoseconds(), float64(i%100), tagsJSON, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = pointStmt.Close()
	rollupStmt, err := tx.PrepareContext(ctx, `INSERT INTO metric_rollups
		(metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano,
		 count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 60, 270, 1710, 0, 9, 0, ?, 9, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		bucket := int64(i) * time.Minute.Nanoseconds()
		if _, err := rollupStmt.ExecContext(ctx, fmt.Sprintf("metric.%02d", i%12), fmt.Sprintf("node-%03d", i%100),
			tagsHash, tagsJSON, time.Minute.Nanoseconds(), bucket, bucket, bucket+59*time.Second.Nanoseconds(), legacyDigest, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = rollupStmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `VACUUM`); err != nil {
		t.Fatal(err)
	}
	_ = legacy.Close()
	before := fileSize(t, path)
	store, err := Open(ctx, SQLite(dsn))
	if err != nil {
		t.Fatalf("migrate representative database: %v", err)
	}
	_ = store.Close()
	after := fileSize(t, path)
	t.Logf("SQLite storage V3 size: before=%d after=%d ratio=%.3f", before, after, float64(after)/float64(before))
	if after*100 >= before*85 {
		t.Fatalf("SQLite V3 size = %d, want at least 15%% below legacy %d", after, before)
	}
}

func createLegacySQLiteMetricSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE metric_definitions (
		 name VARCHAR(191) PRIMARY KEY, type VARCHAR(32) NOT NULL, unit VARCHAR(64) NOT NULL DEFAULT '',
		 description TEXT NOT NULL DEFAULT '', retention_days INTEGER NOT NULL DEFAULT 0, metadata TEXT NOT NULL,
		 created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE metric_points (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
		 tags_hash VARCHAR(64) NOT NULL, ts_nano BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
		 tags TEXT NOT NULL, labels TEXT NOT NULL, created_at BIGINT NOT NULL,
		 UNIQUE(metric_name, entity_id, tags_hash, ts_nano))`,
		`CREATE INDEX metric__points_metric_entity_time_idx ON metric_points (metric_name, entity_id, ts_nano)`,
		`CREATE INDEX metric__points_metric_time_idx ON metric_points (metric_name, ts_nano)`,
		`CREATE INDEX metric__points_entity_time_idx ON metric_points (entity_id, ts_nano)`,
		`CREATE TABLE metric_rollups (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
		 tags_hash VARCHAR(64) NOT NULL, tags TEXT NOT NULL, resolution_nano BIGINT NOT NULL, bucket_nano BIGINT NOT NULL,
		 count BIGINT NOT NULL, sum DOUBLE PRECISION NOT NULL, sum_sq DOUBLE PRECISION NOT NULL,
		 min_val DOUBLE PRECISION NOT NULL, max_val DOUBLE PRECISION NOT NULL, first_val DOUBLE PRECISION NOT NULL,
		 first_ts BIGINT NOT NULL, last_val DOUBLE PRECISION NOT NULL, last_ts BIGINT NOT NULL,
		 digest BLOB, created_at BIGINT NOT NULL,
		 UNIQUE(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano))`,
		`CREATE INDEX metric__rollups_lookup_idx ON metric_rollups (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)`,
		`CREATE INDEX metric__rollups_res_time_idx ON metric_rollups (metric_name, resolution_nano, bucket_nano)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy SQLite schema: %v", err)
		}
	}
}

func assertSQLiteV3Schema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for name, wantKind := range map[string]string{
		"metric_points": "view", "metric_rollups": "view",
		"metric_series": "table", "metric_point_values": "table", "metric_rollup_values": "table",
	} {
		kind, err := sqliteObjectType(ctx, db, name)
		if err != nil || kind != wantKind {
			t.Fatalf("SQLite V3 object %s: kind=%q want=%q err=%v", name, kind, wantKind, err)
		}
	}
	var mode int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != 2 {
		t.Fatalf("SQLite auto_vacuum mode = %d, want incremental(2), err=%v", mode, err)
	}
}

func sqliteRootPage(t *testing.T, ctx context.Context, db *sql.DB, name string) int {
	t.Helper()
	var page int
	if err := db.QueryRowContext(ctx, `SELECT rootpage FROM sqlite_master WHERE name = ?`, name).Scan(&page); err != nil {
		t.Fatalf("read SQLite root page for %s: %v", name, err)
	}
	return page
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func sqlitePragmaInt(t *testing.T, ctx context.Context, db *sql.DB, name string) int {
	t.Helper()
	var value int
	if err := db.QueryRowContext(ctx, `PRAGMA `+name).Scan(&value); err != nil {
		t.Fatalf("read SQLite PRAGMA %s: %v", name, err)
	}
	return value
}

func sqliteQueryPlan(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	return plan
}

func assertSQLiteQueryPlanUses(t *testing.T, plan []string, fragments ...string) {
	t.Helper()
	joined := strings.Join(plan, " | ")
	for _, fragment := range fragments {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("SQLite query plan %q does not use %q", joined, fragment)
		}
	}
}
