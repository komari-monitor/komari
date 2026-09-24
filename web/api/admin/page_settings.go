package admin

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/web/api"
)

// UpdatePageSettings saves the built-in frontend's page configuration.
func UpdatePageSettings(c *gin.Context) {
	var settings map[string]any
	if err := c.ShouldBindJSON(&settings); err != nil || settings == nil {
		api.RespondError(c, http.StatusBadRequest, "无效的页面设置")
		return
	}
	data, err := json.Marshal(settings)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "无效的页面设置")
		return
	}
	db := dbcore.GetDBInstance()
	var saved models.ThemeConfiguration
	if err := db.Where("short = ?", "default").
		Assign(models.ThemeConfiguration{Short: "default", Data: string(data)}).
		FirstOrCreate(&saved).Error; err != nil {
		api.RespondError(c, http.StatusInternalServerError, "保存页面设置失败")
		return
	}
	api.RespondSuccess(c, nil)
}
