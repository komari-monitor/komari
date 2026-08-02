package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/internal/plugin"
	"github.com/komari-monitor/komari/web/api"
)

// Plugin market mirrors the theme market: admin-managed catalog sources that
// publish ZIP packages containing komari-plugin.json. The generic URL
// download/validation helpers are shared with the theme market.

const defaultPluginMarketURL = "https://raw.githubusercontent.com/komari-monitor/plugin-market/main/v1.json"

type PluginMarketSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type PluginMarketPlugin struct {
	Name        any    `json:"name"`
	Short       string `json:"short"`
	Description any    `json:"description"`
	Version     string `json:"version"`
	Author      any    `json:"author"`
	URL         string `json:"url"`
	Download    string `json:"download"`
	SHA256      string `json:"sha256"`
	Komari      string `json:"komari"`
	Installable bool   `json:"installable"`
	SourceID    string `json:"source_id,omitempty"`
	SourceName  string `json:"source_name,omitempty"`
}

type pluginMarketCatalog struct {
	Schema  int                  `json:"schema"`
	Plugins []PluginMarketPlugin `json:"plugins"`
}

type pluginMarketSourceStatus struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

type cachedPluginMarketCatalog struct {
	Plugins   []PluginMarketPlugin
	ExpiresAt time.Time
}

var pluginMarketCache = struct {
	sync.RWMutex
	items map[string]cachedPluginMarketCatalog
}{items: make(map[string]cachedPluginMarketCatalog)}

func defaultPluginMarketSources() []PluginMarketSource {
	return []PluginMarketSource{{
		ID:      "official",
		Name:    "Komari Official",
		URL:     defaultPluginMarketURL,
		Enabled: true,
	}}
}

func getPluginMarketSources() ([]PluginMarketSource, error) {
	return config.GetAs[[]PluginMarketSource](config.PluginMarketSourcesKey, defaultPluginMarketSources())
}

func savePluginMarketSources(sources []PluginMarketSource) error {
	return config.Set(config.PluginMarketSourcesKey, sources)
}

func normalizePluginMarketSource(source PluginMarketSource) (PluginMarketSource, error) {
	source.ID = strings.TrimSpace(source.ID)
	source.Name = strings.TrimSpace(source.Name)
	source.URL = strings.TrimSpace(source.URL)
	if source.Name == "" {
		return source, errors.New("source name is required")
	}
	parsed, err := url.Parse(source.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return source, errors.New("source URL must be a valid HTTP or HTTPS URL")
	}
	return source, nil
}

func ListPluginMarketSources(c *gin.Context) {
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	api.RespondSuccess(c, sources)
}

func CreatePluginMarketSource(c *gin.Context) {
	var source PluginMarketSource
	if err := c.ShouldBindJSON(&source); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	var err error
	source, err = normalizePluginMarketSource(source)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	source.ID, err = newMarketSourceID()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create source ID")
		return
	}
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	for _, existing := range sources {
		if existing.URL == source.URL {
			api.RespondError(c, http.StatusConflict, "A source with this URL already exists")
			return
		}
	}
	sources = append(sources, source)
	if err := savePluginMarketSources(sources); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save plugin market source: "+err.Error())
		return
	}
	api.RespondSuccessMessage(c, "Plugin market source created", source)
}

func UpdatePluginMarketSource(c *gin.Context) {
	var update PluginMarketSource
	if err := c.ShouldBindJSON(&update); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	update.ID = c.Param("id")
	var err error
	update, err = normalizePluginMarketSource(update)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	found := false
	oldURL := ""
	for i := range sources {
		if sources[i].ID == update.ID {
			oldURL = sources[i].URL
			sources[i] = update
			found = true
			continue
		}
		if sources[i].URL == update.URL {
			api.RespondError(c, http.StatusConflict, "A source with this URL already exists")
			return
		}
	}
	if !found {
		api.RespondError(c, http.StatusNotFound, "Plugin market source not found")
		return
	}
	if err := savePluginMarketSources(sources); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save plugin market source: "+err.Error())
		return
	}
	invalidatePluginMarketCache(oldURL)
	if oldURL != update.URL {
		invalidatePluginMarketCache(update.URL)
	}
	api.RespondSuccessMessage(c, "Plugin market source updated", update)
}

func DeletePluginMarketSource(c *gin.Context) {
	id := c.Param("id")
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	next := make([]PluginMarketSource, 0, len(sources))
	var deleted *PluginMarketSource
	for i := range sources {
		if sources[i].ID == id {
			copy := sources[i]
			deleted = &copy
			continue
		}
		next = append(next, sources[i])
	}
	if deleted == nil {
		api.RespondError(c, http.StatusNotFound, "Plugin market source not found")
		return
	}
	if err := savePluginMarketSources(next); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save plugin market sources: "+err.Error())
		return
	}
	invalidatePluginMarketCache(deleted.URL)
	api.RespondSuccessMessage(c, "Plugin market source deleted", nil)
}

func ListPluginMarketCatalog(c *gin.Context) {
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	force := c.Query("refresh") == "true"
	plugins := make([]PluginMarketPlugin, 0)
	statuses := make([]pluginMarketSourceStatus, len(sources))
	results := make([][]PluginMarketPlugin, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		statuses[i] = pluginMarketSourceStatus{ID: source.ID, Name: source.Name, URL: source.URL}
		if !source.Enabled {
			continue
		}
		wg.Add(1)
		go func(index int, item PluginMarketSource) {
			defer wg.Done()
			items, fetchErr := fetchPluginMarketCatalog(item, force)
			if fetchErr != nil {
				statuses[index].Error = fetchErr.Error()
				return
			}
			results[index] = items
			statuses[index].Count = len(items)
		}(i, source)
	}
	wg.Wait()
	for _, items := range results {
		plugins = append(plugins, items...)
	}
	api.RespondSuccess(c, gin.H{"plugins": plugins, "sources": statuses})
}

func InstallPluginFromMarket(c *gin.Context) {
	var req struct {
		SourceID string `json:"source_id" binding:"required"`
		Short    string `json:"short" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	sources, err := getPluginMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load plugin market sources: "+err.Error())
		return
	}
	var source *PluginMarketSource
	for i := range sources {
		if sources[i].ID == req.SourceID && sources[i].Enabled {
			source = &sources[i]
			break
		}
	}
	if source == nil {
		api.RespondError(c, http.StatusNotFound, "Plugin market source not found or disabled")
		return
	}
	items, err := fetchPluginMarketCatalog(*source, true)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Failed to load plugin market source: "+err.Error())
		return
	}
	var selected *PluginMarketPlugin
	for i := range items {
		if items[i].Short == req.Short {
			selected = &items[i]
			break
		}
	}
	if selected == nil {
		api.RespondError(c, http.StatusNotFound, "Plugin not found in source")
		return
	}
	if !selected.Installable {
		api.RespondError(c, http.StatusBadRequest, "This plugin does not provide an installable package")
		return
	}
	data, err := downloadMarketURL(selected.Download, marketPackageMaxSize)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Failed to download plugin: "+err.Error())
		return
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), selected.SHA256) {
		api.RespondError(c, http.StatusBadRequest, "Plugin SHA-256 checksum does not match the market catalog")
		return
	}
	tempFile, err := os.CreateTemp("", "komari-market-plugin-*.zip")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create temporary plugin file")
		return
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		api.RespondError(c, http.StatusInternalServerError, "Failed to save temporary plugin file")
		return
	}
	if err := tempFile.Close(); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save temporary plugin file")
		return
	}
	installed, err := plugin.InstallZip(tempPath)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if installed.Short != selected.Short || installed.Version != selected.Version {
		api.RespondError(c, http.StatusBadRequest, "Plugin manifest does not match the market catalog")
		return
	}
	api.RespondSuccessMessage(c, "Plugin installed from market", installed)
}

func fetchPluginMarketCatalog(source PluginMarketSource, force bool) ([]PluginMarketPlugin, error) {
	if !force {
		pluginMarketCache.RLock()
		cached, ok := pluginMarketCache.items[source.URL]
		pluginMarketCache.RUnlock()
		if ok && time.Now().Before(cached.ExpiresAt) {
			return append([]PluginMarketPlugin(nil), cached.Plugins...), nil
		}
	}
	data, err := downloadMarketURL(source.URL, marketCatalogMaxSize)
	if err != nil {
		return nil, err
	}
	plugins, err := parsePluginMarketCatalog(data)
	if err != nil {
		return nil, err
	}
	for i := range plugins {
		if err := validatePluginMarketPlugin(plugins[i]); err != nil {
			return nil, fmt.Errorf("plugin %q: %w", plugins[i].Short, err)
		}
		plugins[i].SHA256 = strings.TrimPrefix(strings.ToLower(plugins[i].SHA256), "sha256:")
		plugins[i].Installable = plugins[i].Download != "" && plugins[i].SHA256 != "" &&
			plugin.CheckKomariVersion(plugins[i].Komari) == nil
		plugins[i].SourceID = source.ID
		plugins[i].SourceName = source.Name
	}
	pluginMarketCache.Lock()
	pluginMarketCache.items[source.URL] = cachedPluginMarketCatalog{Plugins: plugins, ExpiresAt: time.Now().Add(marketCacheTTL)}
	pluginMarketCache.Unlock()
	return append([]PluginMarketPlugin(nil), plugins...), nil
}

func parsePluginMarketCatalog(data []byte) ([]PluginMarketPlugin, error) {
	var catalog pluginMarketCatalog
	if err := json.Unmarshal(data, &catalog); err == nil && catalog.Plugins != nil {
		return catalog.Plugins, nil
	}
	var plugins []PluginMarketPlugin
	if err := json.Unmarshal(data, &plugins); err == nil && plugins != nil {
		return plugins, nil
	}
	var p PluginMarketPlugin
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid market catalog JSON: %w", err)
	}
	if p.Short == "" {
		return nil, errors.New("market catalog must contain a plugins array or a plugin object")
	}
	return []PluginMarketPlugin{p}, nil
}

func validatePluginMarketPlugin(p PluginMarketPlugin) error {
	if !isMarketText(p.Name) || p.Short == "" || p.Version == "" || !isMarketText(p.Author) {
		return errors.New("name, short, version and author are required")
	}
	if !isValidMarketShort(p.Short) {
		return errors.New("short contains invalid characters")
	}
	if (p.Download == "") != (p.SHA256 == "") {
		return errors.New("download and sha256 must be provided together")
	}
	urls := []struct {
		field string
		value string
	}{{"url", p.URL}, {"download", p.Download}}
	for _, item := range urls {
		field, value := item.field, item.value
		if value == "" && field == "download" {
			continue
		}
		if err := validateMarketURLSyntax(value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	if p.SHA256 == "" {
		return nil
	}
	sha := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(p.SHA256)), "sha256:")
	if len(sha) != sha256.Size*2 {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	return nil
}

func invalidatePluginMarketCache(rawURL string) {
	pluginMarketCache.Lock()
	delete(pluginMarketCache.items, rawURL)
	pluginMarketCache.Unlock()
}
