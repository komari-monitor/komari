// Package notifications wires the built-in notification channels into the
// shared messageSender registry.
package notifications

import (
	"encoding/json"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/managedconfig"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/messageSender/webhook"
)

// Initialize registers the built-in webhook channel and seeds its defaults.
func Initialize() error {
	if err := webhook.Register(); err != nil {
		return err
	}
	for _, item := range messageSender.ListNotificationChannels() {
		if _, err := database.GetMessageSenderConfigByName(item.ID); err == nil {
			continue
		}
		values := map[string]any{}
		for _, field := range managedconfig.Items(item.Configuration) {
			if field.Key != "" {
				values[field.Key] = managedconfig.DefaultValue(field)
			}
		}
		raw, err := json.Marshal(values)
		if err != nil {
			return err
		}
		if err := database.SaveMessageSenderConfig(&models.MessageSenderProvider{
			Name:     item.ID,
			Addition: string(raw),
		}); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown unregisters the built-in webhook channel.
func Shutdown() error {
	return messageSender.UnregisterNotificationChannel("webhook")
}
