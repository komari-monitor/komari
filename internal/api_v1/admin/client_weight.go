package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
)

func OrderWeight(c *gin.Context) {
	var req = make(map[string]int)
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}
	db := dbcore.GetDBInstance()
	for uuid, weight := range req {
		err := db.Model(&models.Client{}).Where("uuid = ?", uuid).Update("weight", weight).Error
		if err != nil {
			resp.RespondError(c, 500, "Failed to update client weight: "+err.Error())
			return
		}
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "order clients", "info")
	resp.RespondSuccess(c, nil)
}
