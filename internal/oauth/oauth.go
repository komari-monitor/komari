package oauth

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/oauth/factory"
)

var (
	currentProvider factory.IOidcProvider
	mu              = sync.Mutex{}
)

func CurrentProvider() factory.IOidcProvider {
	mu.Lock()
	defer mu.Unlock()
	return currentProvider
}

func LoadProvider(name string, configJson string) error {
	mu.Lock()
	defer mu.Unlock()
	if currentProvider != nil {
		if err := currentProvider.Destroy(); err != nil {
			log.Printf("Failed to destroy provider %s: %v", currentProvider.GetName(), err)
		}
	}
	constructor, exists := factory.GetConstructor(name)
	if !exists {
		return fmt.Errorf("provider %s not found", name)
	}
	currentProvider = constructor()
	if err := json.Unmarshal([]byte(configJson), currentProvider.GetConfiguration()); err != nil {
		return fmt.Errorf("failed to unmarshal config for provider %s: %w", name, err)
	}
	err := currentProvider.Init()
	if err != nil {
		return fmt.Errorf("failed to initialize provider %s: %w", name, err)
	}
	return nil
}

func init() {
	event.On(eventType.ServerInitializeStart, event.ListenerFunc(func(e event.Event) error {
		all := factory.GetAllOidcProviders()
		for _, provider := range all {
			if _, err := database.GetOidcConfigByName(provider.GetName()); err == nil {
				continue
			}
			// 如果数据库中没有该提供者的配置，则保存默认配置
			config := provider.GetConfiguration()
			configBytes, err := json.Marshal(config)
			if err != nil {
				return fmt.Errorf("failed to marshal config for provider %s: %v", provider.GetName(), err)
			}
			if err := database.SaveOidcConfig(&models.OidcProvider{
				Name:     provider.GetName(),
				Addition: string(configBytes),
			}); err != nil {
				return fmt.Errorf("failed to save default config for provider %s: %v", provider.GetName(), err)
			}
		}
		return nil
	}))

	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		oldConf, newConf, err := conf.FromEvent(e)
		if err != nil {
			log.Printf("Failed to parse config from event: %v", err)
		}

		if newConf.Login.OAuthProvider != oldConf.Login.OAuthProvider {
			oidcProvider, err := database.GetOidcConfigByName(newConf.Login.OAuthProvider)
			if err != nil {
				log.Printf("Failed to get OIDC provider config: %v", err)
			} else {
				log.Printf("Using %s as OIDC provider", oidcProvider.Name)
			}
			err = LoadProvider(oidcProvider.Name, oidcProvider.Addition)
			if err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
		return nil
	}))
}
