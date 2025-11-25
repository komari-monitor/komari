package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// Client represents a registered client device
type Client struct {
	UUID             string    `json:"uuid,omitempty" gorm:"type:varchar(36);primaryKey"`
	Token            string    `json:"token,omitempty" gorm:"type:varchar(255);unique;not null"`
	Name             string    `json:"name" gorm:"type:varchar(100)"`
	CpuName          string    `json:"cpu_name" gorm:"type:varchar(100)"`
	Virtualization   string    `json:"virtualization" gorm:"type:varchar(50)"`
	Arch             string    `json:"arch" gorm:"type:varchar(50)"`
	CpuCores         int       `json:"cpu_cores" gorm:"type:int"`
	OS               string    `json:"os" gorm:"type:varchar(100)"`
	KernelVersion    string    `json:"kernel_version" gorm:"type:varchar(100)"`
	GpuName          string    `json:"gpu_name" gorm:"type:varchar(100)"`
	IPv4             string    `json:"ipv4,omitempty" gorm:"type:varchar(100)"`
	IPv6             string    `json:"ipv6,omitempty" gorm:"type:varchar(100)"`
	Region           string    `json:"region" gorm:"type:varchar(100)"`
	Remark           string    `json:"remark,omitempty" gorm:"type:longtext"`
	PublicRemark     string    `json:"public_remark,omitempty" gorm:"type:longtext"`
	MemTotal         int64     `json:"mem_total" gorm:"type:bigint"`
	SwapTotal        int64     `json:"swap_total" gorm:"type:bigint"`
	DiskTotal        int64     `json:"disk_total" gorm:"type:bigint"`
	Version          string    `json:"version,omitempty" gorm:"type:varchar(100)"`
	Weight           int       `json:"weight" gorm:"type:int"`
	Price            float64   `json:"price"`
	BillingCycle     int       `json:"billing_cycle"`
	AutoRenewal      bool      `json:"auto_renewal" gorm:"default:false"` // 是否自动续费
	Currency         string    `json:"currency" gorm:"type:varchar(20);default:'$'"`
	ExpiredAt        LocalTime `json:"expired_at" gorm:"type:timestamp"`
	Group            string    `json:"group" gorm:"type:varchar(100)"`
	Tags             string    `json:"tags" gorm:"type:text"` // split by ';'
	Hidden           bool      `json:"hidden" gorm:"default:false"`
	TrafficLimit     int64     `json:"traffic_limit" gorm:"type:bigint"`
	TrafficLimitType string    `json:"traffic_limit_type" gorm:"type:varchar(10);default:'max'"` // 流量阈值类型：sum max min up down
	CreatedAt        LocalTime `json:"created_at"`
	UpdatedAt        LocalTime `json:"updated_at"`
}

// User represents an authenticated user
type User struct {
	UUID      string    `json:"uuid,omitempty" gorm:"type:varchar(36);primaryKey"`
	Username  string    `json:"username" gorm:"type:varchar(50);unique;not null"`
	Passwd    string    `json:"passwd,omitempty" gorm:"type:varchar(255);not null"` // Hashed password
	SSOType   string    `json:"sso_type" gorm:"type:varchar(20)"`                   // e.g., "github", "google"
	SSOID     string    `json:"sso_id" gorm:"type:varchar(100)"`                    // OAuth provider's user ID
	TwoFactor string    `json:"two_factor,omitempty" gorm:"type:varchar(255)"`      // 2FA secret
	Sessions  []Session `json:"sessions,omitempty" gorm:"foreignKey:UUID;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	CreatedAt LocalTime `json:"created_at"`
	UpdatedAt LocalTime `json:"updated_at"`
}

// Session manages user sessions
type Session struct {
	UUID            string    `json:"uuid" gorm:"type:varchar(36)"`
	Session         string    `json:"session" gorm:"type:varchar(255);primaryKey;uniqueIndex:idx_sessions_session;not null"`
	UserAgent       string    `json:"user_agent" gorm:"type:text"`
	Ip              string    `json:"ip" gorm:"type:varchar(100)"`
	LoginMethod     string    `json:"login_method" gorm:"type:varchar(50)"`
	LatestOnline    LocalTime `json:"latest_online" gorm:"type:timestamp"`
	LatestUserAgent string    `json:"latest_user_agent" gorm:"type:text"`
	LatestIp        string    `json:"latest_ip" gorm:"type:varchar(100)"`
	Expires         LocalTime `json:"expires" gorm:"not null"`
	CreatedAt       LocalTime `json:"created_at"`
}

// Record logs client metrics over time
type Record struct {
	Client         string    `json:"client" gorm:"type:varchar(36);index"`
	Time           LocalTime `json:"time" gorm:"index"`
	Cpu            float32   `json:"cpu" gorm:"type:decimal(5,2)"` // e.g., 75.50%
	Gpu            float32   `json:"gpu" gorm:"type:decimal(5,2)"`
	Ram            int64     `json:"ram" gorm:"type:bigint"`
	RamTotal       int64     `json:"ram_total" gorm:"type:bigint"`
	Swap           int64     `json:"swap" gorm:"type:bigint"`
	SwapTotal      int64     `json:"swap_total" gorm:"type:bigint"`
	Load           float32   `json:"load" gorm:"type:decimal(5,2)"`
	Temp           float32   `json:"temp" gorm:"type:decimal(5,2)"`
	Disk           int64     `json:"disk" gorm:"type:bigint"`
	DiskTotal      int64     `json:"disk_total" gorm:"type:bigint"`
	NetIn          int64     `json:"net_in" gorm:"type:bigint"`
	NetOut         int64     `json:"net_out" gorm:"type:bigint"`
	NetTotalUp     int64     `json:"net_total_up" gorm:"type:bigint"`
	NetTotalDown   int64     `json:"net_total_down" gorm:"type:bigint"`
	Process        int       `json:"process"`
	Connections    int       `json:"connections"`
	ConnectionsUdp int       `json:"connections_udp"`
	//Uptime         int64     `json:"uptime" gorm:"type:bigint"`
}

// GPURecord logs individual GPU metrics over time
type GPURecord struct {
	Client      string    `json:"client" gorm:"type:varchar(36);index"`           // 客户端UUID
	Time        LocalTime `json:"time" gorm:"index"`                              // 记录时间
	DeviceIndex int       `json:"device_index" gorm:"index"`                      // GPU设备索引 (0,1,2...)
	DeviceName  string    `json:"device_name" gorm:"type:varchar(100)"`           // GPU型号
	MemTotal    int64     `json:"mem_total" gorm:"type:bigint"`                   // 显存总量(字节)
	MemUsed     int64     `json:"mem_used" gorm:"type:bigint"`                    // 显存使用(字节)
	Utilization float32   `json:"utilization" gorm:"type:decimal(5,2)"`           // GPU使用率(%)
	Temperature int       `json:"temperature"`                                    // GPU温度(°C)
}

// StringArray represents a slice of strings stored as JSON in the database
// StringArray 存储为 JSON 的字符串切片类型
type StringArray []string

func (sa *StringArray) Scan(value interface{}) error {
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan StringArray: value is not []byte")
	}
	return json.Unmarshal(bytes, sa)
}

func (sa StringArray) Value() (driver.Value, error) {
	return json.Marshal(sa)
}
