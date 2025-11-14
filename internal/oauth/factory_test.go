package oauth

import (
	"testing"

	"github.com/komari-monitor/komari/internal/oauth/factory"
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

	//err := LoadProvider("github", `{"client_id":"test_id","client_secret":"test_secret"}`)
	//if err != nil {
	//	t.Errorf("Failed to load provider: %v", err)
	//}
	cfg := CurrentProvider().GetConfiguration()
	if cfg == nil {
		t.Error("Expected non-nil configuration for 'github' provider, got nil")
	}
}
