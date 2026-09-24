package database

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/managedconfig"
	"github.com/komari-monitor/komari/internal/metricstore"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/web/public"
)

func GetPublicInfo() (map[string]interface{}, error) {
	cstPtr, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return nil, err
	}
	cst := *cstPtr

	all, allErr := config.GetAll()
	hasKey := func(k string) bool {
		if allErr != nil {
			return false
		}
		_, ok := all[k]
		return ok
	}

	// Apply defaults only when a key is missing.
	if !hasKey("sitename") {
		cst.Sitename = "Komari"
	}
	if !hasKey("description") {
		cst.Description = "Komari Monitor, a simple server monitoring tool."
	}
	if !hasKey("theme") {
		cst.Theme = "default"
	}
	if !hasKey("o_auth_provider") {
		cst.OAuthProvider = "github"
	}

	// Fallback defaults if we couldn't enumerate keys.
	if allErr != nil {
		if cst.Sitename == "" {
			cst.Sitename = "Komari"
		}
		if cst.Description == "" {
			cst.Description = "Komari Monitor, a simple server monitoring tool."
		}
	}
	retention, err := metricstore.GetRetentionSummary(context.Background())
	if err != nil {
		return nil, err
	}
	db := dbcore.GetDBInstance()
	if cst.Theme != "" && cst.Theme != "default" {
		// Migrate the active theme's saved page values once, then retire the
		// selector. This preserves the user's current Glassmorphism settings
		// even when an older default configuration row already exists.
		var legacy models.ThemeConfiguration
		if err := db.Where("short = ?", cst.Theme).First(&legacy).Error; err == nil {
			var migrated models.ThemeConfiguration
			if err := db.Where("short = ?", "default").
				Assign(models.ThemeConfiguration{Short: "default", Data: legacy.Data}).
				FirstOrCreate(&migrated).Error; err != nil {
				return nil, err
			}
		}
		if err := config.Set(config.ThemeKey, "default"); err != nil {
			return nil, err
		}
	}
	tc := models.ThemeConfiguration{}
	err = db.Model(&models.ThemeConfiguration{}).Where("short = ?", "default").First(&tc).Error
	if err != nil {
		tc.Data = "{}"
	}
	tc_data := gin.H{}
	err = json.Unmarshal([]byte(tc.Data), &tc_data)
	if err != nil {
		logger.Infof("database", "%v", err)
	}
	items := pageConfigurationItems()
	for _, item := range items {
		if item.Key == "" {
			continue
		}
		if _, exists := tc_data[item.Key]; !exists {
			tc_data[item.Key] = managedconfig.DefaultValue(item)
		}
	}
	if err := managedconfig.ResolveForOutput(tc_data, items); err != nil {
		return nil, err
	}

	return gin.H{
		"sitename":                  cst.Sitename,
		"description":               cst.Description,
		"custom_head":               cst.CustomHead,
		"custom_body":               cst.CustomBody,
		"oauth_enable":              cst.OAuthEnabled,
		"oauth_provider":            cst.OAuthProvider,
		"disable_password_login":    cst.DisablePasswordLogin,
		"cors_origin_check_enabled": cst.CorsOriginCheckEnabled,
		"record_enabled":            retention.AllPositive, // 兼容旧版本主题
		"record_preserve_time":      retention.MaxDays * 24,
		"ping_record_preserve_time": retention.MaxDays * 24,
		"private_site":              cst.PrivateSite,
		"visitor_audit_enabled":     cst.VisitorAuditEnabled,
		"theme":                     "default",
		"theme_settings":            tc_data,
	}, nil
}

func pageConfigurationItems() []models.ManagedThemeConfigurationItem {
	var manifest models.Theme
	data, err := public.PublicFS.ReadFile("defaultTheme/komari-theme.json")
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return nil
	}
	return managedconfig.Items(manifest.Configuration)
}
