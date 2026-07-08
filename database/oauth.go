package database

import (
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func GetAllOidcConfigs() []models.OidcProvider {
	db := dbcore.GetDBInstance()
	var result []models.OidcProvider
	if err := db.Find(&result).Error; err != nil {
		return nil
	}
	return result
}

func GetOidcConfigByName(name string) (*models.OidcProvider, error) {
	db := dbcore.GetDBInstance()
	var config models.OidcProvider
	if err := db.Where("name = ?", name).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

func DeleteOidcConfigByName(name string) error {
	db := dbcore.GetDBInstance()
	return db.Delete(&models.OidcProvider{}, "name = ?", name).Error
}

func SaveOidcConfig(config *models.OidcProvider) error {
	db := dbcore.GetDBInstance()
	if err := db.Save(config).Error; err != nil {
		return err
	}
	return nil
}
