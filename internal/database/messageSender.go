package database

import (
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/dbcore"
)

func GetAllMessageSenderConfigs() []models.MessageSenderProvider {
	db := dbcore.GetDBInstance()
	var result []models.MessageSenderProvider
	if err := db.Find(&result).Error; err != nil {
		return nil
	}
	return result
}

func GetMessageSenderConfigByName(name string) (*models.MessageSenderProvider, error) {
	db := dbcore.GetDBInstance()
	var config models.MessageSenderProvider
	if err := db.Where("name = ?", name).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

func SaveMessageSenderConfig(config *models.MessageSenderProvider) error {
	db := dbcore.GetDBInstance()
	return db.Save(config).Error
}
