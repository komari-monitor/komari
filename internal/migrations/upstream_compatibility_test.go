package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/migrations"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const compatibilityNodeID = "upstream-node"

type compatibilityExpected struct {
	trafficUp   int64
	trafficDown int64
}

func TestUpstreamMainDatabaseUpgradeCompatibility(t *testing.T) {
	versions := []struct {
		name        string
		withTraffic bool
	}{
		{name: "1.1.x"},
		{name: "1.2.0"},
		{name: "1.2.6", withTraffic: true},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			ctx := context.Background()
			closeActiveMetricStore(t)
			mainDB := openCompatibilityConfigDB(t)
			base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour).Add(17 * time.Minute)
			seedLegacyMainMonitoring(t, mainDB, base, version.withTraffic)

			metricPath := filepath.Join(t.TempDir(), "metrics.db")
			cfg := &metricstore.MetricStoreConfig{Driver: "sqlite", DSN: metricPath, TablePrefix: "metric_"}
			store, err := metricstore.OpenStoreForMigration(ctx, cfg, 1)
			if err != nil {
				t.Fatalf("open V4 migration target: %v", err)
			}
			if _, err := migrations.MigrateLegacyMonitoring(ctx, mainDB, store, nil); err != nil {
				_ = store.Close()
				t.Fatalf("migrate %s monitoring tables: %v", version.name, err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("close V4 migration target: %v", err)
			}
			activateCompatibilityStore(t, metricPath)

			expected := compatibilityExpected{}
			if version.withTraffic {
				expected.trafficUp = 123456789
				expected.trafficDown = 987654321
			}
			assertCompatibilityReadAPIs(t, base.Truncate(time.Hour), expected)
		})
	}
}

func TestUpstreamMetricDatabaseUpgradeCompatibility(t *testing.T) {
	for _, version := range []string{"1.2.7", "1.3.0"} {
		t.Run(version, func(t *testing.T) {
			closeActiveMetricStore(t)
			_ = openCompatibilityConfigDB(t)
			base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
			metricPath := filepath.Join(t.TempDir(), "metrics.db")
			seedLegacyMetricStore(t, metricPath, base)
			setCompatibilityMetricConfig(t, metricPath)

			summary, err := metricstore.InspectSQLiteStorageMigration(context.Background())
			if err != nil {
				t.Fatalf("inspect %s metric database: %v", version, err)
			}
			if !summary.Required || summary.Layout != "legacy" || summary.SourceRows != 11 {
				t.Fatalf("unexpected %s preflight summary: %#v", version, summary)
			}

			if err := metricstore.InitializeStore(); err != nil {
				t.Fatalf("upgrade %s metric database to V4: %v", version, err)
			}
			t.Cleanup(func() { _ = metricstore.CloseStoreContext(context.Background()) })
			assertCompatibilityReadAPIs(t, base, compatibilityExpected{trafficUp: 123456789, trafficDown: 987654321})

			summary, err = metricstore.InspectSQLiteStorageMigration(context.Background())
			if err != nil {
				t.Fatalf("inspect upgraded %s metric database: %v", version, err)
			}
			if summary.Required || summary.Layout != "current" {
				t.Fatalf("unexpected %s post-upgrade summary: %#v", version, summary)
			}
		})
	}
}

func openCompatibilityConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "komari.db"))
	db, err := gorm.Open(sqlite.Open("file:"+path+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open compatibility main database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get compatibility main SQL database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&appconfig.ConfigItem{}); err != nil {
		t.Fatalf("create compatibility configuration table: %v", err)
	}
	appconfig.SetDb(db)
	return db
}

func seedLegacyMainMonitoring(t *testing.T, db *gorm.DB, base time.Time, withTraffic bool) {
	t.Helper()
	trafficColumns := ""
	trafficColumnNames := ""
	trafficValues := ""
	trafficArgs := []any{}
	if withTraffic {
		trafficColumns = ", traffic_up INTEGER NOT NULL, traffic_down INTEGER NOT NULL"
		trafficColumnNames = ", traffic_up, traffic_down"
		trafficValues = ", ?, ?"
		trafficArgs = append(trafficArgs, int64(123456789), int64(987654321))
	}
	statements := []string{
		`CREATE TABLE records (
		 client TEXT NOT NULL, time DATETIME NOT NULL, cpu REAL NOT NULL, gpu REAL NOT NULL,
		 ram INTEGER NOT NULL, ram_total INTEGER NOT NULL, swap INTEGER NOT NULL, swap_total INTEGER NOT NULL,
		 load REAL NOT NULL, temp REAL NOT NULL, disk INTEGER NOT NULL, disk_total INTEGER NOT NULL,
		 net_in INTEGER NOT NULL, net_out INTEGER NOT NULL, net_total_up INTEGER NOT NULL, net_total_down INTEGER NOT NULL,
		 process INTEGER NOT NULL, connections INTEGER NOT NULL, connections_udp INTEGER NOT NULL` + trafficColumns + `)`,
		`CREATE TABLE gpu_records (
		 client TEXT NOT NULL, time DATETIME NOT NULL, device_index INTEGER NOT NULL, device_name TEXT NOT NULL,
		 mem_total INTEGER NOT NULL, mem_used INTEGER NOT NULL, utilization REAL NOT NULL, temperature INTEGER NOT NULL)`,
		`CREATE TABLE ping_records (
		 client TEXT NOT NULL, task_id INTEGER NOT NULL, time DATETIME NOT NULL, value INTEGER NOT NULL)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create legacy main monitoring schema: %v", err)
		}
	}
	recordArgs := []any{
		compatibilityNodeID, base, 42.5, 13.5,
		int64(2048), int64(4096), int64(128), int64(1024), 0.75, 51.5,
		int64(500000), int64(1000000), int64(1200), int64(3400),
		int64(8000000000123), int64(7000000000456), 87, 321, 12,
	}
	recordArgs = append(recordArgs, trafficArgs...)
	query := `INSERT INTO records
		(client, time, cpu, gpu, ram, ram_total, swap, swap_total, load, temp, disk, disk_total,
		 net_in, net_out, net_total_up, net_total_down, process, connections, connections_udp` + trafficColumnNames + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?` + trafficValues + `)`
	if err := db.Exec(query, recordArgs...).Error; err != nil {
		t.Fatalf("seed legacy load record: %v", err)
	}
	if err := db.Exec(`INSERT INTO gpu_records
		(client, time, device_index, device_name, mem_total, mem_used, utilization, temperature)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, compatibilityNodeID, base, 0, "GPU 0", 8192, 4096, 67.5, 55).Error; err != nil {
		t.Fatalf("seed legacy GPU record: %v", err)
	}
	if err := db.Exec(`INSERT INTO ping_records (client, task_id, time, value) VALUES (?, ?, ?, ?)`, compatibilityNodeID, 7, base, 36).Error; err != nil {
		t.Fatalf("seed legacy ping record: %v", err)
	}
}

func seedLegacyMetricStore(t *testing.T, path string, base time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy metric database: %v", err)
	}
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
		`CREATE TABLE metric_rollups (
		 id INTEGER PRIMARY KEY AUTOINCREMENT, metric_name VARCHAR(191) NOT NULL, entity_id VARCHAR(191) NOT NULL,
		 tags_hash VARCHAR(64) NOT NULL, tags TEXT NOT NULL, resolution_nano BIGINT NOT NULL, bucket_nano BIGINT NOT NULL,
		 count BIGINT NOT NULL, sum DOUBLE PRECISION NOT NULL, sum_sq DOUBLE PRECISION NOT NULL,
		 min_val DOUBLE PRECISION NOT NULL, max_val DOUBLE PRECISION NOT NULL, first_val DOUBLE PRECISION NOT NULL,
		 first_ts BIGINT NOT NULL, last_val DOUBLE PRECISION NOT NULL, last_ts BIGINT NOT NULL,
		 digest BLOB, created_at BIGINT NOT NULL,
		 UNIQUE(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano))`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("create legacy metric schema: %v", err)
		}
	}
	points := []struct {
		name     string
		value    float64
		tagsHash string
		tags     string
	}{
		{name: metricstore.MetricCPU, value: 42.5, tagsHash: "none", tags: `{}`},
		{name: metricstore.MetricNetTotalUp, value: 8000000000123, tagsHash: "none", tags: `{}`},
		{name: metricstore.MetricNetTotalDown, value: 7000000000456, tagsHash: "none", tags: `{}`},
		{name: metricstore.MetricTrafficUp, value: 123456789, tagsHash: "none", tags: `{}`},
		{name: metricstore.MetricTrafficDown, value: 987654321, tagsHash: "none", tags: `{}`},
		{name: metricstore.MetricGPUDeviceUsage, value: 67.5, tagsHash: "gpu-0", tags: `{"device_index":"0","device_name":"GPU 0"}`},
		{name: metricstore.MetricGPUMem, value: 4096, tagsHash: "gpu-0", tags: `{"device_index":"0","device_name":"GPU 0"}`},
		{name: metricstore.MetricGPUMemTotal, value: 8192, tagsHash: "gpu-0", tags: `{"device_index":"0","device_name":"GPU 0"}`},
		{name: metricstore.MetricGPUTemp, value: 55, tagsHash: "gpu-0", tags: `{"device_index":"0","device_name":"GPU 0"}`},
		{name: metricstore.MetricPingLatency, value: 36, tagsHash: "ping-7", tags: `{"task_id":"7"}`},
		{name: metricstore.MetricPingLoss, value: 0, tagsHash: "ping-7", tags: `{"task_id":"7"}`},
	}
	for _, point := range points {
		if _, err := db.Exec(`INSERT INTO metric_points
			(metric_name, entity_id, tags_hash, ts_nano, value, tags, labels, created_at)
			VALUES (?, ?, ?, ?, ?, ?, '{}', ?)`, point.name, compatibilityNodeID, point.tagsHash, base.UnixNano(), point.value, point.tags, base.UnixNano()); err != nil {
			_ = db.Close()
			t.Fatalf("seed legacy metric %s: %v", point.name, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy metric database: %v", err)
	}
}

func activateCompatibilityStore(t *testing.T, metricPath string) {
	t.Helper()
	setCompatibilityMetricConfig(t, metricPath)
	if err := metricstore.InitializeStore(); err != nil {
		t.Fatalf("activate compatibility metric store: %v", err)
	}
	t.Cleanup(func() { _ = metricstore.CloseStoreContext(context.Background()) })
}

func setCompatibilityMetricConfig(t *testing.T, metricPath string) {
	t.Helper()
	if err := appconfig.SetMany(map[string]any{
		metricstore.MetricDBDriverKey:    "sqlite",
		metricstore.MetricDBDSNKey:       filepath.ToSlash(metricPath),
		metricstore.MetricTablePrefixKey: "metric_",
	}); err != nil {
		t.Fatalf("set compatibility metric configuration: %v", err)
	}
}

func closeActiveMetricStore(t *testing.T) {
	t.Helper()
	if err := metricstore.CloseStoreContext(context.Background()); err != nil {
		t.Fatalf("close previous metric store: %v", err)
	}
}

func assertCompatibilityReadAPIs(t *testing.T, timestamp time.Time, expected compatibilityExpected) {
	t.Helper()
	ctx := context.Background()
	start := timestamp.Add(-time.Minute)
	end := timestamp.Add(time.Hour + time.Minute)
	records, err := metricstore.GetRecordsByClientAndTime(ctx, compatibilityNodeID, start, end)
	if err != nil {
		t.Fatalf("read client records after migration: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("client records after migration = %d, want 1: %#v", len(records), records)
	}
	record := records[0]
	if record.Cpu != 42.5 || record.NetTotalUp != 8000000000123 || record.NetTotalDown != 7000000000456 ||
		record.TrafficUp != expected.trafficUp || record.TrafficDown != expected.trafficDown {
		t.Fatalf("load or traffic values changed during migration: %#v", record)
	}
	all, err := metricstore.GetRecordsByTime(ctx, start, end)
	if err != nil || len(all) != 1 || all[0].Client != compatibilityNodeID {
		t.Fatalf("read all records after migration: records=%#v err=%v", all, err)
	}
	gpu, err := metricstore.GetGPURecordsByClientAndTime(ctx, compatibilityNodeID, start, end)
	if err != nil || len(gpu) != 1 || gpu[0].DeviceIndex != 0 || gpu[0].DeviceName != "GPU 0" || gpu[0].Utilization != 67.5 || gpu[0].MemUsed != 4096 {
		t.Fatalf("read GPU records after migration: records=%#v err=%v", gpu, err)
	}
	pings, err := metricstore.GetPingRecords(ctx, compatibilityNodeID, 7, start, end)
	if err != nil || len(pings) != 1 || pings[0].TaskId != 7 || pings[0].Value != 36 {
		t.Fatalf("read ping records after migration: records=%#v err=%v", pings, err)
	}
	if got := fmt.Sprintf("%d/%d/%d/%d", record.NetTotalUp, record.NetTotalDown, record.TrafficUp, record.TrafficDown); got != fmt.Sprintf("%d/%d/%d/%d", int64(8000000000123), int64(7000000000456), expected.trafficUp, expected.trafficDown) {
		t.Fatalf("traffic precision changed during migration: %s", got)
	}
}
