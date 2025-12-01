package messageSender

import (
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		oldConf, newConf, err := conf.FromEvent(e)
		if err != nil {
			log.Printf("Failed to parse config from event: %v", err)
			return err
		}
		if newConf.Notification.NotificationMethod != oldConf.Notification.NotificationMethod {
			Initialize()
		}
		return nil
	}), event.Max)
}
