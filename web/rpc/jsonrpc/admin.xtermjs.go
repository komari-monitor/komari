package jsonrpc

import (
	"context"
	"errors"
	"strings"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.xtermjs.go
// xterm.js 终端设置 RPC2 方法（admin 命名空间），含类型定义与归一化逻辑。

const (
	defaultXtermJSFontFamily = "'Cascadia Mono', 'Noto Sans SC', monospace"
	defaultXtermJSFontSize   = 16
	defaultXtermJSScrollback = 5000
	defaultXtermJSPadding    = 16
)

// XtermJSSettings stores the admin-configurable xtermjs settings payload.
type XtermJSSettings struct {
	TerminalOptions       *TerminalOptions `json:"terminalOptions"`
	TerminalPadding       *int             `json:"terminalPadding"`
	TransparentBackground bool             `json:"transparentBackground"`
	CustomCss             string           `json:"customCss"`
}

// TerminalOptions stores the xterm.js terminal options.
type TerminalOptions struct {
	CursorBlink     *bool        `json:"cursorBlink"`
	ConvertEol      *bool        `json:"convertEol"`
	FontFamily      string       `json:"fontFamily"`
	FontSize        int          `json:"fontSize"`
	MacOptionIsMeta *bool        `json:"macOptionIsMeta"`
	Scrollback      *int         `json:"scrollback"`
	Theme           *ThemeConfig `json:"theme"`
}

// ThemeConfig contains only the xterm.js theme whitelist from the API contract.
type ThemeConfig struct {
	Foreground                  string   `json:"foreground,omitempty"`
	Background                  string   `json:"background,omitempty"`
	Cursor                      string   `json:"cursor,omitempty"`
	CursorAccent                string   `json:"cursorAccent,omitempty"`
	SelectionForeground         string   `json:"selectionForeground,omitempty"`
	SelectionBackground         string   `json:"selectionBackground,omitempty"`
	SelectionInactiveBackground string   `json:"selectionInactiveBackground,omitempty"`
	Black                       string   `json:"black,omitempty"`
	Red                         string   `json:"red,omitempty"`
	Green                       string   `json:"green,omitempty"`
	Yellow                      string   `json:"yellow,omitempty"`
	Blue                        string   `json:"blue,omitempty"`
	Magenta                     string   `json:"magenta,omitempty"`
	Cyan                        string   `json:"cyan,omitempty"`
	White                       string   `json:"white,omitempty"`
	BrightBlack                 string   `json:"brightBlack,omitempty"`
	BrightRed                   string   `json:"brightRed,omitempty"`
	BrightGreen                 string   `json:"brightGreen,omitempty"`
	BrightYellow                string   `json:"brightYellow,omitempty"`
	BrightBlue                  string   `json:"brightBlue,omitempty"`
	BrightMagenta               string   `json:"brightMagenta,omitempty"`
	BrightCyan                  string   `json:"brightCyan,omitempty"`
	BrightWhite                 string   `json:"brightWhite,omitempty"`
	ExtendedAnsi                []string `json:"extendedAnsi,omitempty"`
}

func init() {
	reg("getXtermjsSettings", adminGetXtermjs, "Get xterm.js terminal settings")
	RegisterWithGroupAndMeta("setXtermjsSettings", rpc.RoleAdmin, adminSetXtermjs, &rpc.MethodMeta{
		Name:    "admin:setXtermjsSettings",
		Summary: "Set xterm.js terminal settings",
	})
}

func adminGetXtermjs(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	defaultSettings := defaultXtermJSSettings()
	settings, err := config.GetAs[XtermJSSettings](config.XtermjsSettingsKey, defaultSettings)
	if err != nil {
		_ = config.Set(config.XtermjsSettingsKey, defaultSettings)
		settings = defaultSettings
	}
	normalized, err := normalizeXtermJSSettings(settings)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get xtermjs settings: "+err.Error(), nil)
	}
	return normalized, nil
}

func adminSetXtermjs(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var settings XtermJSSettings
	if err := req.BindParams(&settings); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing request body: "+err.Error(), nil)
	}
	normalized, err := normalizeXtermJSSettings(settings)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid xtermjs settings: "+err.Error(), nil)
	}
	if err := config.Set(config.XtermjsSettingsKey, normalized); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to save settings: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "update xtermjs settings", "info")
	return normalized, nil
}

func defaultXtermJSSettings() XtermJSSettings {
	return XtermJSSettings{
		TerminalOptions: &TerminalOptions{
			CursorBlink:     boolPtr(true),
			ConvertEol:      boolPtr(true),
			FontFamily:      defaultXtermJSFontFamily,
			FontSize:        defaultXtermJSFontSize,
			MacOptionIsMeta: boolPtr(true),
			Scrollback:      intPtr(defaultXtermJSScrollback),
			Theme:           nil,
		},
		TerminalPadding:       intPtr(defaultXtermJSPadding),
		TransparentBackground: false,
		CustomCss:             "",
	}
}

func normalizeXtermJSSettings(input XtermJSSettings) (XtermJSSettings, error) {
	normalized := defaultXtermJSSettings()

	if input.TerminalOptions != nil {
		options := *input.TerminalOptions
		options.CursorBlink = boolOrDefault(input.TerminalOptions.CursorBlink, true)
		options.ConvertEol = boolOrDefault(input.TerminalOptions.ConvertEol, true)
		options.MacOptionIsMeta = boolOrDefault(input.TerminalOptions.MacOptionIsMeta, true)

		if fontFamily := strings.TrimSpace(input.TerminalOptions.FontFamily); fontFamily != "" {
			options.FontFamily = fontFamily
		} else {
			options.FontFamily = defaultXtermJSFontFamily
		}

		if input.TerminalOptions.FontSize >= 1 {
			options.FontSize = input.TerminalOptions.FontSize
		} else {
			options.FontSize = defaultXtermJSFontSize
		}

		if input.TerminalOptions.Scrollback != nil && *input.TerminalOptions.Scrollback >= 0 {
			options.Scrollback = intPtr(*input.TerminalOptions.Scrollback)
		} else {
			options.Scrollback = intPtr(defaultXtermJSScrollback)
		}

		theme, err := normalizeThemeConfig(input.TerminalOptions.Theme)
		if err != nil {
			return XtermJSSettings{}, err
		}
		options.Theme = theme

		normalized.TerminalOptions = &options
	}

	if input.TerminalPadding != nil && *input.TerminalPadding >= 0 {
		normalized.TerminalPadding = intPtr(*input.TerminalPadding)
	}
	normalized.TransparentBackground = input.TransparentBackground
	normalized.CustomCss = input.CustomCss

	return normalized, nil
}

func normalizeThemeConfig(input *ThemeConfig) (*ThemeConfig, error) {
	if input == nil {
		return nil, nil
	}

	normalized := &ThemeConfig{
		Foreground:                  cleanThemeColor(input.Foreground),
		Background:                  cleanThemeColor(input.Background),
		Cursor:                      cleanThemeColor(input.Cursor),
		CursorAccent:                cleanThemeColor(input.CursorAccent),
		SelectionForeground:         cleanThemeColor(input.SelectionForeground),
		SelectionBackground:         cleanThemeColor(input.SelectionBackground),
		SelectionInactiveBackground: cleanThemeColor(input.SelectionInactiveBackground),
		Black:                       cleanThemeColor(input.Black),
		Red:                         cleanThemeColor(input.Red),
		Green:                       cleanThemeColor(input.Green),
		Yellow:                      cleanThemeColor(input.Yellow),
		Blue:                        cleanThemeColor(input.Blue),
		Magenta:                     cleanThemeColor(input.Magenta),
		Cyan:                        cleanThemeColor(input.Cyan),
		White:                       cleanThemeColor(input.White),
		BrightBlack:                 cleanThemeColor(input.BrightBlack),
		BrightRed:                   cleanThemeColor(input.BrightRed),
		BrightGreen:                 cleanThemeColor(input.BrightGreen),
		BrightYellow:                cleanThemeColor(input.BrightYellow),
		BrightBlue:                  cleanThemeColor(input.BrightBlue),
		BrightMagenta:               cleanThemeColor(input.BrightMagenta),
		BrightCyan:                  cleanThemeColor(input.BrightCyan),
		BrightWhite:                 cleanThemeColor(input.BrightWhite),
	}

	if len(input.ExtendedAnsi) > 0 {
		normalized.ExtendedAnsi = make([]string, 0, len(input.ExtendedAnsi))
		for _, entry := range input.ExtendedAnsi {
			trimmed := strings.TrimSpace(entry)
			if trimmed == "" {
				return nil, errors.New("extendedAnsi must not contain empty strings")
			}
			normalized.ExtendedAnsi = append(normalized.ExtendedAnsi, trimmed)
		}
	}

	if themeIsEmpty(normalized) {
		return nil, nil
	}

	return normalized, nil
}

func boolPtr(value bool) *bool { return &value }

func intPtr(value int) *int { return &value }

func boolOrDefault(value *bool, defaultValue bool) *bool {
	if value == nil {
		return boolPtr(defaultValue)
	}
	return boolPtr(*value)
}

func cleanThemeColor(value string) string {
	return strings.TrimSpace(value)
}

func themeIsEmpty(theme *ThemeConfig) bool {
	if theme == nil {
		return true
	}
	return theme.Foreground == "" &&
		theme.Background == "" &&
		theme.Cursor == "" &&
		theme.CursorAccent == "" &&
		theme.SelectionForeground == "" &&
		theme.SelectionBackground == "" &&
		theme.SelectionInactiveBackground == "" &&
		theme.Black == "" &&
		theme.Red == "" &&
		theme.Green == "" &&
		theme.Yellow == "" &&
		theme.Blue == "" &&
		theme.Magenta == "" &&
		theme.Cyan == "" &&
		theme.White == "" &&
		theme.BrightBlack == "" &&
		theme.BrightRed == "" &&
		theme.BrightGreen == "" &&
		theme.BrightYellow == "" &&
		theme.BrightBlue == "" &&
		theme.BrightMagenta == "" &&
		theme.BrightCyan == "" &&
		theme.BrightWhite == "" &&
		len(theme.ExtendedAnsi) == 0
}
