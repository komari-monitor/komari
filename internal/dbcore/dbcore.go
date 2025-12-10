package dbcore

import (
	"fmt"
	"log"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/models"
	"gorm.io/gorm"
)

var (
	instance *gorm.DB
)

type SchemaVersion struct {
	ID      uint   `gorm:"primaryKey"`
	Version string `gorm:"type:varchar(50);not null"`
}

func GetDBInstance() *gorm.DB {
	return instance
}

// needsMigration 检查是否需要运行数据库迁移（版本号不一致时需要）
func needsMigration(db *gorm.DB) bool {
	// 先确保 schema_versions 表存在
	if err := db.AutoMigrate(&SchemaVersion{}); err != nil {
		log.Printf("Failed to create schema_versions table: %v", err)
		return true
	}

	var sv SchemaVersion
	if err := db.First(&sv).Error; err != nil {
		// 没有记录，首次运行
		return true
	}

	// 版本不一致则需要迁移
	return sv.Version != conf.Version
}

// updateSchemaVersion 更新版本记录
func updateSchemaVersion(db *gorm.DB) {
	var sv SchemaVersion
	if err := db.First(&sv).Error; err != nil {
		db.Create(&SchemaVersion{Version: conf.Version})
	} else {
		db.Model(&sv).Update("version", conf.Version)
	}
}

// setMySQLTableCharset 设置 MySQL 表的字符集为 utf8mb4
func setMySQLTableCharset(db *gorm.DB, tableName string) {
	if conf.Conf.Database.DatabaseType == "mysql" {
		db.Exec(fmt.Sprintf("ALTER TABLE `%s` CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", tableName))
	}
}

// runMigrations 执行所有数据库迁移
func runMigrations(db *gorm.DB) error {
	// 定义所有需要迁移的模型
	allModels := []interface{}{
		&models.User{},
		&models.Client{},
		&models.Record{},
		&models.GPURecord{},
		&models.Log{},
		&models.Clipboard{},
		&models.LoadNotification{},
		&models.OfflineNotification{},
		&models.PingRecord{},
		&models.PingTask{},
		&models.OidcProvider{},
		&models.MessageSenderProvider{},
		&models.ThemeConfiguration{},
		&models.Session{},
		&models.Task{},
		&models.TaskResult{},
	}

	// 执行 AutoMigrate
	err := db.AutoMigrate(allModels...)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// 为 MySQL 设置表字符集
	if conf.Conf.Database.DatabaseType == "mysql" {
		tableNames := []string{
			"users", "clients", "records", "gpu_records", "logs", "clipboards",
			"load_notifications", "offline_notifications", "ping_records", "ping_tasks",
			"oidc_providers", "message_sender_providers", "theme_configurations",
			"sessions", "tasks", "task_results", "schema_versions",
		}
		for _, tableName := range tableNames {
			setMySQLTableCharset(db, tableName)
		}
	}

	// 创建长期记录表
	if err := db.Table("records_long_term").AutoMigrate(&models.Record{}); err != nil {
		log.Printf("Failed to create records_long_term table: %v", err)
	} else {
		setMySQLTableCharset(db, "records_long_term")
	}

	if err := db.Table("gpu_records_long_term").AutoMigrate(&models.GPURecord{}); err != nil {
		log.Printf("Failed to create gpu_records_long_term table: %v", err)
	} else {
		setMySQLTableCharset(db, "gpu_records_long_term")
	}

	return nil
}
