package metric

import "time"

var standardQueryIntervals = [...]time.Duration{
	time.Second,
	5 * time.Second,
	10 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

// FloorStandardInterval returns the largest standard query interval that does
// not exceed interval. Values below one second use the minimum interval.
func FloorStandardInterval(interval time.Duration) time.Duration {
	selected := standardQueryIntervals[0]
	for _, candidate := range standardQueryIntervals {
		if candidate > interval {
			break
		}
		selected = candidate
	}
	return selected
}

// CeilStandardInterval returns the smallest standard query interval that is
// at least interval. Values above one day are rounded up to whole days.
func CeilStandardInterval(interval time.Duration) time.Duration {
	for _, candidate := range standardQueryIntervals {
		if candidate >= interval {
			return candidate
		}
	}
	day := standardQueryIntervals[len(standardQueryIntervals)-1]
	return ((interval-1)/day + 1) * day
}
