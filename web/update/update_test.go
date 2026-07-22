package update

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/migrations"
	"github.com/komari-monitor/komari/web/api"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "update.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open update database: %v", err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	} else {
		t.Fatalf("update sql database: %v", err)
	}
	appconfig.SetDb(db)
	return db
}

func TestMetricConfigValidatesSelectedDriverAgainstDSN(t *testing.T) {
	setupConfigDB(t)

	sqliteConfig, err := metricConfig("sqlite", "")
	if err != nil {
		t.Fatalf("build default SQLite config: %v", err)
	}
	if sqliteConfig.Driver != "sqlite" || sqliteConfig.DSN != "./data/metrics.db" {
		t.Fatalf("unexpected default SQLite config: %#v", sqliteConfig)
	}
	if _, err := metricConfig("mysql", "./data/metrics.db"); err == nil {
		t.Fatal("expected mismatched MySQL/SQLite DSN to fail")
	}
	postgresConfig, err := metricConfig("postgresql", "host=127.0.0.1 port=5432 user=komari password=secret dbname=komari sslmode=disable")
	if err != nil {
		t.Fatalf("build PostgreSQL config: %v", err)
	}
	if postgresConfig.Driver != "postgresql" {
		t.Fatalf("unexpected PostgreSQL driver: %q", postgresConfig.Driver)
	}
}

func TestRestrictedControllerOnlyRegistersLoginOAuthAndUpgradeAPIs(t *testing.T) {
	db := setupConfigDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	controller := NewController(db, migrations.LegacyMonitoringSummary{})
	controller.Activate()
	controller.Register(r)

	routes := make(map[string]bool)
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"POST /api/login",
		"GET /api/me",
		"GET /api/oauth",
		"GET /api/oauth_callback",
		"GET " + APIPath + "/auth",
		"GET " + APIPath + "/status",
		"POST " + APIPath + "/cleanup",
		"POST " + APIPath + "/start",
	} {
		if !routes[route] {
			t.Fatalf("required restricted route is missing: %s", route)
		}
	}
	if routes["GET /api/public"] || routes["GET /api/rpc2"] || routes["POST /api/clients/report"] {
		t.Fatalf("ordinary APIs leaked into restricted routes: %#v", routes)
	}

	request := httptest.NewRequest(http.MethodGet, APIPath+"/status", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestControllerRejectsUpgradeAPIsAfterDeactivation(t *testing.T) {
	db := setupConfigDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	controller := NewController(db, migrations.LegacyMonitoringSummary{})
	controller.Register(r)

	request := httptest.NewRequest(http.MethodGet, APIPath+"/auth", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("inactive controller status = %d, want %d", response.Code, http.StatusNotFound)
	}

	controller.Activate()
	controller.Deactivate()

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, APIPath + "/auth"},
		{http.MethodGet, APIPath + "/status"},
		{http.MethodPost, APIPath + "/cleanup"},
		{http.MethodPost, APIPath + "/start"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want %d", route.method, route.path, response.Code, http.StatusNotFound)
		}
	}
}
