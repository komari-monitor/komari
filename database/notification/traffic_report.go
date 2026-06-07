package notification

import (
	"fmt"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm/clause"
)

func validateTrafficReportNotification(notification models.TrafficReportNotification) error {
	if notification.Client == "" {
		return fmt.Errorf("client UUID cannot be empty")
	}
	if notification.Enable && !notification.Daily && !notification.Weekly && !notification.Monthly {
		return fmt.Errorf("at least one cadence must be selected when enabling traffic reports")
	}
	return nil
}

func ValidateTrafficReportNotifications(notifications []models.TrafficReportNotification) error {
	for _, notification := range notifications {
		if err := validateTrafficReportNotification(notification); err != nil {
			return err
		}
	}
	return nil
}

func buildEnabledTrafficReportNotifications(uuids []string, existing []models.TrafficReportNotification) ([]models.TrafficReportNotification, error) {
	if len(uuids) == 0 {
		return nil, fmt.Errorf("at least one client UUID is required")
	}

	existingByClient := make(map[string]models.TrafficReportNotification, len(existing))
	for _, notification := range existing {
		existingByClient[notification.Client] = notification
	}

	notifications := make([]models.TrafficReportNotification, 0, len(uuids))
	for _, uuid := range uuids {
		if uuid == "" {
			return nil, fmt.Errorf("client UUID cannot be empty")
		}
		existingNotification, ok := existingByClient[uuid]
		if !ok || (!existingNotification.Daily && !existingNotification.Weekly && !existingNotification.Monthly) {
			return nil, fmt.Errorf("at least one cadence must be selected when enabling traffic reports")
		}
		notifications = append(notifications, models.TrafficReportNotification{
			Client: uuid,
			Enable: true,
		})
	}

	return notifications, nil
}

// ListTrafficReportNotifications 获取所有流量定时报告配置（关联客户端信息）
func ListTrafficReportNotifications() ([]models.TrafficReportNotification, error) {
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	err := db.Model(&models.TrafficReportNotification{}).Preload("ClientInfo").Find(&notifications).Error
	return notifications, err
}

// EditTrafficReportNotifications 批量更新流量定时报告配置
func EditTrafficReportNotifications(notifications []models.TrafficReportNotification) error {
	if err := ValidateTrafficReportNotifications(notifications); err != nil {
		return err
	}
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
	if len(uuids) == 0 {
		return fmt.Errorf("at least one client UUID is required")
	}

	db := dbcore.GetDBInstance()
	var existing []models.TrafficReportNotification
	if err := db.Where("client IN ?", uuids).Find(&existing).Error; err != nil {
		return err
	}
	notifications, err := buildEnabledTrafficReportNotifications(uuids, existing)
	if err != nil {
		return err
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
	if len(uuids) == 0 {
		return fmt.Errorf("at least one client UUID is required")
	}
	db := dbcore.GetDBInstance()
	var notifications []models.TrafficReportNotification
	for _, uuid := range uuids {
		if uuid == "" {
			return fmt.Errorf("client UUID cannot be empty")
		}
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
