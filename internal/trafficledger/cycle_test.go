package trafficledger

import (
	"testing"
	"time"
)

func TestMonthlyCycleBoundsClampResetDayInConfiguredTimezone(t *testing.T) {
	cfg := CycleConfig{Mode: ModeMonthly, ResetDay: 31, IntervalDays: 30, Timezone: "Asia/Tokyo"}
	before, err := time.Parse(time.RFC3339, "2024-02-28T14:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	start, end, err := CycleBounds(cfg, before)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, 1, 30, 15, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Fatalf("cycle start = %s, want %s", start, want)
	}
	if want := time.Date(2024, 2, 28, 15, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Fatalf("cycle end = %s, want %s", end, want)
	}

	after := before.Add(2 * time.Hour)
	start, end, err = CycleBounds(cfg, after)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, 2, 28, 15, 0, 0, 0, time.UTC); !start.Equal(want) {
		t.Fatalf("clamped cycle start = %s, want %s", start, want)
	}
	if want := time.Date(2024, 3, 30, 15, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Fatalf("next cycle end = %s, want %s", end, want)
	}
}

func TestFixedDayCycleUsesCalendarDaysAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2024, time.March, 9, 0, 0, 0, 0, loc)
	cfg := CycleConfig{Mode: ModeFixedDays, ResetDay: 1, IntervalDays: 2, AnchorAt: &anchor, Timezone: loc.String()}
	at := time.Date(2024, time.March, 10, 12, 0, 0, 0, loc)
	start, end, err := CycleBounds(cfg, at)
	if err != nil {
		t.Fatal(err)
	}
	if want := anchor.UTC(); !start.Equal(want) {
		t.Fatalf("cycle start = %s, want %s", start, want)
	}
	wantEnd := time.Date(2024, time.March, 11, 0, 0, 0, 0, loc).UTC()
	if !end.Equal(wantEnd) {
		t.Fatalf("cycle end = %s, want %s", end, wantEnd)
	}
	if end.Sub(*start) != 47*time.Hour {
		t.Fatalf("DST cycle duration = %s, want 47h", end.Sub(*start))
	}
}

func TestManualAndAgentModesHaveNoCalculatedBounds(t *testing.T) {
	at := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, mode := range []string{ModeAgent, ModeManual} {
		cfg := DefaultCycleConfig()
		cfg.Mode = mode
		start, end, err := CycleBounds(cfg, at)
		if err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
		if start != nil || end != nil {
			t.Fatalf("mode %s bounds = %v .. %v, want nil", mode, start, end)
		}
	}
}

func TestCycleBoundsEmptyTimezoneUsesDefaultFallback(t *testing.T) {
	at := time.Date(2024, time.February, 1, 0, 0, 0, 0, time.UTC)
	start, _, err := cycleBoundsWithDefault(CycleConfig{Mode: ModeMonthly, ResetDay: 1, IntervalDays: 30}, "Asia/Tokyo", at)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, time.January, 31, 15, 0, 0, 0, time.UTC)
	if !start.Equal(want) {
		t.Fatalf("cycle start = %s, want %s", start, want)
	}
}

func TestValidateCycleConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  CycleConfig
		want bool
	}{
		{name: "monthly reset day", cfg: CycleConfig{Mode: ModeMonthly, ResetDay: 0, IntervalDays: 30}, want: true},
		{name: "fixed interval", cfg: CycleConfig{Mode: ModeFixedDays, ResetDay: 1, IntervalDays: 0}, want: true},
		{name: "fixed anchor", cfg: CycleConfig{Mode: ModeFixedDays, ResetDay: 1, IntervalDays: 7}, want: true},
		{name: "timezone", cfg: CycleConfig{Mode: ModeAgent, ResetDay: 1, IntervalDays: 30, Timezone: "Not/AZone"}, want: true},
		{name: "valid manual", cfg: CycleConfig{Mode: ModeManual, ResetDay: 1, IntervalDays: 30}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCycleConfig(tc.cfg); (err != nil) != tc.want {
				t.Fatalf("ValidateCycleConfig() error = %v, want error %v", err, tc.want)
			}
		})
	}
}
