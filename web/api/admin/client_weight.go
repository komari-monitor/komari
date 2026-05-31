package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/web/api"
)

func OrderWeight(c *gin.Context) {
	var req = make(map[string]int)
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	db := dbcore.GetDBInstance()
	for uuid, weight := range req {
		err := db.Model(&models.Client{}).Where("uuid = ?", uuid).Update("weight", weight).Error
		if err != nil {
			api.RespondError(c, 500, "Failed to update client weight: "+err.Error())
			return
		}
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "order clients", "info")
	api.RespondSuccess(c, nil)
}
