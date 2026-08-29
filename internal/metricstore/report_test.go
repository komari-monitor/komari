package metricstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func useReportTestStore(t *testing.T, policy *metric.RollupPolicy) *metric.Store {
	t.Helper()
	ctx := context.Background()
	opts := []metric.Option{metric.WithMaxOpenConns(1)}
	if policy != nil {
		opts = append(opts, metric.WithRollupPolicy(*policy))
	}
	dsn := fmt.Sprintf("file:report-%d?mode=memory&cache=shared", time.Now().UnixNano())
	s, err := metric.Open(ctx, metric.SQLite(dsn, opts...))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	if err := createMetricDefinitions(ctx, s); err != nil {
		_ = s.Close()
		t.Fatalf("create metric definitions: %v", err)
	}

	storeMu.Lock()
	previous := store
	store = s
	storeMu.Unlock()
	clearReportTrafficStates()
	t.Cleanup(func() {
		clearReportTrafficStates()
		storeMu.Lock()
		store = previous
		storeMu.Unlock()
		_ = s.Close()
	})
	return s
}

func TestWriteReportStoresMinuteMetricsAndResetAwareTraffic(t *testing.T) {
	ctx := context.Background()
	policy := defaultRollupPolicy()
	s := useReportTestStore(t, &policy)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	now := base.Add(45 * time.Second)

	report := v2.Report{
		UUID:        "node-a",
		UpdatedAt:   base,
		CPU:         v2.CPUReport{Usage: 12.5},
		Ram:         v2.RamReport{Used: 100, Total: 1000},
		Swap:        v2.RamReport{Used: 20, Total: 200},
		Load:        v2.LoadReport{Load1: 0.5},
		Disk:        v2.DiskReport{Used: 300, Total: 3000},
		Network:     v2.NetworkReport{Up: 3, Down: 4, TotalUp: 100, TotalDown: 200},
		Process:     7,
		Connections: v2.ConnectionsReport{TCP: 8, UDP: 9},
		GPU: &v2.GPUDetailReport{
			AverageUsage: 25,
			DetailedInfo: []v2.GPUDeviceInfo{{
				Name: "GPU 0", MemoryUsed: 400, MemoryTotal: 800, Utilization: 30, Temperature: 55,
			}},
		},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Network.TotalUp = 150
	report.Network.TotalDown = 260
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write second report: %v", err)
	}

	report.UpdatedAt = base.Add(6 * time.Second)
	report.Network.TotalUp = 20
	report.Network.TotalDown = 30
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write reset report: %v", err)
	}

	assertMetricValues(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 50, 0})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 60, 0})
	assertMetricValues(t, s, MetricNetTotalUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{100, 150, 20})
	assertMetricAggregate(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggSum, 50, 3)
	assertMetricAggregate(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggSum, 60, 3)

	gpuPoints, err := s.Query(ctx, metric.Query{
		MetricName: MetricGPUDeviceUsage,
		EntityID:   report.UUID,
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Minute),
		Tags:       map[string]string{"device_index": "0"},
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query GPU points: %v", err)
	}
	if len(gpuPoints) != 3 || !gpuPoints[2].Timestamp.Equal(base.Add(6*time.Second)) || gpuPoints[2].Tags["device_name"] != "GPU 0" {
		t.Fatalf("unexpected GPU points: %#v", gpuPoints)
	}

	if _, err := s.Compact(ctx, now); err != nil {
		t.Fatalf("compact reports: %v", err)
	}
	deleteReportTrafficState(report.UUID)
	report.UpdatedAt = now
	report.Network.TotalUp = 35
	report.Network.TotalDown = 50
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write after restoring rollup baseline: %v", err)
	}
	assertMetricValues(t, s, MetricTrafficUp, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{15})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, now.Add(-time.Second), now.Add(time.Second), []float64{20})
}

func TestWriteReportSkipsMetricsWithoutAgentData(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	timestamp := time.Now().UTC()
	if _, err := WriteReport(ctx, v2.Report{
		UUID: "node-without-gpu", UpdatedAt: timestamp,
	}); err != nil {
		t.Fatalf("write report: %v", err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricGPU, EntityID: "node-without-gpu",
		Start: timestamp.Add(-time.Second), End: timestamp.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("query GPU metric: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("GPU metric was written without GPU data: %#v", points)
	}
}

func TestReportBatcherFlushesQueuedReports(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})

	base := time.Now().UTC().Truncate(time.Minute).Add(10 * time.Second)
	first := v2.Report{
		UUID:      "batched-node",
		UpdatedAt: base,
		CPU:       v2.CPUReport{Usage: 10},
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200},
	}
	second := first
	second.UpdatedAt = base.Add(3 * time.Second)
	second.CPU.Usage = 20
	second.Network.TotalUp = 150
	second.Network.TotalDown = 260

	if _, err := WriteReport(ctx, first); err != nil {
		t.Fatalf("queue first report: %v", err)
	}
	if _, err := WriteReport(ctx, second); err != nil {
		t.Fatalf("queue second report: %v", err)
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   first.UUID,
		Start:      base.Add(-time.Second),
		End:        base.Add(time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query before flush: %v", err)
	}
	if len(points) != 0 {
		t.Fatalf("queued reports were written before flush: %#v", points)
	}

	if err := FlushReportBatch(ctx); err != nil {
		t.Fatalf("flush report batch: %v", err)
	}
	assertMetricValues(t, s, MetricCPU, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{10, 20})
	assertMetricValues(t, s, MetricTrafficUp, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 50})
	assertMetricValues(t, s, MetricTrafficDown, first.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 60})
	assertMetricAggregate(t, s, MetricCPU, first.UUID, base.Add(-time.Second), base.Add(time.Minute), metric.AggAvg, 15, 2)
}

func TestPingBatcherFlushesLatencyAndLossTogether(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	StartReportBatcher()
	t.Cleanup(func() {
		if err := StopReportBatcher(ctx); err != nil {
			t.Errorf("stop report batcher: %v", err)
		}
	})

	base := time.Now().UTC().Truncate(time.Second)
	records := []models.PingRecord{
		{Client: "ping-node", TaskId: 7, Time: base, Value: 24},
		{Client: "ping-node", TaskId: 7, Time: base.Add(time.Minute), Value: -1},
		{Client: "ping-node", TaskId: 8, Time: base, Value: 31},
	}
	for _, record := range records {
		if err := WritePingRecord(ctx, record); err != nil {
			t.Fatalf("queue ping record: %v", err)
		}
	}

	for _, name := range []string{MetricPingLatency, MetricPingLoss} {
		points, err := s.Query(ctx, metric.Query{
			MetricName: name,
			EntityID:   "ping-node",
			Start:      base.Add(-time.Second),
			End:        base.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatalf("query queued %s points: %v", name, err)
		}
		if len(points) != 0 {
			t.Fatalf("queued %s points were written before flush: %#v", name, points)
		}
	}

	if err := FlushReportBatch(ctx); err != nil {
		t.Fatalf("flush ping batch: %v", err)
	}

	latency, err := s.Query(ctx, metric.Query{
		MetricName: MetricPingLatency,
		EntityID:   "ping-node",
		Tags:       map[string]string{"task_id": "7"},
		Start:      base.Add(-time.Second),
		End:        base.Add(2 * time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query flushed latency points: %v", err)
	}
	if len(latency) != 2 || latency[0].Value != 24 || latency[1].Value != -1 {
		t.Fatalf("latency points = %#v, want both original samples", latency)
	}

	loss, err := s.Query(ctx, metric.Query{
		MetricName: MetricPingLoss,
		EntityID:   "ping-node",
		Tags:       map[string]string{"task_id": "7"},
		Start:      base.Add(-time.Second),
		End:        base.Add(2 * time.Minute),
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query flushed loss points: %v", err)
	}
	if len(loss) != 2 || loss[0].Value != 0 || loss[1].Value != 1 {
		t.Fatalf("loss points = %#v, want success and loss samples", loss)
	}
}

func TestReportBatchKeepsEverySample(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Second)
	pending := []v2.Report{
		{UUID: "node-a", UpdatedAt: base, CPU: v2.CPUReport{Usage: 10}, Network: v2.NetworkReport{TotalUp: 100}},
		{UUID: "node-a", UpdatedAt: base, CPU: v2.CPUReport{Usage: 20}, Network: v2.NetworkReport{TotalUp: 150}},
		{UUID: "node-b", UpdatedAt: base, CPU: v2.CPUReport{Usage: 30}, Network: v2.NetworkReport{TotalUp: 200}},
		{UUID: "node-b", UpdatedAt: base.Add(time.Second), CPU: v2.CPUReport{Usage: 40}, Network: v2.NetworkReport{TotalUp: 260}},
	}

	if err := writePendingReports(ctx, &pending); err != nil {
		t.Fatalf("write report batch: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending reports = %d, want 0", len(pending))
	}
	assertMetricValues(t, s, MetricCPU, "node-a", base.Add(-time.Second), base.Add(time.Minute), []float64{10, 20})
	assertMetricValues(t, s, MetricCPU, "node-b", base.Add(-time.Second), base.Add(time.Minute), []float64{30, 40})
}

func TestReportQueueFullReturnsError(t *testing.T) {
	ctx := context.Background()
	useReportTestStore(t, nil)
	worker := &reportBatchWorker{
		queue:    make(chan v2.Report, 1),
		requests: make(chan reportBatchRequest, 1),
		done:     make(chan struct{}),
	}
	worker.queue <- v2.Report{UUID: "already-queued"}
	reportBatcherMu.Lock()
	reportBatcher = worker
	reportBatcherMu.Unlock()
	t.Cleanup(func() {
		reportBatcherMu.Lock()
		if reportBatcher == worker {
			reportBatcher = nil
		}
		reportBatcherMu.Unlock()
	})

	report := v2.Report{
		UUID:      "realtime-node",
		UpdatedAt: time.Now().UTC(),
	}
	_, err := WriteReport(ctx, report)
	if !errors.Is(err, ErrReportBatchQueueFull) {
		t.Fatalf("queue-full error = %v, want %v", err, ErrReportBatchQueueFull)
	}
}

func TestRecordReconstructionUsesMetricSpecificAggregation(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute)
	entityID := "node-aggregation"
	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 10},
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 30},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 100},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 200},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: base.Add(time.Second), Value: 10},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: base.Add(2 * time.Second), Value: 20},
	}
	if err := s.WriteBatch(ctx, points); err != nil {
		t.Fatalf("write points: %v", err)
	}

	records, err := GetRecordsByClientAndTime(ctx, entityID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("reconstruct records: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one bucket", records)
	}
	if records[0].Cpu != 20 || records[0].NetTotalUp != 200 || records[0].TrafficUp != 30 {
		t.Fatalf("unexpected aggregation result: %#v", records[0])
	}
}

func TestTrafficCounterDelta(t *testing.T) {
	tests := []struct {
		name     string
		current  int64
		previous int64
		want     int64
	}{
		{name: "previous zero", current: 120, previous: 0, want: 120},
		{name: "monotonic counter", current: 250, previous: 200, want: 50},
		{name: "unchanged counter", current: 100, previous: 100, want: 0},
		{name: "counter reset", current: 15, previous: 250, want: 15},
		{name: "negative current", current: -1, previous: 100, want: 0},
		{name: "negative previous", current: 15, previous: -1, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TrafficCounterDelta(test.current, test.previous); got != test.want {
				t.Fatalf("TrafficCounterDelta(%d, %d) = %d, want %d", test.current, test.previous, got, test.want)
			}
		})
	}
}

func TestWriteReportRebasesTrafficAfterAgentRestart(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	base := time.Now().UTC().Truncate(time.Minute).Add(5 * time.Second)
	report := v2.Report{
		UUID:      "restarted-node",
		UpdatedAt: base,
		Uptime:    1000,
		Network:   v2.NetworkReport{TotalUp: 100, TotalDown: 200},
	}
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write first report: %v", err)
	}

	report.UpdatedAt = base.Add(3 * time.Second)
	report.Uptime = 1003
	report.Network.TotalUp = 150
	report.Network.TotalDown = 260
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write continuous report: %v", err)
	}

	report.UpdatedAt = base.Add(6 * time.Second)
	report.Uptime = 1
	report.Network.TotalUp = 155
	report.Network.TotalDown = 265
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write report after agent restart: %v", err)
	}

	report.UpdatedAt = base.Add(9 * time.Second)
	report.Uptime = 4
	report.Network.TotalUp = 180
	report.Network.TotalDown = 300
	if _, err := WriteReport(ctx, report); err != nil {
		t.Fatalf("write report after new baseline: %v", err)
	}

	assertMetricValues(t, s, MetricTrafficUp, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 50, 0, 25})
	assertMetricValues(t, s, MetricTrafficDown, report.UUID, base.Add(-time.Second), base.Add(time.Minute), []float64{0, 60, 0, 35})
}

func TestWriteReportNormalizesReceiveTimeToUTC(t *testing.T) {
	ctx := context.Background()
	s := useReportTestStore(t, nil)
	local := time.FixedZone("UTC+8", 8*60*60)
	receiveTime := time.Now().In(local).Add(-10 * time.Second)
	report := v2.Report{
		UUID:      "utc-report",
		UpdatedAt: receiveTime,
		CPU:       v2.CPUReport{Usage: 10},
		Network:   v2.NetworkReport{TotalUp: 1, TotalDown: 2},
	}

	saved, err := WriteReport(ctx, report)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	if !saved.UpdatedAt.Equal(receiveTime) || saved.UpdatedAt.Location() != time.UTC {
		t.Fatalf("saved receive time = %s (%s), want UTC", saved.UpdatedAt, saved.UpdatedAt.Location())
	}
	points, err := s.Query(ctx, metric.Query{
		MetricName: MetricCPU,
		EntityID:   report.UUID,
		Start:      receiveTime.Add(-time.Nanosecond),
		End:        receiveTime.Add(time.Nanosecond),
	})
	if err != nil {
		t.Fatalf("query stored point: %v", err)
	}
	if len(points) != 1 || points[0].Timestamp.Location() != time.UTC || points[0].Timestamp.UnixMilli() != receiveTime.UnixMilli() {
		t.Fatalf("stored points = %#v, want one UTC millisecond point", points)
	}
}

func assertMetricValues(t *testing.T, s *metric.Store, metricName, entityID string, start, end time.Time, want []float64) {
	t.Helper()
	points, err := s.Query(context.Background(), metric.Query{
		MetricName: metricName,
		EntityID:   entityID,
		Start:      start,
		End:        end,
		Order:      metric.OrderAsc,
	})
	if err != nil {
		t.Fatalf("query %s: %v", metricName, err)
	}
	if len(points) != len(want) {
		t.Fatalf("%s point count = %d, want %d: %#v", metricName, len(points), len(want), points)
	}
	for i := range want {
		if points[i].Value != want[i] {
			t.Fatalf("%s point %d = %v, want %v", metricName, i, points[i].Value, want[i])
		}
	}
}

func assertMetricAggregate(t *testing.T, s *metric.Store, metricName, entityID string, start, end time.Time, aggregation metric.Aggregation, want float64, wantCount int) {
	t.Helper()
	points, err := s.Series(context.Background(), metric.AggregateQuery{
		Query:       metric.Query{MetricName: metricName, EntityID: entityID, Start: start, End: end},
		Aggregation: aggregation, Interval: time.Minute, PreserveSeries: true,
	}, end)
	if err != nil {
		t.Fatalf("aggregate %s: %v", metricName, err)
	}
	if len(points) != 1 || points[0].Value != want || points[0].Count != wantCount {
		t.Fatalf("aggregate %s = %#v, want value=%v count=%d", metricName, points, want, wantCount)
	}
}
