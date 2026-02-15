package admin

import (
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"

	"github.com/gin-gonic/gin"
)

// GetSettings 获取自定义配置
func GetSettings(c *gin.Context) {
	cst, err := config.GetAll()
	if err != nil {
		api.RespondError(c, 500, "Failed to get settings: "+err.Error())
		return
	}
	api.RespondSuccess(c, cst)
}

// EditSettings 更新自定义配置
func EditSettings(c *gin.Context) {
	cfg := make(map[string]interface{})
	if err := c.ShouldBindJSON(&cfg); err != nil {
		api.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}

	if err := config.SetMany(cfg); err != nil {
		api.RespondError(c, 500, "Failed to update settings: "+err.Error())
		return
	}

	uuid, _ := c.Get("uuid")
	message := "update settings: "
	for key := range cfg {
		message += key + ", "
	}
	if len(message) > 2 {
		message = message[:len(message)-2]
	}
	auditlog.Log(c.ClientIP(), uuid.(string), message, "info")
	api.RespondSuccess(c, nil)
}

func ClearAllRecords(c *gin.Context) {
	records.DeleteAll()
	tasks.DeleteAllPingRecords()
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "clear all records", "info")
	api.RespondSuccess(c, nil)
}
