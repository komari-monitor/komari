package patch

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/komari-monitor/komari/internal/conf"
	_ "github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// v1_1_4_PreMigration 在数据库和配置加载前执行，直接操作 SQLite 数据库文件
func v1_1_4_PreMigration() {
	// 检查数据库文件是否存在
	if _, err := os.Stat("./data/komari.db"); os.IsNotExist(err) {
		// 数据库文件不存在，无需迁移
		return
	}

	if _, err := os.Stat("./data/komari.json"); err == nil {
		// 配置文件已存在，无需迁移
		return
	}

	// 打开 SQLite 数据库
	db, err := sql.Open("sqlite3", "./data/komari.db")
	if err != nil {
		slog.Error("[>1.1.4] Failed to open database file for migration.", slog.Any("error", err))
		return
	}
	defer db.Close()

	// 检查配置表是否存在
	var tableExists bool
	row := db.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name='configs'`)
	if err := row.Scan(&tableExists); err != nil {
		slog.Error("[>1.1.4] Failed to check configs table existence.", slog.Any("error", err))
		return
	}

	if !tableExists {
		// 表不存在，无需迁移
		return
	}

	// 检查是否已经迁移过（通过检查 id 列）
	var idColumnExists bool
	rows, err := db.Query(`PRAGMA table_info(configs)`)
	if err != nil {
		slog.Error("[>1.1.4] Failed to check configs table schema.", slog.Any("error", err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typeStr string
		var notnull int
		var dfltValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typeStr, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		if name == "id" {
			idColumnExists = true
			break
		}
	}

	if !idColumnExists {
		// id 列不存在，说明已经迁移过或不需要迁移
		return
	}

	slog.Info("[>1.1.4] Starting config table migration to file config...")

	// 从数据库读取配置
	row = db.QueryRow(`SELECT 
		id, sitename, description, allow_cors, theme, private_site, api_key, auto_discovery_key,
		script_domain, geo_ip_enabled, geo_ip_provider,
		nezha_compat_enabled, nezha_compat_listen, o_auth_enabled, o_auth_provider,
		disable_password_login, custom_head, custom_body, notification_enabled,
		notification_method, notification_template, expire_notification_enabled,
		expire_notification_lead_days, login_notification, traffic_limit_percentage,
		record_enabled, record_preserve_time, ping_record_preserve_time
	FROM configs LIMIT 1`)

	var (
		id                         uint
		sitename                   string
		description                string
		allowCors                  bool
		theme                      string
		privateSite                bool
		apiKey                     string
		autoDiscoveryKey           string
		scriptDomain               string
		geoIpEnabled               bool
		geoIpProvider              string
		nezhaCompatEnabled         bool
		nezhaCompatListen          string
		oAuthEnabled               bool
		oAuthProvider              string
		disablePasswordLogin       bool
		customHead                 string
		customBody                 string
		notificationEnabled        bool
		notificationMethod         string
		notificationTemplate       string
		expireNotificationEnabled  bool
		expireNotificationLeadDays int
		loginNotification          bool
		trafficLimitPercentage     float64
		recordEnabled              bool
		recordPreserveTime         int
		pingRecordPreserveTime     int
	)

	if err := row.Scan(
		&id, &sitename, &description, &allowCors, &theme, &privateSite, &apiKey, &autoDiscoveryKey,
		&scriptDomain, &geoIpEnabled, &geoIpProvider,
		&nezhaCompatEnabled, &nezhaCompatListen, &oAuthEnabled, &oAuthProvider,
		&disablePasswordLogin, &customHead, &customBody, &notificationEnabled,
		&notificationMethod, &notificationTemplate, &expireNotificationEnabled,
		&expireNotificationLeadDays, &loginNotification, &trafficLimitPercentage,
		&recordEnabled, &recordPreserveTime, &pingRecordPreserveTime,
	); err != nil {
		slog.Error("[>1.1.4] Failed to read config from database.", slog.Any("error", err))
		return
	}

	// 关闭数据库连接
	db.Close()
	newConf := conf.Default()
	// 将读取到的值赋给新的配置结构体
	newConf.Site.Sitename = sitename
	newConf.Site.Description = description
	newConf.Site.AllowCors = allowCors
	newConf.Site.Theme = theme
	newConf.Site.PrivateSite = privateSite
	newConf.Site.ScriptDomain = scriptDomain
	newConf.Site.SendIpAddrToGuest = false // 较新的字段
	newConf.Site.EulaAccepted = false      // 较新的字段
	newConf.Login.ApiKey = apiKey
	newConf.Login.AutoDiscoveryKey = autoDiscoveryKey
	newConf.Login.OAuthEnabled = oAuthEnabled
	newConf.Login.OAuthProvider = oAuthProvider
	newConf.Login.DisablePasswordLogin = disablePasswordLogin
	newConf.GeoIp.GeoIpEnabled = geoIpEnabled
	newConf.GeoIp.GeoIpProvider = geoIpProvider
	newConf.Notification.NotificationEnabled = notificationEnabled
	newConf.Notification.NotificationMethod = notificationMethod
	newConf.Notification.NotificationTemplate = notificationTemplate
	newConf.Notification.ExpireNotificationEnabled = expireNotificationEnabled
	newConf.Notification.ExpireNotificationLeadDays = expireNotificationLeadDays
	newConf.Notification.LoginNotification = loginNotification
	newConf.Notification.TrafficLimitPercentage = trafficLimitPercentage
	newConf.Record.RecordEnabled = recordEnabled
	newConf.Record.RecordPreserveTime = recordPreserveTime
	newConf.Record.PingRecordPreserveTime = pingRecordPreserveTime
	newConf.Extensions["nezha"] = map[string]interface{}{
		"nezha_compat_enabled": nezhaCompatEnabled,
		"nezha_compat_listen":  nezhaCompatListen,
	}

	b, err := json.MarshalIndent(newConf, "", "  ")

	if err != nil {
		slog.Error("[>1.1.4] Failed to marshal config.", slog.Any("error", err))
		return
	}

	if err := os.WriteFile("./data/komari.json", b, 0644); err != nil {
		slog.Error("[>1.1.4] Failed to write new file config.", slog.Any("error", err))
		return
	}

	slog.Info("[>1.1.4] Config migration to file config finished.")
}

func v1_1_4(db *gorm.DB) {
	// 迁移已在 v1_1_4_PreMigration() 中完成，此处仅清理数据库中的配置表
	slog.Info("[>1.1.4] Dropping configs table...")
	db.Migrator().DropTable(&conf.V1Struct{})
	slog.Info("[>1.1.4] Configs table dropped.")
}
