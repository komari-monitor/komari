package log

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
)

func GetLogs(c *gin.Context) {
	limit := c.Query("limit")
	if limit == "" {
		limit = "100" // Default to 100 logs if not specified
	}
	page := c.Query("page")
	if page == "" {
		page = "1" // Default to page 1 if not specified
	}
	// Convert limit and page to integers
	// If conversion fails, return an error
	limitInt, err := strconv.Atoi(limit)
	if err != nil || limitInt <= 0 {
		resp.RespondError(c, 400, "Invalid limit: "+limit)
		return
	}
	pageInt, err := strconv.Atoi(page)
	if err != nil || pageInt <= 0 {
		resp.RespondError(c, 400, "Invalid page: "+page)
		return
	}
	db := dbcore.GetDBInstance()
	var logs []models.Log
	// 添加分页：计算偏移量并限制数量
	offset := (pageInt - 1) * limitInt

	var total int64
	if err := db.Model(&models.Log{}).Count(&total).Error; err != nil {
		resp.RespondError(c, 500, "Failed to count logs: "+err.Error())
		return
	}

	if err := db.Order("time desc").Limit(limitInt).Offset(offset).Find(&logs).Error; err != nil {
		resp.RespondError(c, 500, "Failed to retrieve logs: "+err.Error())
		return
	}
	resp.RespondSuccess(c, gin.H{"logs": logs, "total": total})
}
