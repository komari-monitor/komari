package trafficledger

import "testing"

func TestCounterDelta(t *testing.T) {
	const oneGiB = int64(1 << 30)
	cases := []struct {
		name string
		cur  int64
		prev int64
		want int64
	}{
		{name: "increase", cur: 150, prev: 100, want: 50},
		{name: "first nonzero after reset", cur: 20, prev: 100, want: 20},
		{name: "large counter jitter", cur: 100*oneGiB - 1, prev: 100 * oneGiB, want: 0},
		{name: "negative current", cur: -1, prev: 1, want: 0},
		{name: "negative previous", cur: 1, prev: -1, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CounterDelta(tc.cur, tc.prev); got != tc.want {
				t.Fatalf("CounterDelta(%d, %d) = %d, want %d", tc.cur, tc.prev, got, tc.want)
			}
		})
	}
}

func TestComputeCounterDeltaUsesUptimeForRebootDetection(t *testing.T) {
	const oneGiB = int64(1 << 30)
	cases := []struct {
		name                  string
		cur, prev             int64
		curUptime, prevUptime int64
		want                  int64
	}{
		{name: "uptime reboot overrides reset size cap", cur: 100 * oneGiB, prev: 200 * oneGiB, curUptime: 3, prevUptime: 1000, want: 100 * oneGiB},
		{name: "increase with increasing uptime", cur: 200, prev: 100, curUptime: 100, prevUptime: 80, want: 100},
		{name: "counter rollback without reboot", cur: 20, prev: 100, curUptime: 100, prevUptime: 99, want: 0},
		{name: "uptime tolerance is five seconds", cur: 20, prev: 100, curUptime: 95, prevUptime: 100, want: 0},
		{name: "legacy uptime fallback counts small reset", cur: 20, prev: 100, curUptime: 0, prevUptime: 100, want: 20},
		{name: "legacy fallback rejects large reset", cur: 100 * oneGiB, prev: 200 * oneGiB, curUptime: 0, prevUptime: 0, want: 0},
		{name: "negative uptime", cur: 10, prev: 1, curUptime: -1, prevUptime: 10, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeCounterDelta(tc.cur, tc.prev, tc.curUptime, tc.prevUptime)
			if got != tc.want {
				t.Fatalf("ComputeCounterDelta() = %d, want %d", got, tc.want)
			}
		})
	}
}
