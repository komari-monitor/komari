package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/web/api"
)

const (
	defaultThemeMarketURL = "https://raw.githubusercontent.com/komari-monitor/theme-market/main/v1.json"
	marketCatalogMaxSize  = 2 << 20
	marketThemeMaxSize    = 100 << 20
	marketCacheTTL        = 10 * time.Minute
)

type ThemeMarketSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type ThemeMarketTheme struct {
	Name        any    `json:"name"`
	Short       string `json:"short"`
	Description any    `json:"description"`
	Version     string `json:"version"`
	Author      any    `json:"author"`
	URL         string `json:"url"`
	Preview     string `json:"preview"`
	Download    string `json:"download"`
	SHA256      string `json:"sha256"`
	Installable bool   `json:"installable"`
	SourceID    string `json:"source_id,omitempty"`
	SourceName  string `json:"source_name,omitempty"`
}

type themeMarketCatalog struct {
	Schema int                `json:"schema"`
	Themes []ThemeMarketTheme `json:"themes"`
}

type themeMarketSourceStatus struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Count int    `json:"count"`
	Error string `json:"error,omitempty"`
}

type cachedThemeMarketCatalog struct {
	Themes    []ThemeMarketTheme
	ExpiresAt time.Time
}

var themeMarketCache = struct {
	sync.RWMutex
	items map[string]cachedThemeMarketCatalog
}{items: make(map[string]cachedThemeMarketCatalog)}

func defaultThemeMarketSources() []ThemeMarketSource {
	return []ThemeMarketSource{{
		ID:      "official",
		Name:    "Komari Official",
		URL:     defaultThemeMarketURL,
		Enabled: true,
	}}
}

func getThemeMarketSources() ([]ThemeMarketSource, error) {
	return config.GetAs[[]ThemeMarketSource](config.ThemeMarketSourcesKey, defaultThemeMarketSources())
}

func saveThemeMarketSources(sources []ThemeMarketSource) error {
	return config.Set(config.ThemeMarketSourcesKey, sources)
}

func normalizeThemeMarketSource(source ThemeMarketSource) (ThemeMarketSource, error) {
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

func newThemeMarketSourceID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func ListThemeMarketSources(c *gin.Context) {
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
		return
	}
	api.RespondSuccess(c, sources)
}

func CreateThemeMarketSource(c *gin.Context) {
	var source ThemeMarketSource
	if err := c.ShouldBindJSON(&source); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	var err error
	source, err = normalizeThemeMarketSource(source)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	source.ID, err = newThemeMarketSourceID()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create source ID")
		return
	}
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
		return
	}
	for _, existing := range sources {
		if existing.URL == source.URL {
			api.RespondError(c, http.StatusConflict, "A source with this URL already exists")
			return
		}
	}
	sources = append(sources, source)
	if err := saveThemeMarketSources(sources); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save theme market source: "+err.Error())
		return
	}
	api.RespondSuccessMessage(c, "Theme market source created", source)
}

func UpdateThemeMarketSource(c *gin.Context) {
	var update ThemeMarketSource
	if err := c.ShouldBindJSON(&update); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	update.ID = c.Param("id")
	var err error
	update, err = normalizeThemeMarketSource(update)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
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
		api.RespondError(c, http.StatusNotFound, "Theme market source not found")
		return
	}
	if err := saveThemeMarketSources(sources); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save theme market source: "+err.Error())
		return
	}
	invalidateThemeMarketCache(oldURL)
	if oldURL != update.URL {
		invalidateThemeMarketCache(update.URL)
	}
	api.RespondSuccessMessage(c, "Theme market source updated", update)
}

func DeleteThemeMarketSource(c *gin.Context) {
	id := c.Param("id")
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
		return
	}
	next := make([]ThemeMarketSource, 0, len(sources))
	var deleted *ThemeMarketSource
	for i := range sources {
		if sources[i].ID == id {
			copy := sources[i]
			deleted = &copy
			continue
		}
		next = append(next, sources[i])
	}
	if deleted == nil {
		api.RespondError(c, http.StatusNotFound, "Theme market source not found")
		return
	}
	if err := saveThemeMarketSources(next); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to delete theme market source: "+err.Error())
		return
	}
	invalidateThemeMarketCache(deleted.URL)
	api.RespondSuccessMessage(c, "Theme market source deleted", nil)
}

func ListThemeMarketCatalog(c *gin.Context) {
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
		return
	}
	force := c.Query("refresh") == "true"
	themes := make([]ThemeMarketTheme, 0)
	statuses := make([]themeMarketSourceStatus, len(sources))
	results := make([][]ThemeMarketTheme, len(sources))
	var wg sync.WaitGroup
	for i, source := range sources {
		statuses[i] = themeMarketSourceStatus{ID: source.ID, Name: source.Name, URL: source.URL}
		if !source.Enabled {
			continue
		}
		wg.Add(1)
		go func(index int, item ThemeMarketSource) {
			defer wg.Done()
			items, fetchErr := fetchThemeMarketCatalog(item, force)
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
		themes = append(themes, items...)
	}
	api.RespondSuccess(c, gin.H{"themes": themes, "sources": statuses})
}

func InstallThemeFromMarket(c *gin.Context) {
	var req struct {
		SourceID string `json:"source_id" binding:"required"`
		Short    string `json:"short" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	sources, err := getThemeMarketSources()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to load theme market sources: "+err.Error())
		return
	}
	var source *ThemeMarketSource
	for i := range sources {
		if sources[i].ID == req.SourceID && sources[i].Enabled {
			source = &sources[i]
			break
		}
	}
	if source == nil {
		api.RespondError(c, http.StatusNotFound, "Theme market source not found or disabled")
		return
	}
	items, err := fetchThemeMarketCatalog(*source, true)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Failed to load theme market source: "+err.Error())
		return
	}
	var selected *ThemeMarketTheme
	for i := range items {
		if items[i].Short == req.Short {
			selected = &items[i]
			break
		}
	}
	if selected == nil {
		api.RespondError(c, http.StatusNotFound, "Theme not found in source")
		return
	}
	if !selected.Installable {
		api.RespondError(c, http.StatusBadRequest, "This theme does not provide an installable package")
		return
	}
	data, err := downloadThemeMarketURL(selected.Download, marketThemeMaxSize)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Failed to download theme: "+err.Error())
		return
	}
	digest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(digest[:]), selected.SHA256) {
		api.RespondError(c, http.StatusBadRequest, "Theme SHA-256 checksum does not match the market catalog")
		return
	}
	tempFile, err := os.CreateTemp("", "komari-market-theme-*.zip")
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create temporary theme file")
		return
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	if _, err := tempFile.Write(data); err != nil {
		tempFile.Close()
		api.RespondError(c, http.StatusInternalServerError, "Failed to save temporary theme file")
		return
	}
	if err := tempFile.Close(); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to save temporary theme file")
		return
	}
	manifest, err := peekThemeFromZip(tempPath)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if manifest.Short != selected.Short || manifest.Version != selected.Version {
		api.RespondError(c, http.StatusBadRequest, "Theme manifest does not match the market catalog")
		return
	}
	installed, err := extractAndValidateTheme(tempPath)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	api.RespondSuccessMessage(c, "Theme installed from market", installed)
}

func fetchThemeMarketCatalog(source ThemeMarketSource, force bool) ([]ThemeMarketTheme, error) {
	if !force {
		themeMarketCache.RLock()
		cached, ok := themeMarketCache.items[source.URL]
		themeMarketCache.RUnlock()
		if ok && time.Now().Before(cached.ExpiresAt) {
			return append([]ThemeMarketTheme(nil), cached.Themes...), nil
		}
	}
	data, err := downloadThemeMarketURL(source.URL, marketCatalogMaxSize)
	if err != nil {
		return nil, err
	}
	themes, err := parseThemeMarketCatalog(data)
	if err != nil {
		return nil, err
	}
	for i := range themes {
		if err := validateThemeMarketTheme(themes[i]); err != nil {
			return nil, fmt.Errorf("theme %q: %w", themes[i].Short, err)
		}
		themes[i].SHA256 = strings.TrimPrefix(strings.ToLower(themes[i].SHA256), "sha256:")
		themes[i].Installable = themes[i].Download != "" && themes[i].SHA256 != ""
		themes[i].SourceID = source.ID
		themes[i].SourceName = source.Name
	}
	themeMarketCache.Lock()
	themeMarketCache.items[source.URL] = cachedThemeMarketCatalog{Themes: themes, ExpiresAt: time.Now().Add(marketCacheTTL)}
	themeMarketCache.Unlock()
	return append([]ThemeMarketTheme(nil), themes...), nil
}

func parseThemeMarketCatalog(data []byte) ([]ThemeMarketTheme, error) {
	var catalog themeMarketCatalog
	if err := json.Unmarshal(data, &catalog); err == nil && catalog.Themes != nil {
		return catalog.Themes, nil
	}
	var themes []ThemeMarketTheme
	if err := json.Unmarshal(data, &themes); err == nil && themes != nil {
		return themes, nil
	}
	var theme ThemeMarketTheme
	if err := json.Unmarshal(data, &theme); err != nil {
		return nil, fmt.Errorf("invalid market catalog JSON: %w", err)
	}
	if theme.Short == "" {
		return nil, errors.New("market catalog must contain a themes array or a theme object")
	}
	return []ThemeMarketTheme{theme}, nil
}

func validateThemeMarketTheme(theme ThemeMarketTheme) error {
	if !isThemeMarketText(theme.Name) || theme.Short == "" || theme.Version == "" || !isThemeMarketText(theme.Author) {
		return errors.New("name, short, version and author are required")
	}
	if !isValidThemeShort(theme.Short) {
		return errors.New("short contains invalid characters")
	}
	if (theme.Download == "") != (theme.SHA256 == "") {
		return errors.New("download and sha256 must be provided together")
	}
	urls := []struct {
		field string
		value string
	}{{"url", theme.URL}, {"preview", theme.Preview}, {"download", theme.Download}}
	for _, item := range urls {
		field, value := item.field, item.value
		if value == "" && (field == "preview" || field == "download") {
			continue
		}
		if err := validateThemeMarketURLSyntax(value); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	if theme.SHA256 == "" {
		return nil
	}
	sha := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(theme.SHA256)), "sha256:")
	if len(sha) != sha256.Size*2 {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return errors.New("sha256 must contain 64 hexadecimal characters")
	}
	return nil
}

func isThemeMarketText(value any) bool {
	switch text := value.(type) {
	case string:
		return strings.TrimSpace(text) != ""
	case map[string]any:
		for _, item := range text {
			if itemText, ok := item.(string); ok && strings.TrimSpace(itemText) != "" {
				return true
			}
		}
	}
	return false
}

func validateThemeMarketURLSyntax(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("must be a valid HTTP or HTTPS URL")
	}
	return nil
}

func downloadThemeMarketURL(rawURL string, maxSize int64) ([]byte, error) {
	validate := func(candidate string) error {
		parsed, err := url.Parse(candidate)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
			return errors.New("only public HTTP and HTTPS URLs are allowed")
		}
		if isPrivateIP(parsed.Hostname()) {
			return errors.New("requests to private or internal addresses are not allowed")
		}
		return nil
	}
	if err := validate(rawURL); err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return validate(req.URL.String())
		},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("response exceeds the %d byte limit", maxSize)
	}
	if len(data) == 0 {
		return nil, errors.New("empty response")
	}
	return data, nil
}

func invalidateThemeMarketCache(rawURL string) {
	themeMarketCache.Lock()
	delete(themeMarketCache.items, rawURL)
	themeMarketCache.Unlock()
}
