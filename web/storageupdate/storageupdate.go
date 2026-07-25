package storageupdate

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/metricstore"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/web/api"
	publicapi "github.com/komari-monitor/komari/web/api/public"
	jsonrpc "github.com/komari-monitor/komari/web/rpc/jsonrpc"
)

const (
	PagePath = "/admin/update/storage-v4"
	APIPath  = "/api/admin/update/storage-v4"
)

type Status struct {
	State     string                        `json:"state"`
	Phase     string                        `json:"phase"`
	Current   int64                         `json:"current"`
	Total     int64                         `json:"total"`
	Preserved int64                         `json:"preserved"`
	Progress  float64                       `json:"progress"`
	ElapsedMS int64                         `json:"elapsed_ms"`
	Summary   metric.SQLiteMigrationSummary `json:"summary"`
	Error     string                        `json:"error,omitempty"`
}

type Controller struct {
	active atomic.Bool

	mu        sync.RWMutex
	status    Status
	startedAt time.Time
	endedAt   time.Time
	running   bool

	startOnce sync.Once
	doneOnce  sync.Once
	done      chan struct{}
	migrate   func(context.Context, metric.MigrationProgressFunc) error
}

func NewController(summary metric.SQLiteMigrationSummary) *Controller {
	controller := &Controller{
		status: Status{
			State:   "pending",
			Phase:   metric.MigrationPhasePreparing,
			Summary: summary,
		},
		done: make(chan struct{}),
	}
	controller.migrate = controller.openAndMigrate
	return controller
}

func (c *Controller) Activate() {
	c.active.Store(true)
	c.startOnce.Do(func() { go c.run() })
}

func (c *Controller) Deactivate() {
	c.active.Store(false)
}

func (c *Controller) Done() <-chan struct{} {
	return c.done
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
	authorized.POST("/retry", c.retry)
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
	api.RespondSuccess(ctx, c.snapshot())
}

func (c *Controller) retry(ctx *gin.Context) {
	c.mu.Lock()
	if c.running || c.status.State != "failed" {
		c.mu.Unlock()
		api.RespondError(ctx, http.StatusConflict, "storage migration is not retryable in its current state")
		return
	}
	c.status.State = "pending"
	c.status.Phase = metric.MigrationPhasePreparing
	c.status.Current = 0
	c.status.Total = 0
	c.status.Preserved = 0
	c.status.Progress = 0
	c.status.ElapsedMS = 0
	c.status.Error = ""
	c.endedAt = time.Time{}
	c.mu.Unlock()

	go c.run()
	api.RespondSuccessMessage(ctx, "storage migration restarted", gin.H{})
}

func (c *Controller) run() {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	c.startedAt = time.Now()
	c.endedAt = time.Time{}
	c.status.State = "migrating"
	c.status.Phase = metric.MigrationPhasePreparing
	c.status.Error = ""
	c.mu.Unlock()

	err := c.migrate(context.Background(), c.onProgress)
	c.mu.Lock()
	c.running = false
	c.endedAt = time.Now()
	if err != nil {
		c.status.State = "failed"
		c.status.Error = err.Error()
		c.status.ElapsedMS = c.endedAt.Sub(c.startedAt).Milliseconds()
		c.mu.Unlock()
		return
	}
	c.status.State = "completed"
	c.status.Phase = metric.MigrationPhaseCompleted
	c.status.Progress = 100
	c.status.Error = ""
	c.status.ElapsedMS = c.endedAt.Sub(c.startedAt).Milliseconds()
	c.mu.Unlock()

	// Keep the final state visible for at least one polling interval before the
	// normal application replaces this restricted listener.
	time.AfterFunc(2*time.Second, func() {
		c.Deactivate()
		c.doneOnce.Do(func() { close(c.done) })
	})
}

func (c *Controller) openAndMigrate(ctx context.Context, progress metric.MigrationProgressFunc) error {
	store, err := metricstore.OpenConfiguredStoreForMigration(ctx, progress)
	if err != nil {
		return err
	}
	return store.Close()
}

func (c *Controller) onProgress(progress metric.MigrationProgress) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.Phase = progress.Phase
	c.status.Current = progress.Current
	c.status.Total = progress.Total
	c.status.Preserved = progress.Preserved
	if progress.Total > 0 {
		c.status.Progress = float64(progress.Current) / float64(progress.Total) * 100
	} else {
		c.status.Progress = 0
	}
	if !c.startedAt.IsZero() {
		c.status.ElapsedMS = time.Since(c.startedAt).Milliseconds()
	}
}

func (c *Controller) snapshot() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := c.status
	if c.running && !c.startedAt.IsZero() {
		status.ElapsedMS = time.Since(c.startedAt).Milliseconds()
	}
	return status
}
