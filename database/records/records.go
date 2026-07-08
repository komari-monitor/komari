package records

import (
	"context"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
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

// GetGPURecordsByClientAndTime 获取 GPU 记录数据。
func GetGPURecordsByClientAndTime(uuid string, start, end time.Time) ([]models.GPURecord, error) {
	return metricstore.GetGPURecordsByClientAndTime(context.Background(), uuid, start, end)
}

func DeleteRecordBefore(before time.Time) error {
	return metricstore.DeleteRecordsBefore(context.Background(), before)
}

func GetRecordsByClientAndTime(uuid string, start, end time.Time) ([]models.Record, error) {
	return metricstore.GetRecordsByClientAndTime(context.Background(), uuid, start, end)
}

// GetRecordsByTime 获取所有客户端在时间范围内的记录。
func GetRecordsByTime(start, end time.Time) ([]models.Record, error) {
	return metricstore.GetRecordsByTime(context.Background(), start, end)
}
