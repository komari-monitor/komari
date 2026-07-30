package metricstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/pkg/metric"
)

func Compact(ctx context.Context, now time.Time) (int, error) {
	if !compactOperations.TryAcquire() {
		return 0, ErrCompactInProgress
	}
	defer compactOperations.Release()
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return 0, fmt.Errorf("wait for metric store operation before compaction: %w", err)
	}
	defer storeOperations.ReleaseShared()

	storeMu.RLock()
	defer storeMu.RUnlock()
	activeStore := store
	if activeStore == nil {
		return 0, fmt.Errorf("metric store not initialized")
	}
	retryMetricWALCheckpoint(ctx, activeStore)

	defs, err := activeStore.ListMetrics(ctx)
	if err != nil {
		return 0, err
	}
	if len(defs) == 0 {
		compactAt = 0
		return 0, nil
	}
	if compactAt >= len(defs) {
		compactAt = 0
	}

	total, err := activeStore.Flush(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("flush closed metric minutes: %w", err)
	}
	start := compactAt
	failedAt := -1
	var compactErrors []error
	for i := 0; i < len(defs); i++ {
		idx := (start + i) % len(defs)
		n, err := activeStore.CompactMetric(ctx, defs[idx].Name, now)
		if err != nil {
			if failedAt < 0 {
				failedAt = idx
			}
			compactErrors = append(compactErrors, fmt.Errorf("compact metric %q: %w", defs[idx].Name, err))
			continue
		}
		total += n
	}
	if err := finishCompactCycle(ctx, activeStore, now); err != nil {
		compactErrors = append(compactErrors, err)
	}
	if failedAt >= 0 {
		compactAt = failedAt
	} else {
		compactAt = start
	}
	return total, errors.Join(compactErrors...)
}

func finishCompactCycle(ctx context.Context, activeStore *metric.Store, now time.Time) error {
	var compactErrors []error
	if _, err := activeStore.CleanupExpired(ctx, now); err != nil {
		compactErrors = append(compactErrors, fmt.Errorf("clean up expired metric data: %w", err))
	}
	if activeStore.Driver() == metric.DriverSQLite {
		checkpointCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := activeStore.CheckpointWAL(checkpointCtx); err != nil {
			checkpointPending = true
			logger.Warnf("metricstore", "Failed to truncate metric WAL after compaction: %v", err)
		} else {
			checkpointPending = false
		}
		cancel()
	}
	return errors.Join(compactErrors...)
}

func retryMetricWALCheckpoint(ctx context.Context, activeStore *metric.Store) {
	if !checkpointPending {
		return
	}
	if activeStore.Driver() != metric.DriverSQLite {
		checkpointPending = false
		return
	}
	retryCtx, cancel := context.WithTimeout(ctx, checkpointRetryTimeout)
	err := activeStore.CheckpointWAL(retryCtx)
	cancel()
	if err == nil {
		checkpointPending = false
	}
}
