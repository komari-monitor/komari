package timeutil

import (
	"testing"
	"time"
)

func TestSystemDateHelpersUseSystemLocal(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocal })

	lateUTC := time.Date(2026, 7, 17, 16, 30, 0, 0, time.UTC)
	earlyUTC := time.Date(2026, 7, 18, 1, 30, 0, 0, time.UTC)
	if !SameSystemDate(lateUTC, earlyUTC) {
		t.Fatal("instants on the same system-local date should match")
	}
	if got := FormatSystemDate(lateUTC); got != "2026-07-18" {
		t.Fatalf("formatted system date = %q, want 2026-07-18", got)
	}
	if got := SystemDateDistance(lateUTC.AddDate(0, 0, -2), lateUTC); got != 2 {
		t.Fatalf("system date distance = %d, want 2", got)
	}
}

func TestFormatSystemDateReturnsEmptyForZeroTime(t *testing.T) {
	if got := FormatSystemDate(time.Time{}); got != "" {
		t.Fatalf("formatted zero time = %q, want empty", got)
	}
}
