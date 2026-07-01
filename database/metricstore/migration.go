package metricstore

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
	"gorm.io/gorm"
)

// 迁移游标与进度配置键
const (
	MigrationCursorRecordsKey         = "metric_migration_cursor_records"
	MigrationCursorRecordsLongTermKey = "metric_migration_cursor_records_long_term"
	MigrationCursorPingKey            = "metric_migration_cursor_ping"
	MigrationPhaseKey                 = "metric_migration_phase" // current migration phase
	// 已迁移计数（持久化，用于重启后/暂停后展示进度）
	MigrationProgressRecordsKey = "metric_migration_progress_records"
	MigrationProgressPingKey    = "metric_migration_progress_ping"
)

// MigrationPhase 迁移阶段
type MigrationPhase string

const (
	PhaseRecords         MigrationPhase = "records"
	PhaseRecordsLongTerm MigrationPhase = "records_long_term"
	PhasePing            MigrationPhase = "ping"
	PhaseCompleted       MigrationPhase = "completed"
)

// migrationCtx 迁移上下文，用于暂停/恢复控制
var (
	migrationCancelMu sync.Mutex
	migrationCancel   context.CancelFunc
	migrationDone     chan struct{}
)

// MigrationProgress 迁移进度信息
type MigrationProgress struct {
	Status           string         `json:"status"`             // not_started, in_progress, paused, completed, failed
	TotalRecords     int64          `json:"total_records"`      // 总记录数
	MigratedRecords  int64          `json:"migrated_records"`   // 已迁移记录数
	TotalGPURecords  int64          `json:"total_gpu_records"`  // 总GPU记录数
	MigratedGPU      int64          `json:"migrated_gpu"`       // 已迁移GPU记录数
	TotalPingRecords int64          `json:"total_ping_records"` // 总Ping记录数
	MigratedPing     int64          `json:"migrated_ping"`      // 已迁移Ping记录数
	StartTime        time.Time      `json:"start_time"`         // 开始时间
	EndTime          time.Time      `json:"end_time"`           // 结束时间
	Error            string         `json:"error,omitempty"`    // 错误信息
	CurrentPhase     MigrationPhase `json:"current_phase"`      // 当前阶段
	CanResume        bool           `json:"can_resume"`         // 是否可以恢复
}

// saveCursor 持久化某张表的迁移游标（Unix 纳秒时间戳字符串）
func saveCursor(key string, t models.LocalTime) {
	val := fmt.Sprintf("%d", t.ToTime().UnixNano())
	if err := config.Set(key, val); err != nil {
		log.Printf("Failed to save migration cursor (%s): %v", key, err)
	}
}

// loadCursor 从配置中读取游标，返回 (cursor, hasCursor)
func loadCursor(key string) (models.LocalTime, bool) {
	val, err := config.GetAs[string](key, "")
	if err != nil || val == "" {
		return models.LocalTime{}, false
	}
	var nanos int64
	if _, err := fmt.Sscanf(val, "%d", &nanos); err != nil || nanos == 0 {
		return models.LocalTime{}, false
	}
	return models.FromTime(time.Unix(0, nanos)), true
}

// clearCursors 清除所有迁移游标（重新开始迁移时调用）
func clearCursors() {
	for _, key := range []string{
		MigrationCursorRecordsKey, MigrationCursorRecordsLongTermKey,
		MigrationCursorPingKey, MigrationPhaseKey,
		MigrationProgressRecordsKey, MigrationProgressPingKey,
	} {
		_ = config.Set(key, "")
	}
}

// saveProgress 持久化已迁移计数
func saveProgress(key string, count int64) {
	_ = config.Set(key, fmt.Sprintf("%d", count))
}

// loadProgress 从配置读取已迁移计数
func loadProgress(key string) int64 {
	val, _ := config.GetAs[string](key, "0")
	var n int64
	fmt.Sscanf(val, "%d", &n)
	return n
}

// MigrateFromLegacyTables 从旧的 SQLite 表迁移数据到 metric store（支持暂停/恢复）
// batchSize: 每批次迁移的记录数，建议 100-1000
// resume: 为 true 时从上次保存的游标处继续，为 false 时从头重新开始
func MigrateFromLegacyTables(batchSize int, resume bool) (*MigrationProgress, error) {
	if batchSize <= 0 {
		batchSize = 500
	}

	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 检查是否已有迁移在运行
	migrationCancelMu.Lock()
	if migrationCancel != nil {
		migrationCancelMu.Unlock()
		return nil, fmt.Errorf("migration is already in progress")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	migrationCancel = cancel
	migrationDone = done
	migrationCancelMu.Unlock()

	defer func() {
		migrationCancelMu.Lock()
		migrationCancel = nil
		migrationDone = nil
		migrationCancelMu.Unlock()
		close(done)
	}()

	if !resume {
		clearCursors()
	}

	progress := &MigrationProgress{
		Status:    "in_progress",
		StartTime: time.Now(),
	}

	if err := config.Set(MetricMigrationStatusKey, "in_progress"); err != nil {
		log.Printf("Failed to update migration status: %v", err)
	}

	db := dbcore.GetDBInstance()

	// 读取当前阶段，按阶段决定从哪里继续
	currentPhase, _ := config.GetAs[string](MigrationPhaseKey, string(PhaseRecords))

	// 1. 迁移 records 表
	if currentPhase == string(PhaseRecords) || currentPhase == "" {
		config.Set(MigrationPhaseKey, string(PhaseRecords))
		log.Println("Starting migration of records table...")
		if err := migrateRecordTable(ctx, s, db, "records", MigrationCursorRecordsKey, batchSize, progress); err != nil {
			if ctx.Err() != nil {
				// 被暂停
				progress.Status = "paused"
				progress.EndTime = time.Now()
				config.Set(MetricMigrationStatusKey, "paused")
				log.Println("Migration paused at records table")
				return progress, nil
			}
			progress.Status = "failed"
			progress.Error = fmt.Sprintf("failed to migrate records: %v", err)
			progress.EndTime = time.Now()
			config.Set(MetricMigrationStatusKey, "failed")
			return progress, err
		}
		currentPhase = string(PhaseRecordsLongTerm)
		config.Set(MigrationPhaseKey, currentPhase)
	}

	// 2. 迁移 records_long_term 表
	if currentPhase == string(PhaseRecordsLongTerm) {
		log.Println("Starting migration of records_long_term table...")
		if err := migrateRecordTable(ctx, s, db, "records_long_term", MigrationCursorRecordsLongTermKey, batchSize, progress); err != nil {
			if ctx.Err() != nil {
				progress.Status = "paused"
				progress.EndTime = time.Now()
				config.Set(MetricMigrationStatusKey, "paused")
				log.Println("Migration paused at records_long_term table")
				return progress, nil
			}
			progress.Status = "failed"
			progress.Error = fmt.Sprintf("failed to migrate records_long_term: %v", err)
			progress.EndTime = time.Now()
			config.Set(MetricMigrationStatusKey, "failed")
			return progress, err
		}
		currentPhase = string(PhasePing)
		config.Set(MigrationPhaseKey, currentPhase)
	}

	// 3. 迁移 ping_records 表
	if currentPhase == string(PhasePing) {
		log.Println("Starting migration of ping_records table...")
		if err := migratePingRecordsTable(ctx, s, db, batchSize, progress); err != nil {
			if ctx.Err() != nil {
				progress.Status = "paused"
				progress.EndTime = time.Now()
				config.Set(MetricMigrationStatusKey, "paused")
				log.Println("Migration paused at ping_records table")
				return progress, nil
			}
			progress.Status = "failed"
			progress.Error = fmt.Sprintf("failed to migrate ping_records: %v", err)
			progress.EndTime = time.Now()
			config.Set(MetricMigrationStatusKey, "failed")
			return progress, err
		}
	}

	progress.Status = "completed"
	progress.EndTime = time.Now()
	config.Set(MetricMigrationStatusKey, "completed")
	config.Set(MigrationPhaseKey, string(PhaseCompleted))
	clearCursors()

	log.Printf("Migration completed. records=%d, ping=%d, elapsed=%v",
		progress.MigratedRecords, progress.MigratedPing,
		progress.EndTime.Sub(progress.StartTime))

	return progress, nil
}

// PauseMigration 请求暂停正在进行的迁移，等待协程实际退出后返回。
// 如果当前没有迁移在运行，返回 error。
func PauseMigration() error {
	migrationCancelMu.Lock()
	cancel := migrationCancel
	done := migrationDone
	migrationCancelMu.Unlock()

	if cancel == nil {
		return fmt.Errorf("no migration is currently running")
	}

	cancel()

	// 等待迁移协程退出（最多 30s）
	select {
	case <-done:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timed out waiting for migration to pause")
	}
}

// IsMigrationRunning 检查迁移是否正在运行
func IsMigrationRunning() bool {
	migrationCancelMu.Lock()
	defer migrationCancelMu.Unlock()
	return migrationCancel != nil
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
// cursorKey: 游标持久化的配置键（用于暂停后恢复）
//
// 性能优化点：
//  1. 使用 keyset 分页（WHERE time > cursor）替代 OFFSET，避免大偏移量下
//     O(n²) 的行扫描开销——OFFSET N 需要数据库先扫描并丢弃前 N 行。
//  2. 将整批记录的所有采样点累积后一次性 WriteBatch 写入，把每条记录一次
//     数据库写入（含 fsync）合并为每批一次事务，SQLite 单写连接下提升显著。
//
// 暂停/恢复支持：
//   - 每次批次完成后将游标（当前已处理的最后一条记录的时间戳）保存到配置。
//   - 恢复时从配置读取游标继续。
//   - 每次循环检查 ctx.Done()，收到取消信号立即返回（由调用方处理 "paused" 状态）。
func migrateRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string, cursorKey string, batchSize int, progress *MigrationProgress) error {
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

	// 尝试从配置加载上次保存的游标（恢复场景）
	cursor, hasCursor := loadCursor(cursorKey)
	if hasCursor {
		log.Printf("Resuming from cursor: %v", cursor.ToTime())
	}

	for {
		// 检查是否收到暂停信号
		select {
		case <-ctx.Done():
			// 保存当前游标，以便下次恢复
			if hasCursor {
				saveCursor(cursorKey, cursor)
				log.Printf("Paused at cursor: %v (saved)", cursor.ToTime())
			}
			return ctx.Err()
		default:
		}

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
		// 持久化进度计数（重启后可恢复展示）
		saveProgress(MigrationProgressRecordsKey, progress.MigratedRecords)
		cursor = process[len(process)-1].Time
		hasCursor = true

		// 每批次完成后保存游标（支持断点续传）
		saveCursor(cursorKey, cursor)

		log.Printf("Migrated %d/%d total records (cursor: %v)", progress.MigratedRecords, progress.TotalRecords, cursor.ToTime())

		if lastPage {
			break
		}
	}

	return nil
}

// migratePingRecordsTable 迁移 ping_records 表（支持游标持久化和取消）
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

	// 尝试从配置加载上次保存的游标（恢复场景）
	cursor, hasCursor := loadCursor(MigrationCursorPingKey)
	if hasCursor {
		log.Printf("Resuming ping migration from cursor: %v", cursor.ToTime())
	}

	for {
		// 检查是否收到暂停信号
		select {
		case <-ctx.Done():
			if hasCursor {
				saveCursor(MigrationCursorPingKey, cursor)
				log.Printf("Paused ping migration at cursor: %v (saved)", cursor.ToTime())
			}
			return ctx.Err()
		default:
		}

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
		// 持久化进度计数（重启后可恢复展示）
		saveProgress(MigrationProgressPingKey, progress.MigratedPing)
		cursor = process[len(process)-1].Time
		hasCursor = true

		// 每批次完成后保存游标
		saveCursor(MigrationCursorPingKey, cursor)

		log.Printf("Migrated %d/%d ping records (cursor: %v)", progress.MigratedPing, progress.TotalPingRecords, cursor.ToTime())

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

// GetFullMigrationStatus 返回完整的迁移状态（含持久化进度），用于前端进度展示。
// 关键逻辑：处理服务器重启后的孤儿 in_progress 状态。
//   - 如果配置中 status 是 in_progress 但内存中迁移未运行，说明服务器中途重启。
//     此时检查游标是否存在：
//     * 有游标 → 迁移被意外中断，自动转为 paused，用户可恢复。
//     * 无游标 → 数据不可靠，重置为 not_started（极少数情况）。
func GetFullMigrationStatus() *MigrationProgress {
	progress := &MigrationProgress{}

	// 读取状态
	status, _ := config.GetAs[string](MetricMigrationStatusKey, "not_started")
	progress.Status = status

	// 内存中是否有迁移正在运行
	running := IsMigrationRunning()

	// 服务器重启后孤儿 in_progress 修复
	if status == "in_progress" && !running {
		// 检查是否有游标（说明迁移被中断，可以恢复）
		hasCursor, _ := config.GetAs[string](MigrationCursorRecordsKey, "")
		hasPingCursor, _ := config.GetAs[string](MigrationCursorPingKey, "")
		if hasCursor != "" || hasPingCursor != "" {
			progress.Status = "paused"
			progress.CanResume = true
			log.Println("Detected orphan in_progress status after restart; auto-paused for resume")
		} else {
			// 无游标，数据不可信，重置
			progress.Status = "not_started"
			_ = config.Set(MetricMigrationStatusKey, "not_started")
			log.Println("Detected orphan in_progress with no cursor; reset to not_started")
		}
	}

	progress.CanResume = progress.Status == "paused"
	phaseStr, _ := config.GetAs[string](MigrationPhaseKey, string(PhaseRecords))
	progress.CurrentPhase = MigrationPhase(phaseStr)

	// 从持久化存储中读取已迁移计数
	progress.MigratedRecords = loadProgress(MigrationProgressRecordsKey)
	progress.MigratedPing = loadProgress(MigrationProgressPingKey)

	// 估算总量（records + records_long_term 合计，ping 单独）
	db := dbcore.GetDBInstance()
	var recCount, ltCount, pingCount int64
	db.Table("records").Count(&recCount)
	db.Table("records_long_term").Count(&ltCount)
	db.Table("ping_records").Count(&pingCount)
	progress.TotalRecords = recCount + ltCount
	progress.TotalPingRecords = pingCount

	return progress
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
