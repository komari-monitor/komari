package factory

import (
	"log"

	"github.com/komari-monitor/komari/utils/item"
)

var (
	providers                = make(map[string]IOidcProvider)
	providerConstructor      = make(map[string]OidcConstructor)
	providersAdditionalItems = make(map[string][]item.Item)
)

func RegisterOidcProvider(constructor OidcConstructor) {
	provider := constructor()
	providerConstructor[provider.GetName()] = constructor
	if provider == nil {
		panic("OIDC provider constructor returned nil")
	}
	if _, exists := providers[provider.GetName()]; exists {
		log.Println("OIDC provider already registered: " + provider.GetName())
	}
	providers[provider.GetName()] = provider

	// 使用反射来提取提供程序的配置字段
	config := provider.GetConfiguration()
	items := item.Parse(config)
	providersAdditionalItems[provider.GetName()] = items
}

func GetProviderConfigs() map[string][]item.Item {
	return providersAdditionalItems
}

func GetAllOidcProviders() map[string]IOidcProvider {
	return providers
}

func GetConstructor(name string) (OidcConstructor, bool) {
	constructor, exists := providerConstructor[name]
	return constructor, exists
}

func GetAllOidcProviderNames() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	return names
}

func Initialize() {
	for _, provider := range providers {
		if err := provider.Init(); err != nil {
			log.Printf("Failed to initialize OIDC provider %s: %v", provider.GetName(), err)
		}
	}
}
