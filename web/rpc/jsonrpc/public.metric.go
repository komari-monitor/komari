package jsonrpc

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const defaultMetricQueryPoints = 500

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

	Start     *time.Time `json:"start"`
	StartTime *time.Time `json:"start_time"`
	End       *time.Time `json:"end"`
	EndTime   *time.Time `json:"end_time"`
	Hours     float64    `json:"hours"`

	Tags map[string]string `json:"tags"`

	FillEmpty *bool `json:"fill_empty"`

	MaxPoints         int            `json:"max_points"`
	MaxPointsByMetric map[string]int `json:"max_points_by_metric"`
	PointsByMetric    map[string]int `json:"points_by_metric"`

	Aggregation         string            `json:"aggregation"`
	Algorithm           string            `json:"algorithm"`
	AggregationByMetric map[string]string `json:"aggregation_by_metric"`
	AlgorithmByMetric   map[string]string `json:"algorithm_by_metric"`
}

type publicMetricPoint struct {
	entityID string
	Time     time.Time         `json:"time"`
	Value    *float64          `json:"value"`
	Count    int               `json:"count,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
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

	Start     *time.Time `json:"start"`
	StartTime *time.Time `json:"start_time"`
	End       *time.Time `json:"end"`
	EndTime   *time.Time `json:"end_time"`
	Hours     float64    `json:"hours"`

	MaxPoints int `json:"max_points"`
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
	Start           time.Time                   `json:"start"`
	End             time.Time                   `json:"end"`
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

	queryNow := time.Now().UTC()
	end := metricQueryTimeOrDefault(firstMetricQueryTime(params.End, params.EndTime), queryNow)
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start := metricQueryTimeOrDefault(firstMetricQueryTime(params.Start, params.StartTime), startFallback)
	if !end.After(start) {
		return nil, rpc.MakeError(rpc.InvalidParams, "end must be after start", nil)
	}

	requestedEntityIDs := normalizeStringList(params.EntityIDs, []string{params.EntityID})
	entityIDs, rpcErr := publicMetricEntityIDs(ctx, requestedEntityIDs)
	if rpcErr != nil {
		return nil, rpcErr
	}

	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}

	type metricLoadSpec struct {
		metricKey string
		algorithm metric.Aggregation
		maxPoints int
		interval  time.Duration
	}
	loadSpecs := make([]metricLoadSpec, 0, len(metricKeys))
	for _, metricKey := range metricKeys {
		maxPoints, err := resolveMetricMaxPoints(metricKey, params)
		if err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		}
		loadSpecs = append(loadSpecs, metricLoadSpec{
			metricKey: metricKey,
			algorithm: resolveMetricAggregation(metricKey, params),
			maxPoints: maxPoints,
		})
	}

	metricFillEmpty := resolveMetricFillEmpty(params)
	querySpecs := make([]metricstore.MetricSpec, 0, len(loadSpecs))
	for _, spec := range loadSpecs {
		querySpecs = append(querySpecs, metricstore.MetricSpec{Name: spec.metricKey, Aggregations: []metric.Aggregation{spec.algorithm}, MaxPoints: spec.maxPoints})
	}
	projection := metricstore.ProjectionFull
	if metricFillEmpty {
		// This branch consumes Filled only; do not retain a second source view.
		projection = metricstore.ProjectionChart
	}
	loaded, err := metricstore.QueryMetrics(ctx, metricstore.MetricsQuery{
		Projection: projection,
		Store:      store, Mode: metricstore.QuerySeries, Source: metricstore.SourceAuto,
		Entities: metricstore.EntityScope{IDs: entityIDs}, Specs: querySpecs,
		Start: start, End: end, Tags: params.Tags, Order: metric.OrderAsc,
		Grouping: metricstore.GroupSeries, Rounding: metricstore.RoundCeil,
		Fill: metricstore.FillPolicy{Gaps: metricFillEmpty, PingNegativeAsNull: metricFillEmpty}, AllowMissing: true, Now: queryNow,
	})
	if err != nil {
		return nil, metricQueryRPCError(err)
	}
	definitions, rawValues, rollupValues := loaded.Definitions, loaded.Raw, loaded.Values
	useRaw := loaded.IsRaw
	for i := range loadSpecs {
		if _, ok := definitions[loadSpecs[i].metricKey]; !ok {
			return nil, rpc.MakeError(rpc.InvalidParams, "unknown metric key: "+loadSpecs[i].metricKey, nil)
		}
		loadSpecs[i].interval = loaded.Intervals[loadSpecs[i].metricKey]
	}

	filledByMetric := make(map[string][]metricstore.MetricSeries)
	for _, filled := range loaded.Filled {
		filledByMetric[filled.MetricName] = append(filledByMetric[filled.MetricName], filled)
	}
	series := make([]publicMetricSeries, 0, len(metricKeys)*maxInt(1, len(entityIDs)))
	for _, spec := range loadSpecs {
		def := definitions[spec.metricKey]
		item := publicMetricSeries{
			MetricKey:     spec.metricKey,
			Type:          string(def.Type),
			Unit:          def.Unit,
			RetentionDays: def.RetentionDays,
			Tags:          params.Tags,
			FillEmpty:     metricFillEmpty,
			MaxPoints:     spec.maxPoints,
			Downsampled:   !useRaw,
		}
		if !useRaw {
			item.DownsampleAlgorithm = string(spec.algorithm)
			item.IntervalSeconds = spec.interval.Seconds()
		}
		if !metricFillEmpty && useRaw {
			points := rawValues[spec.metricKey]
			item.Points = make([]publicMetricPoint, 0, len(points))
			for _, point := range points {
				item.Points = append(item.Points, publicMetricPoint{
					entityID: point.EntityID,
					Time:     point.Timestamp.UTC(),
					Value:    publicMetricValue(point.Value),
					Count:    1,
					Tags:     point.Tags,
					Labels:   point.Labels,
				})
			}
		} else if !metricFillEmpty {
			points := rollupValues[spec.metricKey][spec.algorithm]
			item.Points = make([]publicMetricPoint, 0, len(points))
			for _, point := range points {
				item.Points = append(item.Points, publicMetricPoint{
					entityID: point.EntityID,
					Time:     point.Bucket.UTC(),
					Value:    publicMetricValue(point.Value),
					Count:    point.Count,
					Tags:     point.Tags,
				})
			}
		}

		byEntity := make(map[string][]publicMetricSeries, len(entityIDs))
		if metricFillEmpty {
			for _, filled := range filledByMetric[spec.metricKey] {
				if !useRaw && filled.Aggregation != spec.algorithm {
					continue
				}
				split := publicMetricSeriesFromCore(item, filled)
				byEntity[split.EntityID] = append(byEntity[split.EntityID], split)
			}
		} else {
			for _, split := range splitPublicMetricSeries(item) {
				if split.EntityID != "" {
					byEntity[split.EntityID] = append(byEntity[split.EntityID], split)
				}
			}
		}
		for _, entityID := range entityIDs {
			entitySeries := byEntity[entityID]
			if len(entitySeries) == 0 {
				empty := item
				empty.EntityID = entityID
				empty.Count = 0
				empty.Points = nil
				if metricFillEmpty {
					empty = adaptiveFillPublicMetricSeries(empty, start, end)
				}
				entitySeries = []publicMetricSeries{empty}
			}
			for _, split := range entitySeries {
				series = append(series, split)
			}
		}
	}

	return map[string]any{
		"start":                     start.UTC(),
		"end":                       end.UTC(),
		"server_downsample_default": true,
		"default_points":            defaultMetricQueryPoints,
		"series":                    series,
		"count":                     len(series),
	}, nil
}

// metricQueryRPCError keeps invalid requests separate from storage/cancellation failures.
func metricQueryRPCError(err error) *rpc.JsonRpcError {
	code := rpc.InternalError
	if errors.Is(err, metric.ErrInvalidArgument) || errors.Is(err, metric.ErrNotFound) {
		code = rpc.InvalidParams
	}
	return rpc.MakeError(code, "Failed to query metrics: "+err.Error(), nil)
}

type publicMetricPointResult struct {
	points      []publicMetricPoint
	downsampled bool
	interval    time.Duration
}

func loadPublicMetricPoints(
	ctx context.Context,
	store *metric.Store,
	query metric.Query,
	algorithm metric.Aggregation,
	maxPoints int,
	fillEmpty bool,
	now time.Time,
) (publicMetricPointResult, error) {
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	loaded, err := metricstore.QueryMetrics(ctx, metricstore.MetricsQuery{
		Store: store, Mode: metricstore.QuerySeries, Source: metricstore.SourceAuto,
		Entities: metricstore.EntityScope{IDs: []string{query.EntityID}},
		Specs:    []metricstore.MetricSpec{{Name: query.MetricName, Aggregations: []metric.Aggregation{algorithm}, MaxPoints: maxPoints}},
		Start:    query.Start, End: query.End, Tags: query.Tags, Order: query.Order,
		Grouping: metricstore.GroupSeries, Rounding: metricstore.RoundCeil,
		Fill: metricstore.FillPolicy{PingNegativeAsNull: fillEmpty}, Now: now,
	})
	if err != nil {
		return publicMetricPointResult{}, err
	}
	result := publicMetricPointResult{points: []publicMetricPoint{}, downsampled: !loaded.IsRaw, interval: loaded.Intervals[query.MetricName]}
	if fillEmpty {
		for _, series := range loaded.Filled {
			result.points = append(result.points, publicMetricSeriesFromCore(publicMetricSeries{}, series).Points...)
		}
	} else if loaded.IsRaw {
		for _, point := range loaded.Raw[query.MetricName] {
			result.points = append(result.points, publicMetricPoint{entityID: point.EntityID, Time: point.Timestamp.UTC(), Value: publicMetricValue(point.Value), Count: 1, Tags: point.Tags, Labels: point.Labels})
		}
	} else {
		for _, point := range loaded.Values[query.MetricName][algorithm] {
			result.points = append(result.points, publicMetricPoint{entityID: point.EntityID, Time: point.Bucket.UTC(), Value: publicMetricValue(point.Value), Count: point.Count, Tags: point.Tags})
		}
	}
	return result, nil
}

func publicMetricUsesRawWindow(start, end, now time.Time) bool {
	return metricstore.UsesRawWindow(start, end, now)
}

func publicGetPingMetricStats(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params publicPingMetricStatsParams
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}

	end := metricQueryTimeOrDefault(firstMetricQueryTime(params.End, params.EndTime), time.Now().UTC())
	startFallback := end.Add(-metricQueryHours(params.Hours))
	start := metricQueryTimeOrDefault(firstMetricQueryTime(params.Start, params.StartTime), startFallback)
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
			Start: start.UTC(),
			End:   end.UTC(),
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
	if maxPoints <= 0 {
		maxPoints = defaultMetricQueryPoints
	}
	now := time.Now().UTC()
	groupsByEntity, interval, err := loadPublicPingMetricAggregateGroups(ctx, store, entityIDs, start, end, maxPoints, now)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to query ping stats: "+err.Error(), nil)
	}
	stats := make([]publicPingMetricTaskStats, 0)
	for _, entityID := range entityIDs {
		stats = append(stats, publicPingStatsFromAggregateGroups(entityID, groupsByEntity[entityID], taskMap, taskFilter)...)
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].EntityID != stats[j].EntityID {
			return stats[i].EntityID < stats[j].EntityID
		}
		return stats[i].TaskID < stats[j].TaskID
	})

	return publicPingMetricStatsResponse{
		Start:           start.UTC(),
		End:             end.UTC(),
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
		if point.entityID != "" {
			entityID = point.entityID
		}
		tags := point.Tags
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
	core := metricstore.MetricSeries{MetricName: series.MetricKey, EntityID: series.EntityID, Tags: series.Tags, Interval: time.Duration(series.IntervalSeconds * float64(time.Second))}
	for _, point := range series.Points {
		core.Points = append(core.Points, metricstore.MetricValue{Time: point.Time, Value: point.Value, Count: point.Count, Tags: point.Tags, Labels: point.Labels})
	}
	return publicMetricSeriesFromCore(series, metricstore.FillMetricSeries(core, start, end))
}

func publicMetricSeriesFromCore(base publicMetricSeries, series metricstore.MetricSeries) publicMetricSeries {
	base.EntityID = series.EntityID
	base.Tags = series.Tags
	base.IntervalSeconds = series.Interval.Seconds()
	base.Points = make([]publicMetricPoint, 0, len(series.Points))
	for _, point := range series.Points {
		base.Points = append(base.Points, publicMetricPoint{entityID: series.EntityID, Time: point.Time.UTC(), Value: point.Value, Count: point.Count, Tags: point.Tags, Labels: point.Labels})
	}
	base.Count = len(base.Points)
	return base
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

func firstMetricQueryTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func metricQueryTimeOrDefault(value *time.Time, fallback time.Time) time.Time {
	if value == nil {
		return fallback.UTC()
	}
	return value.UTC()
}

func metricQueryHours(hours float64) time.Duration {
	if hours <= 0 {
		return 4 * time.Hour
	}
	return time.Duration(hours * float64(time.Hour))
}

func resolveMetricMaxPoints(metricKey string, params publicMetricQueryParams) (int, error) {
	maxPoints := params.MaxPoints
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
	raw := firstNonEmpty(params.Aggregation, params.Algorithm)
	if v := firstNonEmpty(
		params.AggregationByMetric[metricKey],
		params.AlgorithmByMetric[metricKey],
	); v != "" {
		raw = v
	}
	if raw == "" {
		raw = string(metric.AggAvg)
	}
	return metric.Aggregation(normalizeMetricAggregation(raw))
}

func resolveMetricFillEmpty(params publicMetricQueryParams) bool {
	return params.FillEmpty != nil && *params.FillEmpty
}

func publicMetricValue(value float64) *float64 {
	return &value
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

func loadPublicPingMetricAggregateGroups(ctx context.Context, store *metric.Store, entityIDs []string, start, end time.Time, maxPoints int, now time.Time) (map[string]publicPingMetricAggregateGroups, time.Duration, error) {
	latencyAggregations := []metric.Aggregation{
		metric.AggAvg,
		metric.AggMin,
		metric.AggMax,
		metric.AggLast,
		metric.AggP50,
		metric.AggP99,
		metric.AggStdDev,
	}
	loaded, err := metricstore.QueryMetrics(ctx, metricstore.MetricsQuery{
		Store: store, Mode: metricstore.QuerySeries, Source: metricstore.SourceRollup,
		Specs: []metricstore.MetricSpec{
			{Name: metricstore.MetricPingLatency, Aggregations: latencyAggregations, MaxPoints: maxPoints},
			{Name: metricstore.MetricPingLoss, Aggregations: []metric.Aggregation{metric.AggAvg}, MaxPoints: maxPoints},
		},
		Entities: metricstore.EntityScope{IDs: entityIDs}, Start: start, End: end, Order: metric.OrderAsc,
		Grouping: metricstore.GroupSeries, Rounding: metricstore.RoundCeil, AllowMissing: true, Now: now,
	})
	if err != nil {
		return nil, 0, err
	}
	latency := loaded.Values[metricstore.MetricPingLatency]
	lossPoints := loaded.Values[metricstore.MetricPingLoss][metric.AggAvg]

	avg := groupPingMetricAggregatePointsByEntity(latency[metric.AggAvg])
	minimum := groupPingMetricAggregatePointsByEntity(latency[metric.AggMin])
	maximum := groupPingMetricAggregatePointsByEntity(latency[metric.AggMax])
	last := groupPingMetricAggregatePointsByEntity(latency[metric.AggLast])
	p50 := groupPingMetricAggregatePointsByEntity(latency[metric.AggP50])
	p99 := groupPingMetricAggregatePointsByEntity(latency[metric.AggP99])
	stddev := groupPingMetricAggregatePointsByEntity(latency[metric.AggStdDev])
	loss := groupPingMetricAggregatePointsByEntity(lossPoints)

	entitySet := make(map[string]struct{})
	for _, groups := range []map[string]map[string][]metric.AggregatePoint{avg, minimum, maximum, last, p50, p99, stddev, loss} {
		for currentEntityID := range groups {
			entitySet[currentEntityID] = struct{}{}
		}
	}
	result := make(map[string]publicPingMetricAggregateGroups, len(entitySet))
	for currentEntityID := range entitySet {
		entityLoss := loss[currentEntityID]
		result[currentEntityID] = publicPingMetricAggregateGroups{
			Avg:           avg[currentEntityID],
			Min:           minimum[currentEntityID],
			Max:           maximum[currentEntityID],
			Last:          last[currentEntityID],
			P50:           p50[currentEntityID],
			P99:           p99[currentEntityID],
			StdDev:        stddev[currentEntityID],
			Loss:          entityLoss,
			LossAvailable: pingMetricGroupsHaveData(entityLoss),
		}
	}
	return result, loaded.Intervals[metricstore.MetricPingLatency], nil
}

func groupPingMetricAggregatePointsByEntity(points []metric.AggregatePoint) map[string]map[string][]metric.AggregatePoint {
	out := make(map[string]map[string][]metric.AggregatePoint)
	for _, point := range points {
		taskID := strings.TrimSpace(point.Tags["task_id"])
		if taskID == "" {
			continue
		}
		byTask := out[point.EntityID]
		if byTask == nil {
			byTask = make(map[string][]metric.AggregatePoint)
			out[point.EntityID] = byTask
		}
		byTask[taskID] = append(byTask[taskID], point)
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

type jsonNumber interface {
	String() string
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
