package metricstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/komari-monitor/komari/pkg/metric"
)

// QueryContext runs a raw read query against the active metric store. The
// caller must invoke the returned release function after closing rows.
func QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, metric.Driver, func(), error) {
	return QueryForDriver(ctx, func(metric.Driver) (string, error) {
		return query, nil
	}, args...)
}

// QueryForDriver runs a raw read query whose SQL depends on the active store's
// driver. The caller must invoke the returned release function after closing rows.
func QueryForDriver(ctx context.Context, query func(metric.Driver) (string, error), args ...any) (*sql.Rows, metric.Driver, func(), error) {
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return nil, "", nil, fmt.Errorf("wait for metric store operations before query: %w", err)
	}

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		storeOperations.ReleaseShared()
		return nil, "", nil, ErrStoreNotInitialized
	}

	statement, err := query(activeStore.Driver())
	if err != nil {
		storeOperations.ReleaseShared()
		return nil, "", nil, err
	}
	rows, err := activeStore.QueryContext(ctx, statement, args...)
	if err != nil {
		storeOperations.ReleaseShared()
		return nil, "", nil, err
	}
	return rows, activeStore.Driver(), storeOperations.ReleaseShared, nil
}

// ExecContext runs a raw statement against the active metric store.
func ExecContext(ctx context.Context, query string, args ...any) (sql.Result, metric.Driver, error) {
	if err := storeOperations.Acquire(ctx); err != nil {
		return nil, "", fmt.Errorf("wait for metric store operations before execution: %w", err)
	}
	defer storeOperations.Release()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return nil, "", ErrStoreNotInitialized
	}

	result, err := activeStore.ExecContext(ctx, query, args...)
	return result, activeStore.Driver(), err
}
