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
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const defaultMetricQueryPoints = 500

func init() {
	regPublic("listMetricDefinitions", publicListMetricDefinitions, "List public metric definitions")
	regPublic("queryMetrics", publicQueryMetrics, "Query metric points")
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
	Value  float64           `json:"value"`
	Count  int               `json:"count,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

type publicMetricSeries struct {
	MetricKey           string              `json:"metric_key"`
	EntityID            string              `json:"entity_id"`
	Type                string              `json:"type,omitempty"`
	Unit                string              `json:"unit,omitempty"`
	RetentionDays       int                 `json:"retention_days,omitempty"`
	Tags                map[string]string   `json:"tags,omitempty"`
	Downsampled         bool                `json:"downsampled"`
	DownsampleAlgorithm string              `json:"downsample_algorithm,omitempty"`
	MaxPoints           int                 `json:"max_points,omitempty"`
	IntervalSeconds     float64             `json:"interval_seconds,omitempty"`
	Count               int                 `json:"count"`
	Points              []publicMetricPoint `json:"points"`
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
				Downsampled:   metricDownsample,
				MaxPoints:     maxPoints,
			}

			if metricDownsample {
				item.DownsampleAlgorithm = string(algorithm)
				interval := metricDownsampleInterval(end.Sub(start), maxPoints)
				item.IntervalSeconds = interval.Seconds()
				points, err := store.Series(ctx, metric.AggregateQuery{
					Query:       query,
					Aggregation: algorithm,
					Interval:    interval,
				}, time.Now())
				if err != nil {
					return nil, rpc.MakeError(rpc.InvalidParams, "Failed to query metric "+metricKey+": "+err.Error(), nil)
				}
				item.Points = make([]publicMetricPoint, 0, len(points))
				for _, point := range points {
					item.Points = append(item.Points, publicMetricPoint{
						Time:  point.Bucket.Format(time.RFC3339Nano),
						Value: point.Value,
						Count: point.Count,
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
						Value:  point.Value,
						Tags:   point.Tags,
						Labels: point.Labels,
					})
				}
			}
			series = append(series, splitPublicMetricSeries(item)...)
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
		item.Tags = group.tags
		item.Points = group.points
		item.Count = len(group.points)
		out = append(out, item)
	}
	return out
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
	return interval
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
