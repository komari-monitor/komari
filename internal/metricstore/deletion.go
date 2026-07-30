package metricstore

import (
	"context"
	"fmt"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/pkg/metric"
)

// farFuture 返回一个足够远的未来时间，用于以 DeleteBefore 语义清空某指标的全部数据。
func farFuture() time.Time {
	return time.Now().UTC().Add(24 * 365 * time.Hour)
}

// DeleteAllRecords 删除所有负载/系统类记录（保留指标定义，不含 ping）。
func DeleteAllRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	for _, metricName := range recordMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			logger.Errorf("metricstore", "Failed to delete metric %s: %v", metricName, err)
		}
	}
	clearReportTrafficStates()

	return nil
}

// DeleteAllPingRecords 删除全部 ping 记录（保留指标定义）。
func DeleteAllPingRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, metricName := range pingMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			return fmt.Errorf("failed to delete ping records: %w", err)
		}
	}
	return nil
}

// DeletePingRecordsByTask 删除指定任务（task_id）的全部 ping 记录。
func DeletePingRecordsByTask(ctx context.Context, taskIDs []uint) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, id := range taskIDs {
		for _, metricName := range pingMetricNames {
			if _, err := s.DeleteSeries(ctx, metric.Query{
				MetricName: metricName,
				Tags:       map[string]string{"task_id": fmt.Sprintf("%d", id)},
			}); err != nil {
				return fmt.Errorf("failed to delete ping records for task %d: %w", id, err)
			}
		}
	}
	return nil
}

// DeleteEntity 删除指定 agent 在所有指标下的历史数据。
func DeleteEntity(ctx context.Context, entityID string) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if _, err := s.DeleteEntity(ctx, entityID); err != nil {
		return fmt.Errorf("failed to delete metric records for entity %s: %w", entityID, err)
	}
	deleteReportTrafficState(entityID)
	return nil
}

// DeleteEntityAsync clears one agent's metric history without delaying the
// client deletion response.
func DeleteEntityAsync(entityID string) {
	go func() {
		if err := DeleteEntity(context.Background(), entityID); err != nil {
			logger.Errorf("metricstore", "Failed to delete metric records for entity %s: %v", entityID, err)
		}
	}()
}

// DeleteMetricDataAsync clears disabled metric history without delaying an
// admin retention-policy update response.
func DeleteMetricDataAsync(metricName string) {
	go func() {
		s := GetStore()
		if s == nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: metric store not enabled", metricName)
			return
		}
		if _, err := s.DeleteMetricDataIfDisabled(context.Background(), metricName); err != nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: %v", metricName, err)
		}
	}()
}
