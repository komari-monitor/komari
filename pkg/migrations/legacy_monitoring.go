package migrations

import (
	"context"
	"fmt"
	"log"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
	"gorm.io/gorm"
)

const (
	legacyMonitoringMigrationDoneKey = "migration_legacy_monitoring_to_metric_store_done"
	legacyMonitoringBatchSize        = 500
)

var legacyMonitoringTables = []string{"records", "records_long_term", "gpu_records", "ping_records"}

type MetricContext struct {
	DB    *gorm.DB
	Store *metric.Store
}

type legacyMonitoringStats struct {
	Records int64
	GPU     int64
	Ping    int64
}

type legacyRecordRow struct {
	RowID int64 `gorm:"column:row_id"`
	models.Record
}

type legacyGPURecordRow struct {
	RowID int64 `gorm:"column:row_id"`
	models.GPURecord
}

type legacyPingRecordRow struct {
	RowID int64 `gorm:"column:row_id"`
	models.PingRecord
}

// RunMetricStoreMigrations executes one-shot migrations that need the metric store
// to be initialized. These cannot run inside Run because Run executes before the
// metric store is opened.
func RunMetricStoreMigrations(ctx MetricContext) error {
	done, err := appconfig.GetAs[bool](legacyMonitoringMigrationDoneKey, false)
	if err != nil {
		return fmt.Errorf("read legacy monitoring migration marker: %w", err)
	}

	stats, err := runLegacyMonitoringMigration(context.Background(), ctx.DB, ctx.Store, done, func() error {
		return appconfig.Set(legacyMonitoringMigrationDoneKey, true)
	})
	if err != nil {
		return err
	}
	if stats.Records > 0 || stats.GPU > 0 || stats.Ping > 0 {
		log.Printf("Legacy monitoring table migration completed (records=%d, gpu=%d, ping=%d)", stats.Records, stats.GPU, stats.Ping)
	}
	return nil
}

func runLegacyMonitoringMigration(ctx context.Context, db *gorm.DB, s *metric.Store, done bool, markDone func() error) (legacyMonitoringStats, error) {
	var stats legacyMonitoringStats
	if done {
		return stats, nil
	}
	if db == nil {
		return stats, fmt.Errorf("migration database is nil")
	}
	if s == nil {
		return stats, fmt.Errorf("metric store is nil")
	}
	if markDone == nil {
		return stats, fmt.Errorf("migration marker writer is nil")
	}

	stats, err := migrateLegacyMonitoringTables(ctx, db, s)
	if err != nil {
		return stats, err
	}
	if err := dropLegacyMonitoringTables(db); err != nil {
		return stats, err
	}
	if err := markDone(); err != nil {
		return stats, fmt.Errorf("mark legacy monitoring migration done: %w", err)
	}
	return stats, nil
}

func migrateLegacyMonitoringTables(ctx context.Context, db *gorm.DB, s *metric.Store) (legacyMonitoringStats, error) {
	var stats legacyMonitoringStats
	for _, table := range []string{"records", "records_long_term"} {
		n, err := migrateLegacyRecordTable(ctx, s, db, table)
		if err != nil {
			return stats, err
		}
		stats.Records += n
	}

	n, err := migrateLegacyGPURecordTable(ctx, s, db, "gpu_records")
	if err != nil {
		return stats, err
	}
	stats.GPU += n

	n, err = migrateLegacyPingRecordTable(ctx, s, db, "ping_records")
	if err != nil {
		return stats, err
	}
	stats.Ping += n

	return stats, nil
}

func migrateLegacyRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string) (int64, error) {
	if !db.Migrator().HasTable(table) {
		return 0, nil
	}

	var total int64
	if err := db.Table(table).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count legacy %s: %w", table, err)
	}
	if total == 0 {
		return 0, nil
	}

	log.Printf("[legacy-migration] importing %d rows from %s", total, table)
	var migrated int64
	var lastRowID int64

	for {
		select {
		case <-ctx.Done():
			return migrated, ctx.Err()
		default:
		}

		var rows []legacyRecordRow
		q := db.Table(table).Select("rowid AS row_id, *").Where("rowid > ?", lastRowID).Order("rowid ASC").Limit(legacyMonitoringBatchSize)
		if err := q.Find(&rows).Error; err != nil {
			return migrated, fmt.Errorf("read legacy %s: %w", table, err)
		}
		if len(rows) == 0 {
			break
		}

		points := make([]metric.Point, 0, len(rows)*19)
		for i := range rows {
			points = append(points, recordToPoints(rows[i].Record)...)
		}
		if err := s.WriteBatch(ctx, points); err != nil {
			return migrated, fmt.Errorf("write legacy %s to metric store: %w", table, err)
		}

		last := rows[len(rows)-1]
		lastRowID = last.RowID
		migrated += int64(len(rows))
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func migrateLegacyGPURecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string) (int64, error) {
	if !db.Migrator().HasTable(table) {
		return 0, nil
	}

	var total int64
	if err := db.Table(table).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count legacy %s: %w", table, err)
	}
	if total == 0 {
		return 0, nil
	}

	log.Printf("[legacy-migration] importing %d rows from %s", total, table)
	var migrated int64
	var lastRowID int64

	for {
		select {
		case <-ctx.Done():
			return migrated, ctx.Err()
		default:
		}

		var rows []legacyGPURecordRow
		q := db.Table(table).Select("rowid AS row_id, *").Where("rowid > ?", lastRowID).Order("rowid ASC").Limit(legacyMonitoringBatchSize)
		if err := q.Find(&rows).Error; err != nil {
			return migrated, fmt.Errorf("read legacy %s: %w", table, err)
		}
		if len(rows) == 0 {
			break
		}

		points := make([]metric.Point, 0, len(rows)*4)
		for i := range rows {
			points = append(points, gpuRecordToPoints(rows[i].GPURecord)...)
		}
		if err := s.WriteBatch(ctx, points); err != nil {
			return migrated, fmt.Errorf("write legacy %s to metric store: %w", table, err)
		}

		last := rows[len(rows)-1]
		lastRowID = last.RowID
		migrated += int64(len(rows))
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func migrateLegacyPingRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string) (int64, error) {
	if !db.Migrator().HasTable(table) {
		return 0, nil
	}

	var total int64
	if err := db.Table(table).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count legacy %s: %w", table, err)
	}
	if total == 0 {
		return 0, nil
	}

	log.Printf("[legacy-migration] importing %d rows from %s", total, table)
	var migrated int64
	var lastRowID int64

	for {
		select {
		case <-ctx.Done():
			return migrated, ctx.Err()
		default:
		}

		var rows []legacyPingRecordRow
		q := db.Table(table).Select("rowid AS row_id, *").Where("rowid > ?", lastRowID).Order("rowid ASC").Limit(legacyMonitoringBatchSize)
		if err := q.Find(&rows).Error; err != nil {
			return migrated, fmt.Errorf("read legacy %s: %w", table, err)
		}
		if len(rows) == 0 {
			break
		}

		points := make([]metric.Point, 0, len(rows)*2)
		for i := range rows {
			points = append(points, pingRecordToPoints(rows[i].PingRecord)...)
		}
		if err := s.WriteBatch(ctx, points); err != nil {
			return migrated, fmt.Errorf("write legacy %s to metric store: %w", table, err)
		}

		last := rows[len(rows)-1]
		lastRowID = last.RowID
		migrated += int64(len(rows))
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func recordToPoints(rec models.Record) []metric.Point {
	ts := rec.Time.ToTime()
	entityID := rec.Client
	return []metric.Point{
		{MetricName: metricstore.MetricCPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Cpu)},
		{MetricName: metricstore.MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Gpu)},
		{MetricName: metricstore.MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(rec.Ram)},
		{MetricName: metricstore.MetricRAMTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.RamTotal)},
		{MetricName: metricstore.MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(rec.Swap)},
		{MetricName: metricstore.MetricSwapTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.SwapTotal)},
		{MetricName: metricstore.MetricLoad, EntityID: entityID, Timestamp: ts, Value: float64(rec.Load)},
		{MetricName: metricstore.MetricTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temp)},
		{MetricName: metricstore.MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(rec.Disk)},
		{MetricName: metricstore.MetricDiskTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.DiskTotal)},
		{MetricName: metricstore.MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetIn)},
		{MetricName: metricstore.MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetOut)},
		{MetricName: metricstore.MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalUp)},
		{MetricName: metricstore.MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalDown)},
		{MetricName: metricstore.MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficUp)},
		{MetricName: metricstore.MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficDown)},
		{MetricName: metricstore.MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(rec.Process)},
		{MetricName: metricstore.MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(rec.Connections)},
		{MetricName: metricstore.MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(rec.ConnectionsUdp)},
	}
}

func gpuRecordToPoints(rec models.GPURecord) []metric.Point {
	ts := rec.Time.ToTime()
	tags := map[string]string{
		"device_index": fmt.Sprintf("%d", rec.DeviceIndex),
		"device_name":  rec.DeviceName,
	}
	return []metric.Point{
		{MetricName: metricstore.MetricGPUMem, EntityID: rec.Client, Timestamp: ts, Value: float64(rec.MemUsed), Tags: tags},
		{MetricName: metricstore.MetricGPUMemTotal, EntityID: rec.Client, Timestamp: ts, Value: float64(rec.MemTotal), Tags: tags},
		{MetricName: metricstore.MetricGPUDeviceUsage, EntityID: rec.Client, Timestamp: ts, Value: float64(rec.Utilization), Tags: tags},
		{MetricName: metricstore.MetricGPUTemp, EntityID: rec.Client, Timestamp: ts, Value: float64(rec.Temperature), Tags: tags},
	}
}

func pingRecordToPoints(rec models.PingRecord) []metric.Point {
	ts := rec.Time.ToTime()
	tags := map[string]string{"task_id": fmt.Sprintf("%d", rec.TaskId)}
	loss := 0.0
	if rec.Value < 0 {
		loss = 1
	}
	return []metric.Point{
		{
			MetricName: metricstore.MetricPingLatency,
			EntityID:   rec.Client,
			Timestamp:  ts,
			Value:      float64(rec.Value),
			Tags:       tags,
		},
		{
			MetricName: metricstore.MetricPingLoss,
			EntityID:   rec.Client,
			Timestamp:  ts,
			Value:      loss,
			Tags:       tags,
		},
	}
}

func dropLegacyMonitoringTables(db *gorm.DB) error {
	for _, table := range legacyMonitoringTables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		log.Printf("[legacy-migration] dropping legacy table %s", table)
		if err := db.Migrator().DropTable(table); err != nil {
			return fmt.Errorf("drop legacy table %s: %w", table, err)
		}
	}
	return nil
}
