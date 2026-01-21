package server

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	_ "github.com/komari-monitor/komari/internal"
	"github.com/komari-monitor/komari/internal/app"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/internal/eventType"
	logutil "github.com/komari-monitor/komari/internal/log"
	"github.com/komari-monitor/komari/public"
)

type httpModule struct {
	engine     *gin.Engine
	httpServer *http.Server
	stopped    chan struct{}
}

var _ app.Module = (*httpModule)(nil)

func NewHTTPModule() app.Module { return &httpModule{stopped: make(chan struct{})} }

func (m *httpModule) Name() string { return "http" }
func (m *httpModule) Depends() []string {
	return []string{dbcore.NewDBModule().Name()}
}

func (m *httpModule) Provide(r app.Registry) error {
	return r.Provide(func(cfg *conf.Config) (*gin.Engine, error) {
		if conf.Version != conf.Version_Development {
			gin.SetMode(gin.ReleaseMode)
		}

		r := gin.New()
		r.Use(logutil.GinLogger())
		r.Use(logutil.GinRecovery())

		corsEnabled := &atomic.Bool{}
		corsEnabled.Store(cfg.Site.AllowCors)

		r.Use(func(c *gin.Context) {
			if corsEnabled.Load() {
				c.Header("Access-Control-Allow-Origin", "*")
				c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
				c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, X-CSRF-Token, X-Requested-With, Set-Cookie")
				c.Header("Access-Control-Expose-Headers", "Content-Length, Authorization, Set-Cookie")
				c.Header("Access-Control-Allow-Credentials", "false")
				c.Header("Access-Control-Max-Age", "43200")
				if c.Request.Method == "OPTIONS" {
					c.AbortWithStatus(204)
					return
				}
			}
			c.Next()
		})

		// Keep compatibility with existing config update flow.
		event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
			newConf := e.Get("new").(conf.Config)
			corsEnabled.Store(newConf.Site.AllowCors)
			public.UpdateIndex(newConf.ToV1Format())
			return nil
		}), event.High)

		return r, nil
	})
}

func (m *httpModule) Hooks() app.Hooks {
	return app.Hooks{
		Init: func(eng *gin.Engine) error {
			m.engine = eng
			return nil
		},
		Start: func(ctx context.Context, cfg *conf.Config) error {
			if m.engine == nil {
				return errors.New("gin engine is nil")
			}

			// Existing route registration is event-driven.
			if err, _ := event.Trigger(eventType.ServerInitializeStart, event.M{"engine": m.engine}); err != nil {
				slog.Error("Something went wrong during ServerInitializeStart event.", slog.Any("error", err))
				return err
			}

			public.Static(m.engine.Group("/"), func(handlers ...gin.HandlerFunc) {
				m.engine.NoRoute(handlers...)
			})

			listen := cfg.Listen
			m.httpServer = &http.Server{Addr: listen, Handler: m.engine}
			event.Trigger(eventType.ServerInitializeDone, event.M{})

			log.Printf("Starting server on %s ...", listen)
			go func() {
				defer close(m.stopped)
				if err := m.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					m.onFatal(err)
					event.Trigger(eventType.ProcessExit, event.M{})
					log.Printf("listen: %v", err)
				}
			}()

			return nil
		},
		Stop: func(ctx context.Context) error {
			m.onShutdown()
			event.Trigger(eventType.ProcessExit, event.M{})
			if m.httpServer == nil {
				return nil
			}
			err := m.httpServer.Shutdown(ctx)
			select {
			case <-m.stopped:
			case <-ctx.Done():
			}
			return err
		},
	}
}

func (m *httpModule) onShutdown() {
	auditlog.Log("", "", "server is shutting down", "info")
}

func (m *httpModule) onFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
}
