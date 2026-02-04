package database

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func GetPublicInfo() (any, error) {
	cstPtr, err := config.GetManyAs[models.Config]()
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
	if !hasKey("geo_ip_enabled") {
		cst.GeoIpEnabled = true
	}
	if !hasKey("geo_ip_provider") {
		cst.GeoIpProvider = "ip-api"
	}
	if !hasKey("notification_method") {
		cst.NotificationMethod = "none"
	}
	if !hasKey("record_enabled") {
		cst.RecordEnabled = true
	}
	if !hasKey("record_preserve_time") {
		cst.RecordPreserveTime = 720
	}
	if !hasKey("ping_record_preserve_time") {
		cst.PingRecordPreserveTime = 24
	}

	// Fallback defaults if we couldn't enumerate keys.
	if allErr != nil {
		if cst.Sitename == "" {
			cst.Sitename = "Komari"
		}
		if cst.Description == "" {
			cst.Description = "Komari Monitor, a simple server monitoring tool."
		}
		if cst.Theme == "" {
			cst.Theme = "default"
		}
		if cst.OAuthProvider == "" {
			cst.OAuthProvider = "github"
		}
		if cst.RecordPreserveTime == 0 {
			cst.RecordPreserveTime = 720
		}
		if cst.PingRecordPreserveTime == 0 {
			cst.PingRecordPreserveTime = 24
		}
	}

	db := dbcore.GetDBInstance()
	tc := models.ThemeConfiguration{}
	err = db.Model(&models.ThemeConfiguration{}).Where("short = ?", cst.Theme).First(&tc).Error
	if err != nil {
		tc.Data = "{}"
	}
	tc_data := gin.H{}
	err = json.Unmarshal([]byte(tc.Data), &tc_data)
	if err != nil {
		log.Printf("%v", err)
	}
	// Try to load theme declaration file and merge defaults for managed configuration
	// Theme declarations live in ./data/theme/<short>/komari-theme.json
	if cst.Theme != "" && cst.Theme != "default" {
		themeConfigPath := filepath.Join("./data/theme", cst.Theme, "komari-theme.json")
		if _, err := os.Stat(themeConfigPath); err == nil {
			b, err := os.ReadFile(themeConfigPath)
			if err == nil {
				var themeDecl struct {
					Configuration struct {
						Type string                                 `json:"type"`
						Data []models.ManagedThemeConfigurationItem `json:"data"`
					} `json:"configuration"`
				}
				if err := json.Unmarshal(b, &themeDecl); err == nil {
					if themeDecl.Configuration.Type == "managed" {
						for _, item := range themeDecl.Configuration.Data {
							if item.Key == "" {
								continue
							}
							// missing
							if _, exists := tc_data[item.Key]; !exists {
								var def any = item.Default
								// select
								if item.Type == "select" {
									if def == nil || def == "" {
										if item.Options != "" {
											opts := strings.Split(item.Options, ",")
											if len(opts) > 0 {
												def = strings.TrimSpace(opts[0])
											}
										}
									}
								}
								// number->0, string->"", switch->false
								if def == nil {
									switch item.Type {
									case "number":
										def = 0
									case "switch":
										def = false
									default:
										def = ""
									}
								}
								tc_data[item.Key] = def
							}
						}
					}
				}
			}
		}
	}

	return gin.H{
		"sitename":                  cst.Sitename,
		"description":               cst.Description,
		"custom_head":               cst.CustomHead,
		"custom_body":               cst.CustomBody,
		"oauth_enable":              cst.OAuthEnabled,
		"oauth_provider":            cst.OAuthProvider,
		"disable_password_login":    cst.DisablePasswordLogin,
		"allow_cors":                cst.AllowCors,
		"record_enabled":            cst.RecordEnabled,
		"record_preserve_time":      cst.RecordPreserveTime,
		"ping_record_preserve_time": cst.PingRecordPreserveTime,
		"private_site":              cst.PrivateSite,
		"theme":                     cst.Theme,
		"theme_settings":            tc_data,
	}, nil
}
