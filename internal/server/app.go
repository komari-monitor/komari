package server

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/config"
)

// cleanupFunc is a cleanup action run during shutdown.
type cleanupFunc struct {
	name string
	fn   func(ctx context.Context) error
}

// App owns the server lifecycle. Individual phases live in focused files:
// bootstrap, metric store, providers, guides, router, and runtime.
type App struct {
	listenAddr              string
	settings                *config.Settings
	engine                  *gin.Engine
	server                  *http.Server
	reload                  *ReloadManager
	dbReady                 bool
	oauthReady              bool
	metricStoreCleanupAdded bool

	cleanups []cleanupFunc
}

// Options configures the process-wide application runtime.
type Options struct {
	ListenAddr string
}

// New constructs an empty application. Initialization happens in the
// explicit lifecycle phases called by the command entrypoint.
func New(options Options) *App {
	return &App{listenAddr: options.ListenAddr, reload: NewReloadManager()}
}

// addCleanup registers a cleanup action. Shutdown executes actions in LIFO
// order, mirroring resource initialization order.
func (a *App) addCleanup(name string, fn func(ctx context.Context) error) {
	a.cleanups = append(a.cleanups, cleanupFunc{name: name, fn: fn})
}
