package metricstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/komari-monitor/komari/pkg/metric"
)

var (
	ErrStoreNotInitialized = errors.New("metric store not initialized")
	ErrStoreBusy           = errors.New("metric store is busy")
)

// StorageInfo describes the physical storage owned by the active metric store.
// Size remains useful even when the store points at an external database because
// pkg/metric limits its query to the three tables managed by this Store.
type StorageInfo struct {
	Driver metric.Driver
	Action metric.MaintenanceAction
	Size   int64
}

// MaintenanceResult keeps measurement failures separate from the maintenance
// error. A database may allow table maintenance while denying catalog queries,
// and callers should still be able to report that the operation succeeded.
type MaintenanceResult struct {
	Driver          metric.Driver
	Action          metric.MaintenanceAction
	Before          int64
	After           int64
	BeforeSizeError error
	AfterSizeError  error
}

// InspectStorage reads physical storage information while preventing a store
// reload from closing the active connection underneath the query.
func InspectStorage(ctx context.Context) (StorageInfo, error) {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return StorageInfo{}, fmt.Errorf("wait for metric store operations before inspection: %w", err)
	}
	defer storeOperations.ReleaseShared()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return StorageInfo{}, ErrStoreNotInitialized
	}

	info := StorageInfo{
		Driver: activeStore.Driver(),
		Action: activeStore.MaintenanceAction(),
	}
	size, err := activeStore.StorageSize(ctx)
	info.Size = size
	return info, err
}

// ReclaimSpace performs the driver-specific physical maintenance operation.
// It takes the exclusive operation lock, so a table/file rewrite cannot run
// concurrently with report writes or compaction.
func ReclaimSpace(ctx context.Context) (MaintenanceResult, error) {
	// Reclamation is intentionally non-cancellable. It waits for all active
	// operations, then keeps the store gate until digest conversion, VACUUM,
	// and the final checkpoint have completed.
	_ = ctx
	if err := storeOperations.Acquire(context.Background()); err != nil {
		return MaintenanceResult{}, err
	}
	defer storeOperations.Release()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return MaintenanceResult{}, ErrStoreNotInitialized
	}

	result := MaintenanceResult{
		Driver: activeStore.Driver(),
		Action: activeStore.MaintenanceAction(),
	}
	maintenanceCtx := context.Background()
	result.Before, result.BeforeSizeError = activeStore.StorageSize(maintenanceCtx)
	maintenanceErr := deleteUndefinedMetrics(maintenanceCtx, activeStore)
	if maintenanceErr == nil {
		maintenanceErr = activeStore.ReclaimSpace(maintenanceCtx)
	}
	result.After, result.AfterSizeError = activeStore.StorageSize(maintenanceCtx)
	return result, maintenanceErr
}

func deleteUndefinedMetrics(ctx context.Context, s *metric.Store) error {
	definitions, err := s.ListMetrics(ctx)
	if err != nil {
		return fmt.Errorf("list metric definitions before reclaim: %w", err)
	}

	builtin := make(map[string]struct{}, len(builtinMetricNames))
	for _, name := range builtinMetricNames {
		builtin[name] = struct{}{}
	}
	for _, definition := range definitions {
		if _, ok := builtin[definition.Name]; ok {
			continue
		}
		if err := s.DeleteMetric(ctx, definition.Name); err != nil {
			return fmt.Errorf("delete undefined metric %q before reclaim: %w", definition.Name, err)
		}
	}
	return nil
}
