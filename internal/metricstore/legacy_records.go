package metricstore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
)

// RecordQueryOptions narrows the record reconstruction work. MetricNames
// limits which stored load metrics are queried; empty means all load metrics.
// MaxPoints is a total response-point budget; it is applied before aggregation,
// so an all-client query does not materialize thousands of points per client
// only to sample them afterwards.
type RecordQueryOptions struct {
	MetricNames []string
	MaxPoints   int
}

// GetRecordsByClientAndTime 从 metric store 查询记录并重构为 models.Record。
// opts 为空表示兼容旧行为；有值时用于指标选择和点数预算。
func GetRecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time, opts ...RecordQueryOptions) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	return getRecordsFromSeries(ctx, s, clientUUID, start, end, false, firstRecordOptions(opts))
}

func firstRecordOptions(opts []RecordQueryOptions) RecordQueryOptions {
	if len(opts) == 0 {
		return RecordQueryOptions{}
	}
	return opts[0]
}

// GetRecordMetricMaxByClientAndTime 查询单项监控指标在各时间桶内的最大值。
func GetRecordMetricMaxByClientAndTime(ctx context.Context, clientUUID, recordMetric string, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	return getRecordMetricMaxByClientAndTimeFromSeries(ctx, s, clientUUID, recordMetric, start, end)
}

// GetRecordsByTime 从 metric store 查询所有客户端在时间范围内的记录。
// opts 为空表示兼容旧行为；有值时用于指标选择和总点数预算。
func GetRecordsByTime(ctx context.Context, start, end time.Time, opts ...RecordQueryOptions) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// Query all entities together. Entity-only grouping retains the old per-client
	// aggregation across tags without repeating the tier scan for every client.
	return getRecordsFromSeries(ctx, s, "", start, end, true, firstRecordOptions(opts))
}

type recordSeriesKey struct {
	client string
	ts     int64
}

func getRecordsFromSeries(ctx context.Context, s *metric.Store, clientUUID string, start, end time.Time, allEntities bool, opts RecordQueryOptions) ([]models.Record, error) {
	metricNames := opts.MetricNames
	if len(metricNames) == 0 {
		metricNames = loadRecordMetricNames
	}
	specs := make([]MetricSpec, 0, len(metricNames))
	for _, name := range metricNames {
		specs = append(specs, MetricSpec{Name: name, Aggregations: []metric.Aggregation{recordMetricAggregation(name)}})
	}
	entities := EntityScope{All: allEntities}
	grouping := GroupMerged
	if allEntities {
		grouping = GroupEntity
	} else {
		entities.IDs = []string{clientUUID}
	}
	rounding := RoundFloor
	if opts.MaxPoints > 0 {
		rounding = RoundCeil
	}
	loaded, err := QueryMetrics(ctx, MetricsQuery{
		Store: s, Mode: QuerySeries, Source: SourceRollup,
		OmitTags: true, OmitLabels: true, CompactDefinitions: true,
		Entities: entities, Specs: specs, Start: start, End: end,
		Order: metric.OrderAsc, Grouping: grouping,
		Budget:   PointBudget{PerSeries: 500, Total: opts.MaxPoints},
		Rounding: rounding, AllowMissing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query metrics: %w", err)
	}
	return assembleRecordsFromSeries(loaded.Values, clientUUID), nil
}

// assembleRecordsFromSeries unions metric buckets without allocating a record per key.
func assembleRecordsFromSeries(values map[string]map[metric.Aggregation][]metric.AggregatePoint, clientUUID string) []models.Record {
	// Aligned metrics normally share buckets: the largest selected metric is a
	// conservative capacity estimate, not the sum of the metric fanout. Missing
	// buckets can grow the union beyond it (and duplicate seconds can shrink it).
	capacity := 0
	for name, aggregations := range values {
		if n := len(aggregations[recordMetricAggregation(name)]); n > capacity {
			capacity = n
		}
	}
	records := make([]models.Record, 0, capacity)
	recordIndex := make(map[recordSeriesKey]int, capacity)
	for metricName, aggMap := range values {
		agg := recordMetricAggregation(metricName)
		points, ok := aggMap[agg]
		if !ok {
			continue
		}
		for _, point := range points {
			entityID := point.EntityID
			if entityID == "" {
				entityID = clientUUID
			}
			key := recordSeriesKey{client: entityID, ts: point.Bucket.Unix()}
			index, exists := recordIndex[key]
			if !exists {
				index = len(records)
				recordIndex[key] = index
				records = append(records, models.Record{
					Client: entityID,
					Time:   point.Bucket.UTC(),
				})
			}
			// Resolve the address after append: the backing array may have moved.
			applyRecordMetricValue(&records[index], metricName, point.Value)
		}
	}

	sortRecords(records)
	return records
}

func getRecordMetricMaxByClientAndTimeFromSeries(ctx context.Context, s *metric.Store, clientUUID, recordMetric string, start, end time.Time) ([]models.Record, error) {
	metricName, ok := metricNameForRecordField(recordMetric)
	if !ok {
		return nil, fmt.Errorf("unsupported record metric %q", recordMetric)
	}

	loaded, err := QueryMetrics(ctx, MetricsQuery{
		Store: s, Mode: QuerySeries, Source: SourceRollup,
		OmitTags: true, OmitLabels: true, CompactDefinitions: true,
		Entities: EntityScope{IDs: []string{clientUUID}},
		Specs:    []MetricSpec{{Name: metricName, Aggregations: []metric.Aggregation{metric.AggMax}}},
		Start:    start, End: end, Order: metric.OrderAsc, Grouping: GroupMerged,
		Budget: PointBudget{PerSeries: 500}, Rounding: RoundFloor, AllowMissing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query metric %s: %w", metricName, err)
	}

	points := loaded.Values[metricName][metric.AggMax]
	records := make([]models.Record, 0, len(points))
	for _, point := range points {
		entityID := point.EntityID
		if entityID == "" {
			entityID = clientUUID
		}
		record := models.Record{Client: entityID, Time: point.Bucket.UTC()}
		applyRecordMetricValue(&record, metricName, point.Value)
		records = append(records, record)
	}
	return records, nil
}

func recordMetricAggregation(metricName string) metric.Aggregation {
	switch metricName {
	case MetricTrafficUp, MetricTrafficDown:
		return metric.AggSum
	case MetricNetTotalUp, MetricNetTotalDown:
		return metric.AggLast
	default:
		return metric.AggAvg
	}
}

func applyRecordMetricValue(rec *models.Record, metricName string, value float64) {
	switch metricName {
	case MetricCPU:
		rec.Cpu = float32(value)
	case MetricGPU:
		rec.Gpu = float32(value)
	case MetricRAM:
		rec.Ram = int64(value)
	case MetricSwap:
		rec.Swap = int64(value)
	case MetricLoad:
		rec.Load = float32(value)
	case MetricDisk:
		rec.Disk = int64(value)
	case MetricNetIn:
		rec.NetIn = int64(value)
	case MetricNetOut:
		rec.NetOut = int64(value)
	case MetricNetTotalUp:
		rec.NetTotalUp = int64(value)
	case MetricNetTotalDown:
		rec.NetTotalDown = int64(value)
	case MetricTrafficUp:
		rec.TrafficUp = int64(value)
	case MetricTrafficDown:
		rec.TrafficDown = int64(value)
	case MetricProcess:
		rec.Process = int(value)
	case MetricConnections:
		rec.Connections = int(value)
	case MetricConnectionsUDP:
		rec.ConnectionsUdp = int(value)
	}
}

func sortRecords(records []models.Record) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Client != records[j].Client {
			return records[i].Client < records[j].Client
		}
		return records[i].Time.Before(records[j].Time)
	})
}

// GetGPURecordsByClientAndTime 从 metric store 查询 GPU 记录
func GetGPURecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.GPURecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 查询 GPU 相关指标（每设备利用率使用独立指标 gpu.device.usage）
	gpuMetrics := []string{MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp}

	// 按设备索引和时间组织数据
	type gpuKey struct {
		deviceIndex int
		timestamp   int64
	}
	recordMap := make(map[gpuKey]*models.GPURecord)

	specs := make([]MetricSpec, 0, len(gpuMetrics))
	for _, name := range gpuMetrics {
		specs = append(specs, MetricSpec{Name: name})
	}
	loaded, err := QueryMetrics(ctx, MetricsQuery{
		Store: s, Mode: QueryPoints, Source: SourceRaw,
		OmitLabels: true, CompactDefinitions: true,
		Entities: EntityScope{IDs: []string{clientUUID}}, Specs: specs,
		Start: start, End: end, Order: metric.OrderAsc, AllowMissing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query GPU metrics: %w", err)
	}
	for _, metricName := range gpuMetrics {
		points := loaded.Raw[metricName]

		for _, p := range points {
			deviceIndex := 0
			deviceName := ""
			if idx, ok := p.Tags["device_index"]; ok {
				fmt.Sscanf(idx, "%d", &deviceIndex)
			}
			if name, ok := p.Tags["device_name"]; ok {
				deviceName = name
			}

			key := gpuKey{deviceIndex: deviceIndex, timestamp: p.Timestamp.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.GPURecord{
					Client:      clientUUID,
					Time:        p.Timestamp.UTC(),
					DeviceIndex: deviceIndex,
					DeviceName:  deviceName,
				}
			}
			rec := recordMap[key]

			switch metricName {
			case MetricGPUDeviceUsage:
				rec.Utilization = float32(p.Value)
			case MetricGPUMem:
				rec.MemUsed = int64(p.Value)
			case MetricGPUMemTotal:
				rec.MemTotal = int64(p.Value)
			case MetricGPUTemp:
				rec.Temperature = int(p.Value)
			}
		}
	}

	// 转换为切片
	records := make([]models.GPURecord, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}

	return records, nil
}
