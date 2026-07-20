package migrations

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/pkg/config"
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
	if err := mainDB.Create(&models.Record{Client: "client-a", Time: base, Cpu: 12.5, Ram: 2048}).Error; err != nil {
		t.Fatalf("seed records: %v", err)
	}
	if err := mainDB.Table("records_long_term").Create(&models.Record{Client: "client-a", Time: base.Add(time.Minute), Cpu: 22.5, Ram: 4096}).Error; err != nil {
		t.Fatalf("seed records_long_term: %v", err)
	}
	if err := mainDB.Create(&models.GPURecord{Client: "client-a", Time: base, DeviceIndex: 0, DeviceName: "GPU 0", MemUsed: 1024, MemTotal: 2048, Utilization: 67, Temperature: 55}).Error; err != nil {
		t.Fatalf("seed gpu_records: %v", err)
	}
	if err := mainDB.Create(&models.PingRecord{Client: "client-a", TaskId: 7, Time: base, Value: 36}).Error; err != nil {
		t.Fatalf("seed ping_records: %v", err)
	}
	if err := mainDB.Create(&models.PingRecord{Client: "client-a", TaskId: 7, Time: base.Add(30 * time.Second), Value: -1}).Error; err != nil {
		t.Fatalf("seed loss ping_records: %v", err)
	}
	summary, err := InspectLegacyMonitoring(mainDB)
	if err != nil {
		t.Fatalf("inspect legacy monitoring summary: %v", err)
	}
	if summary.LoadRows != 2 || summary.GPURows != 1 || summary.LatencyRows != 2 || summary.MonitoringRows != 5 {
		t.Fatalf("unexpected legacy monitoring summary: %#v", summary)
	}
	if summary.EstimatedPoints != 38 || summary.RetentionDays < 1 {
		t.Fatalf("unexpected legacy point estimate or retention: %#v", summary)
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
	var lastProgress LegacyMonitoringProgress
	if _, err := MigrateLegacyMonitoring(ctx, mainDB, metricStore, func(progress LegacyMonitoringProgress) {
		lastProgress = progress
	}); err != nil {
		t.Fatalf("migrate legacy monitoring with progress: %v", err)
	}
	if lastProgress.SourceRowsDone != 5 || lastProgress.SourceRowsTotal != 5 || lastProgress.WrittenPoints != 38 {
		t.Fatalf("unexpected final migration progress: %#v", lastProgress)
	}

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
	if stats.Records != 2 || stats.GPU != 1 || stats.Ping != 2 {
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

	pingPoints, err := metricStore.Query(ctx, metric.Query{MetricName: metricstore.MetricPingLatency, EntityID: "client-a", Start: base.Add(-time.Second), End: base.Add(time.Minute), Tags: map[string]string{"task_id": "7"}})
	if err != nil {
		t.Fatalf("query ping points: %v", err)
	}
	if len(pingPoints) != 2 || pingPoints[0].Value != 36 || pingPoints[1].Value != -1 {
		t.Fatalf("unexpected ping points: %#v", pingPoints)
	}

	pingLossPoints, err := metricStore.Query(ctx, metric.Query{MetricName: metricstore.MetricPingLoss, EntityID: "client-a", Start: base.Add(-time.Second), End: base.Add(time.Minute), Tags: map[string]string{"task_id": "7"}})
	if err != nil {
		t.Fatalf("query ping loss points: %v", err)
	}
	if len(pingLossPoints) != 2 || pingLossPoints[0].Value != 0 || pingLossPoints[1].Value != 1 {
		t.Fatalf("unexpected ping loss points: %#v", pingLossPoints)
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
	if stats != (LegacyMonitoringStats{}) {
		t.Fatalf("completed migration should not scan legacy tables, got %#v", stats)
	}
	if markDoneCalls != 1 {
		t.Fatalf("completed migration rewrote marker, calls=%d", markDoneCalls)
	}
}

func TestDeleteLegacyMonitoringBeforeOnlyRemovesOldHistory(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "cleanup.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open cleanup db: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	} else {
		t.Fatalf("cleanup sql db: %v", err)
	}
	if err := db.AutoMigrate(&models.Client{}, &models.Record{}, &models.GPURecord{}, &models.PingRecord{}); err != nil {
		t.Fatalf("migrate cleanup tables: %v", err)
	}
	if err := db.Table("records_long_term").AutoMigrate(&models.Record{}); err != nil {
		t.Fatalf("migrate cleanup long-term table: %v", err)
	}
	if err := db.Create(&models.Client{UUID: "client-a", Token: "token-a", Name: "A"}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}

	cutoff := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	oldTime := cutoff.Add(-time.Hour)
	newTime := cutoff.Add(time.Hour)
	for _, record := range []models.Record{{Client: "client-a", Time: oldTime}, {Client: "client-a", Time: newTime}} {
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed load record: %v", err)
		}
	}
	for _, record := range []models.GPURecord{{Client: "client-a", Time: oldTime}, {Client: "client-a", Time: newTime}} {
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed GPU record: %v", err)
		}
	}
	for _, record := range []models.PingRecord{{Client: "client-a", TaskId: 1, Time: oldTime}, {Client: "client-a", TaskId: 1, Time: newTime}} {
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed ping record: %v", err)
		}
	}

	deleted, err := DeleteLegacyMonitoringBefore(db, cutoff)
	if err != nil {
		t.Fatalf("delete legacy monitoring before cutoff: %v", err)
	}
	if deleted.LoadRows != 1 || deleted.GPURows != 1 || deleted.LatencyRows != 1 {
		t.Fatalf("unexpected deleted rows: %#v", deleted)
	}
	summary, err := InspectLegacyMonitoring(db)
	if err != nil {
		t.Fatalf("inspect remaining legacy monitoring: %v", err)
	}
	if summary.LoadRows != 1 || summary.GPURows != 1 || summary.LatencyRows != 1 || summary.ServerCount != 1 {
		t.Fatalf("unexpected remaining legacy rows: %#v", summary)
	}
	var clients int64
	if err := db.Model(&models.Client{}).Count(&clients).Error; err != nil || clients != 1 {
		t.Fatalf("cleanup changed clients: count=%d err=%v", clients, err)
	}
}

func TestCompleteLegacyMonitoringMigrationFinalizesBeforeMarkingDone(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "finalize.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open finalization database: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	} else {
		t.Fatalf("finalization sql db: %v", err)
	}
	appconfig.SetDb(db)
	if err := db.AutoMigrate(&models.Record{}); err != nil {
		t.Fatalf("migrate legacy table: %v", err)
	}
	if err := appconfig.Set(legacyMonitoringMigrationDoneKey, false); err != nil {
		t.Fatalf("reset completion marker: %v", err)
	}

	finalized := false
	if err := CompleteLegacyMonitoringMigration(db, func() error {
		finalized = true
		if db.Migrator().HasTable(&models.Record{}) {
			t.Fatal("legacy table still exists during finalization")
		}
		done, err := appconfig.GetAs[bool](legacyMonitoringMigrationDoneKey, false)
		if err != nil {
			return err
		}
		if done {
			t.Fatal("completion marker was written before finalization")
		}
		return nil
	}); err != nil {
		t.Fatalf("complete legacy migration: %v", err)
	}
	if !finalized {
		t.Fatal("finalizer was not called")
	}
	done, err := appconfig.GetAs[bool](legacyMonitoringMigrationDoneKey, false)
	if err != nil {
		t.Fatalf("read completion marker: %v", err)
	}
	if !done {
		t.Fatal("completion marker was not written after finalization")
	}

	if err := db.AutoMigrate(&models.Record{}); err != nil {
		t.Fatalf("restore legacy table: %v", err)
	}
	if err := appconfig.Set(legacyMonitoringMigrationDoneKey, false); err != nil {
		t.Fatalf("reset completion marker after success: %v", err)
	}
	finalizeErr := errors.New("vacuum failed")
	if err := CompleteLegacyMonitoringMigration(db, func() error { return finalizeErr }); !errors.Is(err, finalizeErr) {
		t.Fatalf("finalization error = %v, want %v", err, finalizeErr)
	}
	done, err = appconfig.GetAs[bool](legacyMonitoringMigrationDoneKey, false)
	if err != nil {
		t.Fatalf("read completion marker after failure: %v", err)
	}
	if done {
		t.Fatal("failed finalization wrote the completion marker")
	}
}
