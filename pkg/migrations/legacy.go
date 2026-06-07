package migrations

import "time"

// ClientInfo is the legacy table shape for migrating pre-Client model data.
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
