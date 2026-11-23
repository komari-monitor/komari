package models

import "time"

const (
	TrafficCycleModeAgent     = "agent"
	TrafficCycleModeMonthly   = "monthly"
	TrafficCycleModeFixedDays = "fixed_days"
	TrafficCycleModeManual    = "manual"
)

// ClientTrafficLedger stores one client's traffic-cycle configuration and the
// counter state required to continue accounting across server restarts.
type ClientTrafficLedger struct {
	UUID            string     `json:"uuid" gorm:"type:varchar(36);primaryKey"`
	Mode            string     `json:"mode" gorm:"type:varchar(20);not null;default:'agent'"`
	ResetDay        int        `json:"reset_day" gorm:"not null;default:1"`
	IntervalDays    int        `json:"interval_days" gorm:"not null;default:30"`
	AnchorAt        *time.Time `json:"anchor_at" gorm:"type:timestamp"`
	Timezone        string     `json:"timezone" gorm:"type:varchar(100);not null;default:''"`
	CycleStartAt    *time.Time `json:"cycle_start_at" gorm:"type:timestamp"`
	CycleEndAt      *time.Time `json:"cycle_end_at" gorm:"type:timestamp"`
	AccumulatedUp   int64      `json:"accumulated_up" gorm:"type:bigint;not null;default:0"`
	AccumulatedDown int64      `json:"accumulated_down" gorm:"type:bigint;not null;default:0"`
	OffsetUp        int64      `json:"offset_up" gorm:"type:bigint;not null;default:0"`
	OffsetDown      int64      `json:"offset_down" gorm:"type:bigint;not null;default:0"`
	HasBaseline     bool       `json:"has_baseline" gorm:"not null;default:false"`
	RawLastUp       int64      `json:"raw_last_up" gorm:"type:bigint;not null;default:0"`
	RawLastDown     int64      `json:"raw_last_down" gorm:"type:bigint;not null;default:0"`
	RawLastUptime   int64      `json:"raw_last_uptime" gorm:"type:bigint;not null;default:0"`
	RawLastAt       *time.Time `json:"raw_last_at" gorm:"type:timestamp"`
	CalibratedAt    *time.Time `json:"calibrated_at" gorm:"type:timestamp"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (ClientTrafficLedger) TableName() string {
	return "client_traffic_ledgers"
}
