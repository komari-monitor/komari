package metricstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
)

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		return worker.enqueuePing(ctx, rec)
	}
	return writePingRecords(ctx, []models.PingRecord{rec})
}

func writePingRecords(ctx context.Context, records []models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if len(records) == 0 {
		return nil
	}

	points := make([]metric.Point, 0, len(records)*2)
	for _, rec := range records {
		tags := map[string]string{"task_id": fmt.Sprintf("%d", rec.TaskId)}
		loss := 0.0
		if rec.Value < 0 {
			loss = 1
		}
		points = append(points,
			metric.Point{
				MetricName: MetricPingLatency,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      float64(rec.Value),
				Tags:       tags,
			},
			metric.Point{
				MetricName: MetricPingLoss,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      loss,
				Tags:       tags,
			},
		)
	}
	return s.WriteBatch(ctx, points)
}

func GetPingRecords(ctx context.Context, clientUUID string, taskID int, start, end time.Time) ([]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	query := metric.Query{
		MetricName: MetricPingLatency,
		Start:      start,
		End:        end,
		Order:      metric.OrderAsc,
	}

	if clientUUID != "" {
		query.EntityID = clientUUID
	}

	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	interval := pingQueryInterval(end.Sub(start), 4000)
	interval = s.CompatibleSeriesInterval(start, time.Now().UTC(), interval)
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query:          query,
		Aggregation:    metric.AggLast,
		Interval:       interval,
		PreserveSeries: true,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	records := make([]models.PingRecord, 0, len(points))
	for _, p := range points {
		taskIDVal := uint(0)
		if tid, ok := p.Tags["task_id"]; ok {
			var t uint64
			fmt.Sscanf(tid, "%d", &t)
			taskIDVal = uint(t)
		}

		records = append(records, models.PingRecord{
			Client: p.EntityID,
			TaskId: taskIDVal,
			Time:   p.Bucket.UTC(),
			Value:  int(p.Value),
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Time.After(records[j].Time)
	})

	return records, nil
}

func pingQueryInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 4000
	}
	if rangeDuration <= 0 {
		return time.Second
	}
	interval := time.Duration((rangeDuration.Nanoseconds() + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.FloorStandardInterval(interval)
}
