package messageSender

import (
	"fmt"
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/oauth"
)

func init() {
	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		oldConf, newConf, err := conf.FromEvent(e)
		if err != nil {
			log.Printf("Failed to parse config from event: %v", err)
			return err
		}
		if newConf.Login.OAuthProvider != oldConf.Login.OAuthProvider {
			oidcProvider, err := database.GetOidcConfigByName(newConf.Login.OAuthProvider)
			if err != nil {
				log.Printf("Failed to get OIDC provider config: %v", err)
			} else {
				log.Printf("Using %s as OIDC provider", oidcProvider.Name)
			}
			err = oauth.LoadProvider(oidcProvider.Name, oidcProvider.Addition)
			if err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
		if newConf.Notification.NotificationMethod != oldConf.Notification.NotificationMethod {
			Initialize()
		}
		return nil
	}), event.Max)
}
