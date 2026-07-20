package migrations

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

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

type LegacyMonitoringStats struct {
	Records int64
	GPU     int64
	Ping    int64
}

// LegacyMonitoringSummary describes the legacy source data shown by the
// pre-start upgrade wizard. Row counts deliberately stay separate from the
// estimated metric point count because one legacy row expands into multiple
// metric points.
type LegacyMonitoringSummary struct {
	LoadRows        int64      `json:"load_rows"`
	GPURows         int64      `json:"gpu_rows"`
	LatencyRows     int64      `json:"latency_rows"`
	MonitoringRows  int64      `json:"monitoring_rows"`
	EstimatedPoints int64      `json:"estimated_points"`
	ServerCount     int64      `json:"server_count"`
	RetentionDays   int        `json:"retention_days"`
	OldestAt        *time.Time `json:"oldest_at,omitempty"`
	NewestAt        *time.Time `json:"newest_at,omitempty"`
}

type LegacyMonitoringProgress struct {
	Phase           string `json:"phase"`
	Table           string `json:"table,omitempty"`
	SourceRowsDone  int64  `json:"source_rows_done"`
	SourceRowsTotal int64  `json:"source_rows_total"`
	WrittenPoints   int64  `json:"written_points"`
}

type LegacyMonitoringDeleted struct {
	LoadRows    int64 `json:"load_rows"`
	GPURows     int64 `json:"gpu_rows"`
	LatencyRows int64 `json:"latency_rows"`
}

type legacyMonitoringBounds struct {
	Oldest string `gorm:"column:oldest"`
	Newest string `gorm:"column:newest"`
}

type legacyBatchProgress func(table string, rows, points int64)

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

// LegacyMonitoringMigrationRequired reports whether startup must enter the
// restricted 1.2.7 upgrade flow. Empty legacy tables are handled by the normal
// one-shot migration so fresh installations never see the wizard.
func LegacyMonitoringMigrationRequired(db *gorm.DB) (bool, LegacyMonitoringSummary, error) {
	done, err := appconfig.GetAs[bool](legacyMonitoringMigrationDoneKey, false)
	if err != nil {
		return false, LegacyMonitoringSummary{}, fmt.Errorf("read legacy monitoring migration marker: %w", err)
	}
	summary, err := InspectLegacyMonitoring(db)
	if err != nil {
		return false, LegacyMonitoringSummary{}, err
	}
	return !done && summary.MonitoringRows > 0, summary, nil
}

func InspectLegacyMonitoring(db *gorm.DB) (LegacyMonitoringSummary, error) {
	var summary LegacyMonitoringSummary
	if db == nil {
		return summary, fmt.Errorf("migration database is nil")
	}
	if db.Migrator().HasTable(&models.Client{}) {
		if err := db.Model(&models.Client{}).Count(&summary.ServerCount).Error; err != nil {
			return summary, fmt.Errorf("count monitored servers: %w", err)
		}
	}

	counts := map[string]*int64{
		"records":           &summary.LoadRows,
		"records_long_term": &summary.LoadRows,
		"gpu_records":       &summary.GPURows,
		"ping_records":      &summary.LatencyRows,
	}
	var oldest, newest time.Time
	for _, table := range legacyMonitoringTables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			return summary, fmt.Errorf("count legacy %s: %w", table, err)
		}
		*counts[table] += count
		if count == 0 {
			continue
		}
		var bounds legacyMonitoringBounds
		if err := db.Table(table).Select("MIN(time) AS oldest, MAX(time) AS newest").Scan(&bounds).Error; err != nil {
			return summary, fmt.Errorf("inspect legacy %s time range: %w", table, err)
		}
		tableOldest, err := parseLegacyTimestamp(bounds.Oldest, time.UTC)
		if err != nil {
			return summary, fmt.Errorf("parse legacy %s oldest time: %w", table, err)
		}
		tableNewest, err := parseLegacyTimestamp(bounds.Newest, time.UTC)
		if err != nil {
			return summary, fmt.Errorf("parse legacy %s newest time: %w", table, err)
		}
		if oldest.IsZero() || (!tableOldest.IsZero() && tableOldest.Before(oldest)) {
			oldest = tableOldest
		}
		if newest.IsZero() || tableNewest.After(newest) {
			newest = tableNewest
		}
	}

	summary.MonitoringRows = summary.LoadRows + summary.GPURows + summary.LatencyRows
	summary.EstimatedPoints = summary.LoadRows*15 + summary.GPURows*4 + summary.LatencyRows*2
	if !oldest.IsZero() {
		oldestCopy := oldest
		summary.OldestAt = &oldestCopy
	}
	if !newest.IsZero() {
		newestCopy := newest
		summary.NewestAt = &newestCopy
	}
	if !oldest.IsZero() && !newest.IsZero() {
		retentionEnd := time.Now().UTC()
		if newest.After(retentionEnd) {
			retentionEnd = newest
		}
		summary.RetentionDays = int(math.Ceil(retentionEnd.Sub(oldest).Hours() / 24))
		if summary.RetentionDays == 0 && summary.MonitoringRows > 0 {
			summary.RetentionDays = 1
		}
	}
	return summary, nil
}

// DeleteLegacyMonitoringBefore removes only monitoring history older than the
// cutoff. Clients, users, ping tasks and other business data are untouched.
func DeleteLegacyMonitoringBefore(db *gorm.DB, cutoff time.Time) (LegacyMonitoringDeleted, error) {
	var deleted LegacyMonitoringDeleted
	if db == nil {
		return deleted, fmt.Errorf("migration database is nil")
	}
	if cutoff.IsZero() {
		return deleted, fmt.Errorf("cleanup cutoff is required")
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		for _, table := range legacyMonitoringTables {
			if !tx.Migrator().HasTable(table) {
				continue
			}
			result := tx.Exec("DELETE FROM "+table+" WHERE time < ?", cutoff.UTC())
			if result.Error != nil {
				return fmt.Errorf("delete legacy %s before cutoff: %w", table, result.Error)
			}
			switch table {
			case "records", "records_long_term":
				deleted.LoadRows += result.RowsAffected
			case "gpu_records":
				deleted.GPURows += result.RowsAffected
			case "ping_records":
				deleted.LatencyRows += result.RowsAffected
			}
		}
		return nil
	})
	return deleted, err
}

func MigrateLegacyMonitoring(ctx context.Context, db *gorm.DB, s *metric.Store, progress func(LegacyMonitoringProgress)) (LegacyMonitoringStats, error) {
	if err := metricstore.EnsureBuiltinMetricDefinitions(ctx, s); err != nil {
		return LegacyMonitoringStats{}, fmt.Errorf("ensure built-in metric definitions: %w", err)
	}
	return migrateLegacyMonitoringTables(ctx, db, s, progress)
}

// CompleteLegacyMonitoringMigration removes the legacy tables, runs any
// caller-owned database maintenance, and only then persists the completion
// marker. Keeping the marker last prevents a failed finalization from being
// reported as a completed migration.
func CompleteLegacyMonitoringMigration(db *gorm.DB, finalize func() error) error {
	if err := dropLegacyMonitoringTables(db); err != nil {
		return err
	}
	if finalize != nil {
		if err := finalize(); err != nil {
			return fmt.Errorf("finalize legacy monitoring migration: %w", err)
		}
	}
	if err := appconfig.Set(legacyMonitoringMigrationDoneKey, true); err != nil {
		return fmt.Errorf("mark legacy monitoring migration done: %w", err)
	}
	return nil
}

func runLegacyMonitoringMigration(ctx context.Context, db *gorm.DB, s *metric.Store, done bool, markDone func() error) (LegacyMonitoringStats, error) {
	var stats LegacyMonitoringStats
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

	stats, err := migrateLegacyMonitoringTables(ctx, db, s, nil)
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

func migrateLegacyMonitoringTables(ctx context.Context, db *gorm.DB, s *metric.Store, progress func(LegacyMonitoringProgress)) (LegacyMonitoringStats, error) {
	var stats LegacyMonitoringStats
	summary, err := InspectLegacyMonitoring(db)
	if err != nil {
		return stats, err
	}
	var rowsDone, writtenPoints int64
	emit := func(table string, rows, points int64) {
		rowsDone += rows
		writtenPoints += points
		if progress != nil {
			progress(LegacyMonitoringProgress{
				Phase:           "migrating",
				Table:           table,
				SourceRowsDone:  rowsDone,
				SourceRowsTotal: summary.MonitoringRows,
				WrittenPoints:   writtenPoints,
			})
		}
	}
	if progress != nil {
		emit("", 0, 0)
	}
	for _, table := range []string{"records", "records_long_term"} {
		n, err := migrateLegacyRecordTable(ctx, s, db, table, emit)
		if err != nil {
			return stats, err
		}
		stats.Records += n
	}

	n, err := migrateLegacyGPURecordTable(ctx, s, db, "gpu_records", emit)
	if err != nil {
		return stats, err
	}
	stats.GPU += n

	n, err = migrateLegacyPingRecordTable(ctx, s, db, "ping_records", emit)
	if err != nil {
		return stats, err
	}
	stats.Ping += n

	return stats, nil
}

func migrateLegacyRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string, progress legacyBatchProgress) (int64, error) {
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

		points := make([]metric.Point, 0, len(rows)*15)
		for i := range rows {
			points = append(points, recordToPoints(rows[i].Record)...)
		}
		if err := s.WriteBatch(ctx, points); err != nil {
			return migrated, fmt.Errorf("write legacy %s to metric store: %w", table, err)
		}

		last := rows[len(rows)-1]
		lastRowID = last.RowID
		migrated += int64(len(rows))
		if progress != nil {
			progress(table, int64(len(rows)), int64(len(points)))
		}
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func migrateLegacyGPURecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string, progress legacyBatchProgress) (int64, error) {
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
		if progress != nil {
			progress(table, int64(len(rows)), int64(len(points)))
		}
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func migrateLegacyPingRecordTable(ctx context.Context, s *metric.Store, db *gorm.DB, table string, progress legacyBatchProgress) (int64, error) {
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
		if progress != nil {
			progress(table, int64(len(rows)), int64(len(points)))
		}
	}

	log.Printf("[legacy-migration] imported %d rows from %s", migrated, table)
	return migrated, nil
}

func recordToPoints(rec models.Record) []metric.Point {
	ts := rec.Time
	entityID := rec.Client
	return []metric.Point{
		{MetricName: metricstore.MetricCPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Cpu)},
		{MetricName: metricstore.MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Gpu)},
		{MetricName: metricstore.MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(rec.Ram)},
		{MetricName: metricstore.MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(rec.Swap)},
		{MetricName: metricstore.MetricLoad, EntityID: entityID, Timestamp: ts, Value: float64(rec.Load)},
		{MetricName: metricstore.MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(rec.Disk)},
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
	ts := rec.Time
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
	ts := rec.Time
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
