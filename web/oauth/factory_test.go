package oauth

import (
	"testing"

	"github.com/komari-monitor/komari/web/oauth/factory"
)

// Test function
func TestRegisterAndGetProviderConfigs(t *testing.T) {
	All()
	configs := factory.GetProviderConfigs()
	if len(configs) == 0 {
		t.Error("Expected non-empty provider configs, got empty")
	}
	providers := factory.GetAllOidcProviders()
	if len(providers) == 0 {
		t.Error("Expected non-empty OIDC providers, got empty")
	}
	names := factory.GetAllOidcProviderNames()
	if len(names) == 0 {
		t.Error("Expected non-empty OIDC provider names, got empty")
	}

	provider := providers["github"]
	if provider == nil {
		provider = providers[names[0]]
	}
	cfg := provider.GetConfiguration()
	if cfg == nil {
		t.Errorf("Expected non-nil configuration for %q provider, got nil", provider.GetName())
	}
}
