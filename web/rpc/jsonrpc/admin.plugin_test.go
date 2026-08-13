package jsonrpc

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/plugin"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// TestMain wires a shared in-memory SQLite database for tests that read and
// write plugin configuration through dbcore.
func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:komari_jsonrpc_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

func TestAdminSetPluginEnabledPermissionGate(t *testing.T) {
	withTempPluginDir(t)
	installTestPlugin(t, `{"name":"Demo","short":"demo","version":"1.0.0","permissions":{"allowRoutes":true}}`, "function load() {}")

	request := func(params map[string]any) (any, *rpc.JsonRpcError) {
		return adminSetPluginEnabled(nil, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  "admin:setPluginEnabled",
			Params:  params,
		})
	}

	// Enabling without approval must return requires_approval instead of an error.
	result, jerr := request(map[string]any{"short": "demo", "enabled": true})
	if jerr != nil {
		t.Fatalf("expected requires_approval result, got error: %v", jerr)
	}
	if result == nil || result.(map[string]any)["requires_approval"] != true {
		t.Fatalf("result = %v, want {requires_approval: true}", result)
	}

	// Approving and enabling must succeed, then disabling must unload.
	if _, jerr := request(map[string]any{"short": "demo", "enabled": true, "approved": true}); jerr != nil {
		t.Fatalf("approved enable failed: %v", jerr)
	}
	if _, jerr := request(map[string]any{"short": "demo", "enabled": false}); jerr != nil {
		t.Fatalf("disable failed: %v", jerr)
	}
}

func TestAdminPluginConfigurationRoundTrip(t *testing.T) {
	withTempPluginDir(t)
	installTestPlugin(t, `{"name":"Cfg","short":"cfg","version":"1.0.0","configuration":{"type":"managed","data":[{"key":"greeting","name":"Greeting","type":"string","default":"hi"}]}}`, "function load() {}")

	request := func(params map[string]any) (any, *rpc.JsonRpcError) {
		return adminGetPluginConfiguration(nil, &rpc.JsonRpcRequest{
			Version: rpc.RPC_VERSION,
			Method:  "admin:getPluginConfiguration",
			Params:  params,
		})
	}
	result, jerr := request(map[string]any{"short": "cfg"})
	if jerr != nil {
		t.Fatalf("get failed: %v", jerr)
	}
	cfg, ok := result.(map[string]any)["configuration"].(models.Configuration)
	if !ok || cfg.Type != "managed" {
		t.Fatalf("configuration = %v", result)
	}

	// 保存后读取
	if _, jerr := adminSetPluginConfiguration(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:setPluginConfiguration",
		Params:  map[string]any{"short": "cfg", "data": map[string]any{"greeting": "saved"}},
	}); jerr != nil {
		t.Fatalf("set failed: %v", jerr)
	}
	result, jerr = request(map[string]any{"short": "cfg"})
	if jerr != nil {
		t.Fatalf("get after set failed: %v", jerr)
	}
	data, ok := result.(map[string]any)["data"].(map[string]any)
	if !ok || data["greeting"] != "saved" {
		t.Fatalf("data = %v", result)
	}
}

func TestAdminPluginConfigurationKeepsSavedDataWhenReloadFails(t *testing.T) {
	withTempPluginDir(t)
	installTestPlugin(t, `{"name":"Cfg","short":"cfg","version":"1.0.0","configuration":{"type":"managed","data":[{"key":"greeting","name":"Greeting","type":"string","default":"hi"}]}}`, "function load() {}")

	if err := plugin.SetEnabled("cfg", true, false); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(plugin.DataDir, "cfg", "script.js"), []byte(`function load() { throw new Error("reload-boom"); }`), 0644); err != nil {
		t.Fatal(err)
	}

	_, jerr := adminSetPluginConfiguration(nil, &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION,
		Method:  "admin:setPluginConfiguration",
		Params:  map[string]any{"short": "cfg", "data": map[string]any{"greeting": "saved-before-reload-error"}},
	})
	if jerr == nil || !strings.Contains(jerr.Message, "plugin configuration saved but reload failed") {
		t.Fatalf("expected reload error after saving, got %v", jerr)
	}

	values, err := plugin.GetConfiguration("cfg")
	if err != nil {
		t.Fatal(err)
	}
	if values["greeting"] != "saved-before-reload-error" {
		t.Fatalf("saved configuration was rolled back: %#v", values)
	}
}

// withTempPluginDir redirects plugin.DataDir for the duration of a test.
func withTempPluginDir(t *testing.T) {
	t.Helper()
	old := plugin.DataDir
	plugin.DataDir = t.TempDir()
	t.Cleanup(func() { plugin.DataDir = old })
}

// installTestPlugin writes a one-file plugin ZIP and installs it.
func installTestPlugin(t *testing.T, manifest, script string) {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "plugin.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	files := map[string]string{"komari-plugin.json": manifest}
	if script != "" {
		files["script.js"] = script
	}
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
	if _, err := plugin.InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
}
