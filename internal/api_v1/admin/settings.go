package admin

import (
	"database/sql"

	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"

	"github.com/gin-gonic/gin"
)

// GetSettings 获取自定义配置
func GetSettings(c *gin.Context) {
	cst, err := conf.GetWithV1Format()
	if err != nil {
		if err == sql.ErrNoRows {
			//override
			cst = conf.V1Struct{Sitename: "Komari"}
			cst.ID = 1
			conf.Save(cst)
			resp.RespondSuccess(c, cst)
			return
		}
		c.JSON(500, gin.H{
			"status":  "error",
			"message": "Internal Server Error: " + err.Error(),
		})
	}
	resp.RespondSuccess(c, cst)
}

// EditSettings 更新自定义配置
func EditSettings(c *gin.Context) {
	cfg := make(map[string]interface{})
	if err := c.ShouldBindJSON(&cfg); err != nil {
		resp.RespondError(c, 400, "Invalid or missing request body: "+err.Error())
		return
	}

	cfg["id"] = 1 // Only one record
	if err := conf.Update(cfg); err != nil {
		resp.RespondError(c, 500, "Failed to update settings: "+err.Error())
		return
	}

	uuid, _ := c.Get("uuid")
	message := "update settings: "
	for key := range cfg {
		ignoredKeys := []string{"id", "updated_at"}
		if contains(ignoredKeys, key) {
			continue
		}
		message += key + ", "
	}
	if len(message) > 2 {
		message = message[:len(message)-2]
	}
	auditlog.Log(c.ClientIP(), uuid.(string), message, "info")
	resp.RespondSuccess(c, nil)
}

func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}

func ClearAllRecords(c *gin.Context) {
	records.DeleteAll()
	tasks.DeleteAllPingRecords()
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "clear all records", "info")
	resp.RespondSuccess(c, nil)
}
