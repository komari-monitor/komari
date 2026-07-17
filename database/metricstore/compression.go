package metricstore

import (
	"context"
	"fmt"

	"github.com/komari-monitor/komari/pkg/metric"
)

// InspectCompression reports remote metric-store compression capabilities and
// current state while preventing a concurrent store reload.
func InspectCompression(ctx context.Context) (metric.CompressionStatus, error) {
	if err := storeOperations.Acquire(ctx); err != nil {
		return metric.CompressionStatus{}, fmt.Errorf("wait for metric store operations before compression inspection: %w", err)
	}
	defer storeOperations.Release()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return metric.CompressionStatus{}, ErrStoreNotInitialized
	}
	return activeStore.InspectCompression(ctx)
}

// ConfigureCompression serializes backend DDL with all other exclusive metric
// store maintenance operations.
func ConfigureCompression(ctx context.Context, cfg metric.CompressionConfig) (metric.CompressionStatus, error) {
	if !storeOperations.TryAcquire() {
		return metric.CompressionStatus{}, ErrStoreBusy
	}
	defer storeOperations.Release()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return metric.CompressionStatus{}, ErrStoreNotInitialized
	}
	return activeStore.ConfigureCompression(ctx, cfg)
}
