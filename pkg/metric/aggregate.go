package metric

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// AggregatePoints groups raw points into time buckets and computes aggregate values.
//
// AggregatePoints 在内存中按查询配置将原始点分桶并聚合。
func AggregatePoints(points []Point, query AggregateQuery) ([]AggregatePoint, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if len(points) == 0 {
		if query.FillEmpty {
			return emptyBuckets(query), nil
		}
		return []AggregatePoint{}, nil
	}

	points = sortedPoints(points)
	groups := make(map[int64][]Point)
	for _, point := range points {
		bucket := alignTime(point.Timestamp, query.Interval).UnixNano()
		groups[bucket] = append(groups[bucket], point)
	}

	var buckets []int64
	if query.FillEmpty {
		for t := alignTime(query.Start, query.Interval); !t.After(query.End); t = t.Add(query.Interval) {
			buckets = append(buckets, t.UnixNano())
		}
	} else {
		for bucket := range groups {
			buckets = append(buckets, bucket)
		}
		sort.Slice(buckets, func(i, j int) bool { return buckets[i] < buckets[j] })
	}

	out := make([]AggregatePoint, 0, len(buckets))
	for _, bucket := range buckets {
		group := groups[bucket]
		value, err := aggregateValue(group, query.Aggregation)
		if err != nil {
			return nil, err
		}
		out = append(out, AggregatePoint{
			MetricName: query.MetricName,
			EntityID:   query.EntityID,
			Bucket:     time.Unix(0, bucket).UTC(),
			Value:      value,
			Count:      len(group),
		})
	}
	return out, nil
}

// CalculateStats computes summary statistics for a point series.
//
// CalculateStats 基于一组点计算统计摘要，包括均值、百分位、首尾值和标准差。
func CalculateStats(points []Point) (Stats, error) {
	if len(points) == 0 {
		// Distinguish "the metric/range yielded no samples" from "the metric
		// definition does not exist" (which surfaces as ErrNotFound elsewhere).
		return Stats{}, ErrNoData
	}
	points = sortedPoints(points)
	avg, _ := aggregateValue(points, AggAvg)
	min, _ := aggregateValue(points, AggMin)
	max, _ := aggregateValue(points, AggMax)
	sum, _ := aggregateValue(points, AggSum)
	p50, _ := aggregateValue(points, AggP50)
	p95, _ := aggregateValue(points, AggP95)
	p99, _ := aggregateValue(points, AggP99)
	first, _ := aggregateValue(points, AggFirst)
	last, _ := aggregateValue(points, AggLast)
	rate, _ := aggregateValue(points, AggRate)

	var variance float64
	for _, p := range points {
		diff := p.Value - avg
		variance += diff * diff
	}

	return Stats{
		Count:  len(points),
		Min:    min,
		Max:    max,
		Avg:    avg,
		Sum:    sum,
		P50:    p50,
		P95:    p95,
		P99:    p99,
		First:  first,
		Last:   last,
		Rate:   rate,
		Start:  points[0].Timestamp,
		End:    points[len(points)-1].Timestamp,
		StdDev: math.Sqrt(variance / float64(len(points))),
	}, nil
}

// aggregateValue computes one aggregation over a point group.
//
// aggregateValue 对一组点执行单个聚合类型的计算。
func aggregateValue(points []Point, agg Aggregation) (float64, error) {
	if len(points) == 0 {
		return 0, nil
	}
	switch agg {
	case AggAvg:
		sum, _ := aggregateValue(points, AggSum)
		return sum / float64(len(points)), nil
	case AggMin:
		v := points[0].Value
		for _, p := range points[1:] {
			v = math.Min(v, p.Value)
		}
		return v, nil
	case AggMax:
		v := points[0].Value
		for _, p := range points[1:] {
			v = math.Max(v, p.Value)
		}
		return v, nil
	case AggSum:
		var sum float64
		for _, p := range points {
			sum += p.Value
		}
		return sum, nil
	case AggCount:
		return float64(len(points)), nil
	case AggFirst:
		// Callers (AggregatePoints, CalculateStats) pass time-ordered slices.
		return points[0].Value, nil
	case AggLast:
		return points[len(points)-1].Value, nil
	case AggRate:
		return counterRate(points), nil
	case AggStdDev:
		return stdDevPop(points), nil
	default:
		// Any percentile (p50, p95, p99, and arbitrary pXX / pXX.X) is computed
		// here via linear interpolation over the sorted values. The fixed
		// AggP50/AggP95/AggP99 constants are just common cases of this.
		if frac, ok := parsePercentile(agg); ok {
			return percentile(points, frac), nil
		}
		return 0, fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidArgument, agg)
	}
}

// counterRate computes a per-second rate of change that is resilient to counter
// resets. It walks the time-ordered series and sums only positive deltas; a
// decrease is treated as a reset (the counter restarted) and contributes zero
// rather than a negative spike. For a strictly increasing counter this equals
// (last-first)/seconds. For a gauge it yields the total upward movement per
// second, which is a stable definition for an otherwise ill-defined quantity.
//
// counterRate 计算能抵抗计数器重置的每秒变化率。它会遍历按时间排序的序列，
// 并且只累加正向增量；当值下降时会被视为重置（计数器重新开始），该段贡献为零，
// 而不是产生负向尖峰。对于严格递增的计数器，这等于 (last-first)/seconds。
// 对于 gauge，它表示每秒总上升量，为这种本来定义不明确的量提供稳定定义。
func counterRate(points []Point) float64 {
	if len(points) < 2 {
		return 0
	}
	// Input is time-ordered by the callers; avoid an extra sort here.
	seconds := points[len(points)-1].Timestamp.Sub(points[0].Timestamp).Seconds()
	if seconds <= 0 {
		return 0
	}
	var increase float64
	for i := 1; i < len(points); i++ {
		delta := points[i].Value - points[i-1].Value
		if delta > 0 {
			increase += delta
		}
	}
	return increase / seconds
}

// stdDevPop computes the population standard deviation (dividing by N), matching
// SQL STDDEV_POP and the StdDev field produced by CalculateStats.
//
// stdDevPop 计算总体标准差（除以 N），与 SQL 的 STDDEV_POP 以及
// CalculateStats 返回的 StdDev 语义保持一致。
func stdDevPop(points []Point) float64 {
	if len(points) == 0 {
		return 0
	}
	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	mean := sum / float64(len(points))
	var variance float64
	for _, p := range points {
		d := p.Value - mean
		variance += d * d
	}
	return math.Sqrt(variance / float64(len(points)))
}

// percentile computes a quantile from point values.
//
// percentile 提取点值、排序，并计算指定小数形式的百分位。
func percentile(points []Point, p float64) float64 {
	values := make([]float64, len(points))
	for i, point := range points {
		values[i] = point.Value
	}
	sort.Float64s(values)
	return percentileSorted(values, p)
}

// percentileSorted returns the linear-interpolation percentile of an
// already-sorted slice. Shared by percentile() and the raw-value paths so the
// interpolation method stays identical everywhere.
//
// percentileSorted 基于已排序切片用线性插值计算百分位，供原始值路径和
// percentile 共用，确保所有路径的插值方法一致。
func percentileSorted(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	pos := p * float64(len(values)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return values[lower]
	}
	weight := pos - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

// alignTime floors a timestamp to the start of its interval bucket.
//
// alignTime 将时间向下对齐到指定间隔的桶起点。
func alignTime(t time.Time, interval time.Duration) time.Time {
	nano := t.UTC().UnixNano()
	size := interval.Nanoseconds()
	// Floor division toward negative infinity so timestamps before the Unix
	// epoch (negative nanos) align to the bucket start rather than rounding up.
	// Go's % returns a remainder with the dividend's sign, so normalize it.
	rem := ((nano % size) + size) % size
	return time.Unix(0, nano-rem).UTC()
}

// emptyBuckets builds zero-count aggregate buckets for an empty range.
//
// emptyBuckets 根据查询范围生成空聚合桶，用于 FillEmpty 场景。
func emptyBuckets(query AggregateQuery) []AggregatePoint {
	var out []AggregatePoint
	for t := alignTime(query.Start, query.Interval); !t.After(query.End); t = t.Add(query.Interval) {
		out = append(out, AggregatePoint{
			MetricName: query.MetricName,
			EntityID:   query.EntityID,
			Bucket:     t,
		})
	}
	return out
}
