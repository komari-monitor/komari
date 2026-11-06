package plugin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:komari_plugin_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

// withTempDataDir redirects DataDir and StorageDir, resets the global
// manager, and clears the plugin configuration rows shared through the
// process-wide test database, so each test starts from a clean slate.
func withTempDataDir(t *testing.T) {
	t.Helper()
	oldDir := DataDir
	oldStorage := StorageDir
	oldGlobal := global
	DataDir = t.TempDir()
	StorageDir = t.TempDir()
	// SaveConfiguration writes to the shared in-memory database created by
	// TestMain, so leftover rows would leak into later tests (and repeated
	// runs). Clear them before and after each test.
	clearPluginConfigurations(t)
	t.Cleanup(func() { clearPluginConfigurations(t) })
	mgr := &Manager{
		instances: make(map[string]*Instance),
		routes:    make(map[string]map[string]bool),
		logs:      make(map[string]*LogBuffer),
	}
	global = mgr
	t.Cleanup(func() { _ = mgr.closeAll() }) // release runtimes before TempDir removal
	t.Cleanup(func() {
		DataDir = oldDir
		StorageDir = oldStorage
		global = oldGlobal
	})
}

// clearPluginConfigurations removes every stored plugin configuration row.
func clearPluginConfigurations(t *testing.T) {
	t.Helper()
	if err := dbcore.GetDBInstance().
		Where("1 = 1").
		Delete(&models.PluginConfiguration{}).Error; err != nil {
		t.Fatalf("clear plugin configurations: %v", err)
	}
}

// writePluginZip creates a plugin ZIP from a name -> content map.
func writePluginZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range files {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
