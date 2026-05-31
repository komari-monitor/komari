package common

import "time"

type Message struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
}

type ClientConfig struct {
	ClientUUID  string    `json:"client_uuid" gorm:"type:uuid;primaryKey;foreignKey:ClientUUID;references:UUID;constraint:OnDelete:CASCADE"`
	CPU         bool      `json:"cpu" gorm:"default:true"`
	GPU         bool      `json:"gpu" gorm:"default:true"`
	RAM         bool      `json:"ram" gorm:"default:true"`
	SWAP        bool      `json:"swap" gorm:"default:true"`
	LOAD        bool      `json:"load" gorm:"default:true"`
	UPTIME      bool      `json:"uptime" gorm:"default:true"`
	TEMP        bool      `json:"temp" gorm:"default:true"`
	OS          bool      `json:"os" gorm:"default:true"`
	DISK        bool      `json:"disk" gorm:"default:true"`
	NET         bool      `json:"net" gorm:"default:true"`
	PROCESS     bool      `json:"process" gorm:"default:true"`
	Connections bool      `json:"connections" gorm:"default:true"`
	Interval    int       `json:"interval" gorm:"default:3;check:Interval >= 1"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ClientInfo stores static client information
// Deprecated: Use models.Client instead.
type ClientInfo struct {
	UUID           string    `json:"uuid,omitempty" gorm:"type:varchar(36);primaryKey;foreignKey:ClientUUID;references:UUID;constraint:OnDelete:CASCADE"`
	Name           string    `json:"name" gorm:"type:varchar(100);not null"`
	CpuName        string    `json:"cpu_name" gorm:"type:varchar(100)"`
	Virtualization string    `json:"virtualization" gorm:"type:varchar(50)"`
	Arch           string    `json:"arch" gorm:"type:varchar(50)"`
	CpuCores       int       `json:"cpu_cores" gorm:"type:int"`
	OS             string    `json:"os" gorm:"type:varchar(100)"`
	GpuName        string    `json:"gpu_name" gorm:"type:varchar(100)"`
	IPv4           string    `json:"ipv4,omitempty" gorm:"type:varchar(100)"`
	IPv6           string    `json:"ipv6,omitempty" gorm:"type:varchar(100)"`
	Region         string    `json:"region" gorm:"type:varchar(100)"`
	Remark         string    `json:"remark,omitempty" gorm:"type:longtext"`
	PublicRemark   string    `json:"public_remark,omitempty" gorm:"type:longtext"`
	MemTotal       int64     `json:"mem_total" gorm:"type:bigint"`
	SwapTotal      int64     `json:"swap_total" gorm:"type:bigint"`
	DiskTotal      int64     `json:"disk_total" gorm:"type:bigint"`
	Version        string    `json:"version,omitempty" gorm:"type:varchar(100)"`
	Weight         int       `json:"weight" gorm:"type:int"`
	Price          float64   `json:"price"`
	BillingCycle   int       `json:"billing_cycle"`
	ExpiredAt      time.Time `json:"expired_at" gorm:"type:timestamp"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type IPAddress struct {
	Ipv4 string `json:"ipv4"`
	Ipv6 string `json:"ipv6"`
}
type Report struct {
	UUID        string            `json:"uuid,omitempty"`
	CPU         CPUReport         `json:"cpu"`
	Ram         RamReport         `json:"ram"`
	Swap        RamReport         `json:"swap"`
	Load        LoadReport        `json:"load"`
	Disk        DiskReport        `json:"disk"`
	Network     NetworkReport     `json:"network"`
	Connections ConnectionsReport `json:"connections"`
	GPU         *GPUDetailReport  `json:"gpu,omitempty"` // 新增GPU详细信息
	Uptime      int64             `json:"uptime"`
	Process     int               `json:"process"`
	Message     string            `json:"message"`
	Method      string            `json:"method,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type CPUReport struct {
	Name  string  `json:"name,omitempty"`
	Cores int     `json:"cores,omitempty"`
	Arch  string  `json:"arch,omitempty"`
	Usage float64 `json:"usage,omitempty"`
}

// GPUDetailReport 详细GPU信息报告
type GPUDetailReport struct {
	Count        int             `json:"count"`         // GPU数量
	AverageUsage float64         `json:"average_usage"` // 平均使用率
	DetailedInfo []GPUDeviceInfo `json:"detailed_info"` // 每个GPU的详细信息
}

// GPUDeviceInfo 单个GPU设备信息
type GPUDeviceInfo struct {
	Name        string  `json:"name"`         // GPU型号名称
	MemoryTotal int64   `json:"memory_total"` // 显存总量(字节)
	MemoryUsed  int64   `json:"memory_used"`  // 显存已用(字节)
	Utilization float64 `json:"utilization"`  // GPU使用率(%)
	Temperature int     `json:"temperature"`  // GPU温度(°C)
}

// 保持向后兼容的旧GPUReport结构
type GPUReport struct {
	Name  string  `json:"name,omitempty"`
	Usage float64 `json:"usage,omitempty"`
}

type RamReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type LoadReport struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type DiskReport struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

type NetworkReport struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	TotalUp   int64 `json:"totalUp"`
	TotalDown int64 `json:"totalDown"`
}

type ConnectionsReport struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}
