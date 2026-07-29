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

// UpdateStatistics refreshes query planner statistics for the active store.
// It takes the shared lock because refreshing statistics only samples existing
// rows and is safe to run alongside report writes and compaction; it must still
// keep a store reload from closing the connection underneath it.
//
// UpdateStatistics 刷新当前 store 的查询计划统计信息。它只对已有数据采样,
// 可与上报写入和压缩并发执行,因此取共享锁;但仍需防止 store 重载在执行过程中
// 关闭底层连接。
func UpdateStatistics(ctx context.Context) error {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return fmt.Errorf("wait for metric store operation before updating statistics: %w", err)
	}
	defer storeOperations.ReleaseShared()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return ErrStoreNotInitialized
	}
	return activeStore.UpdateStatistics(ctx)
}

// ReclaimSpace performs the driver-specific physical maintenance operation.
// It takes the exclusive operation lock, so a table/file rewrite cannot run
// concurrently with report writes or compaction.
func ReclaimSpace(ctx context.Context) (MaintenanceResult, error) {
	if !storeOperations.TryAcquire() {
		storeMu.RLock()
		defer storeMu.RUnlock()
		if store == nil {
			return MaintenanceResult{}, ErrStoreNotInitialized
		}
		return MaintenanceResult{
			Driver:          store.Driver(),
			Action:          store.MaintenanceAction(),
			BeforeSizeError: ErrStoreBusy,
			AfterSizeError:  ErrStoreBusy,
		}, ErrStoreBusy
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
	result.Before, result.BeforeSizeError = activeStore.StorageSize(ctx)
	maintenanceErr := activeStore.ReclaimSpace(ctx)
	result.After, result.AfterSizeError = activeStore.StorageSize(ctx)
	return result, maintenanceErr
}
