package patch

import (
	"encoding/json"
	"log"

	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/models"
	"gorm.io/gorm"
)

func v1_0_2_Oidc(db *gorm.DB) {
	log.Println("[>1.0.2] Merge OidcProvider table....")
	var config struct {
		OAuthClientID     string `json:"o_auth_client_id" gorm:"type:varchar(255)"`
		OAuthClientSecret string `json:"o_auth_client_secret" gorm:"type:varchar(255)"`
	}
	if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&config).Error; err != nil {
		log.Println("Failed to get config for OIDC provider migration:", err)
	}
	db.AutoMigrate(&models.OidcProvider{})
	j, err := json.Marshal(&map[string]string{
		"client_id":     config.OAuthClientID,
		"client_secret": config.OAuthClientSecret,
	})
	if err != nil {
		log.Println("Failed to marshal OIDC provider config:", err)
		return
	}
	db.Save(&models.OidcProvider{
		Name:     "github",
		Addition: string(j),
	})
	db.AutoMigrate(&conf.V1Struct{})
	db.Model(&conf.V1Struct{}).Where("id = 1").Update("o_auth_provider", "github")
}

func v1_0_2_MessageSender(db *gorm.DB) {
	log.Println("[>1.0.2] Migrate MessageSender configuration....")
	var config struct {
		TelegramBotToken   string `json:"telegram_bot_token" gorm:"type:varchar(255)"`
		TelegramChatID     string `json:"telegram_chat_id" gorm:"type:varchar(255)"`
		TelegramEndpoint   string `json:"telegram_endpoint" gorm:"type:varchar(255)"`
		EmailHost          string `json:"email_host" gorm:"type:varchar(255)"`
		EmailPort          int    `json:"email_port" gorm:"type:int"`
		EmailUsername      string `json:"email_username" gorm:"type:varchar(255)"`
		EmailPassword      string `json:"email_password" gorm:"type:varchar(255)"`
		EmailSender        string `json:"email_sender" gorm:"type:varchar(255)"`
		EmailReceiver      string `json:"email_receiver" gorm:"type:varchar(255)"`
		EmailUseSSL        bool   `json:"email_use_ssl" gorm:"type:boolean"`
		NotificationMethod string `json:"notification_method" gorm:"type:varchar(50)"`
	}
	if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&config).Error; err != nil {
		log.Println("Failed to get config for MessageSender migration:", err)
	}

	db.AutoMigrate(&models.MessageSenderProvider{})

	// 迁移Telegram配置
	if config.NotificationMethod == "telegram" && config.TelegramBotToken != "" {
		telegramConfig := map[string]interface{}{
			"bot_token": config.TelegramBotToken,
			"chat_id":   config.TelegramChatID,
			"endpoint":  config.TelegramEndpoint,
		}
		if telegramConfig["endpoint"] == "" {
			telegramConfig["endpoint"] = "https://api.telegram.org/bot"
		}
		telegramConfigJSON, err := json.Marshal(telegramConfig)
		if err != nil {
			log.Println("Failed to marshal Telegram config:", err)
		} else {
			db.Save(&models.MessageSenderProvider{
				Name:     "telegram",
				Addition: string(telegramConfigJSON),
			})
		}
	}

	// 迁移Email配置
	if config.NotificationMethod == "email" && config.EmailHost != "" {
		emailConfig := map[string]interface{}{
			"host":     config.EmailHost,
			"port":     config.EmailPort,
			"username": config.EmailUsername,
			"password": config.EmailPassword,
			"sender":   config.EmailSender,
			"receiver": config.EmailReceiver,
			"use_ssl":  config.EmailUseSSL,
		}
		emailConfigJSON, err := json.Marshal(emailConfig)
		if err != nil {
			log.Println("Failed to marshal Email config:", err)
		} else {
			db.Save(&models.MessageSenderProvider{
				Name:     "email",
				Addition: string(emailConfigJSON),
			})
		}
	}

	// 删除旧的配置字段
	if db.Migrator().HasColumn(&conf.V1Struct{}, "telegram_bot_token") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "telegram_bot_token")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "telegram_chat_id") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "telegram_chat_id")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "telegram_endpoint") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "telegram_endpoint")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_host") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_host")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_port") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_port")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_username") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_username")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_password") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_password")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_sender") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_sender")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_receiver") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_receiver")
	}
	if db.Migrator().HasColumn(&conf.V1Struct{}, "email_use_ssl") {
		db.Migrator().DropColumn(&conf.V1Struct{}, "email_use_ssl")
	}
}
