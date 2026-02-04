package test

import (
	"net"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/geoip"
	"github.com/komari-monitor/komari/utils/messageSender"
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
	cfg, err := config.GetAs[bool](config.GeoIpEnabledKey, false)
	if err != nil {
		api.RespondError(c, 500, "Failed to get configuration: "+err.Error())
		return
	}
	if !cfg {
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
