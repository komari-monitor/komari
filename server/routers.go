package server

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal"
	"github.com/komari-monitor/komari/internal/api_rpc"
	"github.com/komari-monitor/komari/internal/database/config"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/geoip"
	"github.com/komari-monitor/komari/internal/messageSender"
	"github.com/komari-monitor/komari/public"
)

var (
	AllowCors bool = false
)

func Init(r *gin.Engine) {
	conf, _ := config.Get()
	AllowCors = conf.AllowCors

	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		newConf := e.Get("new").(models.Config)
		oldConf := e.Get("old").(models.Config)
		AllowCors = newConf.AllowCors
		public.UpdateIndex(newConf)
		if newConf.GeoIpProvider != oldConf.GeoIpProvider {
			go geoip.InitGeoIp()
		}
		if newConf.NotificationMethod != oldConf.NotificationMethod {
			go messageSender.Initialize()
		}
		return nil
	}), event.High)

	r.Use(func(c *gin.Context) {
		if AllowCors {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, X-CSRF-Token, X-Requested-With, Set-Cookie")
			c.Header("Access-Control-Expose-Headers", "Content-Length, Authorization, Set-Cookie")
			c.Header("Access-Control-Allow-Credentials", "false")
			c.Header("Access-Control-Max-Age", "43200") // 12 hours
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(204)
				return
			}
		}
		c.Next()
	})

	public.Static(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(handlers...)
	})
	// #region 静态文件服务
	public.UpdateIndex(conf)

	internal.LoadApiV1Routes(r, conf)

	api_rpc.RegisterRouters("/api/rpc2", r)
}

func ScheduledTasksInit() {
	every1m := time.NewTicker(1 * time.Minute)
	every5m := time.NewTicker(5 * time.Minute)
	every30m := time.NewTicker(30 * time.Minute)
	every1h := time.NewTicker(1 * time.Hour)
	every1d := time.NewTicker(24 * time.Hour)
	for {
		var err error = nil
		var e event.Event
		select {
		case <-every1m.C:
			err, e = event.Trigger(eventType.SchedulerEveryMinute, event.M{"interval": "1m"})
		case <-every5m.C:
			err, e = event.Trigger(eventType.SchedulerEvery5Minutes, event.M{"interval": "5m"})
		case <-every30m.C:
			err, e = event.Trigger(eventType.SchedulerEvery30Minutes, event.M{"interval": "30m"})
		case <-every1h.C:
			err, e = event.Trigger(eventType.SchedulerEveryHour, event.M{"interval": "1h"})
		case <-every1d.C:
			err, e = event.Trigger(eventType.SchedulerEveryDay, event.M{"interval": "1d"})
		}
		if err != nil {
			slog.Warn("Scheduled task error:", "error", err, "event", e)
		}
	}
}
