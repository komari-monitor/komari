package metricstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v1 "github.com/komari-monitor/komari/protocol/v1"
)

type reportTrafficState struct {
	mu sync.Mutex

	initialized bool
	timestamp   time.Time
	hasUp       bool
	totalUp     int64
	hasDown     bool
	totalDown   int64
}

var reportTrafficStates sync.Map

// WriteReport persists one agent report as raw metric points sharing the same
// server receive time. Traffic deltas are derived from the previous counters
// and stored alongside the raw counters so they remain summable after rollup.
func WriteReport(ctx context.Context, report v1.Report) (v1.Report, error) {
	s := GetStore()
	if s == nil {
		return v1.Report{}, fmt.Errorf("metric store not enabled")
	}
	if report.UUID == "" {
		return v1.Report{}, fmt.Errorf("report UUID is required")
	}
	if report.UpdatedAt.IsZero() {
		return v1.Report{}, fmt.Errorf("report receive time is required")
	}

	stateValue, _ := reportTrafficStates.LoadOrStore(report.UUID, &reportTrafficState{})
	state := stateValue.(*reportTrafficState)
	state.mu.Lock()
	defer state.mu.Unlock()

	report.UpdatedAt = report.UpdatedAt.UTC()
	if !state.timestamp.IsZero() && !report.UpdatedAt.After(state.timestamp) {
		report.UpdatedAt = state.timestamp.Add(time.Nanosecond)
	}
	if !state.initialized {
		var err error
		state.totalUp, state.hasUp, err = latestReportCounter(ctx, s, MetricNetTotalUp, report.UUID, report.UpdatedAt)
		if err != nil {
			return v1.Report{}, fmt.Errorf("load previous upload counter: %w", err)
		}
		state.totalDown, state.hasDown, err = latestReportCounter(ctx, s, MetricNetTotalDown, report.UUID, report.UpdatedAt)
		if err != nil {
			return v1.Report{}, fmt.Errorf("load previous download counter: %w", err)
		}
		state.initialized = true
	}

	trafficUp := int64(0)
	if state.hasUp {
		trafficUp = TrafficCounterDelta(report.Network.TotalUp, state.totalUp)
	}
	trafficDown := int64(0)
	if state.hasDown {
		trafficDown = TrafficCounterDelta(report.Network.TotalDown, state.totalDown)
	}

	points := reportMetricPoints(report, trafficUp, trafficDown)
	if err := s.WriteBatch(ctx, points); err != nil {
		return v1.Report{}, err
	}

	state.timestamp = report.UpdatedAt
	state.hasUp = true
	state.totalUp = report.Network.TotalUp
	state.hasDown = true
	state.totalDown = report.Network.TotalDown
	return report, nil
}

func reportMetricPoints(report v1.Report, trafficUp, trafficDown int64) []metric.Point {
	entityID := report.UUID
	ts := report.UpdatedAt
	gpuUsage := 0.0
	if report.GPU != nil {
		gpuUsage = report.GPU.AverageUsage
	}
	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: report.CPU.Usage},
		{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: gpuUsage},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(report.Ram.Used)},
		{MetricName: MetricRAMTotal, EntityID: entityID, Timestamp: ts, Value: float64(report.Ram.Total)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(report.Swap.Used)},
		{MetricName: MetricSwapTotal, EntityID: entityID, Timestamp: ts, Value: float64(report.Swap.Total)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: report.Load.Load1},
		{MetricName: MetricTemp, EntityID: entityID, Timestamp: ts, Value: 0},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(report.Disk.Used)},
		{MetricName: MetricDiskTotal, EntityID: entityID, Timestamp: ts, Value: float64(report.Disk.Total)},
		{MetricName: MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.Down)},
		{MetricName: MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.Up)},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.TotalUp)},
		{MetricName: MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(report.Network.TotalDown)},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(trafficUp)},
		{MetricName: MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(trafficDown)},
		{MetricName: MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(report.Process)},
		{MetricName: MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(report.Connections.TCP)},
		{MetricName: MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(report.Connections.UDP)},
	}
	if report.GPU == nil {
		return points
	}
	for deviceIndex, gpu := range report.GPU.DetailedInfo {
		tags := map[string]string{
			"device_index": strconv.Itoa(deviceIndex),
			"device_name":  gpu.Name,
		}
		points = append(points,
			metric.Point{MetricName: MetricGPUMem, EntityID: entityID, Timestamp: ts, Value: float64(gpu.MemoryUsed), Tags: tags},
			metric.Point{MetricName: MetricGPUMemTotal, EntityID: entityID, Timestamp: ts, Value: float64(gpu.MemoryTotal), Tags: tags},
			metric.Point{MetricName: MetricGPUDeviceUsage, EntityID: entityID, Timestamp: ts, Value: gpu.Utilization, Tags: tags},
			metric.Point{MetricName: MetricGPUTemp, EntityID: entityID, Timestamp: ts, Value: float64(gpu.Temperature), Tags: tags},
		)
	}
	return points
}

func latestReportCounter(ctx context.Context, s *metric.Store, metricName, entityID string, before time.Time) (int64, bool, error) {
	def, err := s.GetMetric(ctx, metricName)
	if errors.Is(err, metric.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if def.RetentionDays <= 0 {
		return 0, false, nil
	}

	end := before.UTC().Add(-time.Nanosecond)
	start := end.Add(-time.Duration(def.RetentionDays) * 24 * time.Hour)
	interval := s.CompatibleSeriesInterval(start, before, 24*time.Hour)
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: metricName,
			EntityID:   entityID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		},
		Aggregation:    metric.AggLast,
		Interval:       interval,
		PreserveSeries: true,
	}, before)
	if err != nil {
		return 0, false, err
	}
	if len(points) == 0 {
		return 0, false, nil
	}
	return int64(points[len(points)-1].Value), true, nil
}

// GetLatestTrafficBefore returns the latest retained upload/download counters
// before a boundary, transparently reading raw points or rollups.
func GetLatestTrafficBefore(ctx context.Context, entityIDs []string, before time.Time) (map[string]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}
	result := make(map[string]models.Record, len(entityIDs))
	for _, entityID := range entityIDs {
		if entityID == "" {
			continue
		}
		up, hasUp, err := latestReportCounter(ctx, s, MetricNetTotalUp, entityID, before)
		if err != nil {
			return nil, err
		}
		down, hasDown, err := latestReportCounter(ctx, s, MetricNetTotalDown, entityID, before)
		if err != nil {
			return nil, err
		}
		if !hasUp && !hasDown {
			continue
		}
		result[entityID] = models.Record{
			Client:       entityID,
			Time:         models.FromTime(before.UTC().Add(-time.Nanosecond)),
			NetTotalUp:   up,
			NetTotalDown: down,
		}
	}
	return result, nil
}

// TrafficCounterDelta returns a reset-aware increase between two cumulative
// traffic counters. After a reset, the current counter is the new increase.
func TrafficCounterDelta(current, previous int64) int64 {
	if current < 0 || previous < 0 {
		return 0
	}
	if current >= previous {
		return current - previous
	}
	return current
}

func deleteReportTrafficState(entityID string) {
	reportTrafficStates.Delete(entityID)
}

func clearReportTrafficStates() {
	reportTrafficStates.Range(func(key, _ any) bool {
		reportTrafficStates.Delete(key)
		return true
	})
}
