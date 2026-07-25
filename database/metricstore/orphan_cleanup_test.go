package metricstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v1 "github.com/komari-monitor/komari/protocol/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupOrphanedDataRemovesDeletedEntitiesAndPingTasks(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	now := time.Now().UTC()
	require.NoError(t, s.WriteBatch(ctx, []metric.Point{
		{MetricName: MetricCPU, EntityID: "client-a", Timestamp: now, Value: 10},
		{MetricName: MetricCPU, EntityID: "deleted-client", Timestamp: now, Value: 20},
		{MetricName: MetricTrafficUp, EntityID: "deleted-client", Timestamp: now, Value: 100},
		{MetricName: MetricTrafficDown, EntityID: "deleted-client", Timestamp: now, Value: 200},
		{MetricName: MetricNetTotalUp, EntityID: "deleted-client", Timestamp: now, Value: 1000},
		{MetricName: MetricNetTotalDown, EntityID: "deleted-client", Timestamp: now, Value: 2000},
		{MetricName: MetricPingLatency, EntityID: "client-a", Timestamp: now, Value: 12, Tags: map[string]string{"task_id": "1"}},
		{MetricName: MetricPingLoss, EntityID: "client-a", Timestamp: now, Value: 0, Tags: map[string]string{"task_id": "1"}},
		{MetricName: MetricPingLatency, EntityID: "client-a", Timestamp: now, Value: 50, Tags: map[string]string{"task_id": "999"}},
		{MetricName: MetricPingLoss, EntityID: "client-a", Timestamp: now, Value: 1, Tags: map[string]string{"task_id": "999"}},
	}))
	reportTrafficStates.Store("deleted-client", &reportTrafficState{})

	result, err := CleanupOrphanedData(ctx,
		map[string]struct{}{"client-a": {}},
		map[uint]struct{}{1: {}},
	)
	require.NoError(t, err)
	assert.Equal(t, OrphanCleanupResult{Entities: 1, PingTasks: 1}, result)

	entityIDs, err := s.AllEntityIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"client-a"}, entityIDs)
	taskIDs, err := s.MetricTagValues(ctx, MetricPingLatency, "task_id")
	require.NoError(t, err)
	assert.Equal(t, []string{"1"}, taskIDs)
	for _, metricName := range []string{MetricTrafficUp, MetricTrafficDown, MetricNetTotalUp, MetricNetTotalDown} {
		points, queryErr := s.Query(ctx, metric.Query{
			MetricName: metricName,
			EntityID:   "deleted-client",
			Start:      now.Add(-time.Second),
			End:        now.Add(time.Second),
		})
		require.NoError(t, queryErr)
		assert.Empty(t, points, metricName)
	}
	_, trafficStateExists := reportTrafficStates.Load("deleted-client")
	assert.False(t, trafficStateExists)
}

func TestBlockedTargetsCannotRecreateDeletedMetrics(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = StopReportBatcher(stopCtx)
	})

	report := v1.Report{UUID: "deleted-client", UpdatedAt: time.Now().UTC(), CPU: v1.CPUReport{Usage: 25}}
	_, err := WriteReport(ctx, report)
	require.NoError(t, err)
	BlockEntityWrites(report.UUID)
	t.Cleanup(func() { UnblockEntityWrites(report.UUID) })
	require.NoError(t, FlushReportBatch(ctx))

	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   report.UUID,
		Start:      report.UpdatedAt.Add(-time.Second),
		End:        report.UpdatedAt.Add(time.Second),
	})
	require.NoError(t, err)
	assert.Empty(t, points)
	_, err = WriteReport(ctx, report)
	assert.ErrorIs(t, err, ErrMetricWriteBlocked)

	BlockPingTaskWrites([]uint{99})
	t.Cleanup(func() { UnblockPingTaskWrites([]uint{99}) })
	err = WritePingRecord(ctx, models.PingRecord{Client: "client-a", TaskId: 99, Time: time.Now().UTC(), Value: 10})
	assert.True(t, errors.Is(err, ErrMetricWriteBlocked))
}

func TestQueuedPingRecordCannotRecreateDeletedTaskMetrics(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = StopReportBatcher(stopCtx)
	})

	now := time.Now().UTC()
	require.NoError(t, WritePingRecord(ctx, models.PingRecord{Client: "client-a", TaskId: 88, Time: now, Value: 12}))
	BlockPingTaskWrites([]uint{88})
	t.Cleanup(func() { UnblockPingTaskWrites([]uint{88}) })
	require.NoError(t, FlushReportBatch(ctx))

	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricPingLatency,
		EntityID:   "client-a",
		Start:      now.Add(-time.Second),
		End:        now.Add(time.Second),
	})
	require.NoError(t, err)
	assert.Empty(t, points)
}
