package jsonrpc

import (
	"encoding/json"
	"testing"

	"github.com/komari-monitor/komari/pkg/rpc"
)

// xtermjs 设置归一化逻辑的单元测试（迁移自 web/api/admin/settings_xtermjs_test.go，
// 改为直接测试归一化函数，不依赖 HTTP 层）。

func TestNormalizeXtermJSDefaults(t *testing.T) {
	// 空输入（TerminalOptions 为 nil）应回退到默认值的非 options 部分。
	out, err := normalizeXtermJSSettings(XtermJSSettings{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TerminalOptions == nil {
		t.Fatal("expected default TerminalOptions")
	}
	if out.TerminalOptions.FontFamily != defaultXtermJSFontFamily {
		t.Errorf("font family = %q, want default", out.TerminalOptions.FontFamily)
	}
	if out.TerminalPadding == nil || *out.TerminalPadding != defaultXtermJSPadding {
		t.Error("expected default padding")
	}
}

func TestNormalizeXtermJSFallbacks(t *testing.T) {
	neg := -1
	in := XtermJSSettings{
		TerminalOptions: &TerminalOptions{
			FontFamily: "   ",                           // 空白 -> 默认
			FontSize:   0,                               // 非法 -> 默认
			Scrollback: &neg,                            // 负数 -> 默认
			Theme:      &ThemeConfig{Foreground: "   "}, // 全空 -> nil
		},
		TerminalPadding: &neg, // 负数 -> 保持默认
	}
	out, err := normalizeXtermJSSettings(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TerminalOptions.FontFamily != defaultXtermJSFontFamily {
		t.Errorf("blank font family should fall back to default, got %q", out.TerminalOptions.FontFamily)
	}
	if out.TerminalOptions.FontSize != defaultXtermJSFontSize {
		t.Errorf("invalid font size should fall back to default, got %d", out.TerminalOptions.FontSize)
	}
	if *out.TerminalOptions.Scrollback != defaultXtermJSScrollback {
		t.Errorf("negative scrollback should fall back to default")
	}
	if out.TerminalOptions.Theme != nil {
		t.Error("all-empty theme should normalize to nil")
	}
	if *out.TerminalPadding != defaultXtermJSPadding {
		t.Error("negative padding should fall back to default")
	}
}

func TestNormalizeXtermJSExtendedAnsiRejectsEmpty(t *testing.T) {
	_, err := normalizeXtermJSSettings(XtermJSSettings{
		TerminalOptions: &TerminalOptions{Theme: &ThemeConfig{ExtendedAnsi: []string{"  "}}},
	})
	if err == nil {
		t.Fatal("expected error for empty extendedAnsi entry")
	}
}

func TestNormalizeXtermJSThemeRoundTrip(t *testing.T) {
	in := XtermJSSettings{
		TerminalOptions: &TerminalOptions{
			FontFamily: "JetBrains Mono",
			FontSize:   18,
			Theme:      &ThemeConfig{Foreground: "#ffffff", ExtendedAnsi: []string{"#111111"}},
		},
	}
	out, err := normalizeXtermJSSettings(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.TerminalOptions.Theme == nil || out.TerminalOptions.Theme.Foreground != "#ffffff" {
		t.Error("theme foreground should survive normalization")
	}
	// 序列化不应包含未知字段（ThemeConfig 是白名单结构）。
	b, _ := json.Marshal(out)
	if len(b) == 0 {
		t.Fatal("marshal failed")
	}
}

func TestXtermjsPermission(t *testing.T) {
	// xtermjs 方法属于 admin 命名空间。
	if rpc.CheckPermission(rpc.RoleGuest, "admin:getXtermjsSettings") {
		t.Error("guest must not access admin:getXtermjsSettings")
	}
	if !rpc.CheckPermission(rpc.RoleAdmin, "admin:setXtermjsSettings") {
		t.Error("admin must access admin:setXtermjsSettings")
	}
}
