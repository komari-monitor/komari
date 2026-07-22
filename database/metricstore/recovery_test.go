package metricstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/komari-monitor/komari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func prepareRecoveryTest(t *testing.T) {
	t.Helper()
	configDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(configDB)

	storeInitMu.Lock()
	storeMigMu.Lock()
	previousClosing := storeClosing
	storeClosing = false
	storeMigMu.Unlock()
	storeMu.Lock()
	previousStore := store
	previousFingerprint := storeFingerprint
	store = nil
	storeFingerprint = ""
	storeMu.Unlock()
	storeInitMu.Unlock()

	t.Cleanup(func() {
		_ = CloseStoreContext(context.Background())
		storeMigMu.Lock()
		storeClosing = previousClosing
		storeMigMu.Unlock()
		storeMu.Lock()
		store = previousStore
		storeFingerprint = previousFingerprint
		storeMu.Unlock()
	})
}

func TestRecoverStoreFailureKeepsCurrentConfig(t *testing.T) {
	prepareRecoveryTest(t)
	if err := config.SetMany(map[string]any{
		MetricDBDriverKey: "sqlite",
		MetricDBDSNKey:    "old.db",
	}); err != nil {
		t.Fatalf("save original config: %v", err)
	}

	missing := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "missing.db")) + "?mode=ro"
	err := RecoverStore(context.Background(), &MetricStoreConfig{Driver: "sqlite", DSN: missing})
	if err == nil {
		t.Fatal("recovery unexpectedly opened a missing read-only database")
	}
	if got, _ := config.GetAs[string](MetricDBDSNKey, ""); got != "old.db" {
		t.Fatalf("DSN changed after failed recovery: %q", got)
	}
	if GetStore() != nil {
		t.Fatal("failed recovery installed a store")
	}
}

func TestRecoverStoreRecordsManualMigrationSource(t *testing.T) {
	prepareRecoveryTest(t)
	dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "recovered.db"))
	t.Cleanup(func() { _ = CloseStoreContext(context.Background()) })
	cfg := &MetricStoreConfig{Driver: "sqlite", DSN: dsn}
	if err := RecoverStore(context.Background(), cfg); err != nil {
		t.Fatalf("recover store: %v", err)
	}
	if GetStore() == nil {
		t.Fatal("successful recovery did not install a store")
	}
	wantTarget := targetFingerprint(cfg)
	if got, _ := config.GetAs[string](MigrationTargetKey, ""); got != wantTarget {
		t.Fatalf("migration target = %q, want %q", got, wantTarget)
	}
}
