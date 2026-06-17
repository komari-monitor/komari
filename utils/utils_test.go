package utils

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/stretchr/testify/assert"
)

func TestAverageReportUsesLatestNetworkTotals(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	record := AverageReport("client-latest-total", base.Add(time.Minute), []v1.Report{
		{
			UpdatedAt: base.Add(10 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 300, TotalDown: 500},
		},
		{
			UpdatedAt: base.Add(50 * time.Second),
			Network:   v1.NetworkReport{TotalUp: 360, TotalDown: 620},
		},
	}, 0)

	assert.Equal(t, int64(360), record.NetTotalUp)
	assert.Equal(t, int64(620), record.NetTotalDown)
}

func TestComputeTrafficDelta(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		previous int64
		want     int64
	}{
		{name: "previous zero counts current delta", current: 120, previous: 0, want: 120},
		{name: "monotonic counter uses difference", current: 250, previous: 200, want: 50},
		{name: "same counter", current: 100, previous: 100, want: 0},
		{name: "counter reset uses current", current: 15, previous: 250, want: 15},
		{name: "negative current remains guarded", current: -1, previous: 100, want: 0},
		{name: "negative previous remains guarded", current: 15, previous: -1, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, ComputeTrafficDelta(test.current, test.previous))
		})
	}
}
