package report

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/protocol/v1"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetReportCache(t *testing.T) {
	t.Helper()
	Records.Flush()
	t.Cleanup(func() {
		Records.Flush()
	})
}

func TestAppendClientReportSerializesCacheMutation(t *testing.T) {
	resetReportCache(t)

	clientUUID := "client-append-helper"
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

	first, err := AppendClientReport(clientUUID, v1.Report{
		UpdatedAt: now.Add(-10 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 100, TotalDown: 200},
	})
	require.NoError(t, err)
	second, err := AppendClientReport(clientUUID, v1.Report{
		UpdatedAt: now.Add(-5 * time.Second),
		Network:   v1.NetworkReport{TotalUp: 150, TotalDown: 260},
	})
	require.NoError(t, err)

	cached, ok := Records.Get(clientUUID)
	require.True(t, ok)
	reports, ok := cached.([]v1.Report)
	require.True(t, ok)
	require.Len(t, reports, 2)
	assert.Equal(t, int64(100), reports[0].Network.TotalUp)
	assert.Equal(t, int64(150), reports[1].Network.TotalUp)
	assert.Equal(t, first.UpdatedAt, reports[0].UpdatedAt)
	assert.Equal(t, second.UpdatedAt, reports[1].UpdatedAt)
	assert.True(t, reports[0].UpdatedAt.After(now.Add(-10*time.Second)))
	assert.True(t, reports[1].UpdatedAt.After(now.Add(-5*time.Second)))
}

func TestAppendClientReportRejectsCorruptedCacheValue(t *testing.T) {
	resetReportCache(t)

	Records.Set("client-bad-cache", "not reports", cache.DefaultExpiration)

	_, err := AppendClientReport("client-bad-cache", v1.Report{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid report type")
}

// newCachedTrafficSummary 构造按时间递增的缓存流量汇总，便于测试增量累加逻辑。
func newCachedTrafficSummary(base time.Time, points ...[2]int64) cachedTrafficSummary {
	pts := make([]trafficTotalPoint, 0, len(points))
	for i, p := range points {
		pts = append(pts, trafficTotalPoint{
			Time:      base.Add(time.Duration(i) * time.Second),
			TotalUp:   p[0],
			TotalDown: p[1],
		})
	}
	return cachedTrafficSummary{Points: pts}
}

func TestSumCachedTrafficDeltasWithBaseline(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	previous := &previousTrafficRecord{
		Time:         models.FromTime(base.Add(-time.Minute)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}
	summary := newCachedTrafficSummary(base, [2]int64{130, 260}, [2]int64{175, 320})

	up, down := sumCachedTrafficDeltas(summary, previous)
	assert.Equal(t, int64(75), up)
	assert.Equal(t, int64(120), down)
}

func TestSumCachedTrafficDeltasWithBaselineAfterCounterReset(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	previous := &previousTrafficRecord{
		Time:         models.FromTime(base.Add(-time.Minute)),
		NetTotalUp:   500,
		NetTotalDown: 700,
	}
	summary := newCachedTrafficSummary(base, [2]int64{30, 45}, [2]int64{40, 55})

	up, down := sumCachedTrafficDeltas(summary, previous)
	assert.Equal(t, int64(40), up)
	assert.Equal(t, int64(55), down)
}

func TestSumCachedTrafficDeltasWithoutBaseline(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	summary := newCachedTrafficSummary(base,
		[2]int64{100, 200}, [2]int64{130, 250}, [2]int64{155, 280})

	up, down := sumCachedTrafficDeltas(summary, nil)
	assert.Equal(t, int64(55), up)
	assert.Equal(t, int64(80), down)
}

func TestSumCachedTrafficDeltasAcrossIntraMinuteReset(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	previous := &previousTrafficRecord{
		Time:         models.FromTime(base.Add(-time.Minute)),
		NetTotalUp:   500,
		NetTotalDown: 700,
	}
	summary := newCachedTrafficSummary(base,
		[2]int64{540, 760}, [2]int64{10, 20}, [2]int64{25, 35})

	up, down := sumCachedTrafficDeltas(summary, previous)
	assert.Equal(t, int64(65), up)
	assert.Equal(t, int64(95), down)
}

func TestSumCachedTrafficDeltasSkipsPointsAtOrBeforeBaseline(t *testing.T) {
	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	previous := &previousTrafficRecord{
		Time:         models.FromTime(base.Add(1 * time.Second)),
		NetTotalUp:   100,
		NetTotalDown: 200,
	}
	// 前两个点时间早于/等于基线时间，应被忽略；仅最后一个点（晚于基线）计入。
	summary := newCachedTrafficSummary(base,
		[2]int64{50, 90}, [2]int64{130, 260}, [2]int64{160, 300})

	up, down := sumCachedTrafficDeltas(summary, previous)
	assert.Equal(t, int64(60), up)
	assert.Equal(t, int64(100), down)
}

