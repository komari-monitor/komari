// Package recovery serves the small, authenticated web surface used when the
// monitoring database cannot be opened during startup.
package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/metricstore"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/web/api"
	publicapi "github.com/komari-monitor/komari/web/api/public"
	jsonrpc "github.com/komari-monitor/komari/web/rpc/jsonrpc"
)

const (
	PagePath = "/database-recovery"
	APIPath  = "/api/admin/database-recovery"

	maxRequestBody = 64 * 1024
)

// Status is deliberately small. The DSN is returned only from the
// authenticated status endpoint so an anonymous request cannot disclose
// credentials stored in the connection string.
type Status struct {
	State       string `json:"state"`
	Failures    int    `json:"failures"`
	MaxFailures int    `json:"max_failures"`
	Error       string `json:"error,omitempty"`
	DSN         string `json:"dsn,omitempty"`
}

type dsnRequest struct {
	DSN string `json:"dsn"`
}

// Controller owns the temporary recovery API. It is deactivated before the
// normal application router starts, so its DSN-only write endpoint cannot
// remain available during normal operation.
type Controller struct {
	active atomic.Bool

	mu     sync.RWMutex
	status Status
	done   chan struct{}
	once   sync.Once
}

func NewController(initialErr error, failures int) *Controller {
	status := Status{
		State:       "waiting",
		Failures:    failures,
		MaxFailures: failures,
	}
	if initialErr != nil {
		status.Error = metricstore.RedactConnectionError(initialErr.Error(), "")
	}
	if cfg, err := appconfig.GetManyAs[metricstore.MetricStoreConfig](); err == nil {
		status.DSN = strings.TrimSpace(cfg.DSN)
		status.Error = metricstore.RedactConnectionError(status.Error, status.DSN)
	}
	return &Controller{status: status, done: make(chan struct{})}
}

func (c *Controller) Activate() { c.active.Store(true) }

func (c *Controller) Deactivate() { c.active.Store(false) }

func (c *Controller) Done() <-chan struct{} { return c.done }

func (c *Controller) Register(r *gin.Engine) {
	// Keep the normal login contract. No regular public/RPC routes are mounted
	// here because many of them require a ready metric store.
	r.POST("/api/login", limitLoginBody, publicapi.Login)
	r.GET("/api/me", jsonrpc.Bind("public:getMe", jsonrpc.WithRaw()))
	r.GET("/api/oauth", publicapi.OAuth)
	r.GET("/api/oauth_callback", publicapi.OAuthCallback)

	g := r.Group(APIPath, c.requireActive)
	g.GET("/auth", c.authStatus)
	authorized := g.Group("", api.RequireRole(api.RoleAdmin))
	authorized.GET("/status", c.getStatus)
	authorized.POST("", c.updateDSN)
}

func limitLoginBody(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxRequestBody)
	ctx.Next()
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

func (c *Controller) updateDSN(ctx *gin.Context) {
	var request dsnRequest
	if err := decodeJSON(ctx, &request); err != nil {
		api.RespondError(ctx, http.StatusBadRequest, err.Error())
		return
	}
	request.DSN = strings.TrimSpace(request.DSN)
	if request.DSN == "" {
		api.RespondError(ctx, http.StatusBadRequest, "monitoring database DSN is required")
		return
	}

	driver, ok := metricstore.InferDriverFromDSN(request.DSN)
	if !ok {
		api.RespondError(ctx, http.StatusBadRequest, "cannot infer monitoring database type from DSN")
		return
	}
	if !c.beginConnection() {
		api.RespondError(ctx, http.StatusConflict, "monitoring database recovery is already running or completed")
		return
	}
	cfg, err := appconfig.GetManyAs[metricstore.MetricStoreConfig]()
	if err != nil {
		c.setFailure(err, request.DSN)
		api.RespondError(ctx, http.StatusInternalServerError, "failed to load monitoring database settings")
		return
	}
	cfg.Driver = string(driver)
	cfg.DSN = request.DSN

	recoverCtx, cancel := context.WithTimeout(ctx.Request.Context(), 30*time.Second)
	err = metricstore.RecoverStore(recoverCtx, cfg)
	cancel()
	if err != nil {
		c.setFailure(err, request.DSN)
		api.RespondError(ctx, http.StatusBadRequest, "monitoring database connection failed: "+metricstore.RedactConnectionError(err.Error(), request.DSN))
		return
	}
	c.mu.Lock()
	c.status.State = "completed"
	c.status.Failures = 0
	c.status.Error = ""
	c.status.DSN = request.DSN
	c.mu.Unlock()

	// Leave the response time to reach the browser before the parent switches
	// from this listener to the normal application router.
	go func() {
		time.Sleep(500 * time.Millisecond)
		c.Deactivate()
		c.once.Do(func() { close(c.done) })
	}()
	api.RespondSuccessMessage(ctx, "monitoring database settings saved", gin.H{})
}

func (c *Controller) beginConnection() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.State != "waiting" {
		return false
	}
	c.status.State = "connecting"
	c.status.Error = ""
	return true
}

func (c *Controller) setFailure(err error, dsn string) {
	c.mu.Lock()
	c.status.State = "waiting"
	c.status.Error = metricstore.RedactConnectionError(err.Error(), dsn)
	c.mu.Unlock()
}

func decodeJSON(ctx *gin.Context, target any) error {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxRequestBody)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid request body: multiple JSON values")
		}
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}
