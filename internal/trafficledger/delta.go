package trafficledger

import "math"

const maxCounterResetValue int64 = 64 << 30

// CounterDelta returns the increase between cumulative counters when uptime is
// unavailable. A plausible post-reset counter is counted from 0, while a small
// dip from a very large counter is treated as jitter.
func CounterDelta(current, previous int64) int64 {
	if current < 0 || previous < 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	if current > maxCounterResetValue {
		return 0
	}
	return current
}

// ComputeCounterDelta uses uptime drops to distinguish OS reboots from counter
// rollback (e.g. virtual NIC removal or filter changes). Legacy agents without
// uptime fall back to the bounded reset heuristic.
func ComputeCounterDelta(current, previous, currentUptime, previousUptime int64) int64 {
	if current < 0 || previous < 0 || currentUptime < 0 || previousUptime < 0 {
		return 0
	}
	if currentUptime > 0 && previousUptime > 0 {
		if previousUptime > 5 && currentUptime < previousUptime-5 {
			return current
		}
		if current >= previous {
			return current - previous
		}
		return 0
	}
	return CounterDelta(current, previous)
}

func saturatingAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func effectiveValue(base, offset int64) int64 {
	return max(saturatingAdd(base, offset), 0)
}
