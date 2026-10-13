package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/metricstore"
	migrationweb "github.com/komari-monitor/komari/web/migration"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func prepareMigrationRetryTest(t *testing.T, dsn string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	config.SetDb(db)
	raw, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if err := config.SetMany(map[string]any{
		metricstore.MetricDBDriverKey: "sqlite",
		metricstore.MetricDBDSNKey:    dsn,
	}); err != nil {
		t.Fatal(err)
	}
}

// The metric database becomes available after the first startup inspection.
// Detection must retry before sending the process into the manual recovery UI,
// and discovering a legacy schema must still require administrator approval.
func TestDatabaseMigrationRequiredRetriesUnavailableStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unavailable")
	path := filepath.Join(dir, "metrics.db")
	prepareMigrationRetryTest(t, "file:"+filepath.ToSlash(path)+"?mode=rw")
	ready := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := os.MkdirAll(dir, 0700); err != nil {
			ready <- err
			return
		}
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			ready <- err
			return
		}
		defer db.Close()
		_, err = db.Exec(`CREATE TABLE metric_definitions (
 name TEXT PRIMARY KEY, type TEXT NOT NULL, unit TEXT NOT NULL,
 description TEXT NOT NULL, retention_days INTEGER NOT NULL,
 metadata TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
 ); INSERT INTO metric_definitions VALUES ('sentinel', 'gauge', '', '', 0, '{}', 1, 1)`)
		ready <- err
	}()
	app := New(Options{})
	requirement, err := app.DatabaseMigrationRequired()
	if setupErr := <-ready; setupErr != nil {
		t.Fatal(setupErr)
	}
	if err != nil {
		t.Fatalf("startup did not retry the unavailable metric database: %v", err)
	}
	if requirement.mode != migrationweb.ModeMetricStructure {
		t.Fatalf("mode = %q, want structure upgrade guide", requirement.mode)
	}
	if metricstore.GetStore() != nil {
		t.Fatal("inspection initialized a store before administrator-approved migration")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM metric_definitions WHERE name = 'sentinel'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy definition changed: count=%d error=%v", count, err)
	}
	var schema string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE name = 'metric_definitions'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "name TEXT PRIMARY KEY") || strings.Contains(schema, "id INTEGER") {
		t.Fatalf("legacy schema was rebuilt: %s", schema)
	}
}

func TestDatabaseMigrationRequiredPermanentFailureRemainsRecoverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "metrics.db")
	prepareMigrationRetryTest(t, "file:"+filepath.ToSlash(path)+"?mode=rw")
	requirement, err := New(Options{}).DatabaseMigrationRequired()
	if err == nil {
		t.Fatal("unavailable database unexpectedly succeeded")
	}
	if requirement.Required() {
		t.Fatal("failed inspection selected a migration")
	}
	if metricstore.GetStore() != nil {
		t.Fatal("failed inspection installed a store")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed inspection created a database: %v", err)
	}
}

func TestRetryMetricStoreConnectionDoesNotRunAfterSuccess(t *testing.T) {
	calls := 0
	if err := retryMetricStoreConnection(3, 0, func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
