package trafficledger

import (
	"time"

	"github.com/komari-monitor/komari/database/models"
	"gorm.io/gorm"
)

// LedgerSnapshot is an immutable view of one client's traffic cycle state.
type LedgerSnapshot struct {
	UUID            string     `json:"uuid"`
	Mode            string     `json:"mode"`
	ResetDay        int        `json:"reset_day"`
	IntervalDays    int        `json:"interval_days"`
	AnchorAt        *time.Time `json:"anchor_at,omitempty"`
	Timezone        string     `json:"timezone"`
	CycleStartAt    *time.Time `json:"cycle_start_at,omitempty"`
	CycleEndAt      *time.Time `json:"cycle_end_at,omitempty"`
	AccumulatedUp   int64      `json:"accumulated_up"`
	AccumulatedDown int64      `json:"accumulated_down"`
	OffsetUp        int64      `json:"offset_up"`
	OffsetDown      int64      `json:"offset_down"`
	HasBaseline     bool       `json:"has_baseline"`
	RawLastUp       int64      `json:"raw_last_up"`
	RawLastDown     int64      `json:"raw_last_down"`
	RawLastUptime   int64      `json:"raw_last_uptime"`
	RawLastAt       *time.Time `json:"raw_last_at,omitempty"`
	CalibratedAt    *time.Time `json:"calibrated_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
	EffectiveUp     int64      `json:"effective_up"`
	EffectiveDown   int64      `json:"effective_down"`
}

// DisplayTraffic is the value exposed to live status APIs and traffic alerts.
type DisplayTraffic struct {
	Up           int64      `json:"up"`
	Down         int64      `json:"down"`
	Mode         string     `json:"mode"`
	CycleStartAt *time.Time `json:"cycle_start_at,omitempty"`
	CycleEndAt   *time.Time `json:"cycle_end_at,omitempty"`
	Calibrated   bool       `json:"calibrated"`
}

const (
	CloseReasonAutoRollover  = "auto_rollover"
	CloseReasonManualReset   = "manual_reset"
	CloseReasonModeChanged   = "mode_changed"
	CloseReasonClientDeleted = "client_deleted"
)

// ClosedCycleSnapshot is passed to CycleCloseHook in the same database
// transaction that persists the transition, keeping cycle history pluggable.
type ClosedCycleSnapshot struct {
	ClientUUID      string    `json:"client_uuid"`
	CycleStartAt    time.Time `json:"cycle_start_at"`
	CycleEndAt      time.Time `json:"cycle_end_at"`
	AccumulatedUp   int64     `json:"accumulated_up"`
	AccumulatedDown int64     `json:"accumulated_down"`
	OffsetUp        int64     `json:"offset_up"`
	OffsetDown      int64     `json:"offset_down"`
	EffectiveUp     int64     `json:"effective_up"`
	EffectiveDown   int64     `json:"effective_down"`
	CloseReason     string    `json:"close_reason"`
}

type CycleCloseHook func(tx *gorm.DB, snapshots []ClosedCycleSnapshot) error

// UpdateConfigRequest combines cycle policy with optional quota updates.
type UpdateConfigRequest struct {
	CycleConfig
	TrafficLimit     *int64  `json:"traffic_limit,omitempty"`
	TrafficLimitType *string `json:"traffic_limit_type,omitempty"`
}

// CalibrateRequest adjusts effective values without altering raw counters.
type CalibrateRequest struct {
	TargetUp      *int64 `json:"target_up,omitempty"`
	TargetDown    *int64 `json:"target_down,omitempty"`
	TargetTotal   *int64 `json:"target_total,omitempty"`
	DeltaUp       *int64 `json:"delta_up,omitempty"`
	DeltaDown     *int64 `json:"delta_down,omitempty"`
	SuppressAlert bool   `json:"suppress_alert"`
	Note          string `json:"note,omitempty"`
}

type CalibrationResult struct {
	BeforeUp   int64 `json:"before_up"`
	BeforeDown int64 `json:"before_down"`
	AfterUp    int64 `json:"after_up"`
	AfterDown  int64 `json:"after_down"`
	OffsetUp   int64 `json:"offset_up"`
	OffsetDown int64 `json:"offset_down"`
}

func snapshotFromModel(row models.ClientTrafficLedger) LedgerSnapshot {
	return LedgerSnapshot{
		UUID:            row.UUID,
		Mode:            row.Mode,
		ResetDay:        row.ResetDay,
		IntervalDays:    row.IntervalDays,
		AnchorAt:        cloneTime(row.AnchorAt),
		Timezone:        row.Timezone,
		CycleStartAt:    cloneTime(row.CycleStartAt),
		CycleEndAt:      cloneTime(row.CycleEndAt),
		AccumulatedUp:   row.AccumulatedUp,
		AccumulatedDown: row.AccumulatedDown,
		OffsetUp:        row.OffsetUp,
		OffsetDown:      row.OffsetDown,
		HasBaseline:     row.HasBaseline,
		RawLastUp:       row.RawLastUp,
		RawLastDown:     row.RawLastDown,
		RawLastUptime:   row.RawLastUptime,
		RawLastAt:       cloneTime(row.RawLastAt),
		CalibratedAt:    cloneTime(row.CalibratedAt),
		UpdatedAt:       row.UpdatedAt,
		EffectiveUp:     effectiveValue(row.AccumulatedUp, row.OffsetUp),
		EffectiveDown:   effectiveValue(row.AccumulatedDown, row.OffsetDown),
	}
}

func displayFromRow(row models.ClientTrafficLedger) DisplayTraffic {
	return DisplayTraffic{
		Up:           effectiveValue(row.AccumulatedUp, row.OffsetUp),
		Down:         effectiveValue(row.AccumulatedDown, row.OffsetDown),
		Mode:         row.Mode,
		CycleStartAt: cloneTime(row.CycleStartAt),
		CycleEndAt:   cloneTime(row.CycleEndAt),
		Calibrated:   row.OffsetUp != 0 || row.OffsetDown != 0,
	}
}

func configFromModel(row models.ClientTrafficLedger) CycleConfig {
	return CycleConfig{
		Mode:         row.Mode,
		ResetDay:     row.ResetDay,
		IntervalDays: row.IntervalDays,
		AnchorAt:     cloneTime(row.AnchorAt),
		Timezone:     row.Timezone,
	}
}

func modelFromConfig(uuid string, cfg CycleConfig) models.ClientTrafficLedger {
	return models.ClientTrafficLedger{
		UUID:         uuid,
		Mode:         cfg.Mode,
		ResetDay:     cfg.ResetDay,
		IntervalDays: cfg.IntervalDays,
		AnchorAt:     cloneTime(cfg.AnchorAt),
		Timezone:     cfg.Timezone,
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func closeSnapshot(row models.ClientTrafficLedger, end time.Time, reason string) ClosedCycleSnapshot {
	start := end.UTC()
	if row.CycleStartAt != nil {
		start = row.CycleStartAt.UTC()
	}
	if row.CycleEndAt != nil && row.CycleEndAt.Before(end) {
		end = row.CycleEndAt.UTC()
	}
	return ClosedCycleSnapshot{
		ClientUUID:      row.UUID,
		CycleStartAt:    start,
		CycleEndAt:      end.UTC(),
		AccumulatedUp:   row.AccumulatedUp,
		AccumulatedDown: row.AccumulatedDown,
		OffsetUp:        row.OffsetUp,
		OffsetDown:      row.OffsetDown,
		EffectiveUp:     effectiveValue(row.AccumulatedUp, row.OffsetUp),
		EffectiveDown:   effectiveValue(row.AccumulatedDown, row.OffsetDown),
		CloseReason:     reason,
	}
}
