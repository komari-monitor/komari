package metric

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLiteStorageV4MigratesV3AndPreservesExactPoints(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	base := time.Date(2026, 7, 24, 8, 0, 0, 123, time.UTC)
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	if err := store.CreateMetric(ctx, Definition{Name: "exact", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	points := make([]Point, 1200)
	for i := range points {
		points[i] = Point{
			MetricName: "exact",
			EntityID:   "node-a",
			Timestamp:  base.Add(time.Duration(i)*3*time.Second + time.Duration(i%17)),
			Value:      math.Float64frombits(math.Float64bits(1000.25) + uint64(i%97)),
			Tags:       map[string]string{"iface": "eth0"},
			Labels:     map[string]string{"source": []string{"agent", "restore"}[i%2]},
		}
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, SQLite(dsn, WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("migrate V3 to V4: %v", err)
	}
	defer store.Close()
	if !store.sqliteStorageV4 {
		t.Fatal("SQLite V4 storage was not enabled")
	}
	var hotCount, blockPointCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_point_values`).Scan(&hotCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(point_count), 0) FROM metric_point_blocks`).Scan(&blockPointCount); err != nil {
		t.Fatal(err)
	}
	if hotCount != 0 || blockPointCount != len(points) {
		t.Fatalf("unexpected V4 physical counts: hot=%d blocks=%d want=%d", hotCount, blockPointCount, len(points))
	}
	got, err := store.Query(ctx, Query{
		MetricName: "exact", EntityID: "node-a", Start: points[0].Timestamp, End: points[len(points)-1].Timestamp,
		Tags: map[string]string{"iface": "eth0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(points) {
		t.Fatalf("migrated point count=%d want=%d", len(got), len(points))
	}
	for i := range points {
		if !got[i].Timestamp.Equal(points[i].Timestamp) || math.Float64bits(got[i].Value) != math.Float64bits(points[i].Value) ||
			got[i].Tags["iface"] != "eth0" || got[i].Labels["source"] != points[i].Labels["source"] {
			t.Fatalf("point %d changed during V4 migration: got=%#v want=%#v", i, got[i], points[i])
		}
	}

	updated := points[100]
	updated.Value = 999999.125
	updated.Labels = map[string]string{"source": "hot-update"}
	if err := store.Write(ctx, updated); err != nil {
		t.Fatal(err)
	}
	got, err = store.Query(ctx, Query{MetricName: "exact", EntityID: "node-a", Start: updated.Timestamp, End: updated.Timestamp})
	if err != nil || len(got) != 1 || math.Float64bits(got[0].Value) != math.Float64bits(updated.Value) || got[0].Labels["source"] != "hot-update" {
		t.Fatalf("hot point did not override the sealed value: points=%#v err=%v", got, err)
	}
}

func TestSQLiteStorageV4SealsQueriesAndPartiallyDeletesBlocks(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
	}
	store, err := Open(ctx, SQLiteInDir(t.TempDir(), WithRollupPolicy(policy), WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateMetric(ctx, Definition{Name: "seal", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	base := now.Add(-10 * time.Minute)
	points := make([]Point, 100)
	for i := range points {
		points[i] = Point{MetricName: "seal", EntityID: "node-a", Timestamp: base.Add(time.Duration(i) * time.Second), Value: float64(i), Tags: map[string]string{"task": "1"}}
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompactMetric(ctx, "seal", now); err != nil {
		t.Fatal(err)
	}
	var hot, blocks int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_point_values`).Scan(&hot); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_point_blocks`).Scan(&blocks); err != nil {
		t.Fatal(err)
	}
	if hot != 0 || blocks == 0 {
		t.Fatalf("points were not sealed: hot=%d blocks=%d", hot, blocks)
	}
	got, err := store.Query(ctx, Query{MetricName: "seal", EntityID: "node-a", Start: points[0].Timestamp, End: points[len(points)-1].Timestamp})
	if err != nil || len(got) != len(points) {
		t.Fatalf("query sealed points: count=%d err=%v", len(got), err)
	}
	latest, err := store.Latest(ctx, "seal", "node-a", 1)
	if err != nil || len(latest) != 1 || latest[0].Value != 99 {
		t.Fatalf("latest sealed point: %#v err=%v", latest, err)
	}
	gap := points[10].Timestamp.Add(time.Nanosecond)
	entities, err := store.EntityIDs(ctx, Query{MetricName: "seal", Start: gap, End: gap})
	if err != nil || len(entities) != 0 {
		t.Fatalf("EntityIDs returned a block whose range overlaps but contains no point: %v err=%v", entities, err)
	}

	cutoff := points[50].Timestamp
	deleted, err := store.DeleteBefore(ctx, "seal", cutoff)
	if err != nil || deleted != 50 {
		t.Fatalf("partial block delete=%d want=50 err=%v", deleted, err)
	}
	got, err = store.Query(ctx, Query{MetricName: "seal", EntityID: "node-a", Start: points[0].Timestamp, End: points[len(points)-1].Timestamp})
	if err != nil || len(got) != 50 || !got[0].Timestamp.Equal(cutoff) || got[0].Value != 50 {
		t.Fatalf("query after partial block delete: points=%d first=%#v err=%v", len(got), got[0], err)
	}
}

func TestSQLiteStorageV4MigrationFailureRollsBackToV3(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	if err := store.CreateMetric(ctx, Definition{Name: "overflow", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	points := []Point{
		{MetricName: "overflow", EntityID: "node-a", Timestamp: time.Unix(0, math.MinInt64).UTC(), Value: 1},
		{MetricName: "overflow", EntityID: "node-a", Timestamp: time.Unix(0, math.MaxInt64).UTC(), Value: 2},
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if migrated, err := Open(ctx, SQLite(dsn)); err == nil {
		_ = migrated.Close()
		t.Fatal("V4 migration unexpectedly accepted an overflowing timestamp delta")
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kind, err := sqliteObjectType(ctx, db, "metric_point_blocks")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_point_values`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if kind != "" || count != 2 {
		t.Fatalf("failed V4 migration changed V3 storage: block_kind=%q point_count=%d", kind, count)
	}
}

func TestSQLiteStorageV4ReducesV3FileSize(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	dsn := sqliteFileDSN(path)
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	if err := store.CreateMetric(ctx, Definition{Name: "size", Type: TypeCounter, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, 20000)
	for i := range points {
		points[i] = Point{
			MetricName: "size", EntityID: fmt.Sprintf("node-%02d", i%20),
			Timestamp: base.Add(time.Duration(i/20) * 3 * time.Second), Value: float64(1_000_000 + i*128),
			Labels: map[string]string{"source": "agent"},
		}
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		t.Fatal(err)
	}
	if err := store.fullSQLiteVacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	v3Size := mustFileSize(t, path)
	store, err := Open(ctx, SQLite(dsn))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	v4Size := mustFileSize(t, path)
	t.Logf("SQLite V4 size: V3=%d V4=%d ratio=%.3f", v3Size, v4Size, float64(v4Size)/float64(v3Size))
	if v4Size*100 >= v3Size*70 {
		t.Fatalf("SQLite V4 size=%d, want at least 30%% below V3 size=%d", v4Size, v3Size)
	}
}

func TestSQLiteStorageV4UpgradesEarlyV4RollupsLosslessly(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	policy := RollupPolicy{RawRetention: 15 * time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}}}
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	store.cfg.RollupPolicy = policy
	if err := store.CreateMetric(ctx, Definition{Name: "latency", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{"task": "117"}
	tagsHash, tagsJSON, err := tagsFingerprint(tags)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 123, time.UTC)
	bucket := newRollupBucket(policy.compression())
	bucket.tagsHash, bucket.tagsJSON = tagsHash, tagsJSON
	for index, value := range []float64{math.Float64frombits(0x3ff0000000000001), 9.25, 17.75} {
		bucket.addPoint(value, base.Add(time.Duration(index)*20*time.Second).UnixNano())
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeRollupBucketsTx(ctx, "latency", time.Minute, map[rollupKey]*rollupBucket{
		{entityID: "node-a", tagsHash: tagsHash, bucket: base.UnixNano()}: bucket,
	}, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sourceDigest []byte
	if err := store.db.QueryRowContext(ctx, `SELECT digest FROM metric_rollup_values`).Scan(&sourceDigest); err != nil {
		t.Fatal(err)
	}
	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.createSQLiteV4PointBlocks(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, SQLite(dsn, WithRollupPolicy(policy), WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("upgrade early V4 database: %v", err)
	}
	defer store.Close()
	var hotRows, blockRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollup_values`).Scan(&hotRows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(bucket_count), 0) FROM metric_rollup_blocks`).Scan(&blockRows); err != nil {
		t.Fatal(err)
	}
	if hotRows != 0 || blockRows != 1 {
		t.Fatalf("early V4 rollup migration counts: hot=%d block=%d", hotRows, blockRows)
	}
	series, err := store.sqliteV4MatchingSeries(ctx, store.db, "latency", "node-a", tags)
	if err != nil || len(series) != 1 {
		t.Fatalf("find migrated series: count=%d err=%v", len(series), err)
	}
	records, err := store.loadAllSQLiteV4RollupBlockRecords(ctx, store.db, series[0].id, time.Minute.Nanoseconds())
	if err != nil || len(records) != 1 {
		t.Fatalf("load migrated rollup: count=%d err=%v", len(records), err)
	}
	if !bytes.Equal(records[0].digest, sourceDigest) || records[0].sumBits != math.Float64bits(bucket.sum) ||
		records[0].sumSqBits != math.Float64bits(bucket.sumSq) || records[0].lastTS != bucket.lastTS {
		t.Fatalf("early V4 rollup changed during migration: %#v", records[0])
	}
	rows, err := store.scanRollupRowsBetween(ctx, "latency", "node-a", tags, time.Minute.Nanoseconds(), base.UnixNano(), base.UnixNano(), true)
	if err != nil || len(rows) != 1 || rows[0].bucketData.count != bucket.count {
		t.Fatalf("query migrated rollup: count=%d err=%v", len(rows), err)
	}
	entities, err := store.EntityIDs(ctx, Query{MetricName: "latency", Start: base, End: base})
	if err != nil || len(entities) != 1 || entities[0] != "node-a" {
		t.Fatalf("entity lookup through rollup blocks: entities=%v err=%v", entities, err)
	}
	latest, found, err := store.LatestBefore(ctx, "latency", "node-a", base.Add(time.Minute))
	if err != nil || !found || latest.Timestamp.UnixNano() != bucket.lastTS || math.Float64bits(latest.Value) != math.Float64bits(bucket.lastVal) {
		t.Fatalf("latest through rollup blocks: point=%#v found=%v err=%v", latest, found, err)
	}
	deleted, err := store.DeleteSeries(ctx, Query{MetricName: "latency", EntityID: "node-a", Tags: tags})
	if err != nil || deleted != 1 {
		t.Fatalf("delete compressed rollup series: deleted=%d err=%v", deleted, err)
	}
	rows, err = store.scanRollupRowsBetween(ctx, "latency", "node-a", tags, time.Minute.Nanoseconds(), base.UnixNano(), base.UnixNano(), true)
	if err != nil || len(rows) != 0 {
		t.Fatalf("compressed rollup remained after delete: count=%d err=%v", len(rows), err)
	}
}

func TestSQLiteStorageV4MigratesLegacyRollupBlocksToSplitStorage(t *testing.T) {
	ctx := context.Background()
	dsn := sqliteFileDSN(filepath.Join(t.TempDir(), "metrics.db"))
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	if err := store.CreateMetric(ctx, Definition{Name: "legacy-split", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if err := store.Write(ctx, Point{MetricName: "legacy-split", EntityID: "node-a", Timestamp: base, Value: 1, Tags: map[string]string{"task": "117"}}); err != nil {
		t.Fatal(err)
	}
	series, err := store.sqliteV4MatchingSeries(ctx, store.db, "legacy-split", "node-a", map[string]string{"task": "117"})
	if err != nil || len(series) != 1 {
		t.Fatalf("find legacy series: count=%d err=%v", len(series), err)
	}
	digest := NewTDigest(100)
	for i := 0; i < 5000; i++ {
		digest.Add(float64((i*37)%1009)/11, 1)
	}
	records := make([]sqliteV4RollupRecord, 120)
	for i := range records {
		bucket := base.Add(time.Duration(i) * time.Minute).UnixNano()
		records[i] = sqliteV4RollupRecord{
			bucketNano: bucket, count: 5000,
			sumBits: math.Float64bits(float64(i) + 100.25), sumSqBits: math.Float64bits(float64(i*i) + 200.5),
			minBits: math.Float64bits(float64(i)), maxBits: math.Float64bits(float64(i) + 100),
			firstBits: math.Float64bits(float64(i) + 0.25), firstTS: bucket,
			lastBits: math.Float64bits(float64(i) + 0.75), lastTS: bucket + 59*time.Second.Nanoseconds(),
			digest: digest.Encode(), createdAt: bucket + time.Hour.Nanoseconds(),
		}
	}
	legacy, err := encodeSQLiteV4LegacyRollupBlock(records)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.createSQLiteV4PointBlocks(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		series_id INTEGER NOT NULL REFERENCES %s(id) ON DELETE CASCADE,
		resolution_nano BIGINT NOT NULL, start_nano BIGINT NOT NULL, end_nano BIGINT NOT NULL,
		bucket_count INTEGER NOT NULL, codec INTEGER NOT NULL, checksum INTEGER NOT NULL, payload BLOB NOT NULL,
		PRIMARY KEY(series_id, resolution_nano, start_nano)
	) WITHOUT ROWID`, store.tables.rollupBlocks, store.tables.series)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s
		(series_id, resolution_nano, start_nano, end_nano, bucket_count, codec, checksum, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, store.tables.rollupBlocks), series[0].id, time.Minute.Nanoseconds(),
		legacy.startNano, legacy.endNano, legacy.count, legacy.codec, int64(legacy.checksum), legacy.payload); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, SQLite(dsn, WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatalf("migrate legacy rollup blocks to split storage: %v", err)
	}
	defer store.Close()
	var codec, digestCodec int
	var digestBytes int64
	if err := store.db.QueryRowContext(ctx, `SELECT codec, digest_codec, length(digest_payload) FROM metric_rollup_blocks`).Scan(&codec, &digestCodec, &digestBytes); err != nil {
		t.Fatal(err)
	}
	if codec != sqliteV4RollupBlockCodec || digestCodec != sqliteV4RollupDigestCodec || digestBytes == 0 {
		t.Fatalf("legacy block was not split: codec=%d digest_codec=%d digest_bytes=%d", codec, digestCodec, digestBytes)
	}
	got, err := store.loadAllSQLiteV4RollupBlockRecords(ctx, store.db, series[0].id, time.Minute.Nanoseconds())
	if err != nil || !sqliteV4RollupRecordsEqual(records, got) {
		t.Fatalf("split migration changed rollup values: count=%d err=%v", len(got), err)
	}
	rows, err := store.scanRollupRowsBetween(ctx, "legacy-split", "node-a", map[string]string{"task": "117"},
		time.Minute.Nanoseconds(), records[0].bucketNano, records[len(records)-1].bucketNano, false)
	if err != nil || len(rows) != len(records) || rows[0].bucketData.digest != nil {
		t.Fatalf("summary-only query after migration: count=%d err=%v", len(rows), err)
	}
}

func TestSQLiteStorageV4ReducesRollupDominatedDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "metrics.db")
	dsn := sqliteFileDSN(path)
	store := createSQLiteV3OnlyStore(t, ctx, dsn)
	if err := store.CreateMetric(ctx, Definition{Name: "history", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	tagsHash, tagsJSON, err := tagsFingerprint(map[string]string{"task": "public"})
	if err != nil {
		t.Fatal(err)
	}
	digest := NewTDigest(100)
	for _, value := range []float64{20.125, 20.5, 21.0, 22.75} {
		digest.Add(value, 15)
	}
	digestBlob := digest.Encode()
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	createdAt := base.Add(31 * 24 * time.Hour).UnixNano()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, store.rollupUpsertSQL())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	const entities = 20
	const bucketsPerEntity = 1440
	for entity := 0; entity < entities; entity++ {
		entityID := fmt.Sprintf("node-%02d", entity)
		for bucketIndex := 0; bucketIndex < bucketsPerEntity; bucketIndex++ {
			bucketNano := base.Add(time.Duration(bucketIndex) * time.Minute).UnixNano()
			value := 20.125 + float64((entity+bucketIndex)%17)/8
			if _, err := statement.ExecContext(ctx,
				"history", entityID, tagsHash, tagsJSON, time.Minute.Nanoseconds(), bucketNano,
				60, value*60, value*value*60, value, value, value, bucketNano,
				value, bucketNano+59*time.Second.Nanoseconds(), digestBlob, createdAt,
			); err != nil {
				_ = statement.Close()
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
	}
	if err := statement.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.fullSQLiteVacuum(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	v3Size := mustFileSize(t, path)

	store, err = Open(ctx, SQLite(dsn, WithRollupPolicy(RollupPolicy{Tiers: []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}}})))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.scanRollupRowsBetween(ctx, "history", "node-00", map[string]string{"task": "public"}, time.Minute.Nanoseconds(), base.UnixNano(), base.Add(24*time.Hour-time.Minute).UnixNano(), true)
	if err != nil || len(rows) != bucketsPerEntity {
		t.Fatalf("query rollup-dominated V4 database: count=%d err=%v", len(rows), err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	v4Size := mustFileSize(t, path)
	t.Logf("SQLite V4 rollup-dominated size: V3=%d V4=%d ratio=%.3f", v3Size, v4Size, float64(v4Size)/float64(v3Size))
	if v4Size*100 >= v3Size*70 {
		t.Fatalf("SQLite V4 rollup-dominated size=%d, want at least 30%% below V3 size=%d", v4Size, v3Size)
	}
}

func TestSQLiteStorageV4PartiallyDeletesAndMergesLateRollups(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{RawRetention: 15 * time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}}}
	store, err := Open(ctx, SQLiteInDir(t.TempDir(), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateMetric(ctx, Definition{Name: "late", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	buckets := make(map[rollupKey]*rollupBucket, 700)
	for index := 0; index < 700; index++ {
		bucketNano := base.Add(time.Duration(index) * time.Minute).UnixNano()
		bucket := newRollupBucket(policy.compression())
		bucket.addPoint(float64(index), bucketNano)
		buckets[rollupKey{entityID: "node-a", bucket: bucketNano}] = bucket
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeRollupBucketsTx(ctx, "late", time.Minute, buckets, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.ReclaimSpace(ctx); err != nil {
		t.Fatal(err)
	}

	cutoff := base.Add(350 * time.Minute)
	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.deleteRollupsBeforeTx(ctx, "late", time.Minute, cutoff, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := store.scanRollupRows(ctx, store.db, "late", time.Minute)
	if err != nil || len(rows) != 350 || rows[0].bucket != cutoff.UnixNano() {
		t.Fatalf("partial compressed-rollup delete: count=%d first=%d err=%v", len(rows), rows[0].bucket, err)
	}

	lateBucketNano := base.Add(400 * time.Minute).UnixNano()
	delta := newRollupBucket(policy.compression())
	delta.addPoint(999, lateBucketNano+30*time.Second.Nanoseconds())
	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.mergeRollupBucketsTx(ctx, "late", time.Minute, map[rollupKey]*rollupBucket{
		{entityID: "node-a", bucket: lateBucketNano}: delta,
	}, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, err = store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.sealSQLiteV4RollupHotTx(ctx, tx, "late", base.Add(701*time.Minute).UnixNano()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err = store.scanRollupRowsBetween(ctx, "late", "node-a", nil, time.Minute.Nanoseconds(), lateBucketNano, lateBucketNano, true)
	if err != nil || len(rows) != 1 || rows[0].bucketData.count != 2 || rows[0].bucketData.lastVal != 999 {
		t.Fatalf("late rollup merge through compressed block: rows=%#v err=%v", rows, err)
	}
	var hotRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollup_values`).Scan(&hotRows); err != nil {
		t.Fatal(err)
	}
	if hotRows != 0 {
		t.Fatalf("late merged rollup remained unsealed: %d", hotRows)
	}
}

func TestSQLiteStorageV4RollupTailFlushPreservesLargeBlocks(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{RawRetention: 15 * time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}}}
	store, err := Open(ctx, SQLiteInDir(t.TempDir(), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateMetric(ctx, Definition{Name: "tail", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	writeRange := func(start, count int) {
		t.Helper()
		buckets := make(map[rollupKey]*rollupBucket, count)
		for index := start; index < start+count; index++ {
			bucketNano := base.Add(time.Duration(index) * time.Minute).UnixNano()
			bucket := newRollupBucket(policy.compression())
			bucket.addPoint(math.Float64frombits(math.Float64bits(100.25)+uint64(index%97)), bucketNano+int64(index%53))
			buckets[rollupKey{entityID: "node-a", bucket: bucketNano}] = bucket
		}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.writeRollupBucketsTx(ctx, "tail", time.Minute, buckets, tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	seal := func(before time.Time) {
		t.Helper()
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.sealSQLiteV4RollupHotTx(ctx, tx, "tail", before.UnixNano()); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	assertBlockLayout := func(wantCounts ...int) {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `SELECT bucket_count FROM metric_rollup_blocks ORDER BY start_nano`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []int
		for rows.Next() {
			var count int
			if err := rows.Scan(&count); err != nil {
				t.Fatal(err)
			}
			got = append(got, count)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(wantCounts) {
			t.Fatalf("rollup block layout = %v, want %v", got, wantCounts)
		}
	}

	writeRange(0, 600)
	if err := store.ReclaimSpace(ctx); err != nil {
		t.Fatal(err)
	}
	assertBlockLayout(512, 88)

	writeRange(600, sqliteV4RollupFlushMinimum(time.Minute.Nanoseconds())-1)
	seal(base.Add(700 * time.Minute))
	var hotRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollup_values`).Scan(&hotRows); err != nil {
		t.Fatal(err)
	}
	if hotRows != 24 {
		t.Fatalf("sub-threshold hot rows = %d, want 24", hotRows)
	}
	assertBlockLayout(512, 88)

	writeRange(624, 1)
	before, err := store.scanRollupRows(ctx, store.db, "tail", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	seal(base.Add(700 * time.Minute))
	after, err := store.scanRollupRows(ctx, store.db, "tail", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assertStoredRollupsExact(t, before, after)
	assertBlockLayout(512, 113)

	writeRange(625, 416)
	before, err = store.scanRollupRows(ctx, store.db, "tail", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	seal(base.Add(1100 * time.Minute))
	after, err = store.scanRollupRows(ctx, store.db, "tail", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assertStoredRollupsExact(t, before, after)
	assertBlockLayout(512, 512, 17)
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_rollup_values`).Scan(&hotRows); err != nil {
		t.Fatal(err)
	}
	if hotRows != 0 {
		t.Fatalf("tail flush left %d hot rows", hotRows)
	}
}

func assertStoredRollupsExact(t *testing.T, before, after []storedRollup) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("rollup count changed: before=%d after=%d", len(before), len(after))
	}
	for index := range before {
		left, right := before[index], after[index]
		leftBucket, rightBucket := left.bucketData, right.bucketData
		if left.entityID != right.entityID || left.bucket != right.bucket ||
			leftBucket.count != rightBucket.count || math.Float64bits(leftBucket.sum) != math.Float64bits(rightBucket.sum) ||
			math.Float64bits(leftBucket.sumSq) != math.Float64bits(rightBucket.sumSq) || math.Float64bits(leftBucket.min) != math.Float64bits(rightBucket.min) ||
			math.Float64bits(leftBucket.max) != math.Float64bits(rightBucket.max) || math.Float64bits(leftBucket.firstVal) != math.Float64bits(rightBucket.firstVal) ||
			leftBucket.firstTS != rightBucket.firstTS || math.Float64bits(leftBucket.lastVal) != math.Float64bits(rightBucket.lastVal) ||
			leftBucket.lastTS != rightBucket.lastTS || leftBucket.tagsHash != rightBucket.tagsHash || leftBucket.tagsJSON != rightBucket.tagsJSON ||
			!bytes.Equal(leftBucket.digest.Encode(), rightBucket.digest.Encode()) {
			t.Fatalf("rollup %d changed during tail flush", index)
		}
	}
}

func TestSQLiteStorageV4ConcurrentReadWriteAndSeal(t *testing.T) {
	ctx := context.Background()
	policy := RollupPolicy{RawRetention: 15 * time.Minute, Tiers: []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}}}
	store, err := Open(ctx, SQLiteInDir(t.TempDir(), WithRollupPolicy(policy), WithSQLiteReadPool(4)))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateMetric(ctx, Definition{Name: "concurrent", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	base := now.Add(-10 * time.Minute)
	initial := make([]Point, 600)
	for i := range initial {
		initial[i] = Point{MetricName: "concurrent", EntityID: "node-a", Timestamp: base.Add(time.Duration(i) * time.Millisecond), Value: float64(i)}
	}
	if err := store.WriteBatch(ctx, initial); err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			points, err := store.Query(ctx, Query{MetricName: "concurrent", EntityID: "node-a", Start: base, End: now.Add(time.Minute)})
			if err != nil {
				errCh <- err
				return
			}
			if len(points) != 600 && len(points) != 700 {
				errCh <- fmt.Errorf("V4 snapshot exposed a partial hot/block transition: %d points", len(points))
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		points := make([]Point, 100)
		for i := range points {
			points[i] = Point{MetricName: "concurrent", EntityID: "node-a", Timestamp: now.Add(time.Duration(i) * time.Millisecond), Value: float64(1000 + i)}
		}
		if err := store.WriteBatch(ctx, points); err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := store.CompactMetric(ctx, "concurrent", now); err != nil {
			errCh <- err
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent V4 operation failed: %v", err)
	}
	got, err := store.Query(ctx, Query{MetricName: "concurrent", EntityID: "node-a", Start: base, End: now.Add(time.Minute)})
	if err != nil || len(got) != 700 {
		t.Fatalf("concurrent V4 point count=%d want=700 err=%v", len(got), err)
	}
}

func createSQLiteV3OnlyStore(t *testing.T, ctx context.Context, dsn string) *Store {
	t.Helper()
	store, err := Open(ctx, SQLite(dsn, WithAutoMigrate(false)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.migrateSQLiteStorageV3(ctx); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return store
}

func mustFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
