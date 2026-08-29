package metricstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func reportMetricPoints(report v2.Report, trafficUp, trafficDown int64) []metric.Point {
	entityID := report.UUID
	ts := report.UpdatedAt
	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: report.CPU.Usage},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(report.Ram.Used)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(report.Swap.Used)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: report.Load.Load1},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(report.Disk.Used)},
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
	points = append(points, metric.Point{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: report.GPU.AverageUsage})
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
	point, ok, err := s.LatestBefore(ctx, metricName, entityID, before)
	if err != nil {
		return 0, false, err
	}
	if !ok {
		return 0, false, nil
	}
	return int64(point.Value), true, nil
}

// GetLatestTrafficBefore returns the latest retained upload/download counters
// before a boundary, transparently reading raw points or rollup summaries.
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
			Time:         before.UTC().Add(-time.Nanosecond),
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
