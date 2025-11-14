package admin

import (
	"database/sql"

	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/config"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"

	"github.com/gin-gonic/gin"
)

// GetSettings 获取自定义配置
func GetSettings(c *gin.Context) {
	cst, err := config.Get()
	if err != nil {
		if err == sql.ErrNoRows {
			//override
			cst = models.Config{Sitename: "Komari"}
			cst.ID = 1
			config.Save(cst)
			api.RespondSuccess(c, cst)
			return
		}
		c.JSON(500, gin.H{
			"status":  "error",
			"message": "Internal Server Error: " + err.Error(),
		})
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

	cfg["id"] = 1 // Only one record
	if err := config.Update(cfg); err != nil {
		api.RespondError(c, 500, "Failed to update settings: "+err.Error())
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
	api.RespondSuccess(c, nil)
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
	api.RespondSuccess(c, nil)
}
