package server

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	_ "github.com/komari-monitor/komari/internal"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/eventType"
	logutil "github.com/komari-monitor/komari/internal/log"
	"github.com/komari-monitor/komari/public"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// FxModule wires up the HTTP server and its dependencies.
func FxModule() fx.Option {
	return fx.Options(
		fx.Provide(newGinEngine),
		fx.Invoke(registerHTTPServer),
	)
}

func newGinEngine(cfg *conf.Config) (*gin.Engine, error) {
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
}

func registerHTTPServer(lc fx.Lifecycle, shutdowner fx.Shutdowner, eng *gin.Engine, cfg *conf.Config, _ *gorm.DB) {
	// _ *gorm.DB enforces DB ready before starting HTTP stack.
	stopped := make(chan struct{})
	var srv *http.Server
	var ln net.Listener

	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			_ = ctx
			if eng == nil {
				return errors.New("gin engine is nil")
			}

			// Existing route registration is event-driven.
			if err, _ := event.Trigger(eventType.ServerInitializeStart, event.M{"engine": eng}); err != nil {
				slog.Error("Something went wrong during ServerInitializeStart event.", slog.Any("error", err))
				return err
			}

			public.Static(eng.Group("/"), func(handlers ...gin.HandlerFunc) {
				eng.NoRoute(handlers...)
			})

			listen := cfg.Listen
			l, err := net.Listen("tcp", listen)
			if err != nil {
				// Bind failure should fail startup so callers can exit.
				return err
			}
			ln = l
			srv = &http.Server{Addr: listen, Handler: eng}
			event.Trigger(eventType.ServerInitializeDone, event.M{})

			log.Printf("Starting server on %s ...", listen)
			go func() {
				defer close(stopped)
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					onFatal(err)
					event.Trigger(eventType.ProcessExit, event.M{})
					log.Printf("listen: %v", err)
					_ = shutdowner.Shutdown(fx.ExitCode(1))
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			onShutdown()
			event.Trigger(eventType.ProcessExit, event.M{})
			if srv == nil {
				return nil
			}
			if ln != nil {
				_ = ln.Close()
			}
			err := srv.Shutdown(ctx)
			select {
			case <-stopped:
			case <-ctx.Done():
			}
			return err
		},
	})
}

func onShutdown() {
	auditlog.Log("", "", "server is shutting down", "info")
}

func onFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
}
