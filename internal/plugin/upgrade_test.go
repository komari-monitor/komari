package plugin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestIsUpgradeRequest 覆盖升级请求判定：真正的协议升级必须同时携带
// Connection: upgrade 记号与 Upgrade 目标协议头（RFC 9110 §7.8）；仅带
// 其中一个头的普通请求（如反代模板给每个请求无条件附加
// Connection: upgrade）不得被视为升级。
func TestIsUpgradeRequest(t *testing.T) {
	cases := []struct {
		name       string
		connection string
		upgrade    string
		want       bool
	}{
		{"websocket upgrade with both headers", "upgrade", "websocket", true},
		{"case insensitive", "Upgrade", "WebSocket", true},
		{"connection list keeps other tokens", "keep-alive, upgrade", "websocket", true},
		{"bare connection upgrade is not an upgrade", "upgrade", "", false},
		{"upgrade header alone is not an upgrade", "", "websocket", false},
		{"plain request", "", "", false},
		{"keep-alive only", "keep-alive", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.connection != "" {
				req.Header.Set("Connection", tc.connection)
			}
			if tc.upgrade != "" {
				req.Header.Set("Upgrade", tc.upgrade)
			}
			if got := isUpgradeRequest(req); got != tc.want {
				t.Fatalf("isUpgradeRequest(Connection=%q, Upgrade=%q) = %v, want %v",
					tc.connection, tc.upgrade, got, tc.want)
			}
		})
	}
}

// TestInjectSurvivesBareConnectionUpgradeHeader 回归：部分反代模板（如 1Panel
// 的 OpenResty 站点模板）会给每个代理请求无条件附加 Connection: upgrade。
// 这类普通页面请求必须照常获得插件 HTML 注入；只有同时携带两个头的真实升级
// 才走放行路径。
func TestInjectSurvivesBareConnectionUpgradeHeader(t *testing.T) {
	withTempDataDir(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/admin/dashboard", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8",
			[]byte("<!DOCTYPE html><html><head></head><body></body></html>"))
	})
	Init(engine)

	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Guard","short":"guard","version":"1.0.0","permissions":{"allowHTMLInject":true,"timeout":5}}`,
		"script.js": `
			const server = require("server");
			function load() {
				server.injectHTML("", "<script>window.__guard = 1;</script>");
			}
		`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled("guard", true, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = SetEnabled("guard", false, false) }()

	handler := HTMLInjectHandler(engine)

	// 仅 Connection: upgrade（反代常见行为）→ 注入照常生效
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("Connection", "upgrade")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "__guard") {
		t.Fatalf("bare Connection: upgrade must not skip injection, body = %q", rec.Body.String())
	}

	// 两个头齐全的真实升级 → 放行，不注入
	req2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req2.Header.Set("Connection", "upgrade")
	req2.Header.Set("Upgrade", "websocket")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), "__guard") {
		t.Fatalf("real upgrade must skip injection, body = %q", rec2.Body.String())
	}
}
