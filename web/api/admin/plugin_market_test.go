package admin

import (
	"testing"
)

func TestParsePluginMarketCatalogShapes(t *testing.T) {
	plugin := `{"name":"Test","short":"Test","version":"1.0.0","author":"Author","download":"https://example.com/plugin.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	tests := []string{
		plugin,
		`[` + plugin + `]`,
		`{"schema":1,"plugins":[` + plugin + `]}`,
	}
	for _, input := range tests {
		plugins, err := parsePluginMarketCatalog([]byte(input))
		if err != nil {
			t.Fatalf("parsePluginMarketCatalog() error = %v", err)
		}
		if len(plugins) != 1 || plugins[0].Short != "Test" {
			t.Fatalf("parsePluginMarketCatalog() = %#v", plugins)
		}
	}
}

func TestValidatePluginMarketPluginChecksum(t *testing.T) {
	valid := PluginMarketPlugin{
		Name: "Test", Short: "Test", Version: "1.0.0", Author: "Author",
		URL:      "https://example.com/plugin",
		Download: "https://example.com/plugin.zip",
		SHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := validatePluginMarketPlugin(valid); err != nil {
		t.Fatalf("validatePluginMarketPlugin() error = %v", err)
	}
	valid.SHA256 = "xxxxxx"
	if err := validatePluginMarketPlugin(valid); err == nil {
		t.Fatal("validatePluginMarketPlugin() accepted an invalid checksum")
	}
}

func TestPluginMarketI18nTextAndSourceOnlyEntry(t *testing.T) {
	plugin := PluginMarketPlugin{
		Name:    map[string]any{"zh-CN": "测试", "en": "Test"},
		Short:   "source-only",
		Version: "source",
		Author:  map[string]any{"en": "Author"},
		URL:     "https://example.com/plugin",
	}
	if err := validatePluginMarketPlugin(plugin); err != nil {
		t.Fatalf("validatePluginMarketPlugin() error = %v", err)
	}
}

func TestDefaultPluginMarketSourcePointsToOfficialRepo(t *testing.T) {
	sources := defaultPluginMarketSources()
	if len(sources) != 1 {
		t.Fatalf("defaultPluginMarketSources() = %#v, want exactly one source", sources)
	}
	source := sources[0]
	if source.ID != "official" || !source.Enabled {
		t.Fatalf("defaultPluginMarketSources() = %#v, want enabled official source", source)
	}
	if source.URL != "https://raw.githubusercontent.com/komari-monitor/plugin-market/main/v1.json" {
		t.Fatalf("default plugin market URL = %q", source.URL)
	}
}
