package metric

import (
	"context"
	"errors"
	"fmt"
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
	if err := store.writeBatch(ctx, store.db, []Point{{
		MetricName: "orphan", EntityID: "node-1", Timestamp: time.Now().UTC(), Value: 1,
	}}); err != nil {
		t.Fatalf("seed orphan point: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano, count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		store.tables.rollups,
	), "orphan", "node-1", "", "{}", int64(time.Minute), time.Now().UTC().UnixNano(), 1, 1, 1, 1, 1, 1, time.Now().UTC().UnixNano(), 1, time.Now().UTC().UnixNano(), []byte{}, time.Now().UTC().UnixNano()); err != nil {
		t.Fatalf("seed orphan rollup: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (metric_name, watermark_nano, updated_at) VALUES (?, ?, ?)`, store.tables.watermarks,
	), "orphan", time.Now().UTC().UnixNano(), time.Now().UTC().UnixNano()); err != nil {
		t.Fatalf("seed orphan watermark: %v", err)
	}

	deleted, err := store.cleanupOrphanedMetricData(ctx)
	if err != nil {
		t.Fatalf("clean orphaned metric data: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted rows = %d, want 3", deleted)
	}
	for _, table := range []string{store.tables.points, store.tables.rollups, store.tables.watermarks} {
		var count int
		if err := store.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE metric_name = ?`, table), "orphan").Scan(&count); err != nil {
			t.Fatalf("count orphan rows in %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("orphan rows remain in %s: %d", table, count)
		}
	}
}

func TestMaintenanceMappings(t *testing.T) {
	tables := tables{
		definitions: "Metric_definitions",
		points:      "Metric_points",
		rollups:     "Metric_rollups",
		watermarks:  "Metric_compaction_watermarks",
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
			reclaim:    "OPTIMIZE TABLE `Metric_definitions`, `Metric_points`, `Metric_rollups`, `Metric_compaction_watermarks`",
			sizeParts:  []string{"information_schema.TABLES", "TABLE_SCHEMA = DATABASE()", "TABLE_NAME IN (?, ?, ?, ?)"},
			sizeArgs:   []any{"Metric_definitions", "Metric_points", "Metric_rollups", "Metric_compaction_watermarks"},
			hasSizeSQL: true,
		},
		{
			name:       "postgresql",
			driver:     DriverPostgreSQL,
			action:     MaintenanceVacuumFull,
			reclaim:    `VACUUM (FULL, ANALYZE) "metric_definitions", "metric_points", "metric_rollups", "metric_compaction_watermarks"`,
			sizeParts:  []string{"pg_total_relation_size(c.oid)", "n.nspname = current_schema()", "c.relname IN ($1, $2, $3, $4)"},
			sizeArgs:   []any{"metric_definitions", "metric_points", "metric_rollups", "metric_compaction_watermarks"},
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
