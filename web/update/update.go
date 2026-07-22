package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/migrations"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/web/api"
	publicapi "github.com/komari-monitor/komari/web/api/public"
	jsonrpc "github.com/komari-monitor/komari/web/rpc/jsonrpc"
	"gorm.io/gorm"
)

const (
	PagePath = "/admin/update/1.2.7"
	APIPath  = "/api/admin/update/1.2.7"
)

const largeDatasetThreshold int64 = 300_000

type Status struct {
	State           string                             `json:"state"`
	Phase           string                             `json:"phase"`
	Table           string                             `json:"table,omitempty"`
	Summary         migrations.LegacyMonitoringSummary `json:"summary"`
	SourceRowsDone  int64                              `json:"source_rows_done"`
	SourceRowsTotal int64                              `json:"source_rows_total"`
	WrittenPoints   int64                              `json:"written_points"`
	Progress        float64                            `json:"progress"`
	TargetDriver    string                             `json:"target_driver,omitempty"`
	Error           string                             `json:"error,omitempty"`
}

type Controller struct {
	db *gorm.DB

	active atomic.Bool
	mu     sync.RWMutex
	status Status
	done   chan struct{}
	once   sync.Once
}

type cleanupRequest struct {
	Before time.Time `json:"before"`
}

type startRequest struct {
	Driver              string `json:"driver"`
	DSN                 string `json:"dsn"`
	ConfirmSQLiteRisk   bool   `json:"confirm_sqlite_risk"`
	ConfirmLargeDataset bool   `json:"confirm_large_dataset"`
}

func NewController(db *gorm.DB, summary migrations.LegacyMonitoringSummary) *Controller {
	return &Controller{
		db: db,
		status: Status{
			State:           "idle",
			Phase:           "ready",
			Summary:         summary,
			SourceRowsTotal: summary.MonitoringRows,
		},
		done: make(chan struct{}),
	}
}

func (c *Controller) Done() <-chan struct{} {
	return c.done
}

// Activate opens the upgrade APIs after startup has confirmed that the legacy
// migration is required.
func (c *Controller) Activate() {
	c.active.Store(true)
}

// Deactivate closes every upgrade-specific endpoint. Login and /api/me are
// registered separately because the normal application also exposes them.
func (c *Controller) Deactivate() {
	c.active.Store(false)
}

func (c *Controller) Register(r *gin.Engine) {
	r.POST("/api/login", publicapi.Login)
	r.GET("/api/me", jsonrpc.Bind("public:getMe", jsonrpc.WithRaw()))
	r.GET("/api/oauth", publicapi.OAuth)
	r.GET("/api/oauth_callback", publicapi.OAuthCallback)

	g := r.Group(APIPath, c.requireActive)
	g.GET("/auth", c.authStatus)
	authorized := g.Group("", api.RequireRole(api.RoleAdmin))
	authorized.GET("/status", c.getStatus)
	authorized.POST("/cleanup", c.cleanup)
	authorized.POST("/start", c.start)
}

func (c *Controller) requireActive(ctx *gin.Context) {
	if !c.active.Load() {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}
	ctx.Next()
}

func (c *Controller) authStatus(ctx *gin.Context) {
	oauthEnabled, _ := appconfig.GetAs[bool](appconfig.OAuthEnabledKey, false)
	oauthProvider, _ := appconfig.GetAs[string](appconfig.OAuthProviderKey, "github")
	disablePassword, _ := appconfig.GetAs[bool](appconfig.DisablePasswordLoginKey, false)
	api.RespondSuccess(ctx, gin.H{
		"oauth_enabled":          oauthEnabled,
		"oauth_provider":         oauthProvider,
		"password_login_enabled": !disablePassword,
	})
}

func (c *Controller) getStatus(ctx *gin.Context) {
	c.mu.RLock()
	status := c.status
	c.mu.RUnlock()
	api.RespondSuccess(ctx, status)
}

func (c *Controller) cleanup(ctx *gin.Context) {
	var request cleanupRequest
	if err := decodeJSON(ctx, &request); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	if request.Before.IsZero() {
		api.RespondError(ctx, http.StatusBadRequest, "before is required and must be an RFC3339 timestamp with a timezone")
		return
	}
	cutoff := request.Before.UTC()
	if cutoff.After(time.Now().UTC()) {
		api.RespondError(ctx, http.StatusBadRequest, "cleanup cutoff cannot be in the future")
		return
	}

	c.mu.Lock()
	if c.status.State == "migrating" || c.status.State == "cleaning" || c.status.State == "completed" {
		c.mu.Unlock()
		api.RespondError(ctx, http.StatusConflict, "upgrade operation is already running or completed")
		return
	}
	c.status.State = "cleaning"
	c.status.Phase = "cleaning"
	c.status.Error = ""
	c.mu.Unlock()

	deleted, err := migrations.DeleteLegacyMonitoringBefore(c.db.WithContext(ctx.Request.Context()), cutoff)
	if err != nil {
		c.fail(err, "cleaning")
		api.RespondError(ctx, http.StatusInternalServerError, "failed to remove legacy monitoring data")
		return
	}
	summary, err := migrations.InspectLegacyMonitoring(c.db)
	if err != nil {
		c.fail(err, "cleaning")
		api.RespondError(ctx, http.StatusInternalServerError, "failed to refresh legacy monitoring summary")
		return
	}

	c.mu.Lock()
	c.status = Status{
		State:           "idle",
		Phase:           "ready",
		Summary:         summary,
		SourceRowsTotal: summary.MonitoringRows,
	}
	c.mu.Unlock()
	api.RespondSuccess(ctx, gin.H{"deleted": deleted, "summary": summary})
}

func (c *Controller) start(ctx *gin.Context) {
	var request startRequest
	if err := decodeJSON(ctx, &request); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := metricConfig(request.Driver, request.DSN)
	if err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	summary, err := migrations.InspectLegacyMonitoring(c.db)
	if err != nil {
		api.RespondError(ctx, http.StatusInternalServerError, "failed to inspect legacy monitoring data")
		return
	}
	driver := metricstore.ResolveDriverFromConfig(cfg.Driver, cfg.DSN)
	if driver == metric.DriverSQLite && summary.ServerCount > 5 && summary.RetentionDays > 7 && !request.ConfirmSQLiteRisk {
		api.RespondError(ctx, http.StatusConflict, "SQLite risk confirmation is required")
		return
	}
	if summary.LoadRows+summary.LatencyRows > largeDatasetThreshold && !request.ConfirmLargeDataset {
		api.RespondError(ctx, http.StatusConflict, "large dataset confirmation is required")
		return
	}

	c.mu.Lock()
	if c.status.State == "migrating" || c.status.State == "cleaning" || c.status.State == "completed" {
		c.mu.Unlock()
		api.RespondError(ctx, http.StatusConflict, "upgrade operation is already running or completed")
		return
	}
	c.status = Status{
		State:           "migrating",
		Phase:           "connecting",
		Summary:         summary,
		SourceRowsTotal: summary.MonitoringRows,
		TargetDriver:    string(driver),
	}
	c.mu.Unlock()

	go c.runMigration(*cfg, summary.RetentionDays)
	api.RespondSuccessMessage(ctx, "upgrade started", gin.H{})
}

func (c *Controller) runMigration(cfg metricstore.MetricStoreConfig, legacyRetentionDays int) {
	ctx := context.Background()
	store, err := metricstore.OpenStoreForMigration(ctx, &cfg, legacyRetentionDays)
	if err != nil {
		c.failTarget(err, cfg.DSN, "connecting")
		return
	}
	defer store.Close()

	if err := appconfig.SetMany(map[string]any{
		metricstore.MetricDBDriverKey: cfg.Driver,
		metricstore.MetricDBDSNKey:    cfg.DSN,
	}); err != nil {
		c.failTarget(err, cfg.DSN, "saving_target")
		return
	}

	_, err = migrations.MigrateLegacyMonitoring(ctx, c.db, store, func(progress migrations.LegacyMonitoringProgress) {
		c.mu.Lock()
		c.status.Phase = progress.Phase
		c.status.Table = progress.Table
		c.status.SourceRowsDone = progress.SourceRowsDone
		c.status.SourceRowsTotal = progress.SourceRowsTotal
		c.status.WrittenPoints = progress.WrittenPoints
		if progress.SourceRowsTotal > 0 {
			c.status.Progress = float64(progress.SourceRowsDone) / float64(progress.SourceRowsTotal) * 100
		}
		c.mu.Unlock()
	})
	if err != nil {
		c.failTarget(err, cfg.DSN, "migrating")
		return
	}

	c.mu.Lock()
	c.status.Phase = "finalizing"
	c.status.Progress = 100
	c.mu.Unlock()
	finalizePhase := "finalizing"
	if err := migrations.CompleteLegacyMonitoringMigration(c.db, func() error {
		finalizePhase = "vacuuming"
		c.mu.Lock()
		c.status.Phase = finalizePhase
		c.mu.Unlock()
		return dbcore.ReclaimSpace(ctx)
	}); err != nil {
		c.failTarget(err, cfg.DSN, finalizePhase)
		return
	}

	c.mu.Lock()
	c.status.State = "completed"
	c.status.Phase = "completed"
	c.status.Table = ""
	c.status.Progress = 100
	c.status.Error = ""
	c.mu.Unlock()

	// Give the browser one final polling window before the restricted listener
	// is replaced by the normal application server on the same address.
	time.AfterFunc(1500*time.Millisecond, func() {
		c.Deactivate()
		c.once.Do(func() { close(c.done) })
	})
}

func (c *Controller) fail(err error, phase string) {
	c.mu.Lock()
	c.status.State = "failed"
	c.status.Phase = phase
	c.status.Error = err.Error()
	c.mu.Unlock()
}

func (c *Controller) failTarget(err error, dsn, phase string) {
	message := err.Error()
	if dsn != "" {
		message = strings.ReplaceAll(message, dsn, "[redacted]")
	}
	c.fail(fmt.Errorf("%s", message), phase)
}

func metricConfig(requestedDriver, requestedDSN string) (*metricstore.MetricStoreConfig, error) {
	requestedDriver = strings.ToLower(strings.TrimSpace(requestedDriver))
	requestedDSN = strings.TrimSpace(requestedDSN)
	if requestedDriver != string(metric.DriverSQLite) && requestedDriver != string(metric.DriverMySQL) && requestedDriver != string(metric.DriverPostgreSQL) {
		return nil, fmt.Errorf("driver must be sqlite, mysql, or postgresql")
	}
	if requestedDSN == "" {
		if requestedDriver != string(metric.DriverSQLite) {
			return nil, fmt.Errorf("dsn is required for remote databases")
		}
		requestedDSN = "./data/metrics.db"
	}
	resolved := metricstore.ResolveDriverFromConfig(requestedDriver, requestedDSN)
	if string(resolved) != requestedDriver {
		return nil, fmt.Errorf("dsn does not match the selected database type")
	}
	cfg, err := appconfig.GetManyAs[metricstore.MetricStoreConfig]()
	if err != nil {
		return nil, fmt.Errorf("load metric store defaults: %w", err)
	}
	cfg.Driver = requestedDriver
	cfg.DSN = requestedDSN
	return cfg, nil
}

func decodeJSON(ctx *gin.Context, target any) error {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 1<<20)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
