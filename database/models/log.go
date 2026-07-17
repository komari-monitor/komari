package models

import "time"

type Log struct {
	ID      uint      `json:"id,omitempty" gorm:"primaryKey;autoIncrement"`
	IP      string    `json:"ip" gorm:"type:varchar(45);"` // IPv4 or IPv6
	UUID    string    `json:"uuid" gorm:"type:varchar(36);"`
	Message string    `json:"message" gorm:"type:text;not null"`
	MsgType string    `json:"msg_type" gorm:"type:varchar(20);not null;index:idx_logs_msg_type_time,priority:1"`
	Time    time.Time `json:"time" gorm:"autoCreateTime;not null;index:idx_logs_time;index:idx_logs_msg_type_time,priority:2"`
}
