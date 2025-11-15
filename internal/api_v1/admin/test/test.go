package test

import (
	"net"

	"github.com/gin-gonic/gin"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/geoip"
	"github.com/komari-monitor/komari/internal/messageSender"
)

func TestSendMessage(c *gin.Context) {
	err := messageSender.SendEvent(models.EventMessage{
		Event:   "Test",
		Message: "This is a test message from Komari.",
	})
	if err != nil {
		api.RespondError(c, 500, "Failed to send message: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

func TestGeoIp(c *gin.Context) {
	ip := c.Query("ip")
	if ip == "" {
		if cfIP := c.GetHeader("CF-Connecting-IP"); cfIP != "" {
			ip = cfIP
		} else {
			ip = c.ClientIP()
		}
	}
	conf, err := conf.GetWithV1Format()
	if err != nil {
		api.RespondError(c, 500, "Failed to get configuration: "+err.Error())
		return
	}
	if !conf.GeoIpEnabled {
		api.RespondError(c, 400, "GeoIP is not enabled in the configuration.")
		return
	}
	GeoIpRecord, err := geoip.GetGeoInfo(net.ParseIP(ip))
	if err != nil {
		api.RespondError(c, 500, "Failed to get GeoIP record: "+err.Error())
		return
	}
	api.RespondSuccess(c, GeoIpRecord)
}
