package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm"
)

// GetConfiguration returns the saved configuration values of one plugin,
// merged with the manifest-declared defaults for keys that were never saved.
// This mirrors the theme configuration behavior so plugins and the admin UI
// always see a complete value set. Missing rows yield only defaults; a
// missing manifest yields only the saved values.
func GetConfiguration(short string) (map[string]any, error) {
	values, err := savedConfiguration(short)
	if err != nil {
		return nil, err
	}
	info, err := readManifest(filepath.Join(DataDir, short))
	if err != nil {
		return values, nil // not installed (or unreadable): keep saved values
	}
	mergeConfigurationDefaults(values, info.Configuration)
	return values, nil
}

func savedConfiguration(short string) (map[string]any, error) {
	db := dbcore.GetDBInstance()
	var cfg models.PluginConfiguration
	if err := db.Where("short = ?", short).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read plugin configuration: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(cfg.Data), &data); err != nil || data == nil {
		return map[string]any{}, nil
	}
	return data, nil
}

// mergeConfigurationDefaults fills manifest-declared defaults for keys that
// are missing from the saved values, following the theme merge rules.
func mergeConfigurationDefaults(values map[string]any, configuration models.Configuration) {
	if configuration.Type != models.ThemeConfigurationManaged {
		return
	}
	raw, err := json.Marshal(configuration.Data)
	if err != nil {
		return
	}
	var items []models.ManagedThemeConfigurationItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for _, item := range items {
		if item.Key == "" {
			continue
		}
		if _, exists := values[item.Key]; exists {
			continue
		}
		values[item.Key] = configurationDefault(item)
	}
}

// configurationDefault resolves one item's effective default, mirroring the
// theme rules: select falls back to its first option; nil defaults become
// 0 for number, false for switch and "" otherwise.
func configurationDefault(item models.ManagedThemeConfigurationItem) any {
	def := item.Default
	if item.Type == "select" {
		if def == nil || def == "" {
			if item.Options != "" {
				opts := strings.Split(item.Options, ",")
				if len(opts) > 0 {
					return strings.TrimSpace(opts[0])
				}
			}
		}
	}
	if def == nil {
		switch item.Type {
		case "number":
			return float64(0) // JSON numbers decode as float64
		case "switch":
			return false
		default:
			return ""
		}
	}
	return def
}

// SaveConfiguration upserts the configuration values of one plugin.
func SaveConfiguration(short string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal plugin configuration: %w", err)
	}
	db := dbcore.GetDBInstance()
	if err := db.Where("short = ?", short).
		Assign(models.PluginConfiguration{Short: short, Data: string(raw)}).
		FirstOrCreate(&models.PluginConfiguration{}).Error; err != nil {
		return fmt.Errorf("save plugin configuration: %w", err)
	}
	return nil
}
