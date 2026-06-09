package migrations

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/pkg/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()

	dsn := "file:" + strings.ReplaceAll(name, " ", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite test db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func TestHasLegacyConfigTable(t *testing.T) {
	t.Run("config item table", func(t *testing.T) {
		db := openTestDB(t, "migrations_config_item")
		if err := db.AutoMigrate(&appconfig.ConfigItem{}); err != nil {
			t.Fatalf("migrate config item table: %v", err)
		}
		if hasLegacyConfigTable(db) {
			t.Fatal("config item table was detected as legacy config table")
		}
	})

	t.Run("legacy config table", func(t *testing.T) {
		db := openTestDB(t, "migrations_legacy_config")
		if err := db.AutoMigrate(&legacyModelConfig{}); err != nil {
			t.Fatalf("migrate legacy config table: %v", err)
		}
		if !hasLegacyConfigTable(db) {
			t.Fatal("legacy config table was not detected")
		}
	})
}

func TestRunSkipsLegacyConfigMigrationForCurrentConfigItemTable(t *testing.T) {
	db := openTestDB(t, "migrations_config_item_run")
	if err := db.AutoMigrate(&appconfig.ConfigItem{}); err != nil {
		t.Fatalf("migrate config item table: %v", err)
	}
	if err := db.Create(&appconfig.ConfigItem{Key: "o_auth_provider", Value: `"github"`}).Error; err != nil {
		t.Fatalf("seed config item: %v", err)
	}

	if err := Run(Context{DB: db}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	if db.Migrator().HasColumn(&legacyModelConfig{}, "id") {
		t.Fatal("config item table was changed into the legacy config shape")
	}
	if db.Migrator().HasTable(&models.OidcProvider{}) {
		t.Fatal("legacy OIDC migration ran against the config item table")
	}

	var item appconfig.ConfigItem
	if err := db.First(&item, "key = ?", "o_auth_provider").Error; err != nil {
		t.Fatalf("config item was not preserved: %v", err)
	}
	if item.Value != `"github"` {
		t.Fatalf("unexpected config value: %s", item.Value)
	}
}

func TestRunPreservesVersion120RuntimeShape(t *testing.T) {
	db := openTestDB(t, "migrations_v120_runtime_shape")
	if err := db.AutoMigrate(
		&appconfig.ConfigItem{},
		&models.OidcProvider{},
		&models.MessageSenderProvider{},
		&models.Client{},
		&models.PingTask{},
	); err != nil {
		t.Fatalf("migrate 1.2.0 runtime shape: %v", err)
	}
	now := models.FromTime(time.Now())
	if err := db.Create(&models.Client{UUID: "client-a", Token: "token-a", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&models.PingTask{
		Name:      "already explicit",
		Clients:   models.StringArray{"client-a"},
		DefaultOn: true,
		Type:      "icmp",
		Target:    "example.com",
		Interval:  60,
	}).Error; err != nil {
		t.Fatalf("seed ping task: %v", err)
	}
	if err := db.Create(&appconfig.ConfigItem{Key: appconfig.OAuthProviderKey, Value: `"github"`}).Error; err != nil {
		t.Fatalf("seed config item: %v", err)
	}
	if err := db.Create(&models.OidcProvider{Name: "github", Addition: `{"client_id":"old","client_secret":"secret"}`}).Error; err != nil {
		t.Fatalf("seed oidc provider: %v", err)
	}
	if err := db.Create(&models.MessageSenderProvider{Name: "telegram", Addition: `{"bot_token":"old-token"}`}).Error; err != nil {
		t.Fatalf("seed message sender provider: %v", err)
	}

	if err := Run(Context{DB: db}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	if db.Migrator().HasColumn(&legacyModelConfig{}, "sitename") {
		t.Fatal("current config item table was treated as legacy wide config")
	}

	var configItem appconfig.ConfigItem
	if err := db.First(&configItem, "key = ?", appconfig.OAuthProviderKey).Error; err != nil {
		t.Fatalf("find config item: %v", err)
	}
	if configItem.Value != `"github"` {
		t.Fatalf("unexpected config item value: %s", configItem.Value)
	}

	var oidc models.OidcProvider
	if err := db.First(&oidc, "name = ?", "github").Error; err != nil {
		t.Fatalf("find oidc provider: %v", err)
	}
	if oidc.Addition != `{"client_id":"old","client_secret":"secret"}` {
		t.Fatalf("oidc provider was unexpectedly changed: %s", oidc.Addition)
	}

	var sender models.MessageSenderProvider
	if err := db.First(&sender, "name = ?", "telegram").Error; err != nil {
		t.Fatalf("find message sender provider: %v", err)
	}
	if sender.Addition != `{"bot_token":"old-token"}` {
		t.Fatalf("message sender provider was unexpectedly changed: %s", sender.Addition)
	}

	var task models.PingTask
	if err := db.First(&task, "name = ?", "already explicit").Error; err != nil {
		t.Fatalf("find ping task: %v", err)
	}
	if len(task.Clients) != 1 || task.Clients[0] != "client-a" {
		t.Fatalf("explicit ping task clients were changed: %v", task.Clients)
	}
}

func TestRunMigratesLegacyConfigTableToConfigItems(t *testing.T) {
	db := openTestDB(t, "migrations_legacy_config_to_items")
	if err := db.AutoMigrate(&legacyModelConfig{}); err != nil {
		t.Fatalf("migrate legacy config table: %v", err)
	}
	legacy := legacyModelConfig{
		Sitename:                   "Old Komari",
		Description:                "legacy description",
		Theme:                      "classic",
		GeoIpEnabled:               true,
		GeoIpProvider:              "ip-api",
		OAuthProvider:              "github",
		NotificationMethod:         "none",
		TrafficLimitPercentage:     66.5,
		RecordEnabled:              true,
		RecordPreserveTime:         48,
		PingRecordPreserveTime:     12,
		ExpireNotificationLeadDays: 3,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	if err := Run(Context{DB: db}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	if db.Migrator().HasColumn(&legacyModelConfig{}, "sitename") {
		t.Fatal("legacy config columns were not removed")
	}

	var sitename appconfig.ConfigItem
	if err := db.First(&sitename, "key = ?", appconfig.SitenameKey).Error; err != nil {
		t.Fatalf("find migrated sitename: %v", err)
	}
	if sitename.Value != `"Old Komari"` {
		t.Fatalf("unexpected sitename value: %s", sitename.Value)
	}

	var corsOriginCheck appconfig.ConfigItem
	if err := db.First(&corsOriginCheck, "key = ?", appconfig.CorsOriginCheckEnabledKey).Error; err == nil {
		t.Fatalf("unexpected migrated cors_origin_check_enabled value: %s", corsOriginCheck.Value)
	}
}

func TestRunExpandsLegacyPingAllClientsTasks(t *testing.T) {
	db := openTestDB(t, "migrations_ping_all_clients")
	if err := db.AutoMigrate(&models.Client{}); err != nil {
		t.Fatalf("migrate clients: %v", err)
	}
	if err := db.Exec(`
		CREATE TABLE ping_tasks (
			id integer primary key autoincrement,
			weight integer not null default 0,
			name varchar(255) not null,
			all_clients boolean not null default false,
			type varchar(12) not null default 'icmp',
			target varchar(255) not null,
			interval integer not null default 60
		)
	`).Error; err != nil {
		t.Fatalf("create legacy ping_tasks: %v", err)
	}
	now := models.FromTime(time.Now())
	clients := []models.Client{
		{UUID: "client-a", Token: "token-a", CreatedAt: now, UpdatedAt: now},
		{UUID: "client-b", Token: "token-b", CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&clients).Error; err != nil {
		t.Fatalf("seed clients: %v", err)
	}
	if err := db.Exec("INSERT INTO ping_tasks (name, all_clients, type, target, interval) VALUES (?, ?, ?, ?, ?)", "legacy task", true, "icmp", "example.com", 60).Error; err != nil {
		t.Fatalf("seed legacy ping task: %v", err)
	}

	if err := Run(Context{DB: db}); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	var task models.PingTask
	if err := db.First(&task).Error; err != nil {
		t.Fatalf("find migrated ping task: %v", err)
	}
	if len(task.Clients) != 2 {
		t.Fatalf("expected two migrated clients, got %v", task.Clients)
	}
	got := map[string]bool{}
	for _, uuid := range task.Clients {
		got[uuid] = true
	}
	if !got["client-a"] || !got["client-b"] {
		t.Fatalf("unexpected migrated clients: %v", task.Clients)
	}

	raw, err := json.Marshal(task.Clients)
	if err != nil {
		t.Fatalf("marshal migrated clients: %v", err)
	}
	if string(raw) != `["client-a","client-b"]` {
		t.Fatalf("unexpected clients json: %s", raw)
	}
}
