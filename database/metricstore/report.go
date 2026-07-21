package metricstore

import (
	"context"
	"errors"
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v1 "github.com/komari-monitor/komari/protocol/v1"
)

type reportTrafficState struct {
	mu sync.Mutex

	reportTrafficValues
}

type reportTrafficValues struct {
	initialized bool
	timestamp   time.Time
	hasUp       bool
	totalUp     int64
	hasDown     bool
	totalDown   int64
}

var reportTrafficStates sync.Map

const (
	reportBatchInterval        = 3 * time.Second
	lowResourceReportWindow    = 30 * time.Second
	reportBatchQueueSize       = 4096
	lowResourceBatchMaxReports = 128
	reportBatchWriteTimeout    = 10 * time.Second
)

var (
	reportBatcherMu sync.Mutex
	reportBatcher   *reportBatchWorker
	lowResourceMode atomic.Bool
	droppedReports  atomic.Uint64
)

var (
	ErrReportBatchQueueFull = errors.New("metric report batch queue is full")
	ErrReportBatchStopped   = errors.New("metric report batcher is stopped")
)

type reportBatchRequest struct {
	ctx  context.Context
	done chan error
	stop bool
}

type reportBatchWorker struct {
	mu       sync.Mutex
	queue    chan v1.Report
	requests chan reportBatchRequest
	done     chan struct{}
	stopping bool
}

func setLowResourceMode(enabled bool) {
	lowResourceMode.Store(enabled)
}

func LowResourceModeEnabled() bool {
	return lowResourceMode.Load()
}

// StartReportBatcher starts the shared report writer. Normal mode uses a short
// window; low-resource mode coalesces each node's 30-second window before write.
func StartReportBatcher() {
	reportBatcherMu.Lock()
	defer reportBatcherMu.Unlock()
	if reportBatcher != nil {
		return
	}
	worker := &reportBatchWorker{
		queue:    make(chan v1.Report, reportBatchQueueSize),
		requests: make(chan reportBatchRequest, 1),
		done:     make(chan struct{}),
	}
	reportBatcher = worker
	go worker.run()
}

// StopReportBatcher stops the report writer after flushing all queued reports.
func StopReportBatcher(ctx context.Context) error {
	reportBatcherMu.Lock()
	worker := reportBatcher
	if worker == nil {
		reportBatcherMu.Unlock()
		return nil
	}
	worker.mu.Lock()
	worker.stopping = true
	worker.mu.Unlock()
	reportBatcherMu.Unlock()

	request := reportBatchRequest{ctx: ctx, done: make(chan error, 1), stop: true}
	select {
	case worker.requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		<-worker.done
		reportBatcherMu.Lock()
		if reportBatcher == worker {
			reportBatcher = nil
		}
		reportBatcherMu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FlushReportBatch synchronously flushes the current queue. It is useful for
// controlled handoff points and deterministic tests; normal operation uses the
// worker ticker and the active mode's flush interval.
func FlushReportBatch(ctx context.Context) error {
	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker == nil {
		return nil
	}
	request := reportBatchRequest{ctx: ctx, done: make(chan error, 1)}
	worker.mu.Lock()
	stopping := worker.stopping
	worker.mu.Unlock()
	if stopping {
		return ErrReportBatchStopped
	}
	select {
	case worker.requests <- request:
	case <-worker.done:
		return ErrReportBatchStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WriteReport persists one agent report as raw metric points sharing the same
// server receive time. Traffic deltas are derived from the previous counters
// and stored alongside the raw counters so they remain summable after rollup.
func WriteReport(ctx context.Context, report v1.Report) (v1.Report, error) {
	if report.UUID == "" {
		return v1.Report{}, fmt.Errorf("report UUID is required")
	}
	if report.UpdatedAt.IsZero() {
		return v1.Report{}, fmt.Errorf("report receive time is required")
	}
	report.UpdatedAt = report.UpdatedAt.UTC()
	if GetStore() == nil {
		return v1.Report{}, fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		if err := worker.enqueue(ctx, report); err != nil {
			if LowResourceModeEnabled() && errors.Is(err, ErrReportBatchQueueFull) {
				droppedReports.Add(1)
				return report, nil
			}
			return v1.Report{}, err
		}
		return report, nil
	}

	saved, err := writeReportBatch(ctx, []v1.Report{report})
	if err != nil {
		return v1.Report{}, err
	}
	return saved[0], nil
}

func (w *reportBatchWorker) enqueue(ctx context.Context, report v1.Report) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return ErrReportBatchStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case w.queue <- report:
		return nil
	default:
		return ErrReportBatchQueueFull
	}
}

func (w *reportBatchWorker) run() {
	ticker := time.NewTicker(reportBatchInterval)
	defer ticker.Stop()

	var pending []v1.Report
	lastFlush := time.Now()
	for {
		select {
		case request := <-w.requests:
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			err := writePendingReports(request.ctx, &pending, LowResourceModeEnabled())
			if request.stop {
				if err != nil {
					logger.Errorf("metricstore", "failed to flush metric report batch during shutdown: %v", err)
				}
				close(w.done)
				request.done <- err
				return
			}
			if err == nil {
				lastFlush = time.Now()
			}
			request.done <- err
		case <-ticker.C:
			lowResource := LowResourceModeEnabled()
			if lowResource && time.Since(lastFlush) < lowResourceReportWindow {
				continue
			}
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			if err := writePendingReports(context.Background(), &pending, lowResource); err != nil {
				logger.Errorf("metricstore", "failed to flush metric report batch: %v", err)
			} else {
				lastFlush = time.Now()
				if dropped := droppedReports.Swap(0); dropped > 0 {
					logger.Warnf("metricstore", "low resource metric batching dropped %d reports after the queue filled", dropped)
				}
			}
		}
	}
}

func drainReportQueue(queue <-chan v1.Report, limit int) []v1.Report {
	if limit <= 0 {
		return nil
	}
	reports := make([]v1.Report, 0, limit)
	for len(reports) < limit {
		select {
		case report := <-queue:
			reports = append(reports, report)
		default:
			return reports
		}
	}
	return reports
}

func writePendingReports(ctx context.Context, pending *[]v1.Report, lowResource bool) error {
	if len(*pending) == 0 {
		return nil
	}
	if lowResource {
		*pending = coalesceReportsP95(*pending)
	}
	batchSize := len(*pending)
	if lowResource && batchSize > lowResourceBatchMaxReports {
		batchSize = lowResourceBatchMaxReports
	}
	for len(*pending) > 0 {
		if batchSize > len(*pending) {
			batchSize = len(*pending)
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		_, err := writeReportBatch(writeCtx, (*pending)[:batchSize])
		cancel()
		if err != nil {
			return err
		}
		*pending = (*pending)[batchSize:]
		if lowResource {
			runtime.Gosched()
		}
	}
	return nil
}

type reportNumber interface {
	~int | ~int64 | ~float64
}

func percentile95[T reportNumber](values []T) T {
	if len(values) == 0 {
		var zero T
		return zero
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := (95*len(values) + 99) / 100
	return values[index-1]
}

func reportP95[T reportNumber](reports []v1.Report, value func(v1.Report) T) T {
	values := make([]T, len(reports))
	for i, report := range reports {
		values[i] = value(report)
	}
	return percentile95(values)
}

func coalesceReportsP95(reports []v1.Report) []v1.Report {
	grouped := make(map[string][]v1.Report)
	order := make([]string, 0)
	for _, report := range reports {
		if _, exists := grouped[report.UUID]; !exists {
			order = append(order, report.UUID)
		}
		grouped[report.UUID] = append(grouped[report.UUID], report)
	}
	coalesced := make([]v1.Report, 0, len(order))
	for _, uuid := range order {
		coalesced = append(coalesced, coalesceNodeReportsP95(grouped[uuid]))
	}
	return coalesced
}

func coalesceNodeReportsP95(reports []v1.Report) v1.Report {
	latestIndex := 0
	for i := 1; i < len(reports); i++ {
		if reports[i].UpdatedAt.After(reports[latestIndex].UpdatedAt) {
			latestIndex = i
		}
	}
	result := reports[latestIndex]
	result.CPU.Usage = reportP95(reports, func(r v1.Report) float64 { return r.CPU.Usage })
	result.Ram.Used = reportP95(reports, func(r v1.Report) int64 { return r.Ram.Used })
	result.Swap.Used = reportP95(reports, func(r v1.Report) int64 { return r.Swap.Used })
	result.Load.Load1 = reportP95(reports, func(r v1.Report) float64 { return r.Load.Load1 })
	result.Disk.Used = reportP95(reports, func(r v1.Report) int64 { return r.Disk.Used })
	result.Network.Up = reportP95(reports, func(r v1.Report) int64 { return r.Network.Up })
	result.Network.Down = reportP95(reports, func(r v1.Report) int64 { return r.Network.Down })
	result.Process = reportP95(reports, func(r v1.Report) int { return r.Process })
	result.Connections.TCP = reportP95(reports, func(r v1.Report) int { return r.Connections.TCP })
	result.Connections.UDP = reportP95(reports, func(r v1.Report) int { return r.Connections.UDP })

	latestGPU := -1
	for i := range reports {
		if reports[i].GPU != nil && (latestGPU < 0 || reports[i].UpdatedAt.After(reports[latestGPU].UpdatedAt)) {
			latestGPU = i
		}
	}
	if latestGPU < 0 {
		result.GPU = nil
		return result
	}
	gpu := *reports[latestGPU].GPU
	gpu.DetailedInfo = append([]v1.GPUDeviceInfo(nil), gpu.DetailedInfo...)
	gpuReports := make([]v1.Report, 0, len(reports))
	for _, report := range reports {
		if report.GPU != nil {
			gpuReports = append(gpuReports, report)
		}
	}
	gpu.AverageUsage = reportP95(gpuReports, func(r v1.Report) float64 { return r.GPU.AverageUsage })
	for deviceIndex := range gpu.DetailedInfo {
		deviceReports := make([]v1.GPUDeviceInfo, 0, len(gpuReports))
		for _, report := range gpuReports {
			if deviceIndex < len(report.GPU.DetailedInfo) {
				deviceReports = append(deviceReports, report.GPU.DetailedInfo[deviceIndex])
			}
		}
		if len(deviceReports) == 0 {
			continue
		}
		memoryUsed := make([]int64, len(deviceReports))
		utilization := make([]float64, len(deviceReports))
		temperature := make([]int, len(deviceReports))
		for i, device := range deviceReports {
			memoryUsed[i] = device.MemoryUsed
			utilization[i] = device.Utilization
			temperature[i] = device.Temperature
		}
		gpu.DetailedInfo[deviceIndex].MemoryUsed = percentile95(memoryUsed)
		gpu.DetailedInfo[deviceIndex].Utilization = percentile95(utilization)
		gpu.DetailedInfo[deviceIndex].Temperature = percentile95(temperature)
	}
	result.GPU = &gpu
	return result
}

func writeReportBatch(ctx context.Context, reports []v1.Report) ([]v1.Report, error) {
	if len(reports) == 0 {
		return nil, nil
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		return nil, fmt.Errorf("wait for metric store operation before writing reports: %w", err)
	}
	defer storeOperations.Release()

	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	prepared := make([]v1.Report, len(reports))
	copy(prepared, reports)
	points := make([]metric.Point, 0, len(reports)*20)
	pendingStates := make(map[*reportTrafficState]reportTrafficValues)
	for i, report := range prepared {
		stateValue, _ := reportTrafficStates.LoadOrStore(report.UUID, &reportTrafficState{})
		state := stateValue.(*reportTrafficState)
		values, ok := pendingStates[state]
		if !ok {
			state.mu.Lock()
			values = state.reportTrafficValues
			state.mu.Unlock()
		}
		if !values.initialized {
			totalUp, hasUp, err := latestReportCounter(ctx, s, MetricNetTotalUp, report.UUID, report.UpdatedAt)
			if err == nil {
				values.totalUp = totalUp
				values.hasUp = hasUp
			} else if ctx.Err() == nil {
				logger.Errorf("metricstore", "failed to restore previous upload counter for %s: %v", report.UUID, err)
			}
			totalDown, hasDown, err := latestReportCounter(ctx, s, MetricNetTotalDown, report.UUID, report.UpdatedAt)
			if err == nil {
				values.totalDown = totalDown
				values.hasDown = hasDown
			} else if ctx.Err() == nil {
				logger.Errorf("metricstore", "failed to restore previous download counter for %s: %v", report.UUID, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			values.initialized = true
		}

		if !values.timestamp.IsZero() && !report.UpdatedAt.After(values.timestamp) {
			report.UpdatedAt = values.timestamp.Add(time.Nanosecond)
		}
		trafficUp := int64(0)
		if values.hasUp {
			trafficUp = TrafficCounterDelta(report.Network.TotalUp, values.totalUp)
		}
		trafficDown := int64(0)
		if values.hasDown {
			trafficDown = TrafficCounterDelta(report.Network.TotalDown, values.totalDown)
		}
		points = append(points, reportMetricPoints(report, trafficUp, trafficDown)...)
		values.timestamp = report.UpdatedAt
		values.hasUp = true
		values.totalUp = report.Network.TotalUp
		values.hasDown = true
		values.totalDown = report.Network.TotalDown
		pendingStates[state] = values
		prepared[i] = report
	}

	if err := s.WriteBatch(ctx, points); err != nil {
		return nil, err
	}
	for state, values := range pendingStates {
		state.mu.Lock()
		state.reportTrafficValues = values
		state.mu.Unlock()
	}
	return prepared, nil
}

func reportMetricPoints(report v1.Report, trafficUp, trafficDown int64) []metric.Point {
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
