package notification

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/web/api"
)

// ListTrafficReportNotifications GET /api/admin/notification/traffic-report
func ListTrafficReportNotifications(c *gin.Context) {
	notifications, err := notification.ListTrafficReportNotifications()
	if err != nil {
		api.RespondError(c, 500, "Failed to list traffic report notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, notifications)
}

// EditTrafficReportNotifications POST /api/admin/notification/traffic-report/edit
// Body: []TrafficReportNotification
func EditTrafficReportNotifications(c *gin.Context) {
	var notifications []models.TrafficReportNotification
	if err := c.ShouldBindJSON(&notifications); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	if len(notifications) == 0 {
		api.RespondError(c, 400, "At least one notification is required")
		return
	}
	if err := notification.ValidateTrafficReportNotifications(notifications); err != nil {
		api.RespondError(c, 400, err.Error())
		return
	}
	if err := notification.EditTrafficReportNotifications(notifications); err != nil {
		api.RespondError(c, 500, "Failed to edit traffic report notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

// EnableTrafficReportNotifications POST /api/admin/notification/traffic-report/enable
// Body: []string (uuid list)
func EnableTrafficReportNotifications(c *gin.Context) {
	var uuids []string
	if err := c.ShouldBindJSON(&uuids); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	if err := notification.EnableTrafficReportNotifications(uuids); err != nil {
		api.RespondError(c, 500, "Failed to enable traffic report notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

// DisableTrafficReportNotifications POST /api/admin/notification/traffic-report/disable
// Body: []string (uuid list)
func DisableTrafficReportNotifications(c *gin.Context) {
	var uuids []string
	if err := c.ShouldBindJSON(&uuids); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	if err := notification.DisableTrafficReportNotifications(uuids); err != nil {
		api.RespondError(c, 500, "Failed to disable traffic report notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}
