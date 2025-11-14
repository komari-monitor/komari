package database

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/database/config"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
)

func GetPublicInfo() (any, error) {
	cst, err := config.Get()
	if err != nil {
		return nil, err
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
