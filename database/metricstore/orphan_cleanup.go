package metricstore

import (
	"context"
	"fmt"
	"strconv"

	"github.com/komari-monitor/komari/pkg/metric"
)

type OrphanCleanupResult struct {
	Entities  int
	PingTasks int
}

// CleanupOrphanedData removes metric history whose client or ping task no
// longer exists in the main database. It runs before report batching starts.
func CleanupOrphanedData(ctx context.Context, validEntities map[string]struct{}, validPingTasks map[uint]struct{}) (OrphanCleanupResult, error) {
	if err := storeOperations.Acquire(ctx); err != nil {
		return OrphanCleanupResult{}, fmt.Errorf("wait for metric store operations before orphan cleanup: %w", err)
	}
	defer storeOperations.Release()
	storeMu.RLock()
	defer storeMu.RUnlock()
	activeStore := store
	if activeStore == nil {
		return OrphanCleanupResult{}, fmt.Errorf("metric store not initialized")
	}

	result := OrphanCleanupResult{}
	entityIDs, err := activeStore.AllEntityIDs(ctx)
	if err != nil {
		return result, fmt.Errorf("list metric entities: %w", err)
	}
	for _, entityID := range entityIDs {
		if _, exists := validEntities[entityID]; exists {
			continue
		}
		if _, err := activeStore.DeleteEntity(ctx, entityID); err != nil {
			return result, fmt.Errorf("delete orphaned metric entity %s: %w", entityID, err)
		}
		deleteReportTrafficState(entityID)
		result.Entities++
	}

	orphanTaskTags := make(map[string]struct{})
	for _, metricName := range pingMetricNames {
		values, err := activeStore.MetricTagValues(ctx, metricName, "task_id")
		if err != nil {
			return result, fmt.Errorf("list %s task tags: %w", metricName, err)
		}
		for _, value := range values {
			taskID, parseErr := strconv.ParseUint(value, 10, strconv.IntSize)
			if parseErr == nil {
				if _, exists := validPingTasks[uint(taskID)]; exists {
					continue
				}
			}
			orphanTaskTags[value] = struct{}{}
		}
	}
	for taskTag := range orphanTaskTags {
		for _, metricName := range pingMetricNames {
			if _, err := activeStore.DeleteSeries(ctx, metric.Query{
				MetricName: metricName,
				Tags:       map[string]string{"task_id": taskTag},
			}); err != nil {
				return result, fmt.Errorf("delete orphaned ping task %s: %w", taskTag, err)
			}
		}
		result.PingTasks++
	}
	return result, nil
}
