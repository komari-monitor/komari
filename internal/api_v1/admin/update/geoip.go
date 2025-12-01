package update

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/geoip"
)

func UpdateMmdbGeoIP(c *gin.Context) {
	if err := geoip.UpdateDatabase(); err != nil {
		resp.RespondError(c, 500, "Failed to update GeoIP database "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "GeoIP database updated", "info")
	resp.RespondSuccess(c, nil)
}
