package server

import (
	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/public"
)

var (
	AllowCors bool = false
)

func Init(r *gin.Engine) {

	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		newConf := e.Get("new").(conf.Config)
		AllowCors = newConf.Site.AllowCors
		public.UpdateIndex(newConf.ToV1Format())
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
}
