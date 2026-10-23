package metricstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

// QueryMode selects exact samples, adaptive series, or exclusive-boundary lookups.
type QueryMode string

const (
	QueryPoints       QueryMode = "points"
	QuerySeries       QueryMode = "series"
	QueryLatestBefore QueryMode = "latest_before"
)

type QuerySource string

const (
	SourceAuto   QuerySource = "auto"
	SourceRaw    QuerySource = "raw"
	SourceRollup QuerySource = "rollup"
)

type QueryGrouping string

const (
	GroupMerged QueryGrouping = "merged"
	GroupEntity QueryGrouping = "entity"
	GroupSeries QueryGrouping = "series"
)

type IntervalRounding string

const (
	RoundFloor IntervalRounding = "floor"
	RoundCeil  IntervalRounding = "ceil"
)

// EntityScope deliberately distinguishes an empty authorized set from all entities.
// All and IDs are mutually exclusive. IDs are deduplicated without modifying callers.
type EntityScope struct {
	All bool
	IDs []string
}
type MetricSpec struct {
	Name         string
	Aggregations []metric.Aggregation
	// Positive intervals are explicit and are preserved, even for historical data.
	Interval time.Duration
	// Zero inherits Budget.PerSeries, then 500. -1 disables adaptive downsampling:
	// a zero Interval then selects the finest compatible tier, not a whole-window bucket.
	MaxPoints int
}

// PointBudget controls adaptive interval planning, not a raw-row SQL LIMIT.
// Total divides the point target across entities (metrics may be columns of one
// legacy record); it is not a hard cap across metrics, aggregations or tag variants.
// Raw queries and explicit intervals retain their exact historical semantics.
// Response adapters can impose their existing final sampling policy separately.
type PointBudget struct {
	Total     int
	PerSeries int
}
type FillPolicy struct {
	Gaps               bool
	PingNegativeAsNull bool
}

// QueryProjection controls retained result representations. The default keeps all
// source data for compatibility; chart requires Fill and retains only Filled.
type QueryProjection string

const (
	ProjectionFull  QueryProjection = "full"
	ProjectionChart QueryProjection = "chart"
)

type MetricsQuery struct {
	// Output omissions are opt-in; filtering and aggregation identity are unchanged.
	OmitTags           bool
	OmitLabels         bool
	CompactDefinitions bool
	Projection         QueryProjection
	Store              *metric.Store
	Mode               QueryMode
	Source             QuerySource
	Entities           EntityScope
	Specs              []MetricSpec
	Start, End         time.Time
	Tags               map[string]string
	Order              metric.Order
	Grouping           QueryGrouping
	Budget             PointBudget
	Rounding           IntervalRounding
	Fill               FillPolicy
	AllowMissing       bool
	Now                time.Time
}
type MetricsResult struct {
	Definitions map[string]metric.Definition
	Raw         map[string][]metric.Point
	Values      map[string]map[metric.Aggregation][]metric.AggregatePoint
	Intervals   map[string]time.Duration
	IsRaw       bool
	Filled      []MetricSeries
	// Latest is keyed by metric name, then entity; missing samples are absent.
	Latest map[string]map[string]metric.Point
}
type MetricSeries struct {
	MetricName, EntityID string
	Aggregation          metric.Aggregation
	Tags                 map[string]string
	Interval             time.Duration
	Points               []MetricValue
}
type MetricValue struct {
	Time         time.Time
	Value        *float64
	Count        int
	Tags, Labels map[string]string
}

// DownsampleInterval returns a standard interval that keeps rangeDuration
// within about maxPoints buckets, never finer than one second. roundUp is used
// by point budgets: flooring can exceed the budget, while compatibility queries
// preserve the historical floor behavior.
func DownsampleInterval(rangeDuration time.Duration, maxPoints int, roundUp bool) time.Duration {
	if maxPoints < 1 {
		maxPoints = 1
	}
	points := time.Duration(maxPoints)
	interval := (rangeDuration + points - 1) / points
	if interval < time.Second {
		return time.Second
	}
	if roundUp {
		return metric.CeilStandardInterval(interval)
	}
	return metric.FloorStandardInterval(interval)
}

// QueryMetrics is the shared query policy boundary. Raw and aggregate slices are
// transferred directly from batch primitives; nullable chart data is allocated only
// when explicitly requested by Fill. All work uses ctx and one captured clock.
func QueryMetrics(ctx context.Context, req MetricsQuery) (MetricsResult, error) {
	if ctx == nil {
		return MetricsResult{}, fmt.Errorf("%w: context is required", metric.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return MetricsResult{}, err
	}
	result := MetricsResult{Definitions: map[string]metric.Definition{}, Raw: map[string][]metric.Point{}, Values: map[string]map[metric.Aggregation][]metric.AggregatePoint{}, Intervals: map[string]time.Duration{}, Latest: map[string]map[string]metric.Point{}}
	if req.Mode == "" {
		req.Mode = QuerySeries
	}
	if req.Source == "" {
		req.Source = SourceAuto
	}
	if req.Grouping == "" {
		req.Grouping = GroupMerged
	}
	if req.Order == "" {
		req.Order = metric.OrderAsc
	}
	if req.Rounding == "" {
		req.Rounding = RoundFloor
	}
	if err := validateMetricsQuery(req); err != nil {
		return result, err
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	req.Start, req.End = req.Start.UTC(), req.End.UTC()
	req.Entities.IDs = uniqueQueryIDs(req.Entities.IDs)
	// Never let the primitive's empty entity filter broaden an empty authorized set.

	s := req.Store
	if s == nil {
		s = GetStore()
	}
	if s == nil {
		return result, fmt.Errorf("metric store not enabled")
	}
	names := make([]string, 0, len(req.Specs))
	for _, spec := range req.Specs {
		names = append(names, spec.Name)
	}
	loadDefinitions := s.GetMetrics
	if req.CompactDefinitions {
		loadDefinitions = s.GetMetricsCompact
	}
	if !req.Entities.All && len(req.Entities.IDs) == 0 {
		definitions, err := loadDefinitions(ctx, names)
		if err != nil {
			return result, err
		}
		result.Definitions = definitions
		result.IsRaw = req.Source == SourceRaw || (req.Source == SourceAuto && (req.Mode == QueryPoints || UsesRawWindow(req.Start, req.End, now)))
		if err := checkQueryDefinitions(names, definitions, req.AllowMissing); err != nil {
			return result, err
		}
		return result, ctx.Err()
	}
	if req.Mode == QueryLatestBefore {
		definitions, err := loadDefinitions(ctx, names)
		if err != nil {
			return result, err
		}
		result.Definitions = definitions
		if err := checkQueryDefinitions(names, definitions, req.AllowMissing); err != nil {
			return result, err
		}
		for _, name := range names {
			if _, ok := definitions[name]; !ok {
				continue
			}
			ids := req.Entities.IDs

			for _, id := range ids {
				if err := ctx.Err(); err != nil {
					return result, err
				}
				point, found, err := s.LatestBefore(ctx, name, id, req.End)
				if err != nil {
					return result, err
				}
				if found {
					if result.Latest[name] == nil {
						result.Latest[name] = map[string]metric.Point{}
					}
					result.Latest[name][id] = point
				}
			}
		}
		return result, ctx.Err()
	}
	result.IsRaw = req.Source == SourceRaw || (req.Source == SourceAuto && (req.Mode == QueryPoints || UsesRawWindow(req.Start, req.End, now)))
	if result.IsRaw {
		definitions, err := loadDefinitions(ctx, names)
		if err != nil {
			return result, err
		}
		result.Definitions = definitions
		if err := checkQueryDefinitions(names, definitions, req.AllowMissing); err != nil {
			return result, err
		}
		known := make([]string, 0, len(names))
		for _, name := range names {
			if _, ok := definitions[name]; ok {
				known = append(known, name)
				result.Intervals[name] = 0
			}
		}
		if len(known) > 0 {
			result.Raw, err = s.QueryBatch(ctx, metric.BatchQuery{MetricNames: known, EntityIDs: req.Entities.IDs, Start: req.Start, End: req.End, Tags: req.Tags, Order: req.Order, OmitTags: req.OmitTags, OmitLabels: req.OmitLabels, IncludeSeriesIdentity: req.Fill.Gaps || req.Fill.PingNegativeAsNull})
			if err != nil {
				return result, err
			}
		}
	} else {
		entityCount := len(req.Entities.IDs)
		if needsTotalEntityCount(req) {
			if req.Entities.All {
				count, err := s.CountEntities(ctx, names)
				if err != nil {
					return result, err
				}
				entityCount = count
			}
			// Explicit IDs are a conservative dictionary-count upper bound; never
			// scan every metric/tier simply to plan a response budget.
		}
		if entityCount < 1 {
			entityCount = 1
		}
		specs := make([]metric.BatchSeriesSpec, 0, len(req.Specs))
		for _, spec := range req.Specs {
			interval := spec.Interval
			if interval == 0 {
				points := spec.MaxPoints
				if points == 0 {
					points = req.Budget.PerSeries
				}
				if points == 0 {
					points = 500
				}
				if points == 500 && spec.MaxPoints == 0 && req.Budget.PerSeries == 0 && req.Budget.Total == -1 {
					points = -1
				}
				interval = time.Second
				if points != -1 {
					if req.Budget.Total > 0 {
						budget := req.Budget.Total / entityCount
						if budget < 1 {
							budget = 1
						}
						if budget < points {
							points = budget
						}
					}
					interval = DownsampleInterval(req.End.Sub(req.Start), points, req.Rounding == RoundCeil)
				}
				interval = s.CompatibleSeriesInterval(req.Start, now, interval)
				if interval <= 0 {
					interval = time.Second
				}
			}
			aggregations := spec.Aggregations
			if len(aggregations) == 0 {
				aggregations = []metric.Aggregation{metric.AggAvg}
			}
			specs = append(specs, metric.BatchSeriesSpec{MetricName: spec.Name, Aggregations: aggregations, Interval: interval, PreserveSeries: req.Grouping == GroupSeries, GroupByEntity: req.Grouping == GroupEntity})
			result.Intervals[spec.Name] = interval
		}
		batch, err := s.SeriesBatch(ctx, metric.BatchSeriesQuery{Specs: specs, EntityIDs: req.Entities.IDs, Start: req.Start, End: req.End, Tags: req.Tags, Order: req.Order, OmitTags: req.OmitTags, CompactDefinitions: req.CompactDefinitions, IncludeSeriesIdentity: req.Fill.Gaps || req.Fill.PingNegativeAsNull}, now)
		if err != nil {
			return result, err
		}
		result.Definitions, result.Values = batch.Definitions, batch.Values
		// SeriesBatch already loads metadata. Validate its result rather than issue a
		// second metadata query; unknown metrics otherwise look like empty series.
		if err := checkQueryDefinitions(names, batch.Definitions, req.AllowMissing); err != nil {
			return result, err
		}
		for _, name := range names {
			if _, ok := batch.Definitions[name]; !ok {
				delete(result.Intervals, name)
				delete(result.Values, name)
			}
		}
	}
	if req.Fill.Gaps || req.Fill.PingNegativeAsNull {
		filled, err := buildQuerySeriesOwned(ctx, req, result, req.Projection == ProjectionChart)
		if err != nil {
			return result, err
		}
		result.Filled = filled
		if req.Projection == ProjectionChart {
			result.Raw, result.Values = nil, nil
		}
	}
	return result, ctx.Err()
}

// needsTotalEntityCount excludes explicit intervals and unlimited specs: neither
// consumes the total adaptive budget. Keep its precedence aligned with planning.
func needsTotalEntityCount(req MetricsQuery) bool {
	if req.Budget.Total <= 0 {
		return false
	}
	for _, spec := range req.Specs {
		if spec.Interval != 0 {
			continue
		}
		points := spec.MaxPoints
		if points == 0 {
			points = req.Budget.PerSeries
		}
		if points != -1 {
			return true
		}
	}
	return false
}

func validateMetricsQuery(req MetricsQuery) error {
	invalid := func(message string) error { return fmt.Errorf("%w: %s", metric.ErrInvalidArgument, message) }
	if req.OmitTags && (req.Fill.Gaps || req.Fill.PingNegativeAsNull) {
		return invalid("omitting tags with filled output is unsupported: series identity must be preserved")
	}
	if req.Mode == QueryLatestBefore && (req.OmitTags || req.OmitLabels) {
		return invalid("latest-before output omissions are unsupported")
	}
	switch req.Mode {
	case QueryPoints, QuerySeries, QueryLatestBefore:
	default:
		return invalid("unsupported query mode")
	}
	switch req.Source {
	case SourceAuto, SourceRaw, SourceRollup:
	default:
		return invalid("unsupported query source")
	}
	switch req.Grouping {
	case GroupMerged, GroupEntity, GroupSeries:
	default:
		return invalid("unsupported query grouping")
	}
	switch req.Projection {
	case "", ProjectionFull:
	case ProjectionChart:
		if req.Mode == QueryLatestBefore || (!req.Fill.Gaps && !req.Fill.PingNegativeAsNull) {
			return invalid("chart projection requires fill and does not support latest-before")
		}
	default:
		return invalid("unsupported query projection")
	}
	switch req.Rounding {
	case RoundFloor, RoundCeil:
	default:
		return invalid("unsupported interval rounding")
	}
	if req.Order != metric.OrderAsc && req.Order != metric.OrderDesc {
		return invalid("unsupported query order")
	}
	if req.Entities.All && len(req.Entities.IDs) > 0 {
		return invalid("all entities and explicit IDs are mutually exclusive")
	}
	for _, id := range req.Entities.IDs {
		if strings.TrimSpace(id) == "" {
			return invalid("entity ID cannot be empty")
		}
	}
	if req.End.IsZero() {
		return invalid("end time is required")
	}
	if req.Mode != QueryLatestBefore && (req.Start.IsZero() || req.End.Before(req.Start)) {
		return invalid("valid start and end times are required")
	}
	if req.Mode == QueryLatestBefore && (len(req.Tags) > 0 || req.Source != SourceAuto) {
		return invalid("latest-before requires auto source and does not support tag filters")
	}
	if req.Mode == QueryPoints && req.Source == SourceRollup {
		return invalid("points mode requires raw or auto source")
	}
	if req.Mode == QueryLatestBefore && req.Entities.All {
		return invalid("latest-before requires explicit entity IDs")
	}
	if req.Budget.Total < -1 || req.Budget.PerSeries < -1 {
		return invalid("invalid point budget")
	}
	if len(req.Specs) == 0 {
		return invalid("at least one metric specification is required")
	}
	seen := map[string]bool{}
	for _, spec := range req.Specs {
		if strings.TrimSpace(spec.Name) == "" || seen[spec.Name] {
			return invalid("metric names must be nonempty and unique")
		}
		seen[spec.Name] = true
		if spec.Interval < 0 || spec.MaxPoints < -1 {
			return invalid("invalid metric interval or point budget")
		}
		for _, agg := range spec.Aggregations {
			// Reuse primitive aggregation validation even when the selected source is raw.
			if err := (metric.AggregateQuery{Query: metric.Query{MetricName: spec.Name, Start: req.End, End: req.End}, Aggregation: agg, Interval: time.Second}).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// UsesRawWindow selects the public adaptive raw window; its end boundary is strict.
func UsesRawWindow(start, end, now time.Time) bool {
	return end.Sub(start) <= 10*time.Minute && end.After(now.Add(-10*time.Minute))
}

func uniqueQueryIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out
}
func checkQueryDefinitions(names []string, definitions map[string]metric.Definition, allowMissing bool) error {
	if !allowMissing {
		for _, name := range names {
			if _, ok := definitions[name]; !ok {
				return fmt.Errorf("%w: metric %q", metric.ErrNotFound, name)
			}
		}
	}
	return nil
}

// querySeriesKey consumes storage's immutable, collision-free output identity.
// Manually constructed points (and low-level queries without the opt-in) retain
// the old canonical fallback. Neither grouping nor ordering uses a hash.
func querySeriesKey(entity string, tags map[string]string, identity *metric.SeriesIdentity) string {
	if key := identity.Key(); key != "" {
		return key
	}
	return metric.NewSeriesIdentity(entity, tags).Key()
}

func buildQuerySeries(ctx context.Context, req MetricsQuery, result MetricsResult) ([]MetricSeries, error) {
	return buildQuerySeriesOwned(ctx, req, result, false)
}

// detach is only valid for QueryMetrics-owned source maps. Release each source
// array after conversion, before allocating gaps or processing the next metric.
func buildQuerySeriesOwned(ctx context.Context, req MetricsQuery, result MetricsResult, detach bool) ([]MetricSeries, error) {
	out := []MetricSeries{}
	for _, spec := range req.Specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := result.Definitions[spec.Name]; !ok {
			continue
		}
		aggregations := spec.Aggregations
		if result.IsRaw {
			aggregations = []metric.Aggregation{""}
		} else if len(aggregations) == 0 {
			aggregations = []metric.Aggregation{metric.AggAvg}
		}
		for aggIndex, agg := range aggregations {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			var seenEntities map[string]bool
			if len(req.Entities.IDs) > 0 && (result.IsRaw || req.Grouping != GroupMerged) {
				seenEntities = make(map[string]bool, len(req.Entities.IDs))
			}
			groups := map[string]*MetricSeries{}
			add := func(entity string, tags map[string]string, identity *metric.SeriesIdentity, value MetricValue) {
				// Exact samples always preserve identity; aggregate identity is determined by
				// the requested primitive grouping and must not be collapsed again here.
				key := querySeriesKey(entity, tags, identity)
				series := groups[key]
				if series == nil {
					series = &MetricSeries{MetricName: spec.Name, EntityID: entity, Aggregation: agg, Tags: tags, Interval: result.Intervals[spec.Name]}
					groups[key] = series
					if seenEntities != nil {
						seenEntities[entity] = true
					}
				}
				if req.Fill.PingNegativeAsNull && (spec.Name == MetricPingLatency || spec.Name == MetricPingLoss) && value.Value != nil && *value.Value == -1 {
					value.Value = nil
				}
				series.Points = append(series.Points, value)
			}
			if result.IsRaw {
				for i, p := range result.Raw[spec.Name] {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					v := p.Value
					add(p.EntityID, p.Tags, p.SeriesIdentity, MetricValue{Time: p.Timestamp, Value: &v, Count: 1, Tags: p.Tags, Labels: p.Labels})
				}
			} else {
				for i, p := range result.Values[spec.Name][agg] {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					v := p.Value
					add(p.EntityID, p.Tags, p.SeriesIdentity, MetricValue{Time: p.Bucket, Value: &v, Count: p.Count, Tags: p.Tags})
				}
			}
			if detach {
				if result.IsRaw {
					delete(result.Raw, spec.Name)
				} else {
					// Duplicate aggregation specs historically produce duplicate series.
					needed := false
					for _, next := range aggregations[aggIndex+1:] {
						needed = needed || next == agg
					}
					if !needed {
						delete(result.Values[spec.Name], agg)
					}
				}
			}
			// Seed missing entities directly; do not scan all groups for each ID or
			// allocate a placeholder point only to discard it.
			seed := func(id string) {
				groups[querySeriesKey(id, req.Tags, nil)] = &MetricSeries{MetricName: spec.Name, EntityID: id, Aggregation: agg, Tags: req.Tags, Interval: result.Intervals[spec.Name]}
				if seenEntities != nil {
					seenEntities[id] = true
				}
			}
			if result.IsRaw || req.Grouping != GroupMerged {
				for i, id := range req.Entities.IDs {
					if i%256 == 0 {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
					}
					if !seenEntities[id] {
						seed(id)
					}
				}
			}
			if len(groups) == 0 {
				ids := req.Entities.IDs
				if len(ids) == 0 || (!result.IsRaw && req.Grouping == GroupMerged) {
					ids = []string{""}
				}
				for _, id := range ids {
					seed(id)
				}
			}

			keys := make([]string, 0, len(groups))
			for key := range groups {
				if len(keys)%256 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				keys = append(keys, key)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			sort.Strings(keys)
			for _, key := range keys {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				series := *groups[key]
				sort.SliceStable(series.Points, func(i, j int) bool { return series.Points[i].Time.Before(series.Points[j].Time) })
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if req.Fill.Gaps {
					var err error
					series, err = fillMetricSeriesOwned(ctx, series, req.Start, req.End)
					if err != nil {
						return nil, err
					}
				}
				if req.Order == metric.OrderDesc {
					for i, j := 0, len(series.Points)-1; i < j; i, j = i+1, j-1 {
						if i%256 == 0 {
							if err := ctx.Err(); err != nil {
								return nil, err
							}
						}
						series.Points[i], series.Points[j] = series.Points[j], series.Points[i]
					}
				}
				out = append(out, series)
			}
		}
	}
	return out, ctx.Err()
}

// FillMetricSeries matches the public chart's adaptive lower-quartile gap policy.
func FillMetricSeries(series MetricSeries, start, end time.Time) MetricSeries {
	filled, _ := fillMetricSeries(context.Background(), series, start, end)
	return filled
}

func fillMetricSeries(ctx context.Context, series MetricSeries, start, end time.Time) (MetricSeries, error) {
	if err := ctx.Err(); err != nil {
		return series, err
	}
	// Public callers retain ownership, including spare capacity. Never write there.
	series.Points = append([]MetricValue(nil), series.Points...)
	return fillMetricSeriesOwned(ctx, series, start, end)
}

// fillMetricSeriesOwned inserts gaps backwards into exclusively owned storage.
// Dense series reuse their point array; sparse series grow only when capacity
// cannot accommodate the exact number of gaps, rather than always copying into
// a second full point array. The public wrapper above remains non-mutating.
func fillMetricSeriesOwned(ctx context.Context, series MetricSeries, start, end time.Time) (MetricSeries, error) {
	if err := ctx.Err(); err != nil {
		return series, err
	}
	deltas := make([]time.Duration, 0, len(series.Points))
	for i := 1; i < len(series.Points); i++ {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return series, err
			}
		}
		if d := series.Points[i].Time.Sub(series.Points[i-1].Time); d > 0 {
			deltas = append(deltas, d)
		}
	}
	if err := ctx.Err(); err != nil {
		return series, err
	}
	expected := series.Interval
	if len(deltas) >= 2 {
		sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
		if observed := deltas[(len(deltas)-1)/4]; observed > expected {
			expected = observed
		}
	}
	if err := ctx.Err(); err != nil {
		return series, err
	}
	if expected > 0 {
		series.Interval = expected
	}
	null := func(at time.Time) MetricValue { return MetricValue{Time: at.UTC(), Tags: series.Tags} }
	n := len(series.Points)
	if n == 0 {
		series.Points = append(series.Points, null(start), null(end))
		return series, ctx.Err()
	}
	leading := start.Before(series.Points[0].Time)
	gap := func(i int) bool {
		return i > 0 && expected > 0 && series.Points[i-1].Value != nil && series.Points[i].Value != nil && series.Points[i].Time.Sub(series.Points[i-1].Time) > expected+expected/2
	}
	extra := 0
	if leading {
		extra++
	}
	for i := 1; i < n; i++ {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return series, err
			}
		}
		if gap(i) {
			extra++
		}
	}
	if extra == 0 {
		return series, ctx.Err()
	}
	if cap(series.Points) < n+extra {
		points := make([]MetricValue, n+extra)
		copy(points, series.Points)
		series.Points = points
	} else {
		series.Points = series.Points[:n+extra]
	}
	write := n + extra - 1
	for i := n - 1; i >= 0; i-- {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return series, err
			}
		}
		// Decide before moving: write may equal i, but never precedes it.
		insert := gap(i)
		var at time.Time
		if insert {
			at = series.Points[i-1].Time.Add(expected)
		}
		series.Points[write] = series.Points[i]
		write--
		if insert {
			series.Points[write] = null(at)
			write--
		}
	}
	if leading {
		series.Points[0] = null(start)
	}
	return series, ctx.Err()
}
