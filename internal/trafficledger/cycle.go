package trafficledger

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
)

const (
	ModeAgent     = models.TrafficCycleModeAgent
	ModeMonthly   = models.TrafficCycleModeMonthly
	ModeFixedDays = models.TrafficCycleModeFixedDays
	ModeManual    = models.TrafficCycleModeManual
)

var locationCache sync.Map // map[string]*time.Location

// CycleConfig describes how a client's server-side traffic cycle is bounded.
type CycleConfig struct {
	Mode         string     `json:"mode"`
	ResetDay     int        `json:"reset_day"`
	IntervalDays int        `json:"interval_days"`
	AnchorAt     *time.Time `json:"anchor_at"`
	Timezone     string     `json:"timezone"`
}

func DefaultCycleConfig() CycleConfig {
	return CycleConfig{Mode: ModeAgent, ResetDay: 1, IntervalDays: 30}
}

func loadLocationCached(zone string) (*time.Location, error) {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return time.Local, nil
	}
	if cached, ok := locationCache.Load(zone); ok {
		return cached.(*time.Location), nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	locationCache.Store(zone, loc)
	return loc, nil
}

// ResolveLocation resolves a node's effective timezone, falling back to
// defaultTimezone and then time.Local.
func ResolveLocation(nodeTimezone, defaultTimezone string) (*time.Location, error) {
	zone := strings.TrimSpace(nodeTimezone)
	if zone == "" {
		zone = strings.TrimSpace(defaultTimezone)
	}
	loc, err := loadLocationCached(zone)
	if err != nil {
		return nil, fmt.Errorf("invalid traffic timezone %q: %w", zone, err)
	}
	return loc, nil
}

func ValidateCycleConfig(cfg CycleConfig) error {
	if cfg.ResetDay < 1 || cfg.ResetDay > 31 {
		return fmt.Errorf("reset_day must be between 1 and 31")
	}
	if cfg.IntervalDays < 1 {
		return fmt.Errorf("interval_days must be at least 1")
	}
	switch cfg.Mode {
	case ModeAgent, ModeMonthly, ModeManual:
	case ModeFixedDays:
		if cfg.AnchorAt == nil || cfg.AnchorAt.IsZero() {
			return fmt.Errorf("anchor_at is required for fixed_days mode")
		}
	default:
		return fmt.Errorf("unsupported traffic cycle mode %q", cfg.Mode)
	}
	if strings.TrimSpace(cfg.Timezone) != "" {
		if _, err := loadLocationCached(cfg.Timezone); err != nil {
			return fmt.Errorf("invalid traffic timezone %q: %w", cfg.Timezone, err)
		}
	}
	return nil
}

// CycleBounds returns the half-open cycle [start, end) containing at using
// cfg.Timezone (or time.Local when empty). Agent and manual modes return nil.
func CycleBounds(cfg CycleConfig, at time.Time) (start, end *time.Time, err error) {
	return cycleBoundsWithDefault(cfg, "", at)
}

func cycleBoundsWithDefault(cfg CycleConfig, defaultTimezone string, at time.Time) (start, end *time.Time, err error) {
	if err := ValidateCycleConfig(cfg); err != nil {
		return nil, nil, err
	}
	if at.IsZero() {
		return nil, nil, fmt.Errorf("cycle time is required")
	}
	if cfg.Mode == ModeAgent || cfg.Mode == ModeManual {
		return nil, nil, nil
	}
	loc, err := ResolveLocation(cfg.Timezone, defaultTimezone)
	if err != nil {
		return nil, nil, err
	}
	localAt := at.In(loc)

	var cycleStart, cycleEnd time.Time
	switch cfg.Mode {
	case ModeMonthly:
		cycleStart = monthlyBoundary(localAt.Year(), localAt.Month(), cfg.ResetDay, loc)
		if localAt.Before(cycleStart) {
			cycleStart = monthlyBoundary(localAt.Year(), localAt.Month()-1, cfg.ResetDay, loc)
		}
		cycleEnd = monthlyBoundary(cycleStart.Year(), cycleStart.Month()+1, cfg.ResetDay, loc)
	case ModeFixedDays:
		anchor := cfg.AnchorAt.In(loc)
		days := calendarDayDifference(anchor, localAt)
		period := floorDiv(days, cfg.IntervalDays)
		cycleStart = anchor.AddDate(0, 0, period*cfg.IntervalDays)
		if cycleStart.After(localAt) {
			cycleStart = cycleStart.AddDate(0, 0, -cfg.IntervalDays)
		}
		cycleEnd = cycleStart.AddDate(0, 0, cfg.IntervalDays)
	}
	startUTC, endUTC := cycleStart.UTC(), cycleEnd.UTC()
	return &startUTC, &endUTC, nil
}

func monthlyBoundary(year int, month time.Month, day int, loc *time.Location) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	lastDay := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(day, lastDay), 0, 0, 0, 0, loc)
}

func calendarDayDifference(anchor, at time.Time) int {
	anchorDay := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)
	atDay := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	return int(atDay.Sub(anchorDay) / (24 * time.Hour))
}

func floorDiv(value, divisor int) int {
	q, r := value/divisor, value%divisor
	if r < 0 {
		q--
	}
	return q
}
