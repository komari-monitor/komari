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
	if err := migrateRecordsTable(ctx, s, db, batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate records: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 2. 迁移 records_long_term 表
	log.Println("Starting migration of records_long_term table...")
	if err := migrateRecordsLongTermTable(ctx, s, db, batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate records_long_term: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 3. 迁移 gpu_records 表
	log.Println("Starting migration of gpu_records table...")
	if err := migrateGPURecordsTable(ctx, s, db, batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate gpu_records: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 4. 迁移 gpu_records_long_term 表
	log.Println("Starting migration of gpu_records_long_term table...")
	if err := migrateGPURecordsLongTermTable(ctx, s, db, batchSize, progress); err != nil {
		progress.Status = "failed"
		progress.Error = fmt.Sprintf("failed to migrate gpu_records_long_term: %v", err)
		progress.EndTime = time.Now()
		config.Set(MetricMigrationStatusKey, "failed")
		return progress, err
	}

	// 5. 迁移 ping_records 表
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

// migrateRecordsTable 迁移 records 表
func migrateRecordsTable(ctx context.Context, s *metric.Store, db *gorm.DB, batchSize int, progress *MigrationProgress) error {
	// 统计总记录数
	var totalCount int64
	if err := db.Table("records").Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalRecords += totalCount

	if totalCount == 0 {
		log.Println("No records to migrate from records table")
		return nil
	}

	log.Printf("Migrating %d records from records table...", totalCount)

	// 分批迁移
	offset := 0
	for {
		var records []models.Record
		err := db.Table("records").
			Order("time ASC").
			Limit(batchSize).
			Offset(offset).
			Find(&records).Error

		if err != nil {
			return err
		}

		if len(records) == 0 {
			break
		}

		// 写入 metric store
		for _, rec := range records {
			if err := WriteRecord(ctx, rec); err != nil {
				log.Printf("Failed to migrate record for client %s at %v: %v", rec.Client, rec.Time.ToTime(), err)
				continue
			}
			progress.MigratedRecords++
		}

		offset += len(records)
		log.Printf("Migrated %d/%d records from records table", progress.MigratedRecords, progress.TotalRecords)

		if len(records) < batchSize {
			break
		}
	}

	return nil
}

// migrateRecordsLongTermTable 迁移 records_long_term 表
func migrateRecordsLongTermTable(ctx context.Context, s *metric.Store, db *gorm.DB, batchSize int, progress *MigrationProgress) error {
	var totalCount int64
	if err := db.Table("records_long_term").Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalRecords += totalCount

	if totalCount == 0 {
		log.Println("No records to migrate from records_long_term table")
		return nil
	}

	log.Printf("Migrating %d records from records_long_term table...", totalCount)

	offset := 0
	for {
		var records []models.Record
		err := db.Table("records_long_term").
			Order("time ASC").
			Limit(batchSize).
			Offset(offset).
			Find(&records).Error

		if err != nil {
			return err
		}

		if len(records) == 0 {
			break
		}

		for _, rec := range records {
			if err := WriteRecord(ctx, rec); err != nil {
				log.Printf("Failed to migrate long-term record for client %s at %v: %v", rec.Client, rec.Time.ToTime(), err)
				continue
			}
			progress.MigratedRecords++
		}

		offset += len(records)
		log.Printf("Migrated %d/%d total records", progress.MigratedRecords, progress.TotalRecords)

		if len(records) < batchSize {
			break
		}
	}

	return nil
}

// migrateGPURecordsTable 迁移 gpu_records 表
func migrateGPURecordsTable(ctx context.Context, s *metric.Store, db *gorm.DB, batchSize int, progress *MigrationProgress) error {
	var totalCount int64
	if err := db.Table("gpu_records").Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalGPURecords += totalCount

	if totalCount == 0 {
		log.Println("No records to migrate from gpu_records table")
		return nil
	}

	log.Printf("Migrating %d GPU records from gpu_records table...", totalCount)

	offset := 0
	for {
		var gpuRecords []models.GPURecord
		err := db.Table("gpu_records").
			Order("time ASC").
			Limit(batchSize).
			Offset(offset).
			Find(&gpuRecords).Error

		if err != nil {
			return err
		}

		if len(gpuRecords) == 0 {
			break
		}

		for _, rec := range gpuRecords {
			if err := WriteGPURecord(ctx, rec); err != nil {
				log.Printf("Failed to migrate GPU record for client %s device %d at %v: %v",
					rec.Client, rec.DeviceIndex, rec.Time.ToTime(), err)
				continue
			}
			progress.MigratedGPU++
		}

		offset += len(gpuRecords)
		log.Printf("Migrated %d/%d GPU records from gpu_records table", progress.MigratedGPU, progress.TotalGPURecords)

		if len(gpuRecords) < batchSize {
			break
		}
	}

	return nil
}

// migrateGPURecordsLongTermTable 迁移 gpu_records_long_term 表
func migrateGPURecordsLongTermTable(ctx context.Context, s *metric.Store, db *gorm.DB, batchSize int, progress *MigrationProgress) error {
	var totalCount int64
	if err := db.Table("gpu_records_long_term").Count(&totalCount).Error; err != nil {
		return err
	}
	progress.TotalGPURecords += totalCount

	if totalCount == 0 {
		log.Println("No records to migrate from gpu_records_long_term table")
		return nil
	}

	log.Printf("Migrating %d GPU records from gpu_records_long_term table...", totalCount)

	offset := 0
	for {
		var gpuRecords []models.GPURecord
		err := db.Table("gpu_records_long_term").
			Order("time ASC").
			Limit(batchSize).
			Offset(offset).
			Find(&gpuRecords).Error

		if err != nil {
			return err
		}

		if len(gpuRecords) == 0 {
			break
		}

		for _, rec := range gpuRecords {
			if err := WriteGPURecord(ctx, rec); err != nil {
				log.Printf("Failed to migrate long-term GPU record for client %s device %d at %v: %v",
					rec.Client, rec.DeviceIndex, rec.Time.ToTime(), err)
				continue
			}
			progress.MigratedGPU++
		}

		offset += len(gpuRecords)
		log.Printf("Migrated %d/%d total GPU records", progress.MigratedGPU, progress.TotalGPURecords)

		if len(gpuRecords) < batchSize {
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

	offset := 0
	for {
		var pingRecords []models.PingRecord
		err := db.Table("ping_records").
			Order("time ASC").
			Limit(batchSize).
			Offset(offset).
			Find(&pingRecords).Error

		if err != nil {
			return err
		}

		if len(pingRecords) == 0 {
			break
		}

		for _, rec := range pingRecords {
			if err := WritePingRecord(ctx, rec); err != nil {
				log.Printf("Failed to migrate ping record for client %s task %d at %v: %v",
					rec.Client, rec.TaskId, rec.Time.ToTime(), err)
				continue
			}
			progress.MigratedPing++
		}

		offset += len(pingRecords)
		log.Printf("Migrated %d/%d ping records", progress.MigratedPing, progress.TotalPingRecords)

		if len(pingRecords) < batchSize {
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
