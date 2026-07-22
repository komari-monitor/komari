package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/web/api"
	frontendpublic "github.com/komari-monitor/komari/web/public"
	"github.com/komari-monitor/komari/web/security"
)

type guideController interface {
	Activate()
	Deactivate()
	Register(*gin.Engine)
	Done() <-chan struct{}
}

type guideServerConfig struct {
	pagePath         string
	missingAPI       string
	logMessage       string
	requireIdentity  bool
	restrictedStatic bool
}

// runGuideServer hosts one temporary, narrowly scoped guide. It deliberately
// constructs a new Gin engine for each mode so normal routes never leak into
// installation, upgrade, or recovery flows.
func (a *App) runGuideServer(controller guideController, cfg guideServerConfig) (bool, error) {
	controller.Activate()
	defer controller.Deactivate()

	r := gin.New()
	r.Use(logger.GinLogger(), logger.GinRecovery(), noStoreAPIResponses())
	if cfg.requireIdentity {
		cors := security.NewCorsController(a.settings.CorsOriginCheckEnabled, a.settings.CorsAllowedOrigins)
		r.Use(cors.Middleware(), api.IdentityMiddleware())
	}
	controller.Register(r)

	serveStatic := frontendpublic.Static
	if cfg.restrictedStatic {
		serveStatic = frontendpublic.StaticRestricted
	}
	serveStatic(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(guideNoRoute(cfg.pagePath, cfg.missingAPI, handlers))
	})

	server := &http.Server{Addr: a.listenAddr, Handler: r}
	a.engine = r
	a.server = server
	serverErr := make(chan error, 1)
	logger.Infof("server", cfg.logMessage, a.listenAddr)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		return false, fmt.Errorf("listen in guide mode: %w", err)
	case <-quit:
		return false, a.Shutdown()
	case <-controller.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return false, fmt.Errorf("stop guide server: %w", err)
		}
		a.server = nil
		a.engine = nil
		return true, nil
	}
}

func noStoreAPIResponses() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}

func guideNoRoute(pagePath, missingAPI string, handlers []gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestPath := c.Request.URL.Path
		if strings.HasPrefix(requestPath, "/api") {
			api.RespondError(c, http.StatusNotFound, missingAPI)
			return
		}
		if c.Request.Method == http.MethodGet && requestPath != pagePath && filepath.Ext(requestPath) == "" {
			c.Redirect(http.StatusTemporaryRedirect, pagePath)
			return
		}
		for _, handler := range handlers {
			handler(c)
			if c.IsAborted() {
				return
			}
		}
	}
}
