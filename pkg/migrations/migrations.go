package migrations

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/pkg/config"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type legacyModelConfig struct {
	ID                         uint    `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	Sitename                   string  `json:"sitename" gorm:"type:varchar(100);not null"`
	Description                string  `json:"description" gorm:"type:text"`
	Theme                      string  `json:"theme" gorm:"type:varchar(100);default:'default'"`
	PrivateSite                bool    `json:"private_site" gorm:"default:false"`
	ApiKey                     string  `json:"api_key" gorm:"type:varchar(255);default:''"`
	AutoDiscoveryKey           string  `json:"auto_discovery_key" gorm:"type:varchar(255);default:''"`
	ScriptDomain               string  `json:"script_domain" gorm:"type:varchar(255);default:''"`
	SendIpAddrToGuest          bool    `json:"send_ip_addr_to_guest" gorm:"default:false"`
	EulaAccepted               bool    `json:"eula_accepted" gorm:"default:false"`
	GeoIpEnabled               bool    `json:"geo_ip_enabled" gorm:"default:true"`
	GeoIpProvider              string  `json:"geo_ip_provider" gorm:"type:varchar(20);default:'ip-api'"`
	NezhaCompatEnabled         bool    `json:"nezha_compat_enabled" gorm:"default:false"`
	NezhaCompatListen          string  `json:"nezha_compat_listen" gorm:"type:varchar(100);default:''"`
	OAuthEnabled               bool    `json:"o_auth_enabled" gorm:"default:false"`
	OAuthProvider              string  `json:"o_auth_provider" gorm:"type:varchar(50);default:'github'"`
	DisablePasswordLogin       bool    `json:"disable_password_login" gorm:"default:false"`
	CustomHead                 string  `json:"custom_head" gorm:"type:longtext"`
	CustomBody                 string  `json:"custom_body" gorm:"type:longtext"`
	NotificationEnabled        bool    `json:"notification_enabled" gorm:"default:false"`
	NotificationMethod         string  `json:"notification_method" gorm:"type:varchar(64);default:'none'"`
	NotificationTemplate       string  `json:"notification_template" gorm:"type:longtext;default:'{{emoji}}{{emoji}}{{emoji}}\nEvent: {{event}}\nClients: {{client}}\nMessage: {{message}}\nTime: {{time}}'"`
	ExpireNotificationEnabled  bool    `json:"expire_notification_enabled" gorm:"default:false"`
	ExpireNotificationLeadDays int     `json:"expire_notification_lead_days" gorm:"default:7"`
	LoginNotification          bool    `json:"login_notification" gorm:"default:false"`
	TrafficLimitPercentage     float64 `json:"traffic_limit_percentage" gorm:"default:80.00"`
	RecordEnabled              bool    `json:"record_enabled" gorm:"default:true"`
	RecordPreserveTime         int     `json:"record_preserve_time" gorm:"default:720"`
	PingRecordPreserveTime     int     `json:"ping_record_preserve_time" gorm:"default:24"`
	CreatedAt                  models.LocalTime
	UpdatedAt                  models.LocalTime
}

func (legacyModelConfig) TableName() string {
	return "configs"
}

type legacyConfig struct {
	ID                         uint      `json:"id,omitempty"`
	Sitename                   string    `json:"sitename"`
	Description                string    `json:"description"`
	Theme                      string    `json:"theme"`
	PrivateSite                bool      `json:"private_site"`
	ApiKey                     string    `json:"api_key"`
	AutoDiscoveryKey           string    `json:"auto_discovery_key"`
	ScriptDomain               string    `json:"script_domain"`
	SendIpAddrToGuest          bool      `json:"send_ip_addr_to_guest"`
	EulaAccepted               bool      `json:"eula_accepted"`
	BaseScriptsURLKey          string    `json:"base_scripts_url"`
	GeoIpEnabled               bool      `json:"geo_ip_enabled"`
	GeoIpProvider              string    `json:"geo_ip_provider"`
	NezhaCompatEnabled         bool      `json:"nezha_compat_enabled"`
	NezhaCompatListen          string    `json:"nezha_compat_listen"`
	OAuthEnabled               bool      `json:"o_auth_enabled"`
	OAuthProvider              string    `json:"o_auth_provider"`
	DisablePasswordLogin       bool      `json:"disable_password_login"`
	CloudflareTunnelToken      string    `json:"cloudflare_tunnel_token"`
	CustomHead                 string    `json:"custom_head"`
	CustomBody                 string    `json:"custom_body"`
	NotificationEnabled        bool      `json:"notification_enabled"`
	NotificationMethod         string    `json:"notification_method"`
	NotificationTemplate       string    `json:"notification_template"`
	ExpireNotificationEnabled  bool      `json:"expire_notification_enabled"`
	ExpireNotificationLeadDays int       `json:"expire_notification_lead_days"`
	LoginNotification          bool      `json:"login_notification"`
	TrafficLimitPercentage     float64   `json:"traffic_limit_percentage"`
	RecordEnabled              bool      `json:"record_enabled"`
	RecordPreserveTime         int       `json:"record_preserve_time"`
	PingRecordPreserveTime     int       `json:"ping_record_preserve_time"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

func (legacyConfig) TableName() string {
	return "configs"
}

type legacyPingTask struct {
	Id      uint   `gorm:"column:id"`
	Clients string `gorm:"column:clients"`
}

type Context struct {
	DB *gorm.DB
}

// Run executes one-shot startup migrations before current runtime paths are used.
func Run(ctx Context) error {
	db := ctx.DB
	if db == nil {
		return fmt.Errorf("migration database is nil")
	}

	legacyConfigTable := hasLegacyConfigTable(db)

	if legacyConfigTable {
		if err := migrateLegacyOidcConfig(db); err != nil {
			return err
		}
		if err := migrateLegacyMessageSenderConfig(db); err != nil {
			return err
		}
	}

	if err := migrateLegacyClientInfo(db); err != nil {
		return err
	}
	if err := migrateLegacyLoadNotification(db); err != nil {
		return err
	}
	if err := migrateLegacyPingAllClientsExpansion(db); err != nil {
		return err
	}
	if legacyConfigTable {
		if err := migrateLegacyConfigToItems(db); err != nil {
			return err
		}
	}

	return nil
}

func hasLegacyConfigTable(db *gorm.DB) bool {
	if !db.Migrator().HasTable("configs") {
		return false
	}
	return hasTableColumn(db, "configs", "id") ||
		hasTableColumn(db, "configs", "sitename")
}

func hasTableColumn(db *gorm.DB, tableName, columnName string) bool {
	columns, err := db.Migrator().ColumnTypes(tableName)
	if err != nil {
		return false
	}
	for _, column := range columns {
		if strings.EqualFold(column.Name(), columnName) {
			return true
		}
	}
	return false
}

func migrateLegacyLoadNotification(db *gorm.DB) error {
	if db.Migrator().HasColumn(&models.LoadNotification{}, "client") {
		log.Println("[>0.1.4] Rebuilding LoadNotification table....")
		return db.Migrator().DropTable(&models.LoadNotification{})
	}
	return nil
}

func migrateLegacyOidcConfig(db *gorm.DB) error {
	if db.Migrator().HasTable(&models.OidcProvider{}) {
		return nil
	}

	log.Println("[>1.0.2] Merge OidcProvider table....")
	var oldData struct {
		OAuthClientID     string `gorm:"column:o_auth_client_id"`
		OAuthClientSecret string `gorm:"column:o_auth_client_secret"`
	}
	if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&oldData).Error; err != nil {
		return fmt.Errorf("get legacy OIDC config: %w", err)
	}

	if err := db.AutoMigrate(&models.OidcProvider{}); err != nil {
		return err
	}
	addition, err := json.Marshal(map[string]string{
		"client_id":     oldData.OAuthClientID,
		"client_secret": oldData.OAuthClientSecret,
	})
	if err != nil {
		return fmt.Errorf("marshal legacy OIDC config: %w", err)
	}
	if err := db.Save(&models.OidcProvider{
		Name:     "github",
		Addition: string(addition),
	}).Error; err != nil {
		return err
	}

	if err := db.AutoMigrate(&legacyModelConfig{}); err != nil {
		return err
	}
	return db.Model(&legacyModelConfig{}).Where("id = 1").Update("o_auth_provider", "github").Error
}

func migrateLegacyMessageSenderConfig(db *gorm.DB) error {
	if db.Migrator().HasTable(&models.MessageSenderProvider{}) {
		return nil
	}

	log.Println("[>1.0.2] Migrate MessageSender configuration....")
	var oldData struct {
		TelegramBotToken   string `gorm:"column:telegram_bot_token"`
		TelegramChatID     string `gorm:"column:telegram_chat_id"`
		TelegramEndpoint   string `gorm:"column:telegram_endpoint"`
		EmailHost          string `gorm:"column:email_host"`
		EmailPort          int    `gorm:"column:email_port"`
		EmailUsername      string `gorm:"column:email_username"`
		EmailPassword      string `gorm:"column:email_password"`
		EmailSender        string `gorm:"column:email_sender"`
		EmailReceiver      string `gorm:"column:email_receiver"`
		EmailUseSSL        bool   `gorm:"column:email_use_ssl"`
		NotificationMethod string `gorm:"column:notification_method"`
	}
	if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&oldData).Error; err != nil {
		return fmt.Errorf("get legacy message sender config: %w", err)
	}
	if err := db.AutoMigrate(&models.MessageSenderProvider{}); err != nil {
		return err
	}

	if oldData.NotificationMethod == "telegram" && oldData.TelegramBotToken != "" {
		telegramConfig := map[string]interface{}{
			"bot_token": oldData.TelegramBotToken,
			"chat_id":   oldData.TelegramChatID,
			"endpoint":  oldData.TelegramEndpoint,
		}
		if telegramConfig["endpoint"] == "" {
			telegramConfig["endpoint"] = "https://api.telegram.org/bot"
		}
		if err := saveLegacyMessageSenderConfig(db, "telegram", telegramConfig); err != nil {
			return err
		}
	}

	if oldData.NotificationMethod == "email" && oldData.EmailHost != "" {
		emailConfig := map[string]interface{}{
			"host":     oldData.EmailHost,
			"port":     oldData.EmailPort,
			"username": oldData.EmailUsername,
			"password": oldData.EmailPassword,
			"sender":   oldData.EmailSender,
			"receiver": oldData.EmailReceiver,
			"use_ssl":  oldData.EmailUseSSL,
		}
		if err := saveLegacyMessageSenderConfig(db, "email", emailConfig); err != nil {
			return err
		}
	}

	for _, column := range []string{
		"telegram_bot_token",
		"telegram_chat_id",
		"telegram_endpoint",
		"email_host",
		"email_port",
		"email_username",
		"email_password",
		"email_sender",
		"email_receiver",
		"email_use_ssl",
	} {
		if hasTableColumn(db, "configs", column) {
			if err := db.Migrator().DropColumn(&legacyModelConfig{}, column); err != nil {
				return err
			}
		}
	}

	return nil
}

func saveLegacyMessageSenderConfig(db *gorm.DB, name string, config map[string]interface{}) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal legacy %s message sender config: %w", name, err)
	}
	return db.Save(&models.MessageSenderProvider{
		Name:     name,
		Addition: string(configJSON),
	}).Error
}

func migrateLegacyConfigToItems(db *gorm.DB) error {
	log.Println("[>1.1.4] Moving legacy config data...")

	var oldData legacyConfig
	if err := db.Order("id desc").First(&oldData).Error; err != nil {
		if err := db.Migrator().DropTable("configs"); err != nil {
			return err
		}
		return db.AutoMigrate(&appconfig.ConfigItem{})
	}

	newRows, err := legacyConfigRows(oldData)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Migrator().DropTable("configs"); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&appconfig.ConfigItem{}); err != nil {
			return err
		}
		if len(newRows) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).Create(&newRows).Error
	})
}

func legacyConfigRows(oldData legacyConfig) ([]appconfig.ConfigItem, error) {
	val := reflect.ValueOf(oldData)
	typ := reflect.TypeOf(oldData)
	newRows := make([]appconfig.ConfigItem, 0, val.NumField())

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		key := strings.Split(tag, ",")[0]
		if key == "" || key == "-" || key == "id" {
			continue
		}

		jsonBytes, err := json.Marshal(val.Field(i).Interface())
		if err != nil {
			return nil, fmt.Errorf("marshal legacy config %s: %w", key, err)
		}
		newRows = append(newRows, appconfig.ConfigItem{
			Key:   key,
			Value: string(jsonBytes),
		})
	}

	return newRows, nil
}

func migrateLegacyClientInfo(db *gorm.DB) error {
	if !db.Migrator().HasTable("client_infos") {
		return nil
	}

	log.Println("[>0.0.5] Legacy ClientInfo table detected, starting data migration...")
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		return err
	}

	var clientInfos []ClientInfo
	if err := db.Find(&clientInfos).Error; err != nil {
		return fmt.Errorf("read legacy ClientInfo table: %w", err)
	}

	for _, info := range clientInfos {
		var client models.Client
		if err := db.Where("uuid = ?", info.UUID).First(&client).Error; err != nil {
			log.Printf("Could not find Client record with UUID %s: %v", info.UUID, err)
			continue
		}

		client.Name = info.Name
		client.CpuName = info.CpuName
		client.Virtualization = info.Virtualization
		client.Arch = info.Arch
		client.CpuCores = info.CpuCores
		client.OS = info.OS
		client.GpuName = info.GpuName
		client.IPv4 = info.IPv4
		client.IPv6 = info.IPv6
		client.Region = info.Region
		client.Remark = info.Remark
		client.PublicRemark = info.PublicRemark
		client.MemTotal = info.MemTotal
		client.SwapTotal = info.SwapTotal
		client.DiskTotal = info.DiskTotal
		client.Version = info.Version
		client.Weight = info.Weight
		client.Price = info.Price
		client.BillingCycle = info.BillingCycle
		client.ExpiredAt = models.FromTime(info.ExpiredAt)
		if err := db.Save(&client).Error; err != nil {
			return fmt.Errorf("update Client record %s: %w", info.UUID, err)
		}
	}

	if err := db.Migrator().RenameTable("client_infos", "client_infos_backup"); err != nil {
		return fmt.Errorf("backup legacy ClientInfo table: %w", err)
	}
	log.Println("Data migration completed, old table has been backed up as client_infos_backup")
	return nil
}

func migrateLegacyPingAllClientsExpansion(db *gorm.DB) error {
	if !db.Migrator().HasTable(&models.PingTask{}) || !db.Migrator().HasTable(&models.Client{}) {
		return nil
	}
	if !hasTableColumn(db, "ping_tasks", "all_clients") {
		return nil
	}
	if !hasTableColumn(db, "ping_tasks", "clients") {
		if err := db.Exec("ALTER TABLE ping_tasks ADD COLUMN clients text").Error; err != nil {
			return fmt.Errorf("add clients column for legacy ping task expansion: %w", err)
		}
	}
	if err := db.Table("ping_tasks").
		Where("clients IS NULL OR clients = '' OR clients = '[]' OR clients = 'null'").
		Update("clients", models.StringArray{}).Error; err != nil {
		return fmt.Errorf("normalize legacy ping task clients: %w", err)
	}

	var pingTasks []legacyPingTask
	if err := db.Table("ping_tasks").Select("id, clients").Where("all_clients = ?", true).Scan(&pingTasks).Error; err != nil {
		return fmt.Errorf("find legacy all_clients ping tasks: %w", err)
	}
	if len(pingTasks) == 0 {
		return nil
	}

	var clients []models.Client
	if err := db.Select("uuid").Find(&clients).Error; err != nil {
		return fmt.Errorf("find clients for legacy ping task expansion: %w", err)
	}
	if len(clients) == 0 {
		return nil
	}

	allUUIDs := make(models.StringArray, 0, len(clients))
	for _, client := range clients {
		if client.UUID != "" {
			allUUIDs = append(allUUIDs, client.UUID)
		}
	}
	if len(allUUIDs) == 0 {
		return nil
	}

	for _, task := range pingTasks {
		if !isLegacyPingClientsEmpty(task.Clients) {
			continue
		}
		if err := db.Table("ping_tasks").Where("id = ?", task.Id).Update("clients", allUUIDs).Error; err != nil {
			return fmt.Errorf("expand legacy all_clients ping task %d: %w", task.Id, err)
		}
	}
	return nil
}

func isLegacyPingClientsEmpty(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return true
	}
	var clients []string
	if err := json.Unmarshal([]byte(raw), &clients); err != nil {
		return false
	}
	return len(clients) == 0
}
