package metric

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStorageSizeAndReclaimSpace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, SQLiteInDir(dir, WithSQLiteWALAutoCheckpoint(1_000_000)))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if got := store.Driver(); got != DriverSQLite {
		t.Fatalf("Driver() = %q, want %q", got, DriverSQLite)
	}
	if got := store.MaintenanceAction(); got != MaintenanceVacuum {
		t.Fatalf("MaintenanceAction() = %q, want %q", got, MaintenanceVacuum)
	}

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE reclaim_fixture (payload BLOB NOT NULL)`); err != nil {
		t.Fatalf("create reclaim fixture: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO reclaim_fixture (payload) VALUES (zeroblob(4194304))`); err != nil {
		t.Fatalf("populate reclaim fixture: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE reclaim_fixture`); err != nil {
		t.Fatalf("drop reclaim fixture: %v", err)
	}

	before, err := store.StorageSize(ctx)
	if err != nil {
		t.Fatalf("storage size before reclaim: %v", err)
	}
	path := filepath.Join(dir, "metrics.db")
	if want := sqliteFileSetSize(t, path); before != want {
		t.Fatalf("StorageSize() = %d, file sum = %d", before, want)
	}

	if err := store.ReclaimSpace(ctx); err != nil {
		t.Fatalf("reclaim sqlite space: %v", err)
	}
	after, err := store.StorageSize(ctx)
	if err != nil {
		t.Fatalf("storage size after reclaim: %v", err)
	}
	if want := sqliteFileSetSize(t, path); after != want {
		t.Fatalf("StorageSize() after reclaim = %d, file sum = %d", after, want)
	}
	if after >= before {
		t.Fatalf("reclaim did not reduce physical storage: before=%d after=%d", before, after)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("store unusable after reclaim: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if _, err := store.StorageSize(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("StorageSize() after Close error = %v, want ErrClosed", err)
	}
	if err := store.ReclaimSpace(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReclaimSpace() after Close error = %v, want ErrClosed", err)
	}
}

func TestCleanupOrphanedMetricData(t *testing.T) {
	ctx := context.Background()
	store := newMemStore(t)
	if err := store.CreateMetric(ctx, Definition{Name: "known", Type: TypeGauge, RetentionDays: 1}); err != nil {
		t.Fatalf("create known definition: %v", err)
	}
	// Simulate a pre-constraint store containing orphaned rows. New stores
	// reject this state through their database foreign keys.
	if _, err := store.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys for legacy fixture: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (metric_name, entity_id, tags_hash, tags) VALUES (?, ?, ?, ?)`, store.tables.series), "orphan", "node-1", "hash", "{}"); err != nil {
		t.Fatalf("seed orphan series: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (labels_hash, labels) VALUES (?, ?)`, store.tables.labels), emptyLabelsHash, "{}"); err != nil {
		t.Fatalf("seed orphan labels: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (resolution_milli) VALUES (?)`, store.tables.resolutions), time.Minute.Milliseconds()); err != nil {
		t.Fatalf("seed orphan resolution: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)
		 SELECT s.id, r.id, l.id, ?, 1, 1, 1, 1, 1, 1, ?, 1, ?, NULL, ? FROM %s s, %s r, %s l WHERE s.metric_name = ?`,
		store.tables.rollups, store.tables.series, store.tables.resolutions, store.tables.labels,
	), now, now, now, now, "orphan"); err != nil {
		t.Fatalf("seed orphan rollup: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("restore foreign keys after legacy fixture: %v", err)
	}

	deleted, err := store.cleanupOrphanedMetricData(ctx)
	if err != nil {
		t.Fatalf("clean orphaned metric data: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("deleted rows = %d, want 4", deleted)
	}
	for _, table := range []string{store.tables.series, store.tables.labels, store.tables.resolutions, store.tables.rollups} {
		var count int
		if err := store.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
			t.Fatalf("count orphan rows in %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("orphan rows remain in %s: %d", table, count)
		}
	}
}

func TestSQLiteReclaimSpaceReencodesLegacyDigestsOnce(t *testing.T) {
	ctx := context.Background()
	store := newMemStore(t)
	if err := store.CreateMetric(ctx, Definition{Name: "latency", Type: TypeGauge, RetentionDays: 30}); err != nil {
		t.Fatalf("create metric: %v", err)
	}
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	digest := NewTDigest(30)
	for i := 0; i < 1000; i++ {
		digest.Add(float64(i%73), 1)
	}
	rollup := PersistedRollup{
		MetricName: "latency", EntityID: "node-a", Resolution: time.Minute, Bucket: base,
		Count: 1000, Sum: 0, SumSq: 0, Min: 0, Max: 72,
		FirstValue: 0, FirstTime: base, LastValue: 50, LastTime: base.Add(59 * time.Second),
		Digest: digest.Encode(), CreatedAt: base.Add(time.Minute),
	}
	for i := 0; i < 1000; i++ {
		rollup.Sum += float64(i % 73)
		rollup.SumSq += float64(i%73) * float64(i%73)
	}
	if err := store.ImportRollups(ctx, []PersistedRollup{rollup}); err != nil {
		t.Fatalf("import rollup: %v", err)
	}
	var rowID int64
	if err := store.db.QueryRowContext(ctx, fmt.Sprintf("SELECT rowid FROM %s", store.tables.rollups)).Scan(&rowID); err != nil {
		t.Fatalf("find rollup rowid: %v", err)
	}
	legacy := digest.Encode()
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = ? WHERE rowid = ?", store.tables.rollups), legacy, rowID); err != nil {
		t.Fatalf("seed legacy digest: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := store.ReclaimSpace(canceledCtx); err != nil {
		t.Fatalf("reclaim with canceled context: %v", err)
	}
	var converted []byte
	if err := store.db.QueryRowContext(ctx, fmt.Sprintf("SELECT digest FROM %s WHERE rowid = ?", store.tables.rollups), rowID).Scan(&converted); err != nil {
		t.Fatalf("read converted digest: %v", err)
	}
	if isLegacyRawTDigest(converted) {
		t.Fatalf("legacy digest was not re-encoded: %q", converted[:2])
	}
	convertedDigest, err := DecodeTDigest(converted)
	if err != nil || math.Abs(convertedDigest.Quantile(0.95)-digest.Quantile(0.95)) > 1e-9 {
		t.Fatalf("converted digest changed percentile semantics: %v", err)
	}
	var phase string
	if err := store.db.QueryRowContext(ctx, fmt.Sprintf("SELECT phase FROM %s WHERE state_key = ?", store.tables.state), digestReencodeStateKey).Scan(&phase); err != nil {
		t.Fatalf("read re-encode state: %v", err)
	}
	if phase != digestReencodeComplete {
		t.Fatalf("re-encode state = %q, want %q", phase, digestReencodeComplete)
	}
	// Completion makes future reclamations skip the historical scan. This row
	// can only be produced by a legacy/manual import path in practice.
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET digest = ? WHERE rowid = ?", store.tables.rollups), legacy, rowID); err != nil {
		t.Fatalf("restore test legacy digest: %v", err)
	}
	if err := store.ReclaimSpace(ctx); err != nil {
		t.Fatalf("second reclaim: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, fmt.Sprintf("SELECT digest FROM %s WHERE rowid = ?", store.tables.rollups), rowID).Scan(&converted); err != nil {
		t.Fatalf("read retained legacy digest: %v", err)
	}
	if !isLegacyRawTDigest(converted) {
		t.Fatal("completed migration unexpectedly scanned legacy rows again")
	}
}

func TestMaintenanceMappings(t *testing.T) {
	tables := tables{
		definitions: "Metric_definitions",
		points:      "Metric_points",
		series:      "Metric_series",
		labels:      "Metric_label_sets",
		resolutions: "Metric_resolutions",
		rollups:     "Metric_rollups",
	}

	tests := []struct {
		name       string
		driver     Driver
		action     MaintenanceAction
		reclaim    string
		sizeParts  []string
		sizeArgs   []any
		hasSizeSQL bool
	}{
		{
			name:       "sqlite",
			driver:     DriverSQLite,
			action:     MaintenanceVacuum,
			reclaim:    "VACUUM",
			hasSizeSQL: false,
		},
		{
			name:       "mysql",
			driver:     DriverMySQL,
			action:     MaintenanceOptimize,
			reclaim:    "OPTIMIZE TABLE `Metric_definitions`, `Metric_series`, `Metric_label_sets`, `Metric_resolutions`, `Metric_rollups`",
			sizeParts:  []string{"information_schema.TABLES", "TABLE_SCHEMA = DATABASE()", "TABLE_NAME IN (?, ?, ?, ?, ?)"},
			sizeArgs:   []any{"Metric_definitions", "Metric_series", "Metric_label_sets", "Metric_resolutions", "Metric_rollups"},
			hasSizeSQL: true,
		},
		{
			name:       "postgresql",
			driver:     DriverPostgreSQL,
			action:     MaintenanceVacuumFull,
			reclaim:    `VACUUM (FULL, ANALYZE) "metric_definitions", "metric_series", "metric_label_sets", "metric_resolutions", "metric_rollups"`,
			sizeParts:  []string{"pg_total_relation_size(c.oid)", "n.nspname = current_schema()", "c.relname IN ($1, $2, $3, $4, $5)"},
			sizeArgs:   []any{"metric_definitions", "metric_series", "metric_label_sets", "metric_resolutions", "metric_rollups"},
			hasSizeSQL: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maintenanceActionFor(tt.driver); got != tt.action {
				t.Fatalf("maintenanceActionFor(%q) = %q, want %q", tt.driver, got, tt.action)
			}
			gotReclaim, err := managedReclaimQuery(tt.driver, tables)
			if err != nil {
				t.Fatalf("managedReclaimQuery(%q): %v", tt.driver, err)
			}
			if gotReclaim != tt.reclaim {
				t.Fatalf("managedReclaimQuery(%q) = %q, want %q", tt.driver, gotReclaim, tt.reclaim)
			}

			gotSize, gotArgs, err := managedStorageSizeQuery(tt.driver, tables)
			if !tt.hasSizeSQL {
				if err == nil {
					t.Fatalf("managedStorageSizeQuery(%q) unexpectedly succeeded: %q", tt.driver, gotSize)
				}
				return
			}
			if err != nil {
				t.Fatalf("managedStorageSizeQuery(%q): %v", tt.driver, err)
			}
			for _, part := range tt.sizeParts {
				if !strings.Contains(gotSize, part) {
					t.Errorf("size query for %q does not contain %q: %s", tt.driver, part, gotSize)
				}
			}
			if !reflect.DeepEqual(gotArgs, tt.sizeArgs) {
				t.Fatalf("size args for %q = %#v, want %#v", tt.driver, gotArgs, tt.sizeArgs)
			}
		})
	}
}

func TestMySQLOptimizeResultError(t *testing.T) {
	if err := mysqlOptimizeResultError("metric_points", "status", "OK"); err != nil {
		t.Fatalf("status result returned an error: %v", err)
	}
	if err := mysqlOptimizeResultError("metric_points", "note", "recreate and analyze instead"); err != nil {
		t.Fatalf("note result returned an error: %v", err)
	}
	err := mysqlOptimizeResultError("komari.metric_points", " Error ", "operation failed")
	if err == nil || !strings.Contains(err.Error(), "komari.metric_points") || !strings.Contains(err.Error(), "operation failed") {
		t.Fatalf("error result was not preserved: %v", err)
	}
}

func sqliteFileSetSize(t *testing.T, path string) int64 {
	t.Helper()
	var size int64
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %q: %v", name, err)
		}
		size += info.Size()
	}
	return size
}
