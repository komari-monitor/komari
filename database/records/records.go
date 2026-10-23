package records

import (
	"context"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/metricstore"
)

// 历史监控数据已完全迁移到 metric store（默认 SQLite ./data/metrics.db，或配置的
// MySQL/PostgreSQL）。旧的 records / records_long_term / gpu_records / ping_records
// 表仅在启动一次性迁移时作为数据源读取，运行期的读写全部走 metric store。
//
// 因此本文件不再保留对旧表的任何读写、压缩（rollup）与流量增量修复逻辑：
// metric store 自行管理 rollup 与保留策略，旧表压缩已无意义。

func DeleteAll() error {
	return metricstore.DeleteAllRecords(context.Background())
}

// QueryGPURecords returns GPU records using the caller's cancellation context.
func QueryGPURecords(ctx context.Context, uuid string, start, end time.Time) ([]models.GPURecord, error) {
	return metricstore.GetGPURecordsByClientAndTime(ctx, uuid, start, end)
}

// GetGPURecordsByClientAndTime is a compatibility-only wrapper without cancellation.
// New request handlers should use QueryGPURecords.
func GetGPURecordsByClientAndTime(uuid string, start, end time.Time) ([]models.GPURecord, error) {
	return QueryGPURecords(context.Background(), uuid, start, end)
}

// RecordQueryOptions narrows metric selection and the response point budget.
type RecordQueryOptions = metricstore.RecordQueryOptions

// LoadTypeMetricNames maps the RPC load_type to the stored metrics that back
// that field. An empty result means "all load metrics" (including temp and
// unknown values, which stay on the compatible full-query path).
func LoadTypeMetricNames(loadType string) []string {
	var names []string
	switch loadType {
	case "cpu":
		return []string{metricstore.MetricCPU}
	case "gpu":
		names = []string{metricstore.MetricGPU}
	case "ram":
		names = []string{metricstore.MetricRAM}
	case "swap":
		names = []string{metricstore.MetricSwap}
	case "load":
		names = []string{metricstore.MetricLoad}
	case "disk":
		names = []string{metricstore.MetricDisk}
	case "network":
		names = []string{metricstore.MetricNetIn, metricstore.MetricNetOut, metricstore.MetricNetTotalUp, metricstore.MetricNetTotalDown}
	case "process":
		names = []string{metricstore.MetricProcess}
	case "connections":
		names = []string{metricstore.MetricConnections, metricstore.MetricConnectionsUDP}
	default:
		return nil
	}
	// CPU is the common presence anchor: the previous implementation built one
	// record per timestamp where any load metric existed. Including CPU keeps
	// those timestamps (and zero values for a missing selected metric) without
	// going back to all fifteen metrics.
	return append(names, metricstore.MetricCPU)
}

// QueryRecords returns one client's records, or all clients when uuid is empty.
// Request handlers should use this context-aware entry point.
func QueryRecords(ctx context.Context, uuid string, start, end time.Time, opts ...RecordQueryOptions) ([]models.Record, error) {
	if uuid == "" {
		return metricstore.GetRecordsByTime(ctx, start, end, opts...)
	}
	return metricstore.GetRecordsByClientAndTime(ctx, uuid, start, end, opts...)
}

// GetRecordsByClientAndTime is a compatibility-only wrapper without cancellation.
// New request handlers should use QueryRecords.
func GetRecordsByClientAndTime(uuid string, start, end time.Time, opts ...RecordQueryOptions) ([]models.Record, error) {
	return metricstore.GetRecordsByClientAndTime(context.Background(), uuid, start, end, opts...)
}

// GetRecordMetricMaxByClientAndTime is a compatibility-only wrapper without cancellation.
// New request handlers should use metricstore.GetRecordMetricMaxByClientAndTime.
func GetRecordMetricMaxByClientAndTime(uuid, metricName string, start, end time.Time) ([]models.Record, error) {
	return metricstore.GetRecordMetricMaxByClientAndTime(context.Background(), uuid, metricName, start, end)
}

// GetRecordsByTime is a compatibility-only wrapper without cancellation.
// New request handlers should use QueryRecords with an empty uuid.
func GetRecordsByTime(start, end time.Time, opts ...RecordQueryOptions) ([]models.Record, error) {
	return metricstore.GetRecordsByTime(context.Background(), start, end, opts...)
}
