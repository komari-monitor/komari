package storageupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/web/api"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "storage-update.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open config SQL database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	appconfig.SetDb(db)
}

func TestRestrictedControllerRoutes(t *testing.T) {
	setupConfigDB(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	controller := NewController(metric.SQLiteMigrationSummary{Required: true, Layout: "legacy", SourceRows: 10})
	controller.active.Store(true)
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
		"POST " + APIPath + "/retry",
	} {
		if !routes[route] {
			t.Fatalf("required restricted route is missing: %s", route)
		}
	}
	if routes["GET /api/public"] || routes["GET /api/rpc2"] || routes["POST /api/clients/report"] {
		t.Fatalf("ordinary APIs leaked into storage migration routes: %#v", routes)
	}

	request := httptest.NewRequest(http.MethodGet, APIPath+"/status", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestControllerTracksProgressAndCompletion(t *testing.T) {
	controller := NewController(metric.SQLiteMigrationSummary{Required: true, Layout: "normalized", SourceRows: 8})
	controller.migrate = func(_ context.Context, progress metric.MigrationProgressFunc) error {
		progress(metric.MigrationProgress{Phase: metric.MigrationPhaseEncodingPoints, Current: 3, Total: 8, Preserved: 3})
		progress(metric.MigrationProgress{Phase: metric.MigrationPhaseCompleted, Current: 8, Total: 8, Preserved: 8})
		return nil
	}
	controller.run()

	status := controller.snapshot()
	if status.State != "completed" || status.Phase != metric.MigrationPhaseCompleted || status.Progress != 100 {
		t.Fatalf("unexpected completed status: %#v", status)
	}
	if status.Current != 8 || status.Total != 8 || status.Preserved != 8 {
		t.Fatalf("unexpected completed counts: %#v", status)
	}
}

func TestControllerKeepsFailureAvailableForRetry(t *testing.T) {
	controller := NewController(metric.SQLiteMigrationSummary{Required: true})
	want := errors.New("migration failed")
	controller.migrate = func(context.Context, metric.MigrationProgressFunc) error { return want }
	controller.run()

	status := controller.snapshot()
	if status.State != "failed" || status.Error != want.Error() {
		t.Fatalf("unexpected failed status: %#v", status)
	}
	select {
	case <-controller.Done():
		t.Fatal("failed migration closed the restricted listener")
	default:
	}
}
