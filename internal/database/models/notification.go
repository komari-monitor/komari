package models

// Notification 定义了通知相关的数据库模型
type OfflineNotification struct {
	Client     string `json:"client" gorm:"type:varchar(36);not null;index;unique;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;foreignKey:client;references:UUID"`
	ClientInfo Client `json:"client_info,omitempty" gorm:"foreignKey:Client;references:UUID"`
	Enable     bool   `json:"enable" gorm:"type:boolean;default:false"`
	//Cooldown     int       `json:"cooldown" gorm:"type:int;not null;default:1800"`                // 冷却时间（秒），默认 30 分钟
	GracePeriod  int       `json:"grace_period" gorm:"type:int;not null;default:180"` // 宽限期（秒），默认 3 分钟
	LastNotified LocalTime `json:"last_notified"`                                     // 上次通知时间
}

// LoadNotification 定义了基于资源占用达标时间比的负载通知规则
type LoadNotification struct {
	Id           uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	Name         string      `json:"name" gorm:"type:varchar(255)"`
	Clients      StringArray `json:"clients" gorm:"type:longtext"`
	Metric       string      `json:"metric" gorm:"type:varchar(50);not null;default:'cpu'"`     // 监控指标，如 cpu, ram, load
	Threshold    float32     `json:"threshold" gorm:"type:decimal(5,2);not null;default:80.00"` // 阈值百分比
	Ratio        float32     `json:"ratio" gorm:"type:decimal(5,2);not null;default:0.80"`      // 达标时间比
	Interval     int         `json:"interval" gorm:"type:int;not null;default:15"`              // 监测间隔（分钟）
	LastNotified LocalTime   `json:"last_notified"`                                             // 上次通知时间
}
