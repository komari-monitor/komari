package metricstore

import (
	"context"
	"database/sql"
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

func TestRecoverStorePersistsLegacyStoreForStructureGuide(t *testing.T) {
	prepareRecoveryTest(t)
	dsn := filepath.ToSlash(filepath.Join(t.TempDir(), "legacy.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE metric_definitions (
		name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL,
		description TEXT NOT NULL, retention_days INTEGER NOT NULL,
		metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`); err != nil {
		_ = db.Close()
		t.Fatalf("create legacy definition table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	cfg := &MetricStoreConfig{Driver: "sqlite", DSN: dsn}
	if err := RecoverStore(context.Background(), cfg); err != nil {
		t.Fatalf("recover legacy store: %v", err)
	}
	if GetStore() != nil {
		t.Fatal("legacy recovery installed the store before structure upgrade")
	}
	if got, _ := config.GetAs[string](MetricDBDSNKey, ""); got != dsn {
		t.Fatalf("saved legacy DSN = %q, want %q", got, dsn)
	}
	required, err := StructureUpgradeRequired(context.Background())
	if err != nil {
		t.Fatalf("detect structure upgrade after recovery: %v", err)
	}
	if !required {
		t.Fatal("legacy recovery did not lead to the structure upgrade guide")
	}
}
