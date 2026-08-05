package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/connection"
)

// wsManifest is a plugin manifest that declares the hooks permission (the
// ws kinds reuse allowHooks).
const wsManifest = `{"name":"Ws","short":"ws","version":"1.0.0","permissions":{"allowHooks":true,"timeout":5}}`

func wsInfo(path string) *connection.ConnInfo {
	return &connection.ConnInfo{ID: 1, Path: path, RemoteIP: "203.0.113.9", UserAgent: "test"}
}

// TestWSHookConnectDenyAndAllow 验证 wsConnect 钩子：返回 {deny:true,reason}
// 拒绝连接，返回 undefined 放行。
func TestWSHookConnectDenyAndAllow(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsConnect", (ctx) => {
					if (ctx.path === "/deny") return { deny: true, reason: "no agents here" };
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	if deny, reason := global.OnConnect(wsInfo("/deny")); !deny || reason != "no agents here" {
		t.Fatalf("deny = %v, reason = %q", deny, reason)
	}
	if deny, reason := global.OnConnect(wsInfo("/api/clients/v2/rpc")); deny || reason != "" {
		t.Fatalf("unmatched path must be allowed, deny = %v, reason = %q", deny, reason)
	}
}

// TestWSHookMessageChainReplaceAndDrop 验证 wsMessage 钩子链：按注册顺序链式
// 传递替换结果，drop 优先于后续钩子。
func TestWSHookMessageChainReplaceAndDrop(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsMessage", (ctx, msg) => {
					if (msg.data === "hello") return { data: "hi" };
				});
				server.hook("wsMessage", (ctx, msg) => {
					if (msg.data === "dropme" || msg.data === "hi") return { drop: true };
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	info := wsInfo("/api/clients/v2/rpc")
	// 第一个钩子改 hello->hi，第二个钩子丢弃 hi：结果应为 drop。
	if _, _, drop := global.OnMessage(info, 1, []byte("hello")); !drop {
		t.Fatal("chained replace followed by drop must drop")
	}
	// 第一个钩子不匹配，第二个钩子丢弃 dropme。
	if _, _, drop := global.OnMessage(info, 1, []byte("dropme")); !drop {
		t.Fatal("direct drop must drop")
	}
	// 两个钩子都不匹配：原样放行。
	typ, data, drop := global.OnMessage(info, 1, []byte("other"))
	if drop || typ != 1 || string(data) != "other" {
		t.Fatalf("pass-through = type %d data %q drop %v", typ, data, drop)
	}
}

// TestWSHookSendReplacesType 验证 wsSend 钩子可以同时替换帧类型与载荷。
func TestWSHookSendReplacesType(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsSend", (ctx, msg) => {
					if (msg.data === "text") return { type: 2, data: "bin" };
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	typ, data, drop := global.OnSend(wsInfo("/api/rpc2"), 1, []byte("text"))
	if drop || typ != 2 || string(data) != "bin" {
		t.Fatalf("send rewrite = type %d data %q drop %v", typ, data, drop)
	}
}

// TestWSHookClose 验证 wsClose 钩子在连接结束时触发。
func TestWSHookClose(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Ws","short":"ws","version":"1.0.0","permissions":{"node":true,"allowHooks":true,"timeout":5}}`,
		"script.js": `
			const fs = require("fs");
			const server = require("server");
			function load() {
				server.hook("wsClose", (ctx) => {
					fs.appendFileSync("closes.txt", ctx.path + "\n");
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	global.OnClose(wsInfo("/api/clients/v2/rpc"))
	global.OnClose(wsInfo("/api/clients/v2/rpc"))

	data, err := os.ReadFile(filepath.Join(DataDir, "ws", "closes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Count(got, "/api/clients/v2/rpc") != 2 {
		t.Fatalf("wsClose fired %d times: %q", strings.Count(got, "/api/clients/v2/rpc"), got)
	}
}

// TestWSHookPathFiltering 验证 ws kind 的路径 matcher 只拦截匹配端点的连接。
func TestWSHookPathFiltering(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsMessage", "/api/clients/v2/rpc", (ctx, msg) => {
					return { data: "filtered" };
				});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	typ, data, _ := global.OnMessage(wsInfo("/api/clients/v2/rpc"), 1, []byte("x"))
	if string(data) != "filtered" {
		t.Fatalf("matching path not intercepted: %q", data)
	}
	typ, data, _ = global.OnMessage(wsInfo("/api/rpc2"), 1, []byte("x"))
	if typ != 1 || string(data) != "x" {
		t.Fatalf("non-matching path intercepted: type %d data %q", typ, data)
	}
}

// TestWSHookMethodPrefixRejected 验证 ws kind 的 matcher 不接受方法前缀。
func TestWSHookMethodPrefixRejected(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsMessage", "POST /api/x", (ctx, msg) => {});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err == nil || !strings.Contains(err.Error(), "path matcher") {
		t.Fatalf("method prefix must fail loading: %v", err)
	}
}

// TestWSHookRequiresPermission 验证 ws kind 缺 allowHooks 时加载失败（与
// 请求/响应钩子共用同一权限）。
func TestWSHookRequiresPermission(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Ws","short":"ws","version":"1.0.0","permissions":{"timeout":5}}`,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsConnect", (ctx) => {});
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err == nil || !strings.Contains(err.Error(), "allowHooks") {
		t.Fatalf("missing allowHooks must fail loading: %v", err)
	}
}

// TestWSHookUnloadRemovesHooks 验证卸载后 ws 钩子全部移除。
func TestWSHookUnloadRemovesHooks(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsMessage", (ctx, msg) => { return { data: "hooked" }; });
				server.hook("wsConnect", (ctx) => { return { deny: true }; });
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	if deny, _ := global.OnConnect(wsInfo("/x")); !deny {
		t.Fatal("wsConnect must deny while loaded")
	}
	if err := SetEnabled("ws", false, false); err != nil {
		t.Fatal(err)
	}
	if deny, _ := global.OnConnect(wsInfo("/x")); deny {
		t.Fatal("wsConnect must allow after unload")
	}
	typ, data, _ := global.OnMessage(wsInfo("/x"), 1, []byte("plain"))
	if typ != 1 || string(data) != "plain" {
		t.Fatalf("frame hook still active after unload: %q", data)
	}
}

// TestWSHookOversizedFramePassesThrough 验证超过帧上限的载荷不经过钩子。
func TestWSHookOversizedFramePassesThrough(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	Init(gin.New())

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": wsManifest,
		"script.js": `
			const server = require("server");
			function load() {
				server.hook("wsMessage", (ctx, msg) => { return { data: "replaced" }; });
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("ws", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("ws", false, false) }()

	big := make([]byte, maxWSFrameBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	typ, data, _ := global.OnMessage(wsInfo("/x"), 1, big)
	if typ != 1 || len(data) != len(big) {
		t.Fatalf("oversized frame must pass through untouched, got %d bytes", len(data))
	}
}
