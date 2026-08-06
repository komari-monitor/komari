package metric

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRestructureRebuildsLegacyPointsIntoNormalizedSchema(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false), WithRollupPolicy(RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: time.Hour}, {Interval: 5 * time.Minute, Retention: 5 * time.Hour}, {Interval: time.Hour, Retention: 24 * time.Hour}, {Interval: 24 * time.Hour, Retention: 365 * 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	legacy := []string{
		fmt.Sprintf(`CREATE TABLE %s (name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL, retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, ts_nano INTEGER NOT NULL, value REAL NOT NULL, tags TEXT NOT NULL, labels TEXT NOT NULL, created_at INTEGER NOT NULL)`, s.tables.points),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, tags TEXT NOT NULL, resolution_nano INTEGER NOT NULL, bucket_nano INTEGER NOT NULL, count INTEGER NOT NULL, sum REAL NOT NULL, sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL, first_val REAL NOT NULL, first_ts INTEGER NOT NULL, last_val REAL NOT NULL, last_ts INTEGER NOT NULL, digest BLOB, created_at INTEGER NOT NULL)`, s.tables.rollups),
		fmt.Sprintf(`CREATE TABLE %s (metric_name TEXT PRIMARY KEY, watermark_nano INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.watermarks),
	}
	for _, statement := range legacy {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy: %v", err)
		}
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	oldDaily := base.Add(-20 * 24 * time.Hour).Truncate(24 * time.Hour)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.definitions), "cpu", "gauge", "", "", 30, "{}", base.UnixNano(), base.UnixNano()); err != nil {
		t.Fatalf("definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.points), 1, "cpu", "node-a", "hash", base.UnixNano(), 42.0, `{"host":"a"}`, `{"source":"test"}`, base.UnixNano()); err != nil {
		t.Fatalf("point: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollups),
		1, "cpu", "node-a", "hash", `{"host":"a"}`, (24 * time.Hour).Nanoseconds(), oldDaily.UnixNano(),
		1, 7.0, 49.0, 7.0, 7.0, 7.0, oldDaily.UnixNano(), 7.0, oldDaily.UnixNano(), nil, base.UnixNano()); err != nil {
		t.Fatalf("old daily rollup: %v", err)
	}
	needs, err := s.NeedsRestructure(ctx)
	if err != nil || !needs {
		t.Fatalf("needs restructure = %v, %v", needs, err)
	}
	if _, err := s.Restructure(ctx, nil); err != nil {
		t.Fatalf("restructure: %v", err)
	}
	var labelsHash string
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT labels_hash FROM %s LIMIT 1", s.tables.labels)).Scan(&labelsHash); err != nil {
		t.Fatalf("read normalized label hash: %v", err)
	}
	if labelsHash == "" {
		t.Fatal("normalized label hash is empty")
	}
	var rebuildIndexes int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE ?`, s.cfg.TablePrefix+"rebuild_%").Scan(&rebuildIndexes); err != nil {
		t.Fatalf("count rebuild indexes: %v", err)
	}
	if rebuildIndexes != 0 {
		t.Fatalf("rebuild indexes left after table switch: %d", rebuildIndexes)
	}
	var rebuildStateTables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE ?`, s.cfg.TablePrefix+"rebuild_store_state").Scan(&rebuildStateTables); err != nil {
		t.Fatalf("count rebuild state tables: %v", err)
	}
	if rebuildStateTables != 0 {
		t.Fatalf("rebuild store state table left after table switch: %d", rebuildStateTables)
	}
	var stateTables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, s.tables.state).Scan(&stateTables); err != nil {
		t.Fatalf("find normalized store state table: %v", err)
	}
	if stateTables != 1 {
		t.Fatalf("normalized store state table count = %d, want 1", stateTables)
	}
	for _, index := range s.normalizedIndexes() {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index.name).Scan(&count); err != nil {
			t.Fatalf("find normalized index %s: %v", index.name, err)
		}
		if count != 1 {
			t.Fatalf("normalized index %s count = %d, want 1", index.name, count)
		}
	}
	var dailyCount int64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT r.count FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds()).Scan(&dailyCount); err != nil {
		t.Fatalf("read rebuilt daily rollup: %v", err)
	}
	if dailyCount != 1 {
		t.Fatalf("rebuilt daily rollup count = %d, want 1", dailyCount)
	}
	var oldDailyValue float64
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT r.last_val FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ? AND r.bucket_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds(), oldDaily.UnixMilli()).Scan(&oldDailyValue); err != nil {
		t.Fatalf("read preserved old daily rollup: %v", err)
	}
	if oldDailyValue != 7 {
		t.Fatalf("old daily rollup value = %v, want 7", oldDailyValue)
	}
	var dailyRows int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r
		JOIN %s series ON series.id = r.series_id
		JOIN %s resolutions ON resolutions.id = r.resolution_id
		WHERE series.metric_name = ? AND resolutions.resolution_milli = ?`,
		s.tables.rollups, s.tables.series, s.tables.resolutions), "cpu", (24 * time.Hour).Milliseconds()).Scan(&dailyRows); err != nil {
		t.Fatalf("count rebuilt daily rollups: %v", err)
	}
	if dailyRows != 1 {
		t.Fatalf("rebuilt daily rollup rows = %d, want preserved sealed legacy day", dailyRows)
	}
	var rawTables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, s.tables.points).Scan(&rawTables); err != nil {
		t.Fatalf("inspect obsolete raw table: %v", err)
	}
	if rawTables != 0 {
		t.Fatalf("metric_points table count = %d, want 0", rawTables)
	}
	points, err := s.Query(ctx, Query{MetricName: "cpu", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("restarted raw window should be empty, got %#v", points)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "cpu", EntityID: "node-a", Start: base.Add(-time.Minute), End: base.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("query rebuilt rollup: %v", err)
	}
	if len(series) != 1 || series[0].Value != 42 || series[0].Tags["host"] != "a" {
		t.Fatalf("rebuilt rollup = %#v", series)
	}
}

func TestDiscardHistoryPreservesDefinitionsAndCreatesEmptyNormalizedSchema(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false), WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for _, statement := range []string{
		fmt.Sprintf(`CREATE TABLE %s (name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL, retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, ts_nano INTEGER NOT NULL, value REAL NOT NULL, tags TEXT NOT NULL, labels TEXT NOT NULL, created_at INTEGER NOT NULL)`, s.tables.points),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, tags TEXT NOT NULL, resolution_nano INTEGER NOT NULL, bucket_nano INTEGER NOT NULL, count INTEGER NOT NULL, sum REAL NOT NULL, sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL, first_val REAL NOT NULL, first_ts INTEGER NOT NULL, last_val REAL NOT NULL, last_ts INTEGER NOT NULL, digest BLOB, created_at INTEGER NOT NULL)`, s.tables.rollups),
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy schema: %v", err)
		}
	}
	at := time.Now().UTC().Add(-time.Hour)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.definitions), "cpu", "gauge", "%", "CPU", 30, "{}", at.UnixNano(), at.UnixNano()); err != nil {
		t.Fatalf("insert definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.points), 1, "cpu", "node-a", "hash", at.UnixNano(), 42.0, "{}", "{}", at.UnixNano()); err != nil {
		t.Fatalf("insert point: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollups),
		1, "cpu", "node-a", "hash", "{}", time.Hour.Nanoseconds(), at.Truncate(time.Hour).UnixNano(),
		1, 42.0, 1764.0, 42.0, 42.0, 42.0, at.UnixNano(), 42.0, at.UnixNano(), nil, at.UnixNano()); err != nil {
		t.Fatalf("insert rollup: %v", err)
	}

	result, err := s.DiscardHistory(ctx, nil)
	if err != nil {
		t.Fatalf("discard history: %v", err)
	}
	if result.RowsCopied != 2 || result.Metrics != 1 {
		t.Fatalf("discard result = %#v", result)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("needs restructure after discard = %v, %v", needs, err)
	}
	definition, err := s.GetMetric(ctx, "cpu")
	if err != nil {
		t.Fatalf("get preserved definition: %v", err)
	}
	if definition.RetentionDays != 30 || definition.Description != "CPU" {
		t.Fatalf("preserved definition = %#v", definition)
	}
	for _, table := range []string{s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.rollups} {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", table, count)
		}
	}
	if exists, err := s.tableExists(ctx, s.tables.points); err != nil || exists {
		t.Fatalf("obsolete point table exists = %v, err = %v", exists, err)
	}
}

func TestNeedsRestructureRejectsPartialNormalizedSchema(t *testing.T) {
	t.Run("missing dictionary table", func(t *testing.T) {
		ctx := context.Background()
		s := newMemStore(t)
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.labels); err != nil {
			t.Fatalf("drop labels table: %v", err)
		}
		needs, err := s.NeedsRestructure(ctx)
		if err != nil || !needs {
			t.Fatalf("NeedsRestructure() = %v, %v, want true", needs, err)
		}
	})

	t.Run("legacy rollup columns", func(t *testing.T) {
		ctx := context.Background()
		s := newMemStore(t)
		if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.rollups); err != nil {
			t.Fatalf("drop rollup table: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s (resolution_nano BIGINT NOT NULL)", s.tables.rollups)); err != nil {
			t.Fatalf("create partial rollup table: %v", err)
		}
		needs, err := s.NeedsRestructure(ctx)
		if err != nil || !needs {
			t.Fatalf("NeedsRestructure() = %v, %v, want true", needs, err)
		}
	})
}

func TestRestructureMigratesPartialMillisecondSchemaWithoutPoints(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "partial.millis", Type: TypeGauge, Unit: "%", Description: "preserved", RetentionDays: 30}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.rollups); err != nil {
		t.Fatalf("drop normalized rollups: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL,
		tags_hash TEXT NOT NULL, tags TEXT NOT NULL, resolution_nano INTEGER NOT NULL,
		bucket_nano INTEGER NOT NULL, count INTEGER NOT NULL, sum REAL NOT NULL,
		sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL,
		first_val REAL NOT NULL, first_ts INTEGER NOT NULL, last_val REAL NOT NULL,
		last_ts INTEGER NOT NULL, digest BLOB, created_at INTEGER NOT NULL
	)`, s.tables.rollups)); err != nil {
		t.Fatalf("create legacy rollups: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollups),
		1, definition.Name, "node-a", "hash", `{"host":"a"}`, time.Minute.Nanoseconds(), at.UnixNano(),
		1, 42.0, 1764.0, 42.0, 42.0, 42.0, at.UnixNano(), 42.0, at.UnixNano(), nil, at.UnixNano()); err != nil {
		t.Fatalf("seed legacy rollup: %v", err)
	}
	if exists, err := s.tableExists(ctx, s.tables.points); err != nil || exists {
		t.Fatalf("points table before restructure = %v, err = %v", exists, err)
	}
	seedIncompleteRebuildRollups(t, ctx, s)

	result, err := s.Restructure(ctx, nil)
	if err != nil {
		t.Fatalf("restructure partial millisecond schema: %v", err)
	}
	if result.RowsCopied != 1 || result.Metrics != 1 {
		t.Fatalf("restructure result = %#v", result)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("needs restructure after repair = %v, %v", needs, err)
	}
	preserved, err := s.GetMetric(ctx, definition.Name)
	if err != nil {
		t.Fatalf("get preserved definition: %v", err)
	}
	if preserved.Description != definition.Description || preserved.RetentionDays != definition.RetentionDays {
		t.Fatalf("preserved definition = %#v", preserved)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: definition.Name, EntityID: "node-a", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("query migrated rollup: %v", err)
	}
	if len(series) != 1 || series[0].Count != 1 || series[0].Value != 42 || series[0].Tags["host"] != "a" {
		t.Fatalf("migrated rollup = %#v", series)
	}
}

func TestRestructureRebuildsNormalizedSchemaWithoutForeignKeys(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	for _, statement := range []string{
		fmt.Sprintf(`CREATE TABLE %s (
			name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL,
			retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at_milli INTEGER NOT NULL, updated_at_milli INTEGER NOT NULL
		)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY AUTOINCREMENT, labels_hash TEXT NOT NULL UNIQUE, labels TEXT NOT NULL)`, s.tables.labels),
		fmt.Sprintf(`CREATE TABLE %s (
			id INTEGER PRIMARY KEY AUTOINCREMENT, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL,
			tags_hash TEXT NOT NULL, tags TEXT NOT NULL, UNIQUE(metric_name, entity_id, tags_hash)
		)`, s.tables.series),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY AUTOINCREMENT, resolution_milli INTEGER NOT NULL UNIQUE)`, s.tables.resolutions),
		fmt.Sprintf(`CREATE TABLE %s (
			series_id INTEGER NOT NULL, resolution_id INTEGER NOT NULL, label_id INTEGER NOT NULL, bucket_milli INTEGER NOT NULL,
			count INTEGER NOT NULL, sum REAL NOT NULL, sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL,
			first_val REAL NOT NULL, first_ts_milli INTEGER NOT NULL, last_val REAL NOT NULL, last_ts_milli INTEGER NOT NULL,
			digest BLOB, created_at_milli INTEGER NOT NULL, UNIQUE(series_id, resolution_id, label_id, bucket_milli)
		)`, s.tables.rollups),
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create normalized table without foreign keys: %v", err)
		}
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.definitions+" VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		"normalized.no-fk", TypeGauge, "%", "preserved", 30, "{}", at.UnixMilli(), at.UnixMilli()); err != nil {
		t.Fatalf("seed definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.labels+" (labels_hash, labels) VALUES (?, ?)", "labels", "{}"); err != nil {
		t.Fatalf("seed labels: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.series+" (metric_name, entity_id, tags_hash, tags) VALUES (?, ?, ?, ?)", "normalized.no-fk", "node-a", "tags", `{"host":"a"}`); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.resolutions+" (resolution_milli) VALUES (?)", time.Minute.Milliseconds()); err != nil {
		t.Fatalf("seed resolution: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.rollups+" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		1, 1, 1, at.UnixMilli(), 1, 7.0, 49.0, 7.0, 7.0, 7.0, at.UnixMilli(), 7.0, at.UnixMilli(), nil, at.UnixMilli()); err != nil {
		t.Fatalf("seed rollup: %v", err)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || !needs {
		t.Fatalf("schema without foreign keys needs restructure = %v, %v", needs, err)
	}
	seedIncompleteRebuildRollups(t, ctx, s)

	result, err := s.Restructure(ctx, nil)
	if err != nil {
		t.Fatalf("rebuild normalized relationships: %v", err)
	}
	if result.RowsCopied != 1 || result.Metrics != 1 {
		t.Fatalf("rebuild result = %#v", result)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("schema still needs restructure = %v, %v", needs, err)
	}
	if foreignKeys, err := s.normalizedForeignKeysExist(ctx); err != nil || !foreignKeys {
		t.Fatalf("normalized foreign keys after rebuild = %v, %v", foreignKeys, err)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "normalized.no-fk", EntityID: "node-a", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("query rebuilt rollup: %v", err)
	}
	if len(series) != 1 || series[0].Value != 7 || series[0].Tags["host"] != "a" {
		t.Fatalf("rebuilt rollup = %#v", series)
	}
}

func TestNeedsRestructureAllowsDictionaryIDGaps(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "id.gaps", Type: TypeGauge, RetentionDays: 7}); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := s.Write(ctx, Point{MetricName: "id.gaps", EntityID: "removed", Timestamp: at, Value: 1}); err != nil {
		t.Fatalf("write removed series: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush removed series: %v", err)
	}
	if _, err := s.DeleteSeries(ctx, Query{MetricName: "id.gaps", EntityID: "removed"}); err != nil {
		t.Fatalf("delete series to create id gaps: %v", err)
	}
	if _, err := s.cleanupOrphanedMetricData(ctx); err != nil {
		t.Fatalf("delete orphaned dictionary rows: %v", err)
	}
	if err := s.Write(ctx, Point{MetricName: "id.gaps", EntityID: "retained", Timestamp: at.Add(time.Minute), Value: 2}); err != nil {
		t.Fatalf("write retained series: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush retained series: %v", err)
	}

	for _, table := range []string{s.tables.series, s.tables.labels, s.tables.resolutions} {
		var id int64
		if err := s.db.QueryRowContext(ctx, "SELECT id FROM "+table+" LIMIT 1").Scan(&id); err != nil {
			t.Fatalf("read dictionary id from %s: %v", table, err)
		}
		if id <= 1 {
			t.Fatalf("dictionary id in %s = %d, want a normal auto-increment gap", table, id)
		}
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("dictionary id gap needs restructure = %v, %v", needs, err)
	}
	if foreignKeys, err := s.normalizedForeignKeysExist(ctx); err != nil || !foreignKeys {
		t.Fatalf("foreign keys with id gap = %v, %v", foreignKeys, err)
	}
}

func TestRestructureRejectsUnrecognizedPartialSchemaBeforeMutation(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "partial.unknown", Type: TypeGauge, Description: "preserved", RetentionDays: 7}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.rollups); err != nil {
		t.Fatalf("drop normalized rollups: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s (resolution_nano BIGINT NOT NULL)", s.tables.rollups)); err != nil {
		t.Fatalf("create unrecognized rollups: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.rollups+" VALUES (?)", time.Minute.Nanoseconds()); err != nil {
		t.Fatalf("seed unrecognized rollups: %v", err)
	}

	_, err := s.Restructure(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "not a recognized legacy layout") {
		t.Fatalf("restructure error = %v", err)
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tables.rollups).Scan(&rows); err != nil {
		t.Fatalf("count source rollups after rejected restructure: %v", err)
	}
	if rows != 1 {
		t.Fatalf("source rollups after rejected restructure = %d, want 1", rows)
	}
	if _, err := s.GetMetric(ctx, definition.Name); err != nil {
		t.Fatalf("definition changed by rejected restructure: %v", err)
	}
	if exists, err := s.tableExists(ctx, s.cfg.TablePrefix+"rebuild_definitions"); err != nil || exists {
		t.Fatalf("rebuild schema exists after preflight rejection = %v, err = %v", exists, err)
	}
}

func TestDiscardHistoryRepairsPartialMillisecondSchemaWithoutPoints(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "partial.discard", Type: TypeGauge, Description: "preserved", RetentionDays: 14}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE "+s.tables.rollups); err != nil {
		t.Fatalf("drop normalized rollups: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s (resolution_nano BIGINT NOT NULL)", s.tables.rollups)); err != nil {
		t.Fatalf("create partial rollups: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO "+s.tables.rollups+" VALUES (?)", time.Minute.Nanoseconds()); err != nil {
		t.Fatalf("seed partial rollups: %v", err)
	}
	seedIncompleteRebuildRollups(t, ctx, s)

	result, err := s.DiscardHistory(ctx, nil)
	if err != nil {
		t.Fatalf("discard partial millisecond history: %v", err)
	}
	if result.RowsCopied != 1 || result.Metrics != 1 {
		t.Fatalf("discard result = %#v", result)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("needs restructure after discard repair = %v, %v", needs, err)
	}
	preserved, err := s.GetMetric(ctx, definition.Name)
	if err != nil {
		t.Fatalf("get preserved definition: %v", err)
	}
	if preserved.Description != definition.Description || preserved.RetentionDays != definition.RetentionDays {
		t.Fatalf("preserved definition = %#v", preserved)
	}
	assertNormalizedHistoryEmpty(t, ctx, s)
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Write(ctx, Point{MetricName: definition.Name, EntityID: "node-a", Timestamp: now, Value: 9}); err != nil {
		t.Fatalf("write after discard repair: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush after discard repair: %v", err)
	}
	var rollups int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tables.rollups).Scan(&rollups); err != nil {
		t.Fatalf("count rollups after repair write: %v", err)
	}
	if rollups == 0 {
		t.Fatal("write after repair did not persist any rollups")
	}
}

func TestDiscardHistoryClearsAlreadyNormalizedStore(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "normalized.clear", Type: TypeGauge, Description: "preserved", RetentionDays: 30}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: definition.Name, EntityID: "node-a", Timestamp: now.Add(-20 * time.Second), Value: 1},
		{MetricName: definition.Name, EntityID: "node-a", Timestamp: now.Add(-10 * time.Second), Value: 2},
	}); err != nil {
		t.Fatalf("write persisted history: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush history: %v", err)
	}
	if err := s.Write(ctx, Point{MetricName: definition.Name, EntityID: "node-a", Timestamp: now, Value: 3}); err != nil {
		t.Fatalf("write active history: %v", err)
	}
	if needs, err := s.NeedsRestructure(ctx); err != nil || needs {
		t.Fatalf("normalized store restructure state = %v, %v", needs, err)
	}

	result, err := s.DiscardHistory(ctx, nil)
	if err != nil {
		t.Fatalf("discard normalized history: %v", err)
	}
	if result.RowsCopied == 0 || result.Metrics != 1 {
		t.Fatalf("discard result = %#v", result)
	}
	assertNormalizedHistoryEmpty(t, ctx, s)
	points, err := s.Query(ctx, Query{MetricName: definition.Name, EntityID: "node-a", Start: now.Add(-time.Minute), End: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("query cleared raw history: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("raw history remains after discard: %#v", points)
	}
	preserved, err := s.GetMetric(ctx, definition.Name)
	if err != nil {
		t.Fatalf("get preserved definition: %v", err)
	}
	if preserved.Description != definition.Description || preserved.RetentionDays != definition.RetentionDays {
		t.Fatalf("preserved definition = %#v", preserved)
	}
}

func TestDiscardHistoryDropsObsoletePointsBeforeDictionaries(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "normalized.obsolete", Type: TypeGauge, RetentionDays: 7}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.Write(ctx, Point{MetricName: definition.Name, EntityID: "node-a", Timestamp: now, Value: 42}); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush history: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		series_id BIGINT NOT NULL, label_id BIGINT NOT NULL, ts_milli BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
		FOREIGN KEY (series_id) REFERENCES %s(id) ON DELETE CASCADE,
		FOREIGN KEY (label_id) REFERENCES %s(id) ON DELETE CASCADE
	)`, s.tables.points, s.tables.series, s.tables.labels)); err != nil {
		t.Fatalf("create obsolete point table: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (series_id, label_id, ts_milli, value)
		SELECT series_id, label_id, first_ts_milli, first_val FROM %s LIMIT 1`, s.tables.points, s.tables.rollups)); err != nil {
		t.Fatalf("seed obsolete point: %v", err)
	}

	if _, err := s.DiscardHistory(ctx, nil); err != nil {
		t.Fatalf("discard history with obsolete point references: %v", err)
	}
	if exists, err := s.tableExists(ctx, s.tables.points); err != nil || exists {
		t.Fatalf("obsolete point table exists = %v, err = %v", exists, err)
	}
	assertNormalizedHistoryEmpty(t, ctx, s)
}

func TestDiscardHistoryRollsBackObsoletePointsWhenDeleteFails(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	definition := Definition{Name: "normalized.rollback", Type: TypeGauge, RetentionDays: 7}
	if err := s.CreateMetric(ctx, definition); err != nil {
		t.Fatalf("create definition: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := s.Write(ctx, Point{MetricName: definition.Name, EntityID: "node-a", Timestamp: now, Value: 42}); err != nil {
		t.Fatalf("write history: %v", err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatalf("flush history: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		series_id BIGINT NOT NULL, label_id BIGINT NOT NULL, ts_milli BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
		FOREIGN KEY (series_id) REFERENCES %s(id) ON DELETE CASCADE,
		FOREIGN KEY (label_id) REFERENCES %s(id) ON DELETE CASCADE
	)`, s.tables.points, s.tables.series, s.tables.labels)); err != nil {
		t.Fatalf("create obsolete points: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (series_id, label_id, ts_milli, value)
		SELECT series_id, label_id, first_ts_milli, first_val FROM %s LIMIT 1`, s.tables.points, s.tables.rollups)); err != nil {
		t.Fatalf("seed obsolete point: %v", err)
	}
	before := make(map[string]int)
	for _, table := range []string{s.tables.points, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions} {
		var rows int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil {
			t.Fatalf("count %s before rollback test: %v", table, err)
		}
		before[table] = rows
	}
	trigger := s.cfg.TablePrefix + "reject_rollup_delete"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON %s
		BEGIN SELECT RAISE(ABORT, 'forced rollup delete failure'); END`, trigger, s.tables.rollups)); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if _, err := s.DiscardHistory(ctx, nil); err == nil || !strings.Contains(err.Error(), "forced rollup delete failure") {
		t.Fatalf("discard error = %v", err)
	}
	for _, table := range []string{s.tables.points, s.tables.rollups, s.tables.series, s.tables.labels, s.tables.resolutions} {
		var rows int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&rows); err != nil {
			t.Fatalf("count %s after rollback: %v", table, err)
		}
		if rows != before[table] {
			t.Fatalf("%s rows after rollback = %d, want %d", table, rows, before[table])
		}
	}
}

func TestDiscardHistoryValidatesLegacyRebuildPrefix(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL,
		retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`, s.tables.definitions)); err != nil {
		t.Fatalf("create legacy definition table: %v", err)
	}
	s.cfg.Driver = DriverMySQL
	s.cfg.TablePrefix = strings.Repeat("x", 28)
	_, err = s.DiscardHistory(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "prefix is too long for MySQL rebuild identifiers") {
		t.Fatalf("discard prefix validation error = %v", err)
	}
}

func assertNormalizedHistoryEmpty(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	for _, table := range []string{s.tables.series, s.tables.labels, s.tables.resolutions, s.tables.rollups} {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d, want 0", table, count)
		}
	}
}

func seedIncompleteRebuildRollups(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	table := s.cfg.TablePrefix + "rebuild_rollups"
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s (bucket_milli BIGINT NOT NULL)", table)); err != nil {
		t.Fatalf("create interrupted rebuild rollups: %v", err)
	}
}

func TestRestructureBulkCopiesRollupsAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, SQLite(":memory:", WithAutoMigrate(false), WithRollupPolicy(RollupPolicy{
		RawRetention: time.Minute,
		Tiers:        []RollupTier{{Interval: time.Minute, Retention: 30 * 24 * time.Hour}},
		Compression:  30,
	})))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	legacy := []string{
		fmt.Sprintf(`CREATE TABLE %s (name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL, description TEXT NOT NULL, retention_days INTEGER NOT NULL, metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.definitions),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, ts_nano INTEGER NOT NULL, value REAL NOT NULL, tags TEXT NOT NULL, labels TEXT NOT NULL, created_at INTEGER NOT NULL)`, s.tables.points),
		fmt.Sprintf(`CREATE TABLE %s (id INTEGER PRIMARY KEY, metric_name TEXT NOT NULL, entity_id TEXT NOT NULL, tags_hash TEXT NOT NULL, tags TEXT NOT NULL, resolution_nano INTEGER NOT NULL, bucket_nano INTEGER NOT NULL, count INTEGER NOT NULL, sum REAL NOT NULL, sum_sq REAL NOT NULL, min_val REAL NOT NULL, max_val REAL NOT NULL, first_val REAL NOT NULL, first_ts INTEGER NOT NULL, last_val REAL NOT NULL, last_ts INTEGER NOT NULL, digest BLOB, created_at INTEGER NOT NULL)`, s.tables.rollups),
		fmt.Sprintf(`CREATE TABLE %s (metric_name TEXT PRIMARY KEY, watermark_nano INTEGER NOT NULL, updated_at INTEGER NOT NULL)`, s.tables.watermarks),
	}
	for _, statement := range legacy {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create legacy: %v", err)
		}
	}
	const rowCount = restructureRollupReadBatchSize + 1
	base := time.Now().UTC().Truncate(time.Minute).Add(-rowCount * time.Minute)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.definitions), "cpu", "gauge", "%", "", 30, "{}", base.UnixNano(), base.UnixNano()); err != nil {
		t.Fatalf("definition: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin seed transaction: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, s.tables.rollups))
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare seed: %v", err)
	}
	for i := 0; i < rowCount; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		value := float64(i % 100)
		if _, err := stmt.ExecContext(ctx,
			i+1, "cpu", "node-a", "hash", `{"host":"a"}`, time.Minute.Nanoseconds(), at.UnixNano(),
			1, value, value*value, value, value, value, at.UnixNano(), value, at.UnixNano(), nil, at.UnixNano(),
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			t.Fatalf("seed rollup %d: %v", i, err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close seed statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	result, err := s.Restructure(ctx, nil)
	if err != nil {
		t.Fatalf("restructure: %v", err)
	}
	if result.RowsCopied != rowCount {
		t.Fatalf("rows copied = %d, want %d", result.RowsCopied, rowCount)
	}
	var rollups int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s r JOIN %s d ON d.id = r.resolution_id WHERE d.resolution_milli = ?`, s.tables.rollups, s.tables.resolutions), time.Minute.Milliseconds()).Scan(&rollups); err != nil {
		t.Fatalf("count rollups: %v", err)
	}
	if rollups != rowCount {
		t.Fatalf("rollups = %d, want %d", rollups, rowCount)
	}
	for table, want := range map[string]int{s.tables.series: 1, s.tables.labels: 1, s.tables.resolutions: 1} {
		var got int
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
	var digestRows int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE digest IS NOT NULL", s.tables.rollups)).Scan(&digestRows); err != nil {
		t.Fatalf("count digests: %v", err)
	}
	if digestRows != 0 {
		t.Fatalf("constant rollup digests = %d, want 0", digestRows)
	}
}

func TestRestructureDropsObsoleteRawTableFromMillisecondSchema(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "normalized", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "normalized", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "normalized", EntityID: "n1", Timestamp: at.Add(10 * time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (
		series_id BIGINT NOT NULL, label_id BIGINT NOT NULL,
		ts_milli BIGINT NOT NULL, value DOUBLE PRECISION NOT NULL,
		PRIMARY KEY(series_id, ts_milli))`, s.tables.points)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (series_id, label_id, ts_milli, value)
		SELECT r.series_id, r.label_id, r.first_ts_milli, r.first_val FROM %s r LIMIT 1`, s.tables.points, s.tables.rollups)); err != nil {
		t.Fatal(err)
	}
	needs, err := s.NeedsRestructure(ctx)
	if err != nil || !needs {
		t.Fatalf("NeedsRestructure() = %v, %v", needs, err)
	}
	result, err := s.Restructure(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics != 1 || result.RowsCopied != 0 {
		t.Fatalf("result = %#v", result)
	}
	if exists, err := s.tableExists(ctx, s.tables.points); err != nil || exists {
		t.Fatalf("obsolete raw table exists = %v, err = %v", exists, err)
	}
	series, err := s.Series(ctx, AggregateQuery{
		Query:       Query{MetricName: "normalized", EntityID: "n1", Start: at.Add(-time.Minute), End: at.Add(time.Minute)},
		Aggregation: AggAvg, Interval: time.Minute, PreserveSeries: true,
	}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || series[0].Count != 2 || series[0].Value != 1.5 {
		t.Fatalf("rollup changed while removing raw table: %#v", series)
	}
}

func TestValidateNormalizedRestructureRejectsMissingDigest(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "invalid", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "invalid", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "invalid", EntityID: "n1", Timestamp: at.Add(time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = NULL WHERE min_val <> max_val", s.tables.rollups)); err != nil {
		t.Fatal(err)
	}
	err := s.validateNormalizedRestructure(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "invalid aggregate rows") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestValidateNormalizedRestructureRejectsWrongDigestCompression(t *testing.T) {
	ctx := context.Background()
	s := newMemStore(t)
	if err := s.CreateMetric(ctx, Definition{Name: "invalid-compression", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := s.WriteBatch(ctx, []Point{
		{MetricName: "invalid-compression", EntityID: "n1", Timestamp: at, Value: 1},
		{MetricName: "invalid-compression", EntityID: "n1", Timestamp: at.Add(time.Second), Value: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.flushAllHotRollups(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := NewTDigest(100)
	legacy.Add(1, 1)
	legacy.Add(2, 1)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = ? WHERE min_val <> max_val", s.tables.rollups), legacy.Encode()); err != nil {
		t.Fatal(err)
	}
	err := s.validateNormalizedRestructure(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "compression = 100") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestPostgreSQLMissingSchemaErrorsAreDistinguished(t *testing.T) {
	missingTable := errors.New(`ERROR: relation "metric_definitions" does not exist (SQLSTATE 42P01)`)
	if !isMissingTableError(missingTable) || isMissingColumnError(missingTable) {
		t.Fatalf("missing PostgreSQL relation classified incorrectly")
	}
	missingColumn := errors.New(`ERROR: column "created_at_milli" does not exist (SQLSTATE 42703)`)
	if !isMissingColumnError(missingColumn) || isMissingTableError(missingColumn) {
		t.Fatalf("missing PostgreSQL column classified incorrectly")
	}
}

// TestRestructureDump is opt-in because it rewrites a complete legacy SQLite
// fixture in place. It provides a repeatable full-data migration and size check
// for an externally supplied MariaDB-to-SQLite conversion.
func TestRestructureDump(t *testing.T) {
	path := os.Getenv("KOMARI_METRIC_RESTRUCTURE_DUMP")
	if path == "" {
		t.Skip("set KOMARI_METRIC_RESTRUCTURE_DUMP to run the full-dump migration")
	}
	ctx := context.Background()
	policy := RollupPolicy{
		RawRetention: 15 * time.Minute,
		Tiers: []RollupTier{
			{Interval: time.Minute, Retention: 600 * time.Minute},
			{Interval: 5 * time.Minute, Retention: 600 * 5 * time.Minute},
			{Interval: time.Hour, Retention: 600 * time.Hour},
			{Interval: 24 * time.Hour, Retention: 100 * 365 * 24 * time.Hour},
		},
		Compression: 30,
	}
	s, err := Open(ctx, SQLite(path, WithAutoMigrate(false), WithMaxOpenConns(1), WithRollupPolicy(policy)))
	if err != nil {
		t.Fatalf("open converted dump: %v", err)
	}
	defer s.Close()
	before, err := s.StorageSize(ctx)
	if err != nil {
		t.Fatalf("measure source storage: %v", err)
	}
	result, err := s.Restructure(ctx, func(progress RestructureProgress) {
		if progress.RowsDone > 0 && progress.RowsDone%100_000 == 0 {
			t.Logf("%s: %d/%d", progress.Phase, progress.RowsDone, progress.RowsTotal)
		}
	})
	if err != nil {
		t.Fatalf("restructure converted dump: %v", err)
	}
	if err := s.ReclaimSpace(ctx); err != nil {
		t.Fatalf("reclaim rebuilt store: %v", err)
	}
	after, err := s.StorageSize(ctx)
	if err != nil {
		t.Fatalf("measure rebuilt storage: %v", err)
	}
	saved := before - after
	if saved < 0 {
		saved = 0
	}
	percent := 0.0
	if before > 0 {
		percent = float64(saved) / float64(before) * 100
	}
	t.Logf("restructured rows=%d metrics=%d before=%d after=%d saved=%d percent=%.2f", result.RowsCopied, result.Metrics, before, after, saved, percent)
}
