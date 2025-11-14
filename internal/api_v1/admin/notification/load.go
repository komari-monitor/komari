package notification

import (
	"net/http"

	"github.com/gin-gonic/gin"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/database/notification"
)

// POST body: clients []string, name string, metric string, threshold float32, ratio float32, interval int
func AddLoadNotification(c *gin.Context) {
	var req struct {
		Clients   []string `json:"clients" binding:"required"`
		Name      string   `json:"name"`
		Metric    string   `json:"metric" binding:"required"`
		Threshold float32  `json:"threshold" binding:"required"` // 阈值百分比
		Ratio     float32  `json:"ratio" binding:"required"`     // 达标时间比
		Interval  int      `json:"interval" binding:"required"`  // 间隔时间，单位秒
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if req.Interval > 4*60 || req.Interval <= 0 {
		api.RespondError(c, http.StatusBadRequest, "Interval must be between 1 and 240 minutes")
		return
	}
	if req.Ratio <= 0 || req.Ratio > 1 {
		api.RespondError(c, http.StatusBadRequest, "Ratio must be between 0 and 1")
		return
	}

	if taskID, err := notification.AddLoadNotification(req.Clients, req.Name, req.Metric, req.Threshold, req.Ratio, req.Interval); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		api.RespondSuccess(c, gin.H{"task_id": taskID})
	}
}

// POST body: id []uint
func DeleteLoadNotification(c *gin.Context) {
	var req struct {
		ID []uint `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}

	if err := notification.DeleteLoadNotification(req.ID); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		api.RespondSuccess(c, nil)
	}
}

// POST body: id []uint, updates map[string]interface{}
func EditLoadNotification(c *gin.Context) {
	var req struct {
		Notifications []*models.LoadNotification `json:"notifications" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request data")
		return
	}

	if err := notification.EditLoadNotification(req.Notifications); err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
	} else {
		// for _, notification := range req.Notifications {
		// 	notification.DeleteLoadNotification([]uint{notification.Id})
		// }
		api.RespondSuccess(c, nil)
	}
}

func GetAllLoadNotifications(c *gin.Context) {
	notifications, err := notification.GetAllLoadNotifications()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, err.Error())
		return
	}

	api.RespondSuccess(c, notifications)
}
