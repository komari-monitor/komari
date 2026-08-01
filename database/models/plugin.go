package models

// Plugin is the manifest of an installed plugin (komari-plugin.json).
//
// Name, Description and Author accept either a plain string or an i18n map
// like {"zh_CN": "...", "en": "..."}, mirroring the theme manifest. The
// backend passes the value through and the frontend resolves it against the
// current locale.
type Plugin struct {
	Name          any               `json:"name"`
	Short         string            `json:"short"` // directory name under data/plugin
	Description   any               `json:"description"`
	Author        any               `json:"author"`
	Version       string            `json:"version"`
	URL           string            `json:"url"`
	Icon          string            `json:"icon"`
	Komari        string            `json:"komari"` // supported server version constraint, e.g. ">=0.0.1"
	Entry         string            `json:"entry"`  // entry script, defaults to "script.js"
	Permissions   PluginPermissions `json:"permissions"`
	Configuration Configuration     `json:"configuration"`   // declared config items, same shape as themes
	Pages         []PluginPage      `json:"pages,omitempty"` // injected admin pages
}

// PluginPermissions maps one-to-one onto jsruntime.Options. Every field
// defaults to its zero value: nothing is granted unless declared.
type PluginPermissions struct {
	Node                bool  `json:"node"`
	AllowExec           bool  `json:"allowExec"`
	AllowListen         bool  `json:"allowListen"`
	AllowAllFileAccess  bool  `json:"allowAllFileAccess"`
	MaxHTTPBodyBytes    int64 `json:"maxHTTPBodyBytes"`
	MaxChildOutputBytes int   `json:"maxChildOutputBytes"`
	TimeoutSeconds      int   `json:"timeout"` // per-turn execution timeout in seconds
}

// PageVisibility controls who can reach a plugin page.
type PageVisibility string

const (
	// PageVisibilityAdmin limits the page to the admin sidebar.
	PageVisibilityAdmin PageVisibility = "admin"
	// PageVisibilityPublic serves the page through the public route without
	// navigation, mirroring the theme public behavior.
	PageVisibilityPublic PageVisibility = "public"
)

// PageType controls how a plugin page is presented.
type PageType string

const (
	// PageTypeIframe embeds a static file from the plugin directory.
	PageTypeIframe PageType = "iframe"
	// PageTypeRedirect navigates to an internal site path, mirroring the
	// theme redirect configuration. The URL must stay on the same origin.
	PageTypeRedirect PageType = "redirect"
)

// PluginPage declares a page provided by the plugin. An iframe page is served
// from the plugin directory (File); a redirect page navigates to an internal
// site path (URL). Visibility defaults to admin so existing packages keep
// their current behavior.
type PluginPage struct {
	File       string         `json:"file"`
	Title      any            `json:"title"`
	Icon       string         `json:"icon"`
	Type       PageType       `json:"type,omitempty"`
	URL        string         `json:"url,omitempty"`
	Visibility PageVisibility `json:"visibility,omitempty"`
}

// PluginConfiguration stores the saved configuration values of one plugin,
// mirroring ThemeConfiguration.
type PluginConfiguration struct {
	Short string `json:"short" gorm:"primaryKey;unique;not null"`
	Data  string `json:"data" gorm:"type:longtext" default:"{}"`
}
