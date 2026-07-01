package metricstore

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
	"gorm.io/gorm"
)

// MigrationProgress 迁移进度信息
type MigrationProgress struct {
	Status           string    `json:"status"`             // not_started, in_progress, completed, failed
	TotalRecords     int64     `json:"total_records"`      // 总记录数
	MigratedRecords  int64     `json:"migrated_records"`   // 已迁移记录数
	TotalGPURecords  int64     `json:"total_gpu_records"`  // 总GPU记录数
	MigratedGPU      int64     `json:"migrated_gpu"`       // 已迁移GPU记录数
	TotalPingRecords int64     `json:"total_ping_records"` // 总Ping记录数
	MigratedPing     int64     `json:"migrated_ping"`      // 已迁移Ping记录数
	StartTime        time.Time `json:"start_time"`         // 开始时间
	EndTime          time.Time `json:"end_time"`           // 结束时间
	Error            string    `json:"error,omitempty"`    // 错误信息
}

// MigrateFromLegacyTables 从旧的 SQLite 表迁移数据到 metric store
// batchSize: 每批次迁移的记录数，建议 100-1000
func MigrateFromLegacyTables(batchSize int) (*MigrationProgress, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	progress := &MigrationProgress{
		Status:    "in_progress",
		StartTime: time.Now(),
	}

	// 更新迁移状态
	if err := config.Set(MetricMigrationStatusKey, "in_progress"); err != nil {
		log.Printf("Failed to update migration status: %v", err)
	}

	db := dbcore.GetDBInstance()
	ctx := context.Background()

	// 1. 迁移 records 表
	log.Println("Starting migration of records table...")
	if err := migrateRecordTable(ctx, s, db, "records", batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate records: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 2. 迁移 records_long_term 表
	log.Println("Starting migration of records_long_term table...")
	if err := migrateRecordTable(ctx, s, db, "records_long_term", batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate records_long_term: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 3. 迁移 ping_records 表
	log.Println("Starting migration of ping_records table...")
	if err := migratePingRecordsTable(ctx, s, db, batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate ping_records: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	progress.Status = "completed"
	progress.EndTime = time.Now()

	// 更新迁移状态为完成
	if err := config.Set(MetricMigrationStatusKey, "completed"); err != nil {
		log.Printf("Failed to update migration status: %v", err)
	}

	log.Printf("Migration completed successfully. Total: %d records, %d GPU records, %d ping records in %v",
		progress.MigratedRecords, progress.MigratedGPU, progress.MigratedPing,
		progress.EndTime.Sub(progress.StartTime))

	return progress, nil
}

// recordToPoints 将一条 models.Record 展开为 metric store 的采样点集合。
func recordToPoints(rec models.Record) []metric.Point {
	ts := rec.Time.ToTime()
	entityID := rec.Client
	return []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Cpu)},
		{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Gpu)},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(rec.Ram)},
		{MetricName: MetricRAMTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.RamTotal)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(rec.Swap)},
		{MetricName: MetricSwapTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.SwapTotal)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: float64(rec.Load)},
		{MetricName: MetricTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temp)},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(rec.Disk)},
		{MetricName: MetricDiskTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.DiskTotal)},
		{MetricName: MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetIn)},
		{MetricName: MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetOut)},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalUp)},
		{MetricName: MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalDown)},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficUp)},
		{MetricName: MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficDown)},
		{MetricName: MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(rec.Process)},
		{MetricName: MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(rec.Connections)},
		{MetricName: MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(rec.ConnectionsUdp)},
	}
}

// migrateRecordTable 迁移 records / records_long_term 结构相同的表。
//
// 性能优化点：
//  1. 使用 keyset 分页（WHERE time > cursor）替代 OFFSET，避免大偏移量下
//     O(n²) 的行扫描开销——OFFSET N 需要数据库先扫描并丢弃前 N 行。
//  2. 将整批记录的所有采样点累积后一次性 WriteBatch 写入，把每条记录一次
//     数据库写入（含 fsync）合并为每批一次事务，SQLite 单写连接下提升显著。
//
// 由于 records 仅按 time 建索引且 time 非唯一，keyset 采用严格 time > cursor，
// 并在批次尾部对与最大 time 相同的记录做修剪（trim），把该时间戳整组留到下一
// 批重新拉取，避免同一时间戳跨批次边界被截断而丢数据。metric store 写入是基于
// (metric_name, entity_id, tags_hash, ts_nano) 的 UPSERT，重复写入幂等安全。
func migrateRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string, batchSize int, progress *MigrationProgress) error {
	var totalCount int64
	if err := db.Table(table).Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalRecords += totalCount

	if totalCount == 0 {
		log.Printf("No records to migrate from %s table", table)
		return nil
	}

	log.Printf("Migrating %d records from %s table...", totalCount, table)

	var cursor models.LocalTime
	hasCursor := false
	for {
		var records []models.Record
		q := db.Table(table).Order("time ASC").Limit(batchSize)
		if hasCursor {
			q = q.Where("time > ?", cursor)
		}
		if err := q.Find(&records).Error; err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}

		// 是否为最后一页：不足一批说明后面没有更多数据，可以整批处理。
		lastPage := len(records) < batchSize

		process := records
		if !lastPage {
			// 修剪掉与本批最大 time 相同的尾部记录，留到下一批整组拉取，
			// 防止同一时间戳被批次边界截断丢失。
			maxT := records[len(records)-1].Time.ToTime()
			k := len(records)
			for k > 0 && records[k-1].Time.ToTime().Equal(maxT) {
				k--
			}
			if k > 0 {
				process = records[:k]
			}
			// k == 0 表示整批同一时间戳（极端罕见）：无法修剪，只能整批处理
			// 并严格前进，此时同一时间戳超出 batchSize 的部分理论上可能被跳过，
			// 属可接受的极端边界。
		}

		allPoints := make([]metric.Point, 0, len(process)*19)
		for i := range process {
			allPoints = append(allPoints, recordToPoints(process[i])...)
		}

		if err := s.WriteBatch(ctx, allPoints); err != nil {
			log.Printf("Failed to write batch of %d points from %s: %v", len(allPoints), table, err)
			return err
		}

		progress.MigratedRecords += int64(len(process))
		cursor = process[len(process)-1].Time
		hasCursor = true
		log.Printf("Migrated %d/%d total records", progress.MigratedRecords, progress.TotalRecords)

		if lastPage {
			break
		}
	}

	return nil
}

// migratePingRecordsTable 迁移 ping_records 表
func migratePingRecordsTable(ctx context.Context, s *metric.Store, db *gorm.DB, batchSize int, progress *MigrationProgress) error {
	var totalCount int64
	if err := db.Table("ping_records").Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalPingRecords = totalCount

	if totalCount == 0 {
		log.Println("No records to migrate from ping_records table")
		return nil
	}

	log.Printf("Migrating %d ping records from ping_records table...", totalCount)

	var cursor models.LocalTime
	hasCursor := false
	for {
		var pingRecords []models.PingRecord
		q := db.Table("ping_records").Order("time ASC").Limit(batchSize)
		if hasCursor {
			q = q.Where("time > ?", cursor)
		}
		if err := q.Find(&pingRecords).Error; err != nil {
			return err
		}
		if len(pingRecords) == 0 {
			break
		}

		lastPage := len(pingRecords) < batchSize

		process := pingRecords
		if !lastPage {
			maxT := pingRecords[len(pingRecords)-1].Time.ToTime()
			k := len(pingRecords)
			for k > 0 && pingRecords[k-1].Time.ToTime().Equal(maxT) {
				k--
			}
			if k > 0 {
				process = pingRecords[:k]
			}
		}

		allPoints := make([]metric.Point, 0, len(process))
		for _, rec := range process {
			allPoints = append(allPoints, metric.Point{
				MetricName: MetricPingLatency,
				EntityID:   rec.Client,
				Timestamp:  rec.Time.ToTime(),
				Value:      float64(rec.Value),
				Tags:       map[string]string{"task_id": fmt.Sprintf("%d", rec.TaskId)},
			})
		}

		if err := s.WriteBatch(ctx, allPoints); err != nil {
			log.Printf("Failed to write batch of %d ping points: %v", len(allPoints), err)
			return err
		}

		progress.MigratedPing += int64(len(process))
		cursor = process[len(process)-1].Time
		hasCursor = true
		log.Printf("Migrated %d/%d ping records", progress.MigratedPing, progress.TotalPingRecords)

		if lastPage {
			break
		}
	}

	return nil
}

// DeleteLegacyTables 删除旧的 SQLite 表（迁移完成后调用）
func DeleteLegacyTables() error {
	db := dbcore.GetDBInstance()

	tables := []string{
		"records",
		"records_long_term",
		"gpu_records",
		"gpu_records_long_term",
		"ping_records",
	}

	for _, table := range tables {
		log.Printf("Dropping legacy table: %s", table)
		if err := db.Migrator().DropTable(table); err != nil {
			log.Printf("Failed to drop table %s: %v", table, err)
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
	}

	log.Println("Successfully deleted all legacy tables")
	return nil
}

// GetMigrationStatus 获取迁移状态
func GetMigrationStatus() (string, error) {
	status, err := config.GetAs[string](MetricMigrationStatusKey, "not_started")
	return status, err
}

// EstimateMigrationSize 估算需要迁移的数据量
func EstimateMigrationSize() (map[string]int64, error) {
	db := dbcore.GetDBInstance()
	result := make(map[string]int64)

	tables := []string{"records", "records_long_term", "gpu_records", "gpu_records_long_term", "ping_records"}

	for _, table := range tables {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			// 表可能不存在
			result[table] = 0
			continue
		}
		result[table] = count
	}

	return result, nil
}
