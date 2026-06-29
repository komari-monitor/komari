package clients

import (
	"testing"

	"github.com/komari-monitor/komari/utils"
	"github.com/stretchr/testify/assert"
)

func TestComputeTrafficDeltaHandlesZeroAndReset(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		previous int64
		want     int64
	}{
		{name: "previous zero counts current delta", current: 120, previous: 0, want: 120},
		{name: "monotonic counter uses difference", current: 250, previous: 200, want: 50},
		{name: "counter reset uses current", current: 15, previous: 250, want: 15},
		{name: "negative previous remains guarded", current: 15, previous: -1, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, utils.ComputeTrafficDelta(test.current, test.previous))
		})
	}
}
