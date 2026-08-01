package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const featureManifest = `{"name":"Feat","short":"feat","version":"1.0.0","permissions":{"timeout":5,"allowRoutes":true,"allowHooks":true}}`

func TestHookRequestAndResponse(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ping", func(c *gin.Context) {
		if c.GetHeader("x-hooked") != "yes" {
			c.String(http.StatusBadRequest, "missing x-hooked")
			return
		}
		c.String(http.StatusOK, "pong")
	})
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("request", (req) => {
					req.headers["x-hooked"] = "yes";
				});
				server.hook("response", (req, res) => {
					res.statusCode = 201;
					res.headers["x-res"] = "1";
					res.body = res.body + "|hooked";
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}

	// 用 WrapHandler 包裹（对应 internal/server 的接入点）
	wrapped := WrapHandler(engine)

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "pong|hooked" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("x-res") != "1" {
		t.Fatalf("headers = %v", rec.Header())
	}

	// 卸载后 hook 移除：请求不再带 x-hooked，handler 返回 400
	if err := SetEnabled("feat", false, false); err != nil {
		t.Fatal(err)
	}
	rec2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec2.Code != http.StatusBadRequest || !strings.Contains(rec2.Body.String(), "missing x-hooked") {
		t.Fatalf("after unload status = %d, body = %q", rec2.Code, rec2.Body.String())
	}
}

func TestHookUpgradePassesThrough(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", func(c *gin.Context) {
		c.String(http.StatusOK, "direct")
	})
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("request", (req) => { req.headers["x"] = "y"; });
				server.hook("response", (req, res) => { res.body = "changed"; });
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	WrapHandler(engine).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "direct" {
		t.Fatalf("upgrade passthrough failed: status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

func TestRegisterRPCCallableThroughRegistry(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.registerRPC("plugin:featEcho", (params) => {
					return { echo: params, from: "feat" };
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("feat", false, false) }()

	resp := rpc.CallWithContext(context.Background(), nil, "plugin:featEcho", map[string]any{"x": 1})
	if resp.Error != nil {
		t.Fatalf("rpc error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok || result["from"] != "feat" {
		t.Fatalf("result = %v", resp.Result)
	}
	echo, ok := result["echo"].(map[string]any)
	if !ok || fmt.Sprint(echo["x"]) != "1" {
		t.Fatalf("echo = %v", result["echo"])
	}

	// 卸载后方法注销
	if err := SetEnabled("feat", false, false); err != nil {
		t.Fatal(err)
	}
	resp2 := rpc.CallWithContext(context.Background(), nil, "plugin:featEcho", nil)
	if resp2.Error == nil || resp2.Error.Code != rpc.MethodNotFound {
		t.Fatalf("expected MethodNotFound after unload, got %v", resp2.Error)
	}
}

func TestRegisterRPCErrorCarriesCode(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.registerRPC("plugin:featFail", () => {
					const err = new Error("boom");
					err.code = -32045;
					err.data = { detail: "x" };
					throw err;
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("feat", false, false) }()

	resp := rpc.CallWithContext(context.Background(), nil, "plugin:featFail", nil)
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != -32045 || resp.Error.Message != "boom" {
		t.Fatalf("error = %+v", resp.Error)
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok || data["detail"] != "x" {
		t.Fatalf("error data = %v", resp.Error.Data)
	}
}

func TestDeleteRemovesPlugin(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js":          `function load() { console.log("hi"); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	if err := Delete("feat"); err != nil {
		t.Fatal(err)
	}
	if infos := List(); len(infos) != 0 {
		t.Fatalf("List = %+v", infos)
	}
	if _, err := Manifest("feat"); err == nil {
		t.Fatal("manifest should be gone")
	}
	if err := Delete("feat"); err == nil {
		t.Fatal("second delete should fail")
	}
}

func TestConfigurationSaveAndGet(t *testing.T) {
	withTempDataDir(t)
	Init(gin.New())

	if err := SaveConfiguration("feat", map[string]any{"greeting": "hello", "n": float64(3)}); err != nil {
		t.Fatal(err)
	}
	values, err := GetConfiguration("feat")
	if err != nil {
		t.Fatal(err)
	}
	if values["greeting"] != "hello" || values["n"] != float64(3) {
		t.Fatalf("values = %v", values)
	}
	// 覆盖保存
	if err := SaveConfiguration("feat", map[string]any{"greeting": "bye"}); err != nil {
		t.Fatal(err)
	}
	values, err = GetConfiguration("feat")
	if err != nil {
		t.Fatal(err)
	}
	if values["greeting"] != "bye" || len(values) != 1 {
		t.Fatalf("values = %v", values)
	}
}

// TestGetConfigurationMergesManifestDefaults 验证与主题一致的行为：未保存的
// key 返回 manifest 声明的默认值（select 取首个选项、number->0、switch->false、
// string->""），已保存的 key 保留保存值。
func TestGetConfigurationMergesManifestDefaults(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Cfg","short":"cfg","version":"1.0.0","configuration":{"type":"managed","data":[
			{"key":"greeting","name":"Greeting","type":"string","default":"Hello"},
			{"key":"count","name":"Count","type":"number"},
			{"key":"enabled","name":"Enabled","type":"switch","default":true},
			{"key":"mode","name":"Mode","type":"select","options":"json,text"},
			{"key":"note","name":"Note","type":"string"}
		]}}`,
		"script.js": `function load() {}`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}

	// 未保存任何值时，全部返回默认值。
	values, err := GetConfiguration("cfg")
	if err != nil {
		t.Fatal(err)
	}
	if values["greeting"] != "Hello" {
		t.Errorf("greeting default = %v", values["greeting"])
	}
	if values["count"] != float64(0) {
		t.Errorf("count default = %v", values["count"])
	}
	if values["enabled"] != true {
		t.Errorf("enabled default = %v", values["enabled"])
	}
	if values["mode"] != "json" {
		t.Errorf("mode default = %v", values["mode"])
	}
	if values["note"] != "" {
		t.Errorf("note default = %v", values["note"])
	}

	// 保存部分值：缺失 key 仍回退默认值，已保存 key 不被默认覆盖。
	if err := SaveConfiguration("cfg", map[string]any{"greeting": "saved"}); err != nil {
		t.Fatal(err)
	}
	values, err = GetConfiguration("cfg")
	if err != nil {
		t.Fatal(err)
	}
	if values["greeting"] != "saved" {
		t.Errorf("greeting saved = %v", values["greeting"])
	}
	if values["mode"] != "json" {
		t.Errorf("mode still default = %v", values["mode"])
	}
}
func TestGetConfigFromScript(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	if err := SaveConfiguration("feat", map[string]any{"greeting": "saved-value"}); err != nil {
		t.Fatal(err)
	}
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.route("GET", "/cfg", async (req, res) => {
					const cfg = await server.getConfig();
					res.end(JSON.stringify(cfg));
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("feat", false, false) }()

	rec := httptest.NewRecorder()
	engine := global.engine
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cfg", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["greeting"] != "saved-value" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestResolvePublicFileScopedToEnabledPublicPages(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Pub","short":"pub","version":"1.0.0","pages":[{"file":"pages/pub.html","title":"Pub","visibility":"public"},{"file":"admin.html","title":"Admin"}]}`,
		"script.js":          `function load() {}`,
		"pages/pub.html":     `<h1>pub</h1>`,
		"pages/pub.js":       `console.log("asset")`,
		"admin.html":         `<h1>admin</h1>`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}

	// 未启用：公开文件不可访问。
	if _, err := ResolvePublicFile("pub", "pages/pub.html"); err == nil {
		t.Fatal("disabled plugin public page must not resolve")
	}

	// 启用后：页面与同目录资源可访问，admin 页面与目录外文件不可访问。
	if err := SetEnabled("pub", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePublicFile("pub", "pages/pub.html"); err != nil {
		t.Fatalf("public page must resolve when enabled: %v", err)
	}
	if _, err := ResolvePublicFile("pub", "pages/pub.js"); err != nil {
		t.Fatalf("relative asset must resolve: %v", err)
	}
	for _, bad := range []string{"admin.html", "script.js", "../evil"} {
		if _, err := ResolvePublicFile("pub", bad); err == nil {
			t.Fatalf("ResolvePublicFile(%q) should fail", bad)
		}
	}
}
func TestResolveFileRejectsTraversal(t *testing.T) {
	withTempDataDir(t)
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js":          `function load() {}`,
		"pages/hello.html":   `<h1>hello</h1>`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	full, err := ResolveFile("feat", "pages/hello.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(full, "hello.html") {
		t.Fatalf("path = %q", full)
	}
	for _, bad := range []string{"../evil", "/abs", "..", "pages/../evil"} {
		if _, err := ResolveFile("feat", bad); err == nil {
			t.Fatalf("ResolveFile(%q) should fail", bad)
		}
	}
}

func TestRouteRequestContextCarriesIdentity(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// 模拟 IdentityMiddleware：写入 principal/role/uuid（与 web/api 一致）
	engine.Use(func(c *gin.Context) {
		c.Set("principal", rpc.NewUserPrincipal("user-1"))
		c.Set("role", "admin")
		c.Set("uuid", "user-1")
		c.Next()
	})
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.route("GET", "/who", (req, res) => {
					res.end(JSON.stringify(req.context));
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("feat", false, false) }()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/who", nil)
	req.RemoteAddr = "203.0.113.9:1234"
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %s", rec.Body.String())
	}
	principal, ok := body["principal"].(map[string]any)
	if !ok || principal["type"] != "user" || principal["user_uuid"] != "user-1" {
		t.Fatalf("principal = %v", body["principal"])
	}
	if body["role"] != "admin" || body["user_uuid"] != "user-1" {
		t.Fatalf("context = %v", body)
	}
	if body["remote_ip"] != "203.0.113.9" {
		t.Fatalf("remote_ip = %v", body["remote_ip"])
	}
}

func TestHookRequestContextHasNetworkOnly(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": featureManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("request", (req) => {
					req.headers["x-ip"] = req.context.remote_ip;
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("feat", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("feat", false, false) }()

	engine.GET("/check", func(c *gin.Context) {
		if c.GetHeader("x-ip") != "198.51.100.7" {
			c.String(http.StatusBadRequest, "bad ip: "+c.GetHeader("x-ip"))
			return
		}
		c.String(http.StatusOK, "ok")
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/check", nil)
	req.RemoteAddr = "198.51.100.7:4567"
	WrapHandler(engine).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestServerModulePermissionEnforcement 验证 server 模块的权限门：
// server.route/server.hook 缺权限时加载失败；server.call 缺权限时 Promise
// reject；registerRPC/getConfig 始终可用；无危险权限的插件无需批准即可启用。
func TestServerModulePermissionEnforcement(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	Init(engine)

	// 无危险权限：直接启用，不要求批准。
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Plain","short":"plain","version":"1.0.0","permissions":{"node":true,"timeout":5}}`,
		"script.js":          `const server = require("server"); function load() { server.registerRPC("plugin:plain", () => ({ok:true})); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("plain", true, false); err != nil {
		t.Fatalf("plain plugin without dangerous permissions must enable without approval: %v", err)
	}
	defer func() { _ = SetEnabled("plain", false, false) }()
	resp := rpc.CallWithContext(context.Background(), nil, "plugin:plain", nil)
	if resp.Error != nil {
		t.Fatalf("default-granted registerRPC failed: %v", resp.Error)
	}

	// server.route 缺 allowRoutes：加载失败并提示权限名。
	zipPath = writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"NoRoute","short":"noroute","version":"1.0.0"}`,
		"script.js": `const server = require("server");
function load() { server.route("GET", "/x", (req, res) => res.end("x")); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("noroute", true, true); err == nil || !strings.Contains(err.Error(), "allowRoutes") {
		t.Fatalf("server.route without allowRoutes must fail loading: %v", err)
	}

	// server.hook 缺 allowHooks：加载失败并提示权限名。
	zipPath = writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"NoHook","short":"nohook","version":"1.0.0"}`,
		"script.js": `const server = require("server");
function load() { server.hook("request", (req) => {}); }`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("nohook", true, true); err == nil || !strings.Contains(err.Error(), "allowHooks") {
		t.Fatalf("server.hook without allowHooks must fail loading: %v", err)
	}

	// server.call 缺 allowSystemRPC：路由可用但 Promise reject。
	zipPath = writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"NoRPC","short":"norpc","version":"1.0.0","permissions":{"allowRoutes":true}}`,
		"script.js": `const server = require("server");
function load() {
	server.route("GET", "/call", async (req, res) => {
		try {
			await server.call("common:getVersion");
			res.end("ok");
		} catch (e) {
			res.end("denied:" + e.message);
		}
	});
}`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("norpc", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("norpc", false, false) }()
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/call", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "allowSystemRPC") {
		t.Fatalf("server.call without allowSystemRPC must reject: status = %d, body = %q", rec.Code, rec.Body.String())
	}
}
