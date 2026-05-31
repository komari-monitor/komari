// Do not use t.Parallel(): config.SetDb mutates package-global state.
package admin

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var xtermJSAuditDBOnce sync.Once

type xtermJSAPIResponse struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func ensureXtermJSAuditDB() {
	xtermJSAuditDBOnce.Do(func() {
		flags.DatabaseType = "sqlite"
		flags.DatabaseFile = "file:xtermjs_audit?mode=memory&cache=shared"
		_ = dbcore.GetDBInstance()
	})
}

func setupXtermJSTestDB(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	testDBName := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	testDSN := fmt.Sprintf("file:%s?mode=memory&cache=shared", testDBName)

	testDB, err := gorm.Open(sqlite.Open(testDSN), &gorm.Config{})
	require.NoError(t, err)

	sqlDB, err := testDB.DB()
	require.NoError(t, err)

	// Initialize the shared audit DB first, then restore the per-test config DB.
	ensureXtermJSAuditDB()
	config.SetDb(testDB)

	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	return testDB, sqlDB
}

func newXtermJSRouter() *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("uuid", "test-uuid")
		c.Next()
	})
	router.GET("/xtermjs", GetXtermJSSettings)
	router.POST("/xtermjs", SetXtermJSSettings)
	return router
}

func performXtermJSRequest(t *testing.T, router *gin.Engine, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	require.NoError(t, err)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func decodeXtermJSResponse(t *testing.T, w *httptest.ResponseRecorder) xtermJSAPIResponse {
	t.Helper()

	var resp xtermJSAPIResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func decodeXtermJSSettings(t *testing.T, raw json.RawMessage) XtermJSSettings {
	t.Helper()

	var settings XtermJSSettings
	require.NoError(t, json.Unmarshal(raw, &settings))
	return settings
}

func ptr[T any](v T) *T {
	return &v
}

func TestGetXtermJSSettingsReturnsDefaultWhenMissing(t *testing.T) {
	testDB, _ := setupXtermJSTestDB(t)
	router := newXtermJSRouter()

	w := performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)
	require.Equal(t, http.StatusOK, w.Code)

	resp := decodeXtermJSResponse(t, w)
	assert.Equal(t, "success", resp.Status)
	assert.Empty(t, resp.Message)

	got := decodeXtermJSSettings(t, resp.Data)
	expected := defaultXtermJSSettings()
	assert.Equal(t, expected, got)

	var stored config.ConfigItem
	require.NoError(t, testDB.First(&stored, "key = ?", config.XtermjsSettingsKey).Error)

	var persisted XtermJSSettings
	require.NoError(t, json.Unmarshal([]byte(stored.Value), &persisted))
	assert.Equal(t, expected, persisted)
}

func TestXtermJSSettingsRoundTripAndThemeVariants(t *testing.T) {
	t.Run("full config round trip", func(t *testing.T) {
		setupXtermJSTestDB(t)
		router := newXtermJSRouter()

		input := XtermJSSettings{
			TerminalOptions: &TerminalOptions{
				CursorBlink:     ptr(false),
				ConvertEol:      ptr(false),
				FontFamily:      "JetBrains Mono",
				FontSize:        18,
				MacOptionIsMeta: ptr(false),
				Scrollback:      ptr(9000),
				Theme: &ThemeConfig{
					Foreground:          "#f8f8f2",
					Background:          "#282a36",
					Cursor:              "#f8f8f2",
					SelectionBackground: "#44475a",
					Blue:                "#6272a4",
					ExtendedAnsi:        []string{"#111111", "#222222"},
				},
			},
			TerminalPadding:       ptr(20),
			TransparentBackground: true,
			CustomCss:             "body { color: red; }",
		}

		body, err := json.Marshal(input)
		require.NoError(t, err)

		w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
		require.Equal(t, http.StatusOK, w.Code)

		resp := decodeXtermJSResponse(t, w)
		assert.Equal(t, "success", resp.Status)
		assert.Equal(t, "settings saved", resp.Message)

		getResp := decodeXtermJSResponse(t, performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil))
		got := decodeXtermJSSettings(t, getResp.Data)
		assert.Equal(t, input, got)
	})

	t.Run("theme null stays null", func(t *testing.T) {
		setupXtermJSTestDB(t)
		router := newXtermJSRouter()

		body := []byte(`{"terminalOptions":{"cursorBlink":true,"convertEol":true,"fontFamily":"JetBrains Mono","fontSize":17,"macOptionIsMeta":true,"scrollback":1000,"theme":null},"terminalPadding":16,"transparentBackground":false,"customCss":"."}`)
		w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
		require.Equal(t, http.StatusOK, w.Code)

		got := decodeXtermJSSettings(t, decodeXtermJSResponse(t, performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)).Data)
		require.NotNil(t, got.TerminalOptions)
		assert.Nil(t, got.TerminalOptions.Theme)
	})

	t.Run("terminalOptions null uses defaults", func(t *testing.T) {
		setupXtermJSTestDB(t)
		router := newXtermJSRouter()

		body := []byte(`{"terminalOptions":null,"terminalPadding":24,"transparentBackground":true,"customCss":"body { background: black; }"}`)
		w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
		require.Equal(t, http.StatusOK, w.Code)

		got := decodeXtermJSSettings(t, decodeXtermJSResponse(t, performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)).Data)
		expected := defaultXtermJSSettings()
		expected.TerminalPadding = ptr(24)
		expected.TransparentBackground = true
		expected.CustomCss = "body { background: black; }"
		assert.Equal(t, expected, got)
	})
}

func TestXtermJSSettingsUnknownThemeFieldsAreDropped(t *testing.T) {
	testDB, _ := setupXtermJSTestDB(t)
	router := newXtermJSRouter()

	body := []byte(`{"terminalOptions":{"fontFamily":"JetBrains Mono","fontSize":16,"theme":{"foreground":"#ffffff","unknownField":"keep-out","extendedAnsi":["#111111"]}}}`)
	w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
	require.Equal(t, http.StatusOK, w.Code)

	resp := decodeXtermJSResponse(t, w)
	assert.Equal(t, "success", resp.Status)

	getResp := decodeXtermJSResponse(t, performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil))
	got := decodeXtermJSSettings(t, getResp.Data)
	require.NotNil(t, got.TerminalOptions)
	require.NotNil(t, got.TerminalOptions.Theme)
	assert.Equal(t, "#ffffff", got.TerminalOptions.Theme.Foreground)

	var stored config.ConfigItem
	require.NoError(t, testDB.First(&stored, "key = ?", config.XtermjsSettingsKey).Error)
	assert.NotContains(t, stored.Value, "unknownField")
}

func TestXtermJSSettingsValidationAndFallbacks(t *testing.T) {
	t.Run("extendedAnsi and body validation", func(t *testing.T) {
		cases := []struct {
			name string
			body []byte
		}{
			{
				name: "empty string",
				body: []byte(`{"terminalOptions":{"theme":{"extendedAnsi":[""]}}}`),
			},
			{
				name: "space string",
				body: []byte(`{"terminalOptions":{"theme":{"extendedAnsi":["   "]}}}`),
			},
			{
				name: "illegal type",
				body: []byte(`{"terminalOptions":{"theme":{"extendedAnsi":[1]}}}`),
			},
			{
				name: "invalid json",
				body: []byte(`{"terminalOptions":`),
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				setupXtermJSTestDB(t)
				router := newXtermJSRouter()

				w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", tc.body)
				require.Equal(t, http.StatusBadRequest, w.Code)

				resp := decodeXtermJSResponse(t, w)
				assert.Equal(t, "error", resp.Status)
				assert.NotEmpty(t, resp.Message)
			})
		}
	})

	t.Run("fallbacks are normalized on save and read", func(t *testing.T) {
		testDB, _ := setupXtermJSTestDB(t)
		router := newXtermJSRouter()

		neg := -1
		body, err := json.Marshal(XtermJSSettings{
			TerminalOptions: &TerminalOptions{
				CursorBlink:     ptr(true),
				ConvertEol:      ptr(true),
				FontFamily:      "   ",
				FontSize:        0,
				MacOptionIsMeta: ptr(true),
				Scrollback:      &neg,
				Theme: &ThemeConfig{
					Foreground: "   ",
				},
			},
			TerminalPadding:       &neg,
			TransparentBackground: true,
			CustomCss:             "body { color: blue; }",
		})
		require.NoError(t, err)

		w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
		require.Equal(t, http.StatusOK, w.Code)

		got := decodeXtermJSSettings(t, decodeXtermJSResponse(t, performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)).Data)
		expected := defaultXtermJSSettings()
		expected.TransparentBackground = true
		expected.CustomCss = "body { color: blue; }"
		assert.Equal(t, expected, got)

		var stored config.ConfigItem
		require.NoError(t, testDB.First(&stored, "key = ?", config.XtermjsSettingsKey).Error)
		var persisted XtermJSSettings
		require.NoError(t, json.Unmarshal([]byte(stored.Value), &persisted))
		assert.Equal(t, expected, persisted)
	})

	t.Run("corrupt JSON is repaired on get", func(t *testing.T) {
		testDB, _ := setupXtermJSTestDB(t)
		router := newXtermJSRouter()

		raw := `{"terminalOptions":`
		require.NoError(t, testDB.Create(&config.ConfigItem{
			Key:   config.XtermjsSettingsKey,
			Value: raw,
		}).Error)

		w := performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)
		require.Equal(t, http.StatusOK, w.Code)

		got := decodeXtermJSSettings(t, decodeXtermJSResponse(t, w).Data)
		expected := defaultXtermJSSettings()
		assert.Equal(t, expected, got)

		var stored config.ConfigItem
		require.NoError(t, testDB.First(&stored, "key = ?", config.XtermjsSettingsKey).Error)
		var persisted XtermJSSettings
		require.NoError(t, json.Unmarshal([]byte(stored.Value), &persisted))
		assert.Equal(t, expected, persisted)
	})
}

func TestXtermJSSettingsNormalizesSeededLegacyConfig(t *testing.T) {
	testDB, _ := setupXtermJSTestDB(t)
	router := newXtermJSRouter()

	seeded := `{"terminalOptions":{"cursorBlink":true,"convertEol":true,"fontFamily":"JetBrains Mono","fontSize":0,"macOptionIsMeta":true,"scrollback":1200,"theme":null},"terminalPadding":12,"transparentBackground":true,"customCss":"seeded"}`
	require.NoError(t, testDB.Create(&config.ConfigItem{
		Key:   config.XtermjsSettingsKey,
		Value: seeded,
	}).Error)

	w := performXtermJSRequest(t, router, http.MethodGet, "/xtermjs", nil)
	require.Equal(t, http.StatusOK, w.Code)

	got := decodeXtermJSSettings(t, decodeXtermJSResponse(t, w).Data)
	expected := XtermJSSettings{
		TerminalOptions: &TerminalOptions{
			CursorBlink:     ptr(true),
			ConvertEol:      ptr(true),
			FontFamily:      "JetBrains Mono",
			FontSize:        16,
			MacOptionIsMeta: ptr(true),
			Scrollback:      ptr(1200),
			Theme:           nil,
		},
		TerminalPadding:       ptr(12),
		TransparentBackground: true,
		CustomCss:             "seeded",
	}
	assert.Equal(t, expected, got)
}

func TestXtermJSSettingsSaveFailureReturns500(t *testing.T) {
	_, sqlDB := setupXtermJSTestDB(t)
	router := newXtermJSRouter()

	require.NoError(t, sqlDB.Close())

	body := []byte(`{"terminalOptions":{"fontFamily":"JetBrains Mono","fontSize":16,"theme":null}}`)
	w := performXtermJSRequest(t, router, http.MethodPost, "/xtermjs", body)
	require.Equal(t, http.StatusInternalServerError, w.Code)

	resp := decodeXtermJSResponse(t, w)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Message, "Failed to save settings:")
}
