package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const demoManifest = `{"name":"Demo","short":"demo","version":"1.0.0","komari":">=0.0.1","permissions":{"node":true,"timeout":5,"allowRoutes":true,"allowSystemRPC":true}}`

func TestManagerSwitchRouteCallAndLogs(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js": `
			const server = require("server");
			function load() {
				console.log("demo loaded");
				server.route("GET", "/plug", async (req, res) => {
					const result = await server.call("plugin:testEcho", { x: req.query.x });
					res.setHeader("Content-Type", "application/json");
					res.end(JSON.stringify(result));
				});
			}
			function unload() {
				console.log("demo unloaded");
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}

	// The switch must ask for permission approval on first enable.
	if err := SetEnabled("demo", true, false); err != ErrPermissionApprovalRequired {
		t.Fatalf("first enable: expected permission approval required, got %v", err)
	}
	// Approving permissions and enabling must load the plugin.
	if err := SetEnabled("demo", true, true); err != nil {
		t.Fatal(err)
	}

	// Register a test RPC method the plugin calls through server.call.
	if err := rpc.Register("plugin:testEcho", func(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
		return req.Params, nil
	}); err != nil {
		t.Fatal(err)
	}
	defer rpc.Unregister("plugin:testEcho")

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plug?x=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("route status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("route body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["x"] != "7" {
		t.Fatalf("server.call echo mismatch: %s", rec.Body.String())
	}

	logs := GetLogs("demo")
	if !strings.Contains(logs, "demo loaded") {
		t.Fatalf("logs missing console output: %q", logs)
	}

	// Disabling runs unload() and makes the route slot return 404.
	if err := SetEnabled("demo", false, false); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	engine.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/plug", nil))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after unload, got %d", rec2.Code)
	}
	if logs := GetLogs("demo"); !strings.Contains(logs, "demo unloaded") {
		t.Fatalf("logs missing unload marker: %q", logs)
	}

	// Re-enabling reuses the route slot (approval already stored).
	if err := SetEnabled("demo", true, false); err != nil {
		t.Fatal(err)
	}
	rec3 := httptest.NewRecorder()
	engine.ServeHTTP(rec3, httptest.NewRequest(http.MethodGet, "/plug?x=9", nil))
	if rec3.Code != http.StatusOK || !strings.Contains(rec3.Body.String(), "9") {
		t.Fatalf("reload route status = %d, body = %s", rec3.Code, rec3.Body.String())
	}

	infos := List()
	if len(infos) != 1 || infos[0].Short != "demo" || !infos[0].Enabled || !infos[0].Running {
		t.Fatalf("List mismatch: %+v", infos)
	}
}

func TestSetEnabledFailureAutoDisablesAndRecordsError(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Bad","short":"bad","version":"1.0.0"}`,
		"script.js":          `throw new Error("boom");`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	err := SetEnabled("bad", true, true)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
	st := global.stateStore().get("bad")
	if st.Enabled {
		t.Fatal("failed plugin must be auto-disabled")
	}
	if !strings.Contains(st.LastError, "boom") {
		t.Fatalf("last_error = %q", st.LastError)
	}
	infos := List()
	if len(infos) != 1 || infos[0].Enabled || infos[0].Running || !strings.Contains(infos[0].LastError, "boom") {
		t.Fatalf("List mismatch: %+v", infos)
	}
}

func TestLoadHookFailureIsReported(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Hook","short":"hook","version":"1.0.0"}`,
		"script.js": `function load() {
			throw new Error("loadhook");
		}`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	err := SetEnabled("hook", true, true)
	if err == nil || !strings.Contains(err.Error(), "load() failed") || !strings.Contains(err.Error(), "loadhook") {
		t.Fatalf("unexpected load() error: %v", err)
	}
}

func TestLoadAllSkipsDisabledAndApprovesEnabled(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js":          `function load() { console.log("auto loaded"); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	// Not enabled yet: LoadAll must not load it.
	if err := LoadAll(); err != nil {
		t.Fatal(err)
	}
	if inst := global.instanceFor("demo"); inst != nil {
		t.Fatal("disabled plugin was loaded")
	}

	// Persist an enabled+approved state, then LoadAll must load it.
	hash := approvalPermissionsHash(models.PluginPermissions{AllowRoutes: true, AllowSystemRPC: true})
	global.stateStore().set("demo", PluginState{Enabled: true, ApprovedPermissionsHash: hash})
	if err := LoadAll(); err != nil {
		t.Fatal(err)
	}
	infos := List()
	if len(infos) != 1 || !infos[0].Running {
		t.Fatalf("enabled plugin not running after LoadAll: %+v", infos)
	}
	if logs := GetLogs("demo"); !strings.Contains(logs, "auto loaded") {
		t.Fatalf("logs missing output: %q", logs)
	}
}

func TestLoadAllAutoDisablesUnapprovedPlugin(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js":          `function load() {}`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	// Simulate a previously enabled plugin whose approval was lost.
	global.stateStore().set("demo", PluginState{Enabled: true})
	err := LoadAll()
	if err == nil || !strings.Contains(err.Error(), "approval") {
		t.Fatalf("expected approval error, got %v", err)
	}
	st := global.stateStore().get("demo")
	if st.Enabled {
		t.Fatal("unapproved plugin must be auto-disabled")
	}
	if !strings.Contains(st.LastError, "approval") {
		t.Fatalf("last_error = %q", st.LastError)
	}
}

func TestCloseAllUnloadsEverything(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": demoManifest,
		"script.js":          `function unload() { console.log("bye"); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("demo", true, true); err != nil {
		t.Fatal(err)
	}
	if err := CloseAll(); err != nil {
		t.Fatal(err)
	}
	if inst := global.instanceFor("demo"); inst != nil {
		t.Fatal("plugin still loaded after CloseAll")
	}
	if logs := GetLogs("demo"); !strings.Contains(logs, "bye") {
		t.Fatalf("unload hook not called: %q", logs)
	}
}
