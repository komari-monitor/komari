package metricstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

func TestInspectAndReclaimStorage(t *testing.T) {
	ctx := context.Background()
	s, err := metric.Open(ctx, metric.SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	installTestStore(t, s)

	info, err := InspectStorage(ctx)
	if err != nil {
		t.Fatalf("inspect storage: %v", err)
	}
	if info.Driver != metric.DriverSQLite || info.Action != metric.MaintenanceVacuum {
		t.Fatalf("unexpected storage info: %#v", info)
	}
	if info.Size != 0 {
		t.Fatalf("in-memory storage size = %d, want 0", info.Size)
	}

	result, err := ReclaimSpace(ctx)
	if err != nil {
		t.Fatalf("reclaim space: %v", err)
	}
	if result.Driver != metric.DriverSQLite || result.Action != metric.MaintenanceVacuum {
		t.Fatalf("unexpected maintenance result: %#v", result)
	}
	if result.BeforeSizeError != nil || result.AfterSizeError != nil {
		t.Fatalf("unexpected size errors: before=%v after=%v", result.BeforeSizeError, result.AfterSizeError)
	}
}

func TestReclaimSpaceReportsBusyStore(t *testing.T) {
	s, err := metric.Open(context.Background(), metric.SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	installTestStore(t, s)

	if err := storeOperations.AcquireShared(context.Background()); err != nil {
		t.Fatalf("acquire shared store operation gate: %v", err)
	}
	defer storeOperations.ReleaseShared()

	result, err := ReclaimSpace(context.Background())
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("reclaim error = %v, want %v", err, ErrStoreBusy)
	}
	if result.Driver != metric.DriverSQLite || result.Action != metric.MaintenanceVacuum {
		t.Fatalf("busy result lost store metadata: %#v", result)
	}
	if !errors.Is(result.BeforeSizeError, ErrStoreBusy) || !errors.Is(result.AfterSizeError, ErrStoreBusy) {
		t.Fatalf("busy result should mark both measurements unavailable: %#v", result)
	}
	compactCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Compact(compactCtx, time.Now()); err != nil {
		t.Fatalf("compact should not wait for report writes: %v", err)
	}
	if !compactOperations.TryAcquire() {
		t.Fatal("acquire compact operation gate")
	}
	defer compactOperations.Release()
	if _, err := Compact(context.Background(), time.Now()); !errors.Is(err, ErrCompactInProgress) {
		t.Fatalf("overlapping compact error = %v, want %v", err, ErrCompactInProgress)
	}
}

func TestInspectStorageRequiresInitializedStore(t *testing.T) {
	storeMu.Lock()
	previous := store
	store = nil
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMu.Lock()
		store = previous
		storeMu.Unlock()
	})

	if _, err := InspectStorage(context.Background()); !errors.Is(err, ErrStoreNotInitialized) {
		t.Fatalf("inspect error = %v, want %v", err, ErrStoreNotInitialized)
	}
}

func TestCloseStoreContextCancelsMigrationBeforeTakingStoreLock(t *testing.T) {
	s, err := metric.Open(context.Background(), metric.SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	installTestStore(t, s)

	migrationCtx, cancelMigration := context.WithCancel(context.Background())
	done := make(chan struct{})
	if !storeOperations.TryAcquire() {
		t.Fatal("acquire store operation gate")
	}
	storeMigMu.Lock()
	storeMigCancel = cancelMigration
	storeMigDone = done
	storeMigMu.Unlock()

	go func() {
		<-migrationCtx.Done()
		storeOperations.Release()
		storeMigMu.Lock()
		storeMigCancel = nil
		storeMigDone = nil
		close(done)
		storeMigMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := CloseStoreContext(ctx); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if migrationCtx.Err() == nil {
		t.Fatal("store migration was not canceled before close")
	}
	if err := Reload(context.Background()); !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("reload after close error = %v, want %v", err, ErrStoreBusy)
	}
}

func TestStoreOperationWaitsRespectContext(t *testing.T) {
	if !storeOperations.TryAcquire() {
		t.Fatal("acquire store operation gate")
	}
	defer storeOperations.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectStorage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspect error = %v, want context canceled", err)
	}
	if err := Reload(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("reload error = %v, want context canceled", err)
	}
	if err := CloseStoreContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close error = %v, want context canceled", err)
	}
}

func installTestStore(t *testing.T, s *metric.Store) {
	t.Helper()
	storeMigMu.Lock()
	previousClosing := storeClosing
	storeClosing = false
	storeMigMu.Unlock()
	storeMu.Lock()
	previous := store
	previousFingerprint := storeFingerprint
	store = s
	storeFingerprint = "test|memory"
	storeMu.Unlock()
	t.Cleanup(func() {
		storeMigMu.Lock()
		storeClosing = previousClosing
		storeMigMu.Unlock()
		storeMu.Lock()
		store = previous
		storeFingerprint = previousFingerprint
		storeMu.Unlock()
		_ = s.Close()
	})
}
