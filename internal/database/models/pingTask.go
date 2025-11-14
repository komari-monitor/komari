package models

type PingRecord struct {
	Client     string    `json:"client" gorm:"type:varchar(36);not null;index"`
	ClientInfo Client    `json:"client_info" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	TaskId     uint      `json:"task_id" gorm:"not null;index"`
	Task       PingTask  `json:"task" gorm:"foreignKey:TaskId;references:Id;constraint:OnDelete:CASCADE,OnUpdate:CASCADE;"`
	Time       LocalTime `json:"time" gorm:"index;not null"`
	Value      int       `json:"value" gorm:"type:int;not null"` // Ping 值，单位毫秒
}

type PingTask struct {
	Id       uint        `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	Name     string      `json:"name" gorm:"type:varchar(255);not null;index"`
	Clients  StringArray `json:"clients" gorm:"type:longtext"`
	Type     string      `json:"type" gorm:"type:varchar(12);not null;default:'icmp'"` // icmp tcp http
	Target   string      `json:"target" gorm:"type:varchar(255);not null"`
	Interval int         `json:"interval" gorm:"type:int;not null;default:60"` // 间隔时间
}
