package metricstore

import (
	"context"
	"fmt"
	"time"
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
	total, err := activeStore.Flush(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("flush closed metric minutes: %w", err)
	}
	coarse, err := activeStore.FlushCoarse(ctx, now)
	if err != nil {
		return total, fmt.Errorf("seal closed metric rollups: %w", err)
	}
	return total + coarse, nil
}

// CleanupExpired applies retention in a separate, low-frequency operation.
// It intentionally shares Compact's gate so delete-heavy maintenance never
// overlaps minute or coarse-rollup persistence.
func CleanupExpired(ctx context.Context, now time.Time) (int64, error) {
	if !compactOperations.TryAcquire() {
		return 0, ErrCompactInProgress
	}
	defer compactOperations.Release()
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return 0, fmt.Errorf("wait for metric store operation before retention cleanup: %w", err)
	}
	defer storeOperations.ReleaseShared()

	storeMu.RLock()
	defer storeMu.RUnlock()
	activeStore := store
	if activeStore == nil {
		return 0, fmt.Errorf("metric store not initialized")
	}
	deleted, err := activeStore.CleanupExpired(ctx, now)
	if err != nil {
		return deleted, fmt.Errorf("clean up expired metric data: %w", err)
	}
	return deleted, nil
}
