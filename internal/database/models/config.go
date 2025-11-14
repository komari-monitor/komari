package models

type Config struct {
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
	CreatedAt              LocalTime
	UpdatedAt              LocalTime
}
