package metricstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
	v2 "github.com/komari-monitor/komari/protocol/v2"
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
	hasUptime   bool
	uptime      int64
}

var reportTrafficStates sync.Map

const (
	reportBatchInterval     = 3 * time.Second
	reportBatchQueueSize    = 4096
	pingBatchMaxRecords     = 512
	reportBatchWriteTimeout = 10 * time.Second
)

var (
	reportBatcherMu sync.Mutex
	reportBatcher   *reportBatchWorker
)

var (
	ErrReportBatchQueueFull = errors.New("metric report batch queue is full")
	ErrReportBatchStopped   = errors.New("metric report batcher is stopped")
	ErrPingBatchQueueFull   = errors.New("ping batch queue is full")
)

type reportBatchRequest struct {
	ctx  context.Context
	done chan error
	stop bool
}

type reportBatchWorker struct {
	mu        sync.Mutex
	queue     chan v2.Report
	pingQueue chan models.PingRecord
	requests  chan reportBatchRequest
	done      chan struct{}
	stopping  bool
}

// StartReportBatcher starts the shared report writer. Exact samples are kept in
// the short raw window while the same writes update in-memory minute buckets.
func StartReportBatcher() {
	reportBatcherMu.Lock()
	defer reportBatcherMu.Unlock()
	if reportBatcher != nil {
		return
	}
	worker := &reportBatchWorker{
		queue:     make(chan v2.Report, reportBatchQueueSize),
		pingQueue: make(chan models.PingRecord, reportBatchQueueSize),
		requests:  make(chan reportBatchRequest, 1),
		done:      make(chan struct{}),
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

// WriteReport persists one agent report and adds it to in-memory minute
// summaries using the same server receive time. Traffic deltas remain summable
// after rollup.
func WriteReport(ctx context.Context, report v2.Report) (v2.Report, error) {
	if report.UUID == "" {
		return v2.Report{}, fmt.Errorf("report UUID is required")
	}
	if report.UpdatedAt.IsZero() {
		return v2.Report{}, fmt.Errorf("report receive time is required")
	}
	report.UpdatedAt = report.UpdatedAt.UTC()
	if GetStore() == nil {
		return v2.Report{}, fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		if err := worker.enqueue(ctx, report); err != nil {
			return v2.Report{}, err
		}
		return report, nil
	}

	saved, err := writeReportBatch(ctx, []v2.Report{report})
	if err != nil {
		return v2.Report{}, err
	}
	return saved[0], nil
}

func (w *reportBatchWorker) enqueue(ctx context.Context, report v2.Report) error {
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

	var pending []v2.Report
	var pendingPings []models.PingRecord
	for {
		select {
		case request := <-w.requests:
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			pendingPings = append(pendingPings, drainPingQueue(w.pingQueue, reportBatchQueueSize)...)
			err := errors.Join(
				writePendingReports(request.ctx, &pending),
				writePendingPingRecords(request.ctx, &pendingPings),
			)
			if request.stop {
				if err != nil {
					logger.Errorf("metricstore", "failed to flush metric report batch during shutdown: %v", err)
				}
				close(w.done)
				request.done <- err
				return
			}
			request.done <- err
		case <-ticker.C:
			pendingPings = append(pendingPings, drainPingQueue(w.pingQueue, reportBatchQueueSize)...)
			if err := writePendingPingRecords(context.Background(), &pendingPings); err != nil {
				logger.Errorf("metricstore", "failed to flush ping batch: %v", err)
			}
			pending = append(pending, drainReportQueue(w.queue, reportBatchQueueSize)...)
			if err := writePendingReports(context.Background(), &pending); err != nil {
				logger.Errorf("metricstore", "failed to flush metric report batch: %v", err)
			}
		}
	}
}

func (w *reportBatchWorker) enqueuePing(ctx context.Context, record models.PingRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return ErrReportBatchStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case w.pingQueue <- record:
		return nil
	default:
		return ErrPingBatchQueueFull
	}
}

func drainReportQueue(queue <-chan v2.Report, limit int) []v2.Report {
	if limit <= 0 {
		return nil
	}
	capacity := len(queue)
	if capacity > limit {
		capacity = limit
	}
	if capacity == 0 {
		return nil
	}
	reports := make([]v2.Report, 0, capacity)
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

func drainPingQueue(queue <-chan models.PingRecord, limit int) []models.PingRecord {
	if limit <= 0 {
		return nil
	}
	capacity := len(queue)
	if capacity > limit {
		capacity = limit
	}
	if capacity == 0 {
		return nil
	}
	records := make([]models.PingRecord, 0, capacity)
	for len(records) < limit {
		select {
		case record := <-queue:
			records = append(records, record)
		default:
			return records
		}
	}
	return records
}

func writePendingReports(ctx context.Context, pending *[]v2.Report) error {
	if len(*pending) == 0 {
		return nil
	}
	batchSize := len(*pending)
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
	}
	return nil
}

func writePendingPingRecords(ctx context.Context, pending *[]models.PingRecord) error {
	for len(*pending) > 0 {
		batchSize := len(*pending)
		if batchSize > pingBatchMaxRecords {
			batchSize = pingBatchMaxRecords
		}
		writeCtx, cancel := context.WithTimeout(ctx, reportBatchWriteTimeout)
		err := writePingRecords(writeCtx, (*pending)[:batchSize])
		cancel()
		if err != nil {
			return err
		}
		*pending = (*pending)[batchSize:]
	}
	return nil
}

func writeReportBatch(ctx context.Context, reports []v2.Report) ([]v2.Report, error) {
	if len(reports) == 0 {
		return nil, nil
	}
	if err := storeOperations.AcquireShared(ctx); err != nil {
		return nil, fmt.Errorf("wait for metric store operation before writing reports: %w", err)
	}
	defer storeOperations.ReleaseShared()

	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	prepared := make([]v2.Report, len(reports))
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
			report.UpdatedAt = values.timestamp.Add(time.Millisecond)
		}
		agentRestart := values.hasUptime && report.Uptime < values.uptime
		counterResetUp := values.hasUp && report.Network.TotalUp < values.totalUp
		counterResetDown := values.hasDown && report.Network.TotalDown < values.totalDown
		trafficUp := int64(0)
		if values.hasUp && !agentRestart && !counterResetUp {
			trafficUp = TrafficCounterDelta(report.Network.TotalUp, values.totalUp)
		}
		trafficDown := int64(0)
		if values.hasDown && !agentRestart && !counterResetDown {
			trafficDown = TrafficCounterDelta(report.Network.TotalDown, values.totalDown)
		}
		points = append(points, reportMetricPoints(report, trafficUp, trafficDown)...)
		values.timestamp = report.UpdatedAt
		values.hasUp = true
		values.totalUp = report.Network.TotalUp
		values.hasDown = true
		values.totalDown = report.Network.TotalDown
		values.hasUptime = true
		values.uptime = report.Uptime
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
