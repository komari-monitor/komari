package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLegacyMonitoringTablesMigratedByOneShotMigration(t *testing.T) {
	ctx := context.Background()
	mainDB, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "komari.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if sqlDB, err := mainDB.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	} else {
		t.Fatalf("legacy sql db: %v", err)
	}
	if err := mainDB.AutoMigrate(&models.Record{}, &models.GPURecord{}, &models.PingRecord{}); err != nil {
		t.Fatalf("migrate legacy tables: %v", err)
	}
	if err := mainDB.Table("records_long_term").AutoMigrate(&models.Record{}); err != nil {
		t.Fatalf("migrate legacy long-term table: %v", err)
	}

	base := time.Date(2026, 7, 8, 23, 42, 0, 0, time.UTC)
	if err := mainDB.Create(&models.Record{Client: "client-a", Time: models.FromTime(base), Cpu: 12.5, Ram: 2048}).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}
	if err := mainDB.Table("records_long_term").Create(&models.Record{Client: "client-a", Time: models.FromTime(base.Add(time.Minute)), Cpu: 22.5, Ram: 4096}).Error; err != nil {
		t.Fatalf("seed records_long_term: %v", err)
	}
	if err := mainDB.Create(&models.GPURecord{Client: "client-a", Time: models.FromTime(base), DeviceIndex: 0, DeviceName: "GPU 0", MemUsed: 1024, MemTotal: 2048, Utilization: 67, Temperature: 55}).Error; err != nil {
		t.Fatalf("seed gpu_records: %v", err)
	}
	if err := mainDB.Create(&models.PingRecord{Client: "client-a", TaskId: 7, Time: models.FromTime(base), Value: 36}).Error; err != nil {
		t.Fatalf("seed ping_records: %v", err)
	}

	metricDB, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "metrics.db"))+"?mode=rwc")
	if err != nil {
		t.Fatalf("open metric db: %v", err)
	}
	metricStore, err := metric.Open(ctx, metric.SQLite("", metric.WithDB(metricDB)))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	t.Cleanup(func() {
		_ = metricStore.Close()
		_ = metricDB.Close()
	})

	markDoneCalls := 0
	stats, err := runLegacyMonitoringMigration(ctx, mainDB, metricStore, false, func() error {
		markDoneCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("run legacy monitoring migration: %v", err)
	}
	if markDoneCalls != 1 {
		t.Fatalf("expected migration marker to be written once, got %d", markDoneCalls)
	}
	if stats.Records != 2 || stats.GPU != 1 || stats.Ping != 1 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	cpuPoints, err := metricStore.Query(ctx, metric.Query{MetricName: metricstore.MetricCPU, EntityID: "client-a", Start: base.Add(-time.Second), End: base.Add(2 * time.Minute)})
	if err != nil {
		t.Fatalf("query cpu points: %v", err)
	}
	if len(cpuPoints) != 2 || cpuPoints[0].Value != 12.5 || cpuPoints[1].Value != 22.5 {
		t.Fatalf("unexpected cpu points: %#v", cpuPoints)
	}

	gpuPoints, err := metricStore.Query(ctx, metric.Query{MetricName: metricstore.MetricGPUDeviceUsage, EntityID: "client-a", Start: base.Add(-time.Second), End: base.Add(time.Second), Tags: map[string]string{"device_index": "0"}})
	if err != nil {
		t.Fatalf("query gpu points: %v", err)
	}
	if len(gpuPoints) != 1 || gpuPoints[0].Value != 67 {
		t.Fatalf("unexpected gpu points: %#v", gpuPoints)
	}

	pingPoints, err := metricStore.Query(ctx, metric.Query{MetricName: metricstore.MetricPingLatency, EntityID: "client-a", Start: base.Add(-time.Second), End: base.Add(time.Second), Tags: map[string]string{"task_id": "7"}})
	if err != nil {
		t.Fatalf("query ping points: %v", err)
	}
	if len(pingPoints) != 1 || pingPoints[0].Value != 36 {
		t.Fatalf("unexpected ping points: %#v", pingPoints)
	}

	for _, table := range legacyMonitoringTables {
		if mainDB.Migrator().HasTable(table) {
			t.Fatalf("legacy table %s still exists", table)
		}
	}

	stats, err = runLegacyMonitoringMigration(ctx, mainDB, metricStore, true, func() error {
		markDoneCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("rerun completed legacy monitoring migration: %v", err)
	}
	if stats != (legacyMonitoringStats{}) {
		t.Fatalf("completed migration should not scan legacy tables, got %#v", stats)
	}
	if markDoneCalls != 1 {
		t.Fatalf("completed migration rewrote marker, calls=%d", markDoneCalls)
	}
}
