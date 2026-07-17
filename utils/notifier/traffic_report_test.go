package notifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPreviousTrafficReportRangesUseSystemTimezone(t *testing.T) {
	originalLocal := time.Local
	time.Local = time.FixedZone("UTC+8", 8*60*60)
	t.Cleanup(func() { time.Local = originalLocal })

	now := time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC) // 2026-07-01 00:00 local
	tests := []struct {
		period    string
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			period:    "daily",
			wantStart: time.Date(2026, 6, 29, 16, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC).Add(-time.Nanosecond),
		},
		{
			period:    "weekly",
			wantStart: time.Date(2026, 6, 21, 16, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 6, 28, 16, 0, 0, 0, time.UTC).Add(-time.Nanosecond),
		},
		{
			period:    "monthly",
			wantStart: time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 6, 30, 16, 0, 0, 0, time.UTC).Add(-time.Nanosecond),
		},
	}

	for _, test := range tests {
		t.Run(test.period, func(t *testing.T) {
			start, end := previousTrafficReportRange(now, test.period)
			if !start.Equal(test.wantStart) || !end.Equal(test.wantEnd) {
				t.Fatalf("range = [%s, %s], want [%s, %s]", start, end, test.wantStart, test.wantEnd)
			}
			if start.Location() != time.UTC || end.Location() != time.UTC {
				t.Fatalf("range locations = [%s, %s], want UTC", start.Location(), end.Location())
			}
		})
	}
}

func TestPreviousDailyTrafficReportRangeHandlesDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	originalLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = originalLocal })

	now := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC) // 2026-03-09 00:00 EDT
	start, end := previousTrafficReportRange(now, "daily")
	if got := end.Add(time.Nanosecond).Sub(start); got != 23*time.Hour {
		t.Fatalf("DST day duration = %s, want 23h", got)
	}
}

func TestSumTrafficDeltasUsesStoredDeltas(t *testing.T) {
	records := []trafficDeltaRecord{
		{TrafficUp: 30, TrafficDown: 40, NetTotalUp: 130, NetTotalDown: 240},
		{TrafficUp: 25, TrafficDown: 35, NetTotalUp: 155, NetTotalDown: 275},
	}

	up, down := sumTrafficDeltas(records, nil)
	assert.Equal(t, int64(55), up)
	assert.Equal(t, int64(75), down)
}

func TestSumTrafficDeltasFallsBackToCumulativeTotals(t *testing.T) {
	previous := &trafficDeltaRecord{NetTotalUp: 100, NetTotalDown: 200}
	records := []trafficDeltaRecord{
		{TrafficUp: 0, TrafficDown: 0, NetTotalUp: 130, NetTotalDown: 250},
		{TrafficUp: 0, TrafficDown: 0, NetTotalUp: 160, NetTotalDown: 310},
	}

	up, down := sumTrafficDeltas(records, previous)
	assert.Equal(t, int64(60), up)
	assert.Equal(t, int64(110), down)
}

func TestSumTrafficDeltasHandlesCounterResetInFallback(t *testing.T) {
	previous := &trafficDeltaRecord{NetTotalUp: 400, NetTotalDown: 550}
	records := []trafficDeltaRecord{
		{TrafficUp: 0, TrafficDown: 0, NetTotalUp: 500, NetTotalDown: 600},
		{TrafficUp: 0, TrafficDown: 0, NetTotalUp: 100, NetTotalDown: 150},
	}

	up, down := sumTrafficDeltas(records, previous)
	assert.Equal(t, int64(200), up)
	assert.Equal(t, int64(200), down)
}

func TestSumTrafficDeltasEmpty(t *testing.T) {
	up, down := sumTrafficDeltas(nil, nil)
	assert.Equal(t, int64(0), up)
	assert.Equal(t, int64(0), down)
}

func TestTrafficDeltaOrFallback(t *testing.T) {
	// 存储的增量为正时直接使用
	assert.Equal(t, int64(42), trafficDeltaOrFallback(42, 500, 100))
	// 增量缺失（<=0）时回退到累计差值
	assert.Equal(t, int64(400), trafficDeltaOrFallback(0, 500, 100))
	// 回退路径识别计数器重置
	assert.Equal(t, int64(50), trafficDeltaOrFallback(0, 50, 500))
}

func TestComputeUsedByType(t *testing.T) {
	assert.Equal(t, int64(30), computeUsedByType("up", 30, 70))
	assert.Equal(t, int64(70), computeUsedByType("down", 30, 70))
	assert.Equal(t, int64(100), computeUsedByType("sum", 30, 70))
	assert.Equal(t, int64(30), computeUsedByType("min", 30, 70))
	assert.Equal(t, int64(70), computeUsedByType("max", 30, 70))
	// 未知类型默认取较大值
	assert.Equal(t, int64(70), computeUsedByType("unknown", 30, 70))
}
