package renewal

import (
	"testing"
	"time"
)

func TestAddMonthsKeepsMonthEnd(t *testing.T) {
	for _, c := range []struct {
		from   string
		months int
		want   string
	}{
		{"2026-10-31", 1, "2026-11-30"},
		{"2027-01-31", 1, "2027-02-28"},
		{"2026-11-30", 3, "2027-02-28"},
		{"2028-02-29", 12, "2029-02-28"},
		{"2026-10-15", 1, "2026-11-15"},
	} {
		from, _ := time.ParseInLocation("2006-01-02", c.from, time.Local)
		if got := addMonths(from, c.months).Format("2006-01-02"); got != c.want {
			t.Errorf("addMonths(%s, %d) = %s, want %s", c.from, c.months, got, c.want)
		}
	}
}
