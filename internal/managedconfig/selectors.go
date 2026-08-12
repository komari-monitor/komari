// Package managedconfig implements the shared managed-configuration behavior
// used by themes and plugins.
package managedconfig

import (
	"encoding/json"
	"strings"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

const (
	TypeNodes     = "nodes"
	TypePingTasks = "pingtasks"
)

// Items returns the declared managed configuration items. An omitted type is
// managed, matching the theme configuration default.
func Items(configuration models.Configuration) []models.ManagedThemeConfigurationItem {
	if configuration.Type != "" && !strings.EqualFold(configuration.Type, models.ThemeConfigurationManaged) {
		return nil
	}
	raw, err := json.Marshal(configuration.Data)
	if err != nil {
		return nil
	}
	var items []models.ManagedThemeConfigurationItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	return items
}

func DefaultValue(item models.ManagedThemeConfigurationItem) any {
	value := item.Default
	if item.Type == "select" && (value == nil || value == "") && item.Options != "" {
		options := strings.Split(item.Options, ",")
		if len(options) > 0 {
			return strings.TrimSpace(options[0])
		}
	}
	if value != nil {
		return value
	}
	switch item.Type {
	case "number":
		return float64(0)
	case "switch":
		return false
	case TypeNodes, TypePingTasks:
		return "[]"
	default:
		return ""
	}
}

// ResolveForOutput decodes declared selectors, drops deleted references, and
// returns typed arrays suitable for public theme settings and plugin config.
func ResolveForOutput(values map[string]any, items []models.ManagedThemeConfigurationItem) error {
	hasNodes := false
	hasPingTasks := false
	for _, item := range items {
		switch item.Type {
		case TypeNodes:
			hasNodes = true
		case TypePingTasks:
			hasPingTasks = true
		}
	}

	db := dbcore.GetDBInstance()
	liveNodes := map[string]struct{}{}
	if hasNodes {
		var nodes []models.Client
		if err := db.Select("uuid").Find(&nodes).Error; err != nil {
			return err
		}
		for _, node := range nodes {
			liveNodes[node.UUID] = struct{}{}
		}
	}
	livePingTasks := map[uint]struct{}{}
	if hasPingTasks {
		var tasks []models.PingTask
		if err := db.Select("id").Find(&tasks).Error; err != nil {
			return err
		}
		for _, task := range tasks {
			livePingTasks[task.Id] = struct{}{}
		}
	}

	for _, item := range items {
		if item.Key == "" {
			continue
		}
		switch item.Type {
		case TypeNodes:
			selected := NodeIDs(values[item.Key])
			filtered := make([]string, 0, len(selected))
			for _, id := range selected {
				if _, ok := liveNodes[id]; ok {
					filtered = append(filtered, id)
				}
			}
			values[item.Key] = filtered
		case TypePingTasks:
			selected := PingTaskIDs(values[item.Key])
			filtered := make([]uint, 0, len(selected))
			for _, id := range selected {
				if _, ok := livePingTasks[id]; ok {
					filtered = append(filtered, id)
				}
			}
			values[item.Key] = filtered
		}
	}
	return nil
}

func NodeIDs(value any) []string {
	raw, ok := value.(string)
	if !ok {
		return nil
	}
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil
	}
	return ids
}

func PingTaskIDs(value any) []uint {
	raw, ok := value.(string)
	if !ok {
		return nil
	}
	var ids []uint
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil
	}
	return ids
}
