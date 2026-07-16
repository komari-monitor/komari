package jsonrpc

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const defaultMetricQueryPoints = 500

var metricDownsampleStandardIntervals = []time.Duration{
	time.Second,
	5 * time.Second,
	10 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

func init() {
	regPublic("listMetricDefinitions", publicListMetricDefinitions, "List public metric definitions")
	regPublic("queryMetrics", publicQueryMetrics, "Query metric points")
	regPublic("getPingMetricStats", publicGetPingMetricStats, "Get ping metric statistics")
}

type publicMetricQueryParams struct {
	MetricKey  string   `json:"metric_key"`
	MetricKeys []string `json:"metric_keys"`
	Metrics    []string `json:"metrics"`

	EntityID  string   `json:"entity_id"`
	EntityIDs []string `json:"entity_ids"`

	Start     any `json:"start"`
	StartTime any `json:"start_time"`
	End       any `json:"end"`
	EndTime   any `json:"end_time"`
	Hours     any `json:"hours"`

	Tags map[string]string `json:"tags"`

	Downsample               *bool           `json:"downsample"`
	ServerDownsample         *bool           `json:"server_downsample"`
	DownsampleByMetric       map[string]bool `json:"downsample_by_metric"`
	ServerDownsampleByMetric map[string]bool `json:"server_downsample_by_metric"`
	FillEmpty                *bool           `json:"fill_empty"`

	MaxPoints         int            `json:"max_points"`
	DownsamplePoints  int            `json:"downsample_points"`
	MaxPointsByMetric map[string]int `json:"max_points_by_metric"`
	PointsByMetric    map[string]int `json:"points_by_metric"`

	Aggregation                 string            `json:"aggregation"`
	DownsampleAlgorithm         string            `json:"downsample_algorithm"`
	Algorithm                   string            `json:"algorithm"`
	AggregationByMetric         map[string]string `json:"aggregation_by_metric"`
	DownsampleAlgorithmByMetric map[string]string `json:"downsample_algorithm_by_metric"`
	AlgorithmByMetric           map[string]string `json:"algorithm_by_metric"`
}

type publicMetricPoint struct {
	Time   string            `json:"time"`
	Value  *float64          `json:"value"`
	Count  int               `json:"count,omitempty"`
	Tag    map[string]string `json:"tag,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type publicMetricSeries struct {
	MetricKey           string              `json:"metric_key"`
	EntityID            string              `json:"entity_id"`
	Type                string              `json:"type,omitempty"`
	Unit                string              `json:"unit,omitempty"`
	RetentionDays       int                 `json:"retention_days,omitempty"`
	Tag                 map[string]string   `json:"tag,omitempty"`
	Tags                map[string]string   `json:"tags,omitempty"`
	Downsampled         bool                `json:"downsampled"`
	DownsampleAlgorithm string              `json:"downsample_algorithm,omitempty"`
	FillEmpty           bool                `json:"fill_empty,omitempty"`
	MaxPoints           int                 `json:"max_points,omitempty"`
	IntervalSeconds     float64             `json:"interval_seconds,omitempty"`
	Count               int                 `json:"count"`
	Points              []publicMetricPoint `json:"points"`
}

type publicPingMetricStatsParams struct {
	UUID      string   `json:"uuid"`
	EntityID  string   `json:"entity_id"`
	EntityIDs []string `json:"entity_ids"`

	TaskID  any   `json:"task_id"`
	TaskIDs []any `json:"task_ids"`

	Start     any `json:"start"`
	StartTime any `json:"start_time"`
	End       any `json:"end"`
	EndTime   any `json:"end_time"`
	Hours     any `json:"hours"`

	MaxPoints        int `json:"max_points"`
	DownsamplePoints int `json:"downsample_points"`
}

type publicPingMetricTaskStats struct {
	EntityID        string            `json:"entity_id"`
	TaskID          string            `json:"task_id"`
	Name            string            `json:"name,omitempty"`
	Type            string            `json:"type,omitempty"`
	Interval        int               `json:"interval,omitempty"`
	Tags            map[string]string `json:"tags,omitempty"`
	Total           int               `json:"total"`
	Valid           int               `json:"valid"`
	Loss            float64           `json:"loss"`
	LossApproximate bool              `json:"loss_approximate,omitempty"`
	Min             *float64          `json:"min,omitempty"`
	Max             *float64          `json:"max,omitempty"`
	Avg             *float64          `json:"avg,omitempty"`
	Latest          *float64          `json:"latest,omitempty"`
	P50             *float64          `json:"p50,omitempty"`
	P99             *float64          `json:"p99,omitempty"`
	StdDev          *float64          `json:"stddev,omitempty"`
	P99P50Ratio     float64           `json:"p99_p50_ratio"`
}

type publicPingMetricStatsResponse struct {
	Start           string                      `json:"start"`
	End             string                      `json:"end"`
	IntervalSeconds float64                     `json:"interval_seconds,omitempty"`
	Stats           []publicPingMetricTaskStats `json:"stats"`
	Count           int                         `json:"count"`
}

func publicListMetricDefinitions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	defs, err := store.ListMetrics(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list metric definitions: "+err.Error(), nil)
	}
	out := make([]metricDefinitionResponse, 0, len(defs))
	for _, def := range defs {
		out = append(out, metricDefinitionResponse{
			Name:          def.Name,
			Description:   metricDescriptionValue(def.Description),
			Type:          string(def.Type),
			Unit:          def.Unit,
			RetentionDays: def.RetentionDays,
			Metadata:      def.Metadata,
			CreatedAt:     def.CreatedAt,
			UpdatedAt:     def.UpdatedAt,
		})
	}
	return out, nil
}

func publicQueryMetrics(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicMetricQueryParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	metricKeys := normalizeStringList(params.MetricKeys, params.Metrics, []string{params.MetricKey})
	if len(metricKeys) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "metric_keys is required", nil)
	}

	end, err := parseMetricQueryTimeOrDefault(firstNonNil(params.End, params.EndTime), time.Now().UTC())
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid end time: "+err.Error(), nil)
	}
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start, err := parseMetricQueryTimeOrDefault(firstNonNil(params.Start, params.StartTime), startFallback)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid start time: "+err.Error(), nil)
	}
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	entityIDs, rpcErr := publicMetricEntityIDs(ctx, normalizeStringList(params.EntityIDs, []string{params.EntityID}))
	if rpcErr != nil {
		return nil, rpcErr
	}

	downsample := true
	if params.Downsample != nil {
		downsample = *params.Downsample
	}
	if params.ServerDownsample != nil {
		downsample = *params.ServerDownsample
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	defs, err := store.ListMetrics(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list metric definitions: "+err.Error(), nil)
	}
	defMap := make(map[string]metric.Definition, len(defs))
	for _, def := range defs {
		defMap[def.Name] = def
	}

	series := make([]publicMetricSeries, 0, len(metricKeys)*maxInt(1, len(entityIDs)))
	for _, metricKey := range metricKeys {
		def, ok := defMap[metricKey]
		if !ok {
			return nil, rpc.MakeError(rpc.InvalidParams, "unknown metric key: "+metricKey, nil)
		}
		maxPoints, err := resolveMetricMaxPoints(metricKey, params)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		metricDownsample := resolveMetricDownsample(metricKey, params)
		algorithm := resolveMetricAggregation(metricKey, params)
		metricFillEmpty := resolveMetricFillEmpty(params)

		for _, entityID := range entityIDs {
			query := metric.Query{
				MetricName: metricKey,
				EntityID:   entityID,
				Start:      start,
				End:        end,
				Tags:       params.Tags,
				Order:      metric.OrderAsc,
			}
			item := publicMetricSeries{
				MetricKey:     metricKey,
				EntityID:      entityID,
				Type:          string(def.Type),
				Unit:          def.Unit,
				RetentionDays: def.RetentionDays,
				Tags:          params.Tags,
				Tag:           params.Tags,
				Downsampled:   metricDownsample,
				FillEmpty:     metricFillEmpty,
				MaxPoints:     maxPoints,
			}

			if metricDownsample {
				item.DownsampleAlgorithm = string(algorithm)
				now := time.Now()
				interval := metricDownsampleInterval(end.Sub(start), maxPoints)
				interval = store.CompatibleSeriesInterval(start, now, interval)
				item.IntervalSeconds = interval.Seconds()
				points, err := store.Series(ctx, metric.AggregateQuery{
					Query:          query,
					Aggregation:    algorithm,
					Interval:       interval,
					PreserveSeries: true,
				}, now)
				if err != nil {
					return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query metric "+metricKey+": "+err.Error(), nil)
				}
				item.Points = make([]publicMetricPoint, 0, len(points))
				for _, point := range points {
					item.Points = append(item.Points, publicMetricPoint{
						Time:  point.Bucket.Format(time.RFC3339Nano),
						Value: publicRawMetricValue(point.MetricName, point.Value, metricFillEmpty),
						Count: point.Count,
						Tag:   point.Tags,
						Tags:  point.Tags,
					})
				}
			} else {
				points, err := store.Query(ctx, query)
				if err != nil {
					return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query metric "+metricKey+": "+err.Error(), nil)
				}
				item.Points = make([]publicMetricPoint, 0, len(points))
				for _, point := range points {
					item.Points = append(item.Points, publicMetricPoint{
						Time:   point.Timestamp.Format(time.RFC3339Nano),
						Value:  publicRawMetricValue(metricKey, point.Value, metricFillEmpty),
						Tag:    point.Tags,
						Tags:   point.Tags,
						Labels: point.Labels,
					})
				}
			}
			for _, split := range splitPublicMetricSeries(item) {
				if metricFillEmpty {
					split = adaptiveFillPublicMetricSeries(split, start, end)
				}
				series = append(series, split)
			}
		}
	}

	return map[string]any{
		"start":                     start.Format(time.RFC3339Nano),
		"end":                       end.Format(time.RFC3339Nano),
		"server_downsample_default": downsample,
		"default_points":            defaultMetricQueryPoints,
		"series":                    series,
		"count":                     len(series),
	}, nil
}

func publicGetPingMetricStats(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicPingMetricStatsParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	end, err := parseMetricQueryTimeOrDefault(firstNonNil(params.End, params.EndTime), time.Now().UTC())
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid end time: "+err.Error(), nil)
	}
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start, err := parseMetricQueryTimeOrDefault(firstNonNil(params.Start, params.StartTime), startFallback)
	if err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid start time: "+err.Error(), nil)
	}
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	requestedEntities := normalizeStringList(params.EntityIDs, []string{firstNonEmpty(params.EntityID, params.UUID)})
	entityIDs, rpcErr := publicMetricEntityIDs(ctx, requestedEntities)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if len(entityIDs) == 0 {
		return publicPingMetricStatsResponse{
			Start: start.Format(time.RFC3339Nano),
			End:   end.Format(time.RFC3339Nano),
			Stats: []publicPingMetricTaskStats{},
			Count: 0,
		}, nil
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	taskList, err := tasks.GetAllPingTasks()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to fetch ping tasks: "+err.Error(), nil)
	}
	taskMap := make(map[string]models.PingTask, len(taskList))
	for _, task := range taskList {
		taskMap[strconv.FormatUint(uint64(task.Id), 10)] = task
	}
	taskFilter := normalizePingMetricTaskIDs(params.TaskID, params.TaskIDs)

	maxPoints := params.MaxPoints
	if maxPoints == 0 {
		maxPoints = params.DownsamplePoints
	}
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	now := time.Now()
	interval := metricDownsampleInterval(end.Sub(start), maxPoints)
	interval = store.CompatibleSeriesInterval(start, now, interval)

	stats := make([]publicPingMetricTaskStats, 0)
	for _, entityID := range entityIDs {
		groups, err := loadPublicPingMetricAggregateGroups(ctx, store, entityID, start, end, interval, now)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to query ping stats: "+err.Error(), nil)
		}
		entityStats := publicPingStatsFromAggregateGroups(entityID, groups, taskMap, taskFilter)
		stats = append(stats, entityStats...)
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].EntityID != stats[j].EntityID {
			return stats[i].EntityID < stats[j].EntityID
		}
		return stats[i].TaskID < stats[j].TaskID
	})

	return publicPingMetricStatsResponse{
		Start:           start.Format(time.RFC3339Nano),
		End:             end.Format(time.RFC3339Nano),
		IntervalSeconds: interval.Seconds(),
		Stats:           stats,
		Count:           len(stats),
	}, nil
}

type publicMetricSeriesGroup struct {
	entityID string
	tagsKey  string
	tags     map[string]string
	points   []publicMetricPoint
}

func splitPublicMetricSeries(base publicMetricSeries) []publicMetricSeries {
	if len(base.Points) == 0 {
		base.Count = 0
		return []publicMetricSeries{base}
	}

	groups := make(map[string]*publicMetricSeriesGroup)
	order := make([]string, 0)
	for _, point := range base.Points {
		entityID := base.EntityID
		tags := point.Tags
		if len(tags) == 0 {
			tags = point.Tag
		}
		point.Tag = tags
		point.Tags = tags
		tagsKey := publicMetricTagsKey(tags)
		key := entityID + "\x00" + tagsKey
		group := groups[key]
		if group == nil {
			group = &publicMetricSeriesGroup{
				entityID: entityID,
				tagsKey:  tagsKey,
				tags:     clonePublicMetricTags(tags),
			}
			groups[key] = group
			order = append(order, key)
		}
		group.points = append(group.points, point)
	}

	sort.SliceStable(order, func(i, j int) bool {
		a := groups[order[i]]
		b := groups[order[j]]
		if a.entityID != b.entityID {
			return a.entityID < b.entityID
		}
		return a.tagsKey < b.tagsKey
	})

	out := make([]publicMetricSeries, 0, len(order))
	for _, key := range order {
		group := groups[key]
		item := base
		item.EntityID = group.entityID
		item.Tag = group.tags
		item.Tags = group.tags
		item.Points = group.points
		item.Count = len(group.points)
		out = append(out, item)
	}
	return out
}

// adaptiveFillPublicMetricSeries inserts only the null points needed to mark
// chart boundaries and real collection gaps. The typical collection interval
// is inferred per metric/entity/tag series, so sparse periodic data does not
// expand into hundreds of artificial empty buckets.
func adaptiveFillPublicMetricSeries(series publicMetricSeries, start, end time.Time) publicMetricSeries {
	pointTimes := make([]time.Time, len(series.Points))
	deltas := make([]time.Duration, 0, len(series.Points))
	for i, point := range series.Points {
		parsed, err := time.Parse(time.RFC3339Nano, point.Time)
		if err != nil {
			return series
		}
		pointTimes[i] = parsed
		if i > 0 {
			delta := parsed.Sub(pointTimes[i-1])
			if delta > 0 {
				deltas = append(deltas, delta)
			}
		}
	}

	expectedInterval := time.Duration(series.IntervalSeconds * float64(time.Second))
	// Two deltas are the minimum needed to distinguish a regular cadence from
	// one isolated long gap. A lower quartile keeps outages from inflating the
	// inferred cadence when the rest of the series is regular.
	if len(deltas) >= 2 {
		sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
		observedInterval := deltas[(len(deltas)-1)/4]
		if observedInterval > expectedInterval {
			expectedInterval = observedInterval
		}
	}
	if expectedInterval > 0 {
		series.IntervalSeconds = expectedInterval.Seconds()
	}

	nullPoint := func(at time.Time) publicMetricPoint {
		return publicMetricPoint{
			Time:  at.UTC().Format(time.RFC3339Nano),
			Value: nil,
			Tag:   series.Tag,
			Tags:  series.Tags,
		}
	}
	filled := make([]publicMetricPoint, 0, len(series.Points)+2)
	if len(pointTimes) == 0 || start.Before(pointTimes[0]) {
		filled = append(filled, nullPoint(start))
	}
	for i, point := range series.Points {
		if i > 0 && expectedInterval > 0 && series.Points[i-1].Value != nil && point.Value != nil {
			delta := pointTimes[i].Sub(pointTimes[i-1])
			if delta > expectedInterval+expectedInterval/2 {
				filled = append(filled, nullPoint(pointTimes[i-1].Add(expectedInterval)))
			}
		}
		filled = append(filled, point)
	}
	if len(pointTimes) == 0 || pointTimes[len(pointTimes)-1].Before(end) {
		filled = append(filled, nullPoint(end))
	}
	series.Points = filled
	series.Count = len(filled)
	return series
}

func publicMetricTagsKey(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(tags[key])
		b.WriteByte('\x00')
	}
	return b.String()
}

func clonePublicMetricTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[key] = value
	}
	return out
}

func publicMetricEntityIDs(ctx context.Context, requested []string) ([]string, *rpc.JsonRpcError) {
	allClients, err := clients.GetAllClientBasicInfo()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve client information: "+err.Error(), nil)
	}
	isLogin := isLoginFromCtx(ctx)
	hidden := make(map[string]bool, len(allClients))
	visible := make(map[string]bool, len(allClients))
	var allVisible []string
	for _, client := range allClients {
		if client.Hidden {
			hidden[client.UUID] = true
		}
		if client.Hidden && !isLogin {
			continue
		}
		visible[client.UUID] = true
		allVisible = append(allVisible, client.UUID)
	}
	if len(requested) == 0 {
		return allVisible, nil
	}
	out := make([]string, 0, len(requested))
	for _, entityID := range requested {
		if hidden[entityID] && !isLogin {
			continue
		}
		if visible[entityID] || !hidden[entityID] {
			out = append(out, entityID)
		}
	}
	return out, nil
}

func normalizeStringList(groups ...[]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func parseMetricQueryTimeOrDefault(value any, fallback time.Time) (time.Time, error) {
	if value == nil {
		return fallback.UTC(), nil
	}
	switch v := value.(type) {
	case string:
		raw := strings.TrimSpace(v)
		if raw == "" {
			return fallback.UTC(), nil
		}
		if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return ts.UTC(), nil
		}
		if ts, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
			return ts.UTC(), nil
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
		}
		return metricUnixNumberToTime(n), nil
	case float64:
		return metricUnixNumberToTime(v), nil
	case float32:
		return metricUnixNumberToTime(float64(v)), nil
	case int:
		return metricUnixNumberToTime(float64(v)), nil
	case int64:
		return metricUnixNumberToTime(float64(v)), nil
	case jsonNumber:
		n, err := strconv.ParseFloat(v.String(), 64)
		if err != nil {
			return time.Time{}, err
		}
		return metricUnixNumberToTime(n), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time value type %T", value)
	}
}

type jsonNumber interface {
	String() string
}

func metricUnixNumberToTime(value float64) time.Time {
	if math.Abs(value) >= 1e17 {
		return time.Unix(0, int64(value)).UTC()
	}
	if math.Abs(value) >= 1e14 {
		return time.Unix(0, int64(value)*int64(time.Microsecond)).UTC()
	}
	if math.Abs(value) >= 1e11 {
		return time.UnixMilli(int64(value)).UTC()
	}
	return time.Unix(int64(value), 0).UTC()
}

func metricQueryHours(value any) time.Duration {
	if value == nil {
		return 4 * time.Hour
	}
	var hours float64
	switch v := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 4 * time.Hour
		}
		hours = parsed
	case float64:
		hours = v
	case int:
		hours = float64(v)
	default:
		return 4 * time.Hour
	}
	if hours <= 0 {
		return 4 * time.Hour
	}
	return time.Duration(hours * float64(time.Hour))
}

func resolveMetricMaxPoints(metricKey string, params publicMetricQueryParams) (int, error) {
	maxPoints := params.MaxPoints
	if maxPoints == 0 {
		maxPoints = params.DownsamplePoints
	}
	if maxPoints == 0 {
		maxPoints = defaultMetricQueryPoints
	}
	if v, ok := params.PointsByMetric[metricKey]; ok {
		maxPoints = v
	}
	if v, ok := params.MaxPointsByMetric[metricKey]; ok {
		maxPoints = v
	}
	if maxPoints <= 0 {
		return 0, fmt.Errorf("max points for %s must be a positive integer", metricKey)
	}
	return maxPoints, nil
}

func resolveMetricAggregation(metricKey string, params publicMetricQueryParams) metric.Aggregation {
	raw := firstNonEmpty(params.Aggregation, params.DownsampleAlgorithm, params.Algorithm)
	if v := firstNonEmpty(
		params.AggregationByMetric[metricKey],
		params.DownsampleAlgorithmByMetric[metricKey],
		params.AlgorithmByMetric[metricKey],
	); v != "" {
		raw = v
	}
	if raw == "" {
		raw = string(metric.AggAvg)
	}
	return metric.Aggregation(normalizeMetricAggregation(raw))
}

func resolveMetricDownsample(metricKey string, params publicMetricQueryParams) bool {
	downsample := true
	if params.Downsample != nil {
		downsample = *params.Downsample
	}
	if params.ServerDownsample != nil {
		downsample = *params.ServerDownsample
	}
	if v, ok := params.DownsampleByMetric[metricKey]; ok {
		downsample = v
	}
	if v, ok := params.ServerDownsampleByMetric[metricKey]; ok {
		downsample = v
	}
	return downsample
}

func resolveMetricFillEmpty(params publicMetricQueryParams) bool {
	return params.FillEmpty != nil && *params.FillEmpty
}

func publicMetricValue(value float64) *float64 {
	return &value
}

func publicRawMetricValue(metricName string, value float64, fillEmpty bool) *float64 {
	if isNullPingMetricValue(metricName, value, fillEmpty) {
		return nil
	}
	return publicMetricValue(value)
}

func isNullPingMetricValue(metricName string, value float64, fillEmpty bool) bool {
	if !fillEmpty || value != -1 {
		return false
	}
	return metricName == metricstore.MetricPingLatency || metricName == metricstore.MetricPingLoss
}

type publicPingMetricAggregateGroups struct {
	Avg           map[string][]metric.AggregatePoint
	Min           map[string][]metric.AggregatePoint
	Max           map[string][]metric.AggregatePoint
	Last          map[string][]metric.AggregatePoint
	P50           map[string][]metric.AggregatePoint
	P99           map[string][]metric.AggregatePoint
	StdDev        map[string][]metric.AggregatePoint
	Loss          map[string][]metric.AggregatePoint
	LossAvailable bool
}

func loadPublicPingMetricAggregateGroups(ctx context.Context, store *metric.Store, entityID string, start, end time.Time, interval time.Duration, now time.Time) (publicPingMetricAggregateGroups, error) {
	query := func(metricName string, aggregation metric.Aggregation) (map[string][]metric.AggregatePoint, error) {
		points, err := store.Series(ctx, metric.AggregateQuery{
			Query: metric.Query{
				MetricName: metricName,
				EntityID:   entityID,
				Start:      start,
				End:        end,
				Order:      metric.OrderAsc,
			},
			Aggregation:    aggregation,
			Interval:       interval,
			PreserveSeries: true,
		}, now)
		if err != nil {
			return nil, err
		}
		return groupPingMetricAggregatePointsByTask(points), nil
	}

	avg, err := query(metricstore.MetricPingLatency, metric.AggAvg)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	minimum, err := query(metricstore.MetricPingLatency, metric.AggMin)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	maximum, err := query(metricstore.MetricPingLatency, metric.AggMax)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	last, err := query(metricstore.MetricPingLatency, metric.AggLast)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	p50, err := query(metricstore.MetricPingLatency, metric.AggP50)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	p99, err := query(metricstore.MetricPingLatency, metric.AggP99)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}
	stddev, err := query(metricstore.MetricPingLatency, metric.AggStdDev)
	if err != nil {
		return publicPingMetricAggregateGroups{}, err
	}

	loss, lossErr := query(metricstore.MetricPingLoss, metric.AggAvg)
	return publicPingMetricAggregateGroups{
		Avg:           avg,
		Min:           minimum,
		Max:           maximum,
		Last:          last,
		P50:           p50,
		P99:           p99,
		StdDev:        stddev,
		Loss:          loss,
		LossAvailable: lossErr == nil && pingMetricGroupsHaveData(loss),
	}, nil
}

func groupPingMetricAggregatePointsByTask(points []metric.AggregatePoint) map[string][]metric.AggregatePoint {
	out := make(map[string][]metric.AggregatePoint)
	for _, point := range points {
		taskID := strings.TrimSpace(point.Tags["task_id"])
		if taskID == "" {
			continue
		}
		out[taskID] = append(out[taskID], point)
	}
	return out
}

func pingMetricGroupsHaveData(groups map[string][]metric.AggregatePoint) bool {
	for _, points := range groups {
		for _, point := range points {
			if point.Count > 0 {
				return true
			}
		}
	}
	return false
}

func publicPingStatsFromAggregateGroups(entityID string, groups publicPingMetricAggregateGroups, taskMap map[string]models.PingTask, taskFilter map[string]bool) []publicPingMetricTaskStats {
	taskIDs := make(map[string]struct{})
	for _, group := range []map[string][]metric.AggregatePoint{
		groups.Avg, groups.Min, groups.Max, groups.Last, groups.P50, groups.P99, groups.StdDev, groups.Loss,
	} {
		for taskID := range group {
			taskIDs[taskID] = struct{}{}
		}
	}

	out := make([]publicPingMetricTaskStats, 0, len(taskIDs))
	for taskID := range taskIDs {
		if len(taskFilter) > 0 && !taskFilter[taskID] {
			continue
		}

		total := aggregatePointCount(groups.Avg[taskID])
		if total == 0 {
			total = aggregatePointCount(groups.Loss[taskID])
		}
		if total == 0 {
			continue
		}

		lossRate, valid, approximate := publicPingLossRate(groups.Avg[taskID], groups.Loss[taskID], total, groups.LossAvailable)
		avg, _ := weightedAggregateValue(groups.Avg[taskID], true)
		p50, _ := weightedAggregateValue(groups.P50[taskID], true)
		p99, _ := weightedAggregateValue(groups.P99[taskID], true)
		stddev, _ := weightedAggregateValue(groups.StdDev[taskID], false)
		minimum := positiveAggregateMin(groups.Min[taskID])
		maximum := positiveAggregateMax(groups.Max[taskID])
		latest := latestPositiveAggregate(groups.Last[taskID])
		if latest == nil {
			latest = latestPositiveAggregate(groups.Avg[taskID])
		}

		stat := publicPingMetricTaskStats{
			EntityID:        entityID,
			TaskID:          taskID,
			Tags:            map[string]string{"task_id": taskID},
			Total:           total,
			Valid:           valid,
			Loss:            lossRate,
			LossApproximate: approximate,
			Min:             minimum,
			Max:             maximum,
			Avg:             avg,
			Latest:          latest,
			P50:             p50,
			P99:             p99,
			StdDev:          stddev,
		}
		if task, ok := taskMap[taskID]; ok {
			stat.Name = task.Name
			stat.Type = task.Type
			stat.Interval = task.Interval
		}
		if p50 != nil && p99 != nil && *p50 > 0 && *p99 >= *p50 {
			adjustedBase := math.Max(math.Min(*p50, 50.0), 10.0)
			stat.P99P50Ratio = (*p99 - *p50) / adjustedBase
		}
		out = append(out, stat)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].TaskID < out[j].TaskID
	})
	return out
}

func normalizePingMetricTaskIDs(taskID any, taskIDs []any) map[string]bool {
	out := make(map[string]bool)
	add := func(value any) {
		switch v := value.(type) {
		case nil:
			return
		case string:
			if raw := strings.TrimSpace(v); raw != "" {
				out[raw] = true
			}
		case float64:
			out[strconv.FormatInt(int64(v), 10)] = true
		case int:
			out[strconv.Itoa(v)] = true
		case int64:
			out[strconv.FormatInt(v, 10)] = true
		case jsonNumber:
			if raw := strings.TrimSpace(v.String()); raw != "" {
				out[raw] = true
			}
		default:
			raw := strings.TrimSpace(fmt.Sprint(v))
			if raw != "" {
				out[raw] = true
			}
		}
	}
	add(taskID)
	for _, value := range taskIDs {
		add(value)
	}
	return out
}

func aggregatePointCount(points []metric.AggregatePoint) int {
	total := 0
	for _, point := range points {
		total += point.Count
	}
	return total
}

func publicPingLossRate(latencyPoints, lossPoints []metric.AggregatePoint, total int, lossAvailable bool) (float64, int, bool) {
	if total <= 0 {
		return 0, 0, !lossAvailable
	}
	if lossAvailable {
		lossCount := 0.0
		for _, point := range lossPoints {
			if point.Count <= 0 {
				continue
			}
			lossCount += math.Max(0, math.Min(1, point.Value)) * float64(point.Count)
		}
		lost := int(math.Round(lossCount))
		if lost > total {
			lost = total
		}
		return lossCount / float64(total) * 100, total - lost, false
	}

	lost := 0
	valid := 0
	for _, point := range latencyPoints {
		if point.Count <= 0 {
			continue
		}
		if point.Value < 0 {
			lost += point.Count
			continue
		}
		valid += point.Count
	}
	return float64(lost) / float64(total) * 100, valid, true
}

func weightedAggregateValue(points []metric.AggregatePoint, skipNegative bool) (*float64, int) {
	sum := 0.0
	count := 0
	for _, point := range points {
		if point.Count <= 0 {
			continue
		}
		if skipNegative && point.Value < 0 {
			continue
		}
		sum += point.Value * float64(point.Count)
		count += point.Count
	}
	if count == 0 {
		return nil, 0
	}
	value := sum / float64(count)
	return &value, count
}

func positiveAggregateMin(points []metric.AggregatePoint) *float64 {
	var out *float64
	for _, point := range points {
		if point.Count <= 0 || point.Value < 0 {
			continue
		}
		value := point.Value
		if out == nil || value < *out {
			out = &value
		}
	}
	return out
}

func positiveAggregateMax(points []metric.AggregatePoint) *float64 {
	var out *float64
	for _, point := range points {
		if point.Count <= 0 || point.Value < 0 {
			continue
		}
		value := point.Value
		if out == nil || value > *out {
			out = &value
		}
	}
	return out
}

func latestPositiveAggregate(points []metric.AggregatePoint) *float64 {
	var out *float64
	var latest time.Time
	for _, point := range points {
		if point.Count <= 0 || point.Value < 0 {
			continue
		}
		if out == nil || point.Bucket.After(latest) {
			value := point.Value
			out = &value
			latest = point.Bucket
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeMetricAggregation(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "average", "mean":
		return string(metric.AggAvg)
	case "std_dev", "stddev_pop", "std_dev_pop":
		return string(metric.AggStdDev)
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func metricDownsampleInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	nanos := rangeDuration.Nanoseconds()
	if nanos <= 0 {
		return time.Second
	}
	interval := time.Duration((nanos + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return ceilMetricDownsampleInterval(interval)
}

func ceilMetricDownsampleInterval(interval time.Duration) time.Duration {
	for _, candidate := range metricDownsampleStandardIntervals {
		if candidate >= interval {
			return candidate
		}
	}
	day := metricDownsampleStandardIntervals[len(metricDownsampleStandardIntervals)-1]
	return ((interval-1)/day + 1) * day
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
