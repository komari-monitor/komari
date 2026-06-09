package config

import "time"

type Settings struct {
	ID                     uint   `json:"id,omitempty"`                                        // 1
	Sitename               string `json:"sitename" default:"Komari"`                           // 站点名称，默认 "Komari"
	Description            string `json:"description" default:"A simple server monitor tool."` // 站点描述
	CorsOriginCheckEnabled bool   `json:"cors_origin_check_enabled" default:"true"`            // 是否启用 API CORS 跨域请求校验，默认 true
	CorsAllowedOrigins     string `json:"cors_allowed_origins" default:""`                     // API 跨域允许列表
	WsOriginCheckEnabled   bool   `json:"ws_origin_check_enabled" default:"true"`              // 是否校验 WebSocket Origin
	WsAllowedOrigins       string `json:"ws_allowed_origins" default:""`                       // WebSocket Origin 允许列表
	Theme                  string `json:"theme" default:"default"`                             // 主题名称，默认 'default'
	PrivateSite            bool   `json:"private_site" default:"false"`                        // 是否为私有站点，默认 false
	ApiKey                 string `json:"api_key" default:""`                                  // API 密钥，默认空字符串
	AutoDiscoveryKey       string `json:"auto_discovery_key" default:""`                       // 自动发现密钥
	ScriptDomain           string `json:"script_domain" default:""`                            // 自定义脚本域名
	SendIpAddrToGuest      bool   `json:"send_ip_addr_to_guest" default:"false"`               // 是否向访客页面发送 IP 地址，默认 false
	EulaAccepted           bool   `json:"eula_accepted" default:"false"`
	BaseScriptsURLKey      string `json:"base_scripts_url" default:""`
	// GeoIP 配置
	GeoIpEnabled  bool   `json:"geo_ip_enabled" default:"true"`
	GeoIpProvider string `json:"geo_ip_provider" default:"ipinfo"` // empty, mmdb, ip-api, geojs
	// Nezha 兼容（Agent gRPC）
	NezhaCompatEnabled bool   `json:"nezha_compat_enabled" default:"false"`
	NezhaCompatListen  string `json:"nezha_compat_listen" default:""` // 例如 0.0.0.0:5555
	// OAuth 配置
	OAuthEnabled          bool   `json:"o_auth_enabled" default:"false"`
	OAuthProvider         string `json:"o_auth_provider" default:"github"`
	DisablePasswordLogin  bool   `json:"disable_password_login" default:"false"`
	CloudflareTunnelToken string `json:"cloudflare_tunnel_token" default:""`
	// 自定义美化
	CustomHead string `json:"custom_head" default:""`
	CustomBody string `json:"custom_body" default:""`
	// 通知
	NotificationEnabled        bool    `json:"notification_enabled" default:"true"` // 通知总开关
	NotificationMethod         string  `json:"notification_method" default:"none"`
	NotificationTemplate       string  `json:"notification_template" default:"{{emoji}}{{emoji}}{{emoji}}\nEvent: {{event}}\nClients: {{client}}\nMessage: {{message}}\nTime: {{time}}"`
	ExpireNotificationEnabled  bool    `json:"expire_notification_enabled" default:"true"` // 是否启用过期通知
	ExpireNotificationLeadDays int     `json:"expire_notification_lead_days" default:"7"`  // 过期前多少天通知，默认7天
	LoginNotification          bool    `json:"login_notification" default:"true"`          // 登录通知
	TrafficLimitPercentage     float64 `json:"traffic_limit_percentage" default:"80.00"`   // 流量限制百分比，默认80.00%
	// Record
	RecordEnabled          bool `json:"record_enabled" default:"true"`          // 是否启用记录功能
	RecordPreserveTime     int  `json:"record_preserve_time" default:"720"`     // 记录保留时间，单位小时，默认30天
	PingRecordPreserveTime int  `json:"ping_record_preserve_time" default:"24"` // Ping 记录保留时间，单位小时，默认1天
	UpdatedAt              time.Time
}

const (
	SitenameKey                   = "sitename"
	DescriptionKey                = "description"
	CorsOriginCheckEnabledKey     = "cors_origin_check_enabled"
	CorsAllowedOriginsKey         = "cors_allowed_origins"
	WsOriginCheckEnabledKey       = "ws_origin_check_enabled"
	WsAllowedOriginsKey           = "ws_allowed_origins"
	ThemeKey                      = "theme"
	PrivateSiteKey                = "private_site"
	ApiKeyKey                     = "api_key"
	AutoDiscoveryKeyKey           = "auto_discovery_key"
	ScriptDomainKey               = "script_domain"
	SendIpAddrToGuestKey          = "send_ip_addr_to_guest"
	EulaAcceptedKey               = "eula_accepted"
	BaseScriptsURLKey             = "base_scripts_url"
	GeoIpEnabledKey               = "geo_ip_enabled"
	GeoIpProviderKey              = "geo_ip_provider"
	NezhaCompatEnabledKey         = "nezha_compat_enabled"
	NezhaCompatListenKey          = "nezha_compat_listen"
	OAuthEnabledKey               = "o_auth_enabled"
	OAuthProviderKey              = "o_auth_provider"
	DisablePasswordLoginKey       = "disable_password_login"
	CloudflareTunnelTokenKey      = "cloudflare_tunnel_token"
	CustomHeadKey                 = "custom_head"
	CustomBodyKey                 = "custom_body"
	NotificationEnabledKey        = "notification_enabled"
	NotificationMethodKey         = "notification_method"
	NotificationTemplateKey       = "notification_template"
	ExpireNotificationEnabledKey  = "expire_notification_enabled"
	ExpireNotificationLeadDaysKey = "expire_notification_lead_days"
	LoginNotificationKey          = "login_notification"
	TrafficLimitPercentageKey     = "traffic_limit_percentage"
	RecordEnabledKey              = "record_enabled"
	RecordPreserveTimeKey         = "record_preserve_time"
	PingRecordPreserveTimeKey     = "ping_record_preserve_time"
	UpdatedAtKey                  = "updated_at"
	XtermjsSettingsKey            = "xtermjs_settings"
)
