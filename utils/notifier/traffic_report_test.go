package notifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
