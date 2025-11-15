package conf

import "time"

var (
	Version    = Version_Development
	CommitHash = "unknown"
)

var (
	Conf *Config // 当直接修改时，请手动触发 eventType.ConfigUpdated 事件，或者使用 EditAndTrigger(func() { ... } 包裹
)

type Config struct {
	Site         Site         `json:"site"`
	Login        Login        `json:"login"`
	GeoIp        GeoIp        `json:"geo_ip"`
	Notification Notification `json:"notification"`
	Record       Record       `json:"record"`
	Compact      Compact      `json:"compact"`
}

type Site struct {
	Sitename          string `json:"sitename"`
	Description       string `json:"description"`
	AllowCors         bool   `json:"allow_cors"`
	PrivateSite       bool   `json:"private_site"`          // 是否为私有站点，默认 false
	SendIpAddrToGuest bool   `json:"send_ip_addr_to_guest"` // 是否向访客页面发送 IP 地址，默认 false
	ScriptDomain      string `json:"script_domain"`         // 自定义脚本域名
	EulaAccepted      bool   `json:"eula_accepted"`
	// 自定义美化
	CustomHead string `json:"custom_head"`
	CustomBody string `json:"custom_body"`
	Theme      string `json:"theme"` // 主题名称，默认 'default'
}

type Login struct {
	ApiKey           string `json:"api_key"`
	AutoDiscoveryKey string `json:"auto_discovery_key"` // 自动发现密钥

	// OAuth 配置
	OAuthEnabled         bool   `json:"o_auth_enabled"`
	OAuthProvider        string `json:"o_auth_provider"`
	DisablePasswordLogin bool   `json:"disable_password_login"`
}

type GeoIp struct {
	GeoIpEnabled  bool   `json:"geo_ip_enabled"`
	GeoIpProvider string `json:"geo_ip_provider"` // empty, mmdb, ip-api, geojs
}

type Notification struct {
	NotificationEnabled        bool    `json:"notification_enabled"` // 通知总开关
	NotificationMethod         string  `json:"notification_method"`
	NotificationTemplate       string  `json:"notification_template"`
	ExpireNotificationEnabled  bool    `json:"expire_notification_enabled"`   // 是否启用过期通知
	ExpireNotificationLeadDays int     `json:"expire_notification_lead_days"` // 过期前多少天通知，默认7天
	LoginNotification          bool    `json:"login_notification"`            // 登录通知
	TrafficLimitPercentage     float64 `json:"traffic_limit_percentage"`      // 流量限制百分比，默认80.00%
}

type Record struct {
	RecordEnabled          bool `json:"record_enabled"`            // 是否启用记录功能
	RecordPreserveTime     int  `json:"record_preserve_time"`      // 记录保留时间，单位小时，默认30天
	PingRecordPreserveTime int  `json:"ping_record_preserve_time"` // Ping 记录保留时间，单位小时，默认1天
}

type Compact struct {
	Nezha Nezha `json:"nezha"`
}

type Nezha struct {
	// Nezha 兼容（Agent gRPC）
	NezhaCompatEnabled bool   `json:"nezha_compat_enabled"`
	NezhaCompatListen  string `json:"nezha_compat_listen"` // 例如 0.0.0.0:5555
}

// [DEPRECATED] 旧的数据结构，将不再维护，请考虑使用 conf.Config 结构体
type V1Struct struct {
	ID                uint   `json:"id,omitempty" gorm:"primaryKey;autoIncrement"` // 1
	Sitename          string `json:"sitename" gorm:"type:varchar(100);not null"`
	Description       string `json:"description" gorm:"type:text"`
	AllowCors         bool   `json:"allow_cors" gorm:"column:allow_cors;default:false"`
	Theme             string `json:"theme" gorm:"type:varchar(100);default:'default'"` // 主题名称，默认 'default'
	PrivateSite       bool   `json:"private_site" gorm:"default:false"`                // 是否为私有站点，默认 false
	ApiKey            string `json:"api_key" gorm:"type:varchar(255);default:''"`
	AutoDiscoveryKey  string `json:"auto_discovery_key" gorm:"type:varchar(255);default:''"` // 自动发现密钥
	ScriptDomain      string `json:"script_domain" gorm:"type:varchar(255);default:''"`      // 自定义脚本域名
	SendIpAddrToGuest bool   `json:"send_ip_addr_to_guest" gorm:"default:false"`             // 是否向访客页面发送 IP 地址，默认 false
	EulaAccepted      bool   `json:"eula_accepted" gorm:"default:false"`
	// GeoIP 配置
	GeoIpEnabled  bool   `json:"geo_ip_enabled" gorm:"default:true"`
	GeoIpProvider string `json:"geo_ip_provider" gorm:"type:varchar(20);default:'ip-api'"` // empty, mmdb, ip-api, geojs
	// Nezha 兼容（Agent gRPC）
	NezhaCompatEnabled bool   `json:"nezha_compat_enabled" gorm:"default:false"`
	NezhaCompatListen  string `json:"nezha_compat_listen" gorm:"type:varchar(100);default:''"` // 例如 0.0.0.0:5555
	// OAuth 配置
	OAuthEnabled         bool   `json:"o_auth_enabled" gorm:"default:false"`
	OAuthProvider        string `json:"o_auth_provider" gorm:"type:varchar(50);default:'github'"`
	DisablePasswordLogin bool   `json:"disable_password_login" gorm:"default:false"`
	// 自定义美化
	CustomHead string `json:"custom_head" gorm:"type:longtext"`
	CustomBody string `json:"custom_body" gorm:"type:longtext"`
	// 通知
	NotificationEnabled        bool    `json:"notification_enabled" gorm:"default:false"` // 通知总开关
	NotificationMethod         string  `json:"notification_method" gorm:"type:varchar(64);default:'none'"`
	NotificationTemplate       string  `json:"notification_template" gorm:"type:longtext;default:'{{emoji}}{{emoji}}{{emoji}}\nEvent: {{event}}\nClients: {{client}}\nMessage: {{message}}\nTime: {{time}}'"`
	ExpireNotificationEnabled  bool    `json:"expire_notification_enabled" gorm:"default:false"` // 是否启用过期通知
	ExpireNotificationLeadDays int     `json:"expire_notification_lead_days" gorm:"default:7"`   // 过期前多少天通知，默认7天
	LoginNotification          bool    `json:"login_notification" gorm:"default:false"`          // 登录通知
	TrafficLimitPercentage     float64 `json:"traffic_limit_percentage" gorm:"default:80.00"`    // 流量限制百分比，默认80.00%
	// Record
	RecordEnabled          bool `json:"record_enabled" gorm:"default:true"`          // 是否启用记录功能
	RecordPreserveTime     int  `json:"record_preserve_time" gorm:"default:720"`     // 记录保留时间，单位小时，默认30天
	PingRecordPreserveTime int  `json:"ping_record_preserve_time" gorm:"default:24"` // Ping 记录保留时间，单位小时，默认1天
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func (cst *V1Struct) ToConfig() Config {
	return Config{
		Site: Site{
			Sitename:          cst.Sitename,
			Description:       cst.Description,
			AllowCors:         cst.AllowCors,
			PrivateSite:       cst.PrivateSite,
			SendIpAddrToGuest: cst.SendIpAddrToGuest,
			ScriptDomain:      cst.ScriptDomain,
			EulaAccepted:      cst.EulaAccepted,
			CustomHead:        cst.CustomHead,
			CustomBody:        cst.CustomBody,
			Theme:             cst.Theme,
		},
		Login: Login{
			ApiKey:               cst.ApiKey,
			AutoDiscoveryKey:     cst.AutoDiscoveryKey,
			OAuthEnabled:         cst.OAuthEnabled,
			OAuthProvider:        cst.OAuthProvider,
			DisablePasswordLogin: cst.DisablePasswordLogin,
		},
		GeoIp: GeoIp{
			GeoIpEnabled:  cst.GeoIpEnabled,
			GeoIpProvider: cst.GeoIpProvider,
		},
		Notification: Notification{
			NotificationEnabled:        cst.NotificationEnabled,
			NotificationMethod:         cst.NotificationMethod,
			NotificationTemplate:       cst.NotificationTemplate,
			ExpireNotificationEnabled:  cst.ExpireNotificationEnabled,
			ExpireNotificationLeadDays: cst.ExpireNotificationLeadDays,
			LoginNotification:          cst.LoginNotification,
			TrafficLimitPercentage:     cst.TrafficLimitPercentage,
		},
		Record: Record{
			RecordEnabled:          cst.RecordEnabled,
			RecordPreserveTime:     cst.RecordPreserveTime,
			PingRecordPreserveTime: cst.PingRecordPreserveTime,
		},
		Compact: Compact{
			Nezha: Nezha{
				NezhaCompatEnabled: cst.NezhaCompatEnabled,
				NezhaCompatListen:  cst.NezhaCompatListen,
			},
		},
	}
}

func (cfg *Config) ToV1Format() V1Struct {
	return V1Struct{
		ID:                         1,
		Sitename:                   cfg.Site.Sitename,
		Description:                cfg.Site.Description,
		AllowCors:                  cfg.Site.AllowCors,
		Theme:                      cfg.Site.Theme,
		PrivateSite:                cfg.Site.PrivateSite,
		ApiKey:                     cfg.Login.ApiKey,
		AutoDiscoveryKey:           cfg.Login.AutoDiscoveryKey,
		ScriptDomain:               cfg.Site.ScriptDomain,
		SendIpAddrToGuest:          cfg.Site.SendIpAddrToGuest,
		EulaAccepted:               cfg.Site.EulaAccepted,
		GeoIpEnabled:               cfg.GeoIp.GeoIpEnabled,
		GeoIpProvider:              cfg.GeoIp.GeoIpProvider,
		NezhaCompatEnabled:         cfg.Compact.Nezha.NezhaCompatEnabled,
		NezhaCompatListen:          cfg.Compact.Nezha.NezhaCompatListen,
		OAuthEnabled:               cfg.Login.OAuthEnabled,
		OAuthProvider:              cfg.Login.OAuthProvider,
		DisablePasswordLogin:       cfg.Login.DisablePasswordLogin,
		CustomHead:                 cfg.Site.CustomHead,
		CustomBody:                 cfg.Site.CustomBody,
		NotificationEnabled:        cfg.Notification.NotificationEnabled,
		NotificationMethod:         cfg.Notification.NotificationMethod,
		NotificationTemplate:       cfg.Notification.NotificationTemplate,
		ExpireNotificationEnabled:  cfg.Notification.ExpireNotificationEnabled,
		ExpireNotificationLeadDays: cfg.Notification.ExpireNotificationLeadDays,
		LoginNotification:          cfg.Notification.LoginNotification,
		TrafficLimitPercentage:     cfg.Notification.TrafficLimitPercentage,
		RecordEnabled:              cfg.Record.RecordEnabled,
		RecordPreserveTime:         cfg.Record.RecordPreserveTime,
		PingRecordPreserveTime:     cfg.Record.PingRecordPreserveTime,
		CreatedAt:                  time.Now(),
		UpdatedAt:                  time.Now(),
	}
}
