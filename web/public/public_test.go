package public

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"hyphen language": {
			input: "zh-CN",
			want:  "zh-CN",
		},
		"underscore language": {
			input: "zh_CN",
			want:  "zh-CN",
		},
		"reject script injection": {
			input: `zh-CN" autofocus`,
		},
		"reject too short": {
			input: "z",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHTMLLanguage(tt.input); got != tt.want {
				t.Fatalf("normalizeHTMLLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		html     string
		language string
		want     string
	}{
		"replace existing lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: "zh-CN",
			want:     `<html lang="zh-CN"><head></head></html>`,
		},
		"insert missing lang": {
			html:     `<html><head></head></html>`,
			language: "ja_JP",
			want:     `<html lang="ja-JP"><head></head></html>`,
		},
		"ignore invalid lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: `zh-CN" autofocus`,
			want:     `<html lang="en"><head></head></html>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceHTMLLanguage(tt.html, tt.language); got != tt.want {
				t.Fatalf("replaceHTMLLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFrontendCacheHardeningHelpers(t *testing.T) {
	html := `<html><head><script id="vite-plugin-pwa:register-sw" src="/registerSW.js"></script></head><body></body></html>`
	hardened := injectServiceWorkerCleanup(stripServiceWorkerRegistration(html))
	if strings.Contains(hardened, `vite-plugin-pwa:register-sw`) {
		t.Fatal("service-worker registration was not removed")
	}
	if strings.Count(hardened, serviceWorkerCleanupPath) != 1 {
		t.Fatalf("cleanup script count = %d, want 1", strings.Count(hardened, serviceWorkerCleanupPath))
	}
	if hardened != injectServiceWorkerCleanup(hardened) {
		t.Fatal("cleanup-script injection is not idempotent")
	}

	tests := map[string]bool{
		"/":                           true,
		"/instance/example-node":      true,
		"/settings/profile.html":      true,
		"/assets/stale-build-hash.js": false,
		"/assets/stale-style.css":     false,
		"/assets/logo.png":            false,
	}
	for requestPath, want := range tests {
		if got := shouldServeSPAIndex(requestPath); got != want {
			t.Errorf("shouldServeSPAIndex(%q) = %v, want %v", requestPath, got, want)
		}
	}
}

func TestEmbeddedDistDoesNotEmbedRawFiles(t *testing.T) {
	if _, err := PublicFS.ReadFile("defaultTheme/dist/index.html"); err == nil {
		t.Fatal("PublicFS still embeds the raw frontend files")
	}
	if content, ok := defaultDistFiles[IndexFile]; !ok || len(content) == 0 {
		t.Fatalf("embedded dist does not contain a non-empty %q", IndexFile)
	}
}

func TestStaticRestrictedDoesNotServeCustomAssetOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	assetPath := filepath.Join("data", "theme", "custom", "dist", "assets")
	if err := os.MkdirAll(assetPath, 0o755); err != nil {
		t.Fatalf("create custom theme asset directory: %v", err)
	}
	const assetName = "about-D4JKo971.css"
	if err := os.WriteFile(filepath.Join(assetPath, assetName), []byte("custom override"), 0o644); err != nil {
		t.Fatalf("write custom theme asset: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, "custom"); err != nil {
		t.Fatalf("set custom theme: %v", err)
	}

	router := gin.New()
	StaticRestricted(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})
	for _, requestPath := range []string{"/assets/" + assetName} {
		request := httptest.NewRequest("GET", requestPath, nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Fatalf("restricted asset %s status = %d, want 200", requestPath, recorder.Code)
		}
		body, err := io.ReadAll(recorder.Result().Body)
		if err != nil {
			t.Fatalf("read restricted asset %s: %v", requestPath, err)
		}
		if string(body) == "custom override" {
			t.Fatalf("restricted listener served a custom theme asset override for %s", requestPath)
		}
	}

	indexRequest := httptest.NewRequest("GET", "/database-recovery", nil)
	indexRecorder := httptest.NewRecorder()
	router.ServeHTTP(indexRecorder, indexRequest)
	indexBody, err := io.ReadAll(indexRecorder.Result().Body)
	if err != nil {
		t.Fatalf("read restricted index: %v", err)
	}
	if strings.Contains(string(indexBody), `vite-plugin-pwa:register-sw`) {
		t.Fatal("restricted index still registers a service worker")
	}
}

func TestStaticPreventsStaleFrontendCacheFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	config.SetDb(db)
	if err := config.Set(config.ThemeKey, DefaultTheme); err != nil {
		t.Fatalf("set default theme: %v", err)
	}

	router := gin.New()
	Static(router.Group("/"), func(handlers ...gin.HandlerFunc) {
		router.NoRoute(handlers...)
	})

	indexRequest := httptest.NewRequest("GET", "/", nil)
	indexRecorder := httptest.NewRecorder()
	router.ServeHTTP(indexRecorder, indexRequest)
	if indexRecorder.Code != 200 {
		t.Fatalf("index status = %d, want 200", indexRecorder.Code)
	}
	if cacheControl := indexRecorder.Header().Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("index Cache-Control = %q, want no-store", cacheControl)
	}
	indexBody := indexRecorder.Body.String()
	if strings.Contains(indexBody, `vite-plugin-pwa:register-sw`) {
		t.Fatal("index still registers a service worker")
	}
	if !strings.Contains(indexBody, serviceWorkerCleanupPath) {
		t.Fatal("index does not load the service-worker cleanup script")
	}

	assetRequest := httptest.NewRequest("GET", "/assets/stale-build-hash.js", nil)
	assetRecorder := httptest.NewRecorder()
	router.ServeHTTP(assetRecorder, assetRequest)
	if assetRecorder.Code != 404 {
		t.Fatalf("missing asset status = %d, want 404", assetRecorder.Code)
	}
	if contentType := assetRecorder.Header().Get("Content-Type"); strings.Contains(contentType, "text/html") {
		t.Fatalf("missing asset Content-Type = %q, must not be HTML", contentType)
	}

	routeRequest := httptest.NewRequest("GET", "/instance/example-node", nil)
	routeRecorder := httptest.NewRecorder()
	router.ServeHTTP(routeRecorder, routeRequest)
	if routeRecorder.Code != 200 {
		t.Fatalf("SPA route status = %d, want 200", routeRecorder.Code)
	}

	workerRequest := httptest.NewRequest("GET", "/sw.js", nil)
	workerRecorder := httptest.NewRecorder()
	router.ServeHTTP(workerRecorder, workerRequest)
	if workerRecorder.Code != 200 {
		t.Fatalf("retiring service worker status = %d, want 200", workerRecorder.Code)
	}
	if !strings.Contains(workerRecorder.Body.String(), "registration.unregister") {
		t.Fatal("retiring service worker does not unregister itself")
	}
}
