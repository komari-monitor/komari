package notification

import (
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm/clause"
)

// ListTrafficReportNotifications 获取所有流量定时报告配置（关联客户端信息）
func ListTrafficReportNotifications() ([]models.TrafficReportNotification, error) {
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	err := db.Model(&models.TrafficReportNotification{}).Preload("ClientInfo").Find(&notifications).Error
	return notifications, err
}

// EditTrafficReportNotifications 批量更新流量定时报告配置
func EditTrafficReportNotifications(notifications []models.TrafficReportNotification) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.TrafficReportNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable", "daily", "weekly", "monthly"}),
		}).
		Select("client", "enable", "daily", "weekly", "monthly").
		Create(notifications).Error
}

// EnableTrafficReportNotifications 批量启用（仅更新 enable 字段）
func EnableTrafficReportNotifications(uuids []string) error {
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	for _, uuid := range uuids {
		notifications = append(notifications, models.TrafficReportNotification{
			Client: uuid,
			Enable: true,
		})
	}
	return db.Model(&models.TrafficReportNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").
		Create(notifications).Error
}

// DisableTrafficReportNotifications 批量禁用
func DisableTrafficReportNotifications(uuids []string) error {
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	for _, uuid := range uuids {
		notifications = append(notifications, models.TrafficReportNotification{
			Client: uuid,
			Enable: false,
		})
	}
	return db.Model(&models.TrafficReportNotification{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "client"}},
			DoUpdates: clause.AssignmentColumns([]string{"enable"}),
		}).
		Select("client", "enable").
		Create(notifications).Error
}

// GetEnabledTrafficReportByType 获取启用了指定类型报告的客户端配置
func GetEnabledTrafficReportByType(daily, weekly, monthly bool) ([]models.TrafficReportNotification, error) {
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	query := db.Model(&models.TrafficReportNotification{}).Where("enable = ?", true)
	if daily {
		query = query.Where("daily = ?", true)
	} else if weekly {
		query = query.Where("weekly = ?", true)
	} else if monthly {
		query = query.Where("monthly = ?", true)
	}
	err := query.Find(&notifications).Error
	return notifications, err
}
