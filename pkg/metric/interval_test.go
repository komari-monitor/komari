package metric

import (
	"testing"
	"time"
)

func TestStandardIntervals(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		floor    time.Duration
		ceil     time.Duration
	}{
		{name: "below minimum", interval: 500 * time.Millisecond, floor: time.Second, ceil: time.Second},
		{name: "exact", interval: 5 * time.Minute, floor: 5 * time.Minute, ceil: 5 * time.Minute},
		{name: "between standards", interval: 7 * time.Minute, floor: 5 * time.Minute, ceil: 10 * time.Minute},
		{name: "above one day", interval: 25 * time.Hour, floor: 24 * time.Hour, ceil: 48 * time.Hour},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FloorStandardInterval(test.interval); got != test.floor {
				t.Fatalf("FloorStandardInterval(%s) = %s, want %s", test.interval, got, test.floor)
			}
			if got := CeilStandardInterval(test.interval); got != test.ceil {
				t.Fatalf("CeilStandardInterval(%s) = %s, want %s", test.interval, got, test.ceil)
			}
		})
	}
}
