package notification

import (
	"github.com/gin-gonic/gin"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	"gorm.io/gorm/clause"
)

// POST body : []uuid
func EnableOfflineNotification(c *gin.Context) {
	var uuids []string
	if err := c.ShouldBindJSON(&uuids); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	var notifications []models.OfflineNotification
	for _, uuid := range uuids {
		notifications = append(notifications, models.OfflineNotification{
			Client: uuid,
			Enable: true,
		})
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").
		Create(notifications).Error
	if err != nil {
		api.RespondError(c, 500, "Failed to enable offline notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

// POST body : []uuid
func DisableOfflineNotification(c *gin.Context) {
	var uuids []string
	if err := c.ShouldBindJSON(&uuids); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	var notifications []models.OfflineNotification
	for _, uuid := range uuids {
		notifications = append(notifications, models.OfflineNotification{
			Client: uuid,
			Enable: false,
		})
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").
		Create(notifications).Error
	if err != nil {
		api.RespondError(c, 500, "Failed to disable offline notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

func EditOfflineNotification(c *gin.Context) {
	var notifications []models.OfflineNotification
	if err := c.ShouldBindJSON(&notifications); err != nil {
		api.RespondError(c, 400, "Invalid request body: "+err.Error())
		return
	}
	if len(notifications) == 0 {
		api.RespondError(c, 400, "At least one notification is required")
		return
	}
	for _, noti := range notifications {
		if noti.Client == "" {
			api.RespondError(c, 400, "Client UUID cannot be empty")
			return
		}
		if noti.GracePeriod <= 0 {
			api.RespondError(c, 400, "GracePeriod must be a positive integer")
			return
		}
	}
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable", "grace_period"}),
		}).
		Select("*").
		Create(notifications).Error
	if err != nil {
		api.RespondError(c, 500, "Failed to edit offline notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, nil)
}

func ListOfflineNotifications(c *gin.Context) {
	var notifications []models.OfflineNotification
	err := dbcore.GetDBInstance().Model(&models.OfflineNotification{}).Find(&notifications).Error
	if err != nil {
		api.RespondError(c, 500, "Failed to list offline notifications: "+err.Error())
		return
	}
	api.RespondSuccess(c, notifications)
}
