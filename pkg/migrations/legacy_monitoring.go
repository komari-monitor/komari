package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"math"
	"sort"
	"strings"
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
	legacyMonitoringInterval         = time.Hour
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
// estimated metric point count because many source rows collapse into one
// hourly P95 point per metric, entity, and tag set.
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

type legacyP95Group struct {
	metricName string
	entityID   string
	tags       map[string]string
	values     []float64
}

type legacyHourlyP95Aggregator struct {
	store        *metric.Store
	partitionKey string
	bucket       time.Time
	groups       map[string]*legacyP95Group
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
		logger.Infof("migration", "Legacy monitoring table migration completed (records=%d, gpu=%d, ping=%d)", stats.Records, stats.GPU, stats.Ping)
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
	if !oldest.IsZero() {
		oldestCopy := oldest
		summary.OldestAt = &oldestCopy
	}
	if !newest.IsZero() {
		newestCopy := newest
		summary.NewestAt = &newestCopy
	}
	if !oldest.IsZero() && !newest.IsZero() {
		estimatedPoints, err := estimateLegacyHourlyP95Points(db, summary, oldest, newest)
		if err != nil {
			return summary, err
		}
		summary.EstimatedPoints = estimatedPoints
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
	n, err := migrateLegacyRecordTables(ctx, s, db, []string{"records", "records_long_term"}, emit)
	if err != nil {
		return stats, err
	}
	stats.Records += n

	n, err = migrateLegacyGPURecordTable(ctx, s, db, "gpu_records", emit)
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

func migrateLegacyRecordTables(ctx context.Context, s *metric.Store, db *gorm.DB, tables []string, progress legacyBatchProgress) (int64, error) {
	var existing []string
	var total int64
	for _, table := range tables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			return 0, fmt.Errorf("count legacy %s: %w", table, err)
		}
		if count > 0 {
			existing = append(existing, table)
			total += count
		}
	}
	if len(existing) == 0 {
		return 0, nil
	}

	parts := make([]string, 0, len(existing))
	for _, table := range existing {
		projection, err := legacyRecordProjection(db, table)
		if err != nil {
			return 0, err
		}
		parts = append(parts, projection)
	}
	query := strings.Join(parts, " UNION ALL ") + " ORDER BY client ASC, time ASC"
	rows, err := db.Raw(query).Rows()
	if err != nil {
		return 0, fmt.Errorf("stream legacy record tables: %w", err)
	}
	defer rows.Close()

	logger.Infof("migration", "[legacy-migration] aggregating %d rows from %s into 1h P95 points", total, strings.Join(existing, ","))
	migrated, err := migrateLegacyStream(ctx, db, s, rows, "records", func() *models.Record { return &models.Record{} }, func(value models.Record) []metric.Point {
		return recordToPoints(value)
	}, progress)
	if err != nil {
		return migrated, fmt.Errorf("aggregate legacy record tables: %w", err)
	}
	logger.Infof("migration", "[legacy-migration] aggregated %d rows from %s", migrated, strings.Join(existing, ","))
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

	rows, err := db.Table(table).Select("*").Order("client ASC, device_index ASC, device_name ASC, time ASC").Rows()
	if err != nil {
		return 0, fmt.Errorf("stream legacy %s: %w", table, err)
	}
	defer rows.Close()
	logger.Infof("migration", "[legacy-migration] aggregating %d rows from %s into 1h P95 points", total, table)
	migrated, err := migrateLegacyStream(ctx, db, s, rows, table, func() *models.GPURecord { return &models.GPURecord{} }, func(value models.GPURecord) []metric.Point {
		return gpuRecordToPoints(value)
	}, progress)
	if err != nil {
		return migrated, fmt.Errorf("aggregate legacy %s: %w", table, err)
	}
	logger.Infof("migration", "[legacy-migration] aggregated %d rows from %s", migrated, table)
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

	rows, err := db.Table(table).Select("*").Order("client ASC, task_id ASC, time ASC").Rows()
	if err != nil {
		return 0, fmt.Errorf("stream legacy %s: %w", table, err)
	}
	defer rows.Close()
	logger.Infof("migration", "[legacy-migration] aggregating %d rows from %s into 1h P95 points", total, table)
	migrated, err := migrateLegacyStream(ctx, db, s, rows, table, func() *models.PingRecord { return &models.PingRecord{} }, func(value models.PingRecord) []metric.Point {
		return pingRecordToPoints(value)
	}, progress)
	if err != nil {
		return migrated, fmt.Errorf("aggregate legacy %s: %w", table, err)
	}
	logger.Infof("migration", "[legacy-migration] aggregated %d rows from %s", migrated, table)
	return migrated, nil
}

func migrateLegacyStream[T any](ctx context.Context, db *gorm.DB, s *metric.Store, rows *sql.Rows, table string, newValue func() *T, toPoints func(T) []metric.Point, progress legacyBatchProgress) (int64, error) {
	aggregator := &legacyHourlyP95Aggregator{store: s}
	var migrated, batchRows, batchPoints int64
	emit := func(force bool) {
		if progress != nil && (batchRows > 0 || force && batchPoints > 0) {
			progress(table, batchRows, batchPoints)
		}
		batchRows = 0
		batchPoints = 0
	}
	for rows.Next() {
		select {
		case <-ctx.Done():
			return migrated, ctx.Err()
		default:
		}
		value := newValue()
		if err := db.ScanRows(rows, value); err != nil {
			return migrated, err
		}
		written, err := aggregator.Add(ctx, toPoints(*value))
		if err != nil {
			return migrated, err
		}
		migrated++
		batchRows++
		batchPoints += written
		if batchRows >= legacyMonitoringBatchSize {
			emit(false)
		}
	}
	if err := rows.Err(); err != nil {
		return migrated, err
	}
	written, err := aggregator.Flush(ctx)
	if err != nil {
		return migrated, err
	}
	batchPoints += written
	emit(true)
	return migrated, nil
}

func legacyRecordProjection(db *gorm.DB, table string) (string, error) {
	columnTypes, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return "", fmt.Errorf("inspect legacy %s columns: %w", table, err)
	}
	available := make(map[string]struct{}, len(columnTypes))
	for _, column := range columnTypes {
		available[strings.ToLower(column.Name())] = struct{}{}
	}
	for _, required := range []string{"client", "time"} {
		if _, ok := available[required]; !ok {
			return "", fmt.Errorf("legacy %s is missing required column %s", table, required)
		}
	}
	columns := []string{
		"client", "time", "cpu", "gpu", "ram", "swap", "load", "disk",
		"net_in", "net_out", "net_total_up", "net_total_down", "traffic_up",
		"traffic_down", "process", "connections", "connections_udp",
	}
	projection := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, ok := available[column]; ok {
			projection = append(projection, column)
		} else {
			projection = append(projection, "0 AS "+column)
		}
	}
	return "SELECT " + strings.Join(projection, ", ") + " FROM " + table, nil
}

func (a *legacyHourlyP95Aggregator) Add(ctx context.Context, points []metric.Point) (int64, error) {
	var written int64
	for _, point := range points {
		bucket := point.Timestamp.UTC().Truncate(legacyMonitoringInterval)
		partition, err := legacyPointPartition(point)
		if err != nil {
			return written, err
		}
		if a.groups != nil && (partition != a.partitionKey || !bucket.Equal(a.bucket)) {
			if partition == a.partitionKey && bucket.Before(a.bucket) {
				return written, fmt.Errorf("legacy points are not ordered within series: %s before %s", bucket, a.bucket)
			}
			n, err := a.Flush(ctx)
			written += n
			if err != nil {
				return written, err
			}
		}
		if a.groups == nil {
			a.partitionKey = partition
			a.bucket = bucket
			a.groups = make(map[string]*legacyP95Group)
		}
		group := a.groups[point.MetricName]
		if group == nil {
			group = &legacyP95Group{
				metricName: point.MetricName,
				entityID:   point.EntityID,
				tags:       cloneLegacyTags(point.Tags),
			}
			a.groups[point.MetricName] = group
		}
		group.values = append(group.values, point.Value)
	}
	return written, nil
}

func (a *legacyHourlyP95Aggregator) Flush(ctx context.Context) (int64, error) {
	if len(a.groups) == 0 {
		return 0, nil
	}
	names := make([]string, 0, len(a.groups))
	for name := range a.groups {
		names = append(names, name)
	}
	sort.Strings(names)
	points := make([]metric.Point, 0, len(names))
	for _, name := range names {
		group := a.groups[name]
		points = append(points, metric.Point{
			MetricName: group.metricName,
			EntityID:   group.entityID,
			Timestamp:  a.bucket,
			Value:      legacyP95(group.values),
			Tags:       group.tags,
		})
	}
	if err := a.store.WriteBatch(ctx, points); err != nil {
		return 0, err
	}
	a.partitionKey = ""
	a.bucket = time.Time{}
	a.groups = nil
	return int64(len(points)), nil
}

func legacyP95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	if len(values) == 1 {
		return values[0]
	}
	position := 0.95 * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func legacyPointPartition(point metric.Point) (string, error) {
	tags, err := json.Marshal(point.Tags)
	if err != nil {
		return "", fmt.Errorf("encode legacy point tags: %w", err)
	}
	return point.EntityID + "\x00" + string(tags), nil
}

func cloneLegacyTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

func estimateLegacyHourlyP95Points(db *gorm.DB, summary LegacyMonitoringSummary, oldest, newest time.Time) (int64, error) {
	type estimate struct {
		Count int64 `gorm:"column:count"`
	}
	countSeries := func(query string) (int64, error) {
		var result estimate
		if err := db.Raw("SELECT COUNT(*) AS count FROM (" + query + ")").Scan(&result).Error; err != nil {
			return 0, err
		}
		return result.Count, nil
	}
	firstHour := oldest.UTC().Truncate(legacyMonitoringInterval)
	lastHour := newest.UTC().Truncate(legacyMonitoringInterval)
	hours := int64(lastHour.Sub(firstHour)/legacyMonitoringInterval) + 1
	if hours < 1 {
		hours = 1
	}

	var recordParts []string
	for _, table := range []string{"records", "records_long_term"} {
		if db.Migrator().HasTable(table) {
			recordParts = append(recordParts, "SELECT client FROM "+table)
		}
	}
	var total int64
	if len(recordParts) > 0 {
		series, err := countSeries(strings.Join(recordParts, " UNION "))
		if err != nil {
			return 0, fmt.Errorf("estimate load series: %w", err)
		}
		total += minLegacyBuckets(summary.LoadRows, series*hours) * 15
	}
	if db.Migrator().HasTable("gpu_records") {
		series, err := countSeries("SELECT client, device_index, device_name FROM gpu_records GROUP BY client, device_index, device_name")
		if err != nil {
			return 0, fmt.Errorf("estimate GPU series: %w", err)
		}
		total += minLegacyBuckets(summary.GPURows, series*hours) * 4
	}
	if db.Migrator().HasTable("ping_records") {
		series, err := countSeries("SELECT client, task_id FROM ping_records GROUP BY client, task_id")
		if err != nil {
			return 0, fmt.Errorf("estimate ping series: %w", err)
		}
		total += minLegacyBuckets(summary.LatencyRows, series*hours) * 2
	}
	return total, nil
}

func minLegacyBuckets(sourceRows, estimatedBuckets int64) int64 {
	if sourceRows < estimatedBuckets {
		return sourceRows
	}
	return estimatedBuckets
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
		logger.Infof("migration", "[legacy-migration] dropping legacy table %s", table)
		if err := db.Migrator().DropTable(table); err != nil {
			return fmt.Errorf("drop legacy table %s: %w", table, err)
		}
	}
	return nil
}
