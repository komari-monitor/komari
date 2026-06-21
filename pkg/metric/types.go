package metric

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Driver names a supported database backend.
//
// Driver 表示 metric store 使用的数据库后端类型。
type Driver string

const (
	// DriverSQLite selects the SQLite backend.
	//
	// DriverSQLite 选择 SQLite 后端。
	DriverSQLite Driver = "sqlite"
	// DriverMySQL selects the MySQL backend.
	//
	// DriverMySQL 选择 MySQL 后端。
	DriverMySQL Driver = "mysql"
	// DriverPostgreSQL selects the PostgreSQL backend.
	//
	// DriverPostgreSQL 选择 PostgreSQL 后端。
	DriverPostgreSQL Driver = "postgresql"
)

// MetricType describes the semantic type of a metric.
//
// MetricType 表示指标的语义类型。
type MetricType string

const (
	// TypeGauge represents a point-in-time value.
	//
	// TypeGauge 表示某一时刻的数值。
	TypeGauge MetricType = "gauge"
	// TypeCounter represents a monotonically increasing counter.
	//
	// TypeCounter 表示单调递增计数器。
	TypeCounter MetricType = "counter"
	// TypeHistogram represents histogram-style measurements.
	//
	// TypeHistogram 表示直方图类度量。
	TypeHistogram MetricType = "histogram"
	// TypeSummary represents pre-summarized measurements.
	//
	// TypeSummary 表示预汇总类度量。
	TypeSummary MetricType = "summary"
)

// Aggregation names a supported aggregation operation.
//
// Aggregation 表示聚合方式，例如 avg、p95 或 rate。
type Aggregation string

const (
	// AggAvg computes the arithmetic mean.
	//
	// AggAvg 计算算术平均值。
	AggAvg Aggregation = "avg"
	// AggMin computes the minimum value.
	//
	// AggMin 计算最小值。
	AggMin Aggregation = "min"
	// AggMax computes the maximum value.
	//
	// AggMax 计算最大值。
	AggMax Aggregation = "max"
	// AggSum computes the sum of values.
	//
	// AggSum 计算值的总和。
	AggSum Aggregation = "sum"
	// AggCount counts the number of points.
	//
	// AggCount 计算点数量。
	AggCount Aggregation = "count"
	// AggP50 computes the 50th percentile.
	//
	// AggP50 计算第 50 百分位。
	AggP50 Aggregation = "p50"
	// AggP95 computes the 95th percentile.
	//
	// AggP95 计算第 95 百分位。
	AggP95 Aggregation = "p95"
	// AggP99 computes the 99th percentile.
	//
	// AggP99 计算第 99 百分位。
	AggP99 Aggregation = "p99"
	// AggFirst returns the first value in time order.
	//
	// AggFirst 返回时间顺序上的第一个值。
	AggFirst Aggregation = "first"
	// AggLast returns the last value in time order.
	//
	// AggLast 返回时间顺序上的最后一个值。
	AggLast Aggregation = "last"
	// AggRate computes the reset-aware per-second rate.
	//
	// AggRate 计算可处理重置的每秒速率。
	AggRate Aggregation = "rate"
	// AggStdDev computes the population standard deviation.
	//
	// AggStdDev 计算总体标准差。
	AggStdDev Aggregation = "stddev"
)

// Order controls chronological query ordering.
//
// Order 表示查询结果按时间升序或降序排列。
type Order string

const (
	// OrderAsc orders points from oldest to newest.
	//
	// OrderAsc 按从旧到新的顺序排列点。
	OrderAsc Order = "asc"
	// OrderDesc orders points from newest to oldest.
	//
	// OrderDesc 按从新到旧的顺序排列点。
	OrderDesc Order = "desc"
)

// Definition describes a metric and its metadata.
//
// Definition 描述一个指标的元数据和保留策略。
type Definition struct {
	// Name is the unique metric name.
	//
	// Name 是唯一指标名称。
	Name string `json:"name"`
	// Description is optional human-readable metric text.
	//
	// Description 是可选的人类可读指标说明。
	Description string `json:"description,omitempty"`
	// Type describes the metric's semantic type.
	//
	// Type 描述指标的语义类型。
	Type MetricType `json:"type"`
	// Unit names the value unit, such as bytes or percent.
	//
	// Unit 表示数值单位，例如 bytes 或 percent。
	Unit string `json:"unit,omitempty"`
	// RetentionDays controls raw-point retention for this metric.
	//
	// RetentionDays 控制该指标原始点的保留天数。
	RetentionDays int `json:"retention_days,omitempty"`
	// Metadata stores caller-defined metric metadata.
	//
	// Metadata 保存调用方定义的指标元数据。
	Metadata map[string]string `json:"metadata,omitempty"`
	// CreatedAt records when the metric definition was created.
	//
	// CreatedAt 记录指标定义创建时间。
	CreatedAt time.Time `json:"created_at,omitempty"`
	// UpdatedAt records when the metric definition was last updated.
	//
	// UpdatedAt 记录指标定义最后更新时间。
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// withDefaults fills default values on a metric definition.
//
// withDefaults 为指标定义填充默认类型和默认保留天数。
func (d Definition) withDefaults(defaultRetentionDays int) Definition {
	if d.Type == "" {
		d.Type = TypeGauge
	}
	if d.RetentionDays == 0 {
		d.RetentionDays = defaultRetentionDays
	}
	return d
}

// Validate checks whether the value is well formed.
//
// Validate 检查指标定义是否合法。
func (d Definition) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	switch d.Type {
	case "", TypeGauge, TypeCounter, TypeHistogram, TypeSummary:
	default:
		return fmt.Errorf("%w: unsupported metric type %q", ErrInvalidArgument, d.Type)
	}
	if d.RetentionDays < 0 {
		return fmt.Errorf("%w: retention days cannot be negative", ErrInvalidArgument)
	}
	return nil
}

// Point stores one metric sample.
//
// Point 表示某个实体在某一时刻的一条指标采样。
type Point struct {
	// MetricName names the metric this sample belongs to.
	//
	// MetricName 表示该采样所属的指标名称。
	MetricName string `json:"metric_name"`
	// EntityID identifies the entity that emitted the sample.
	//
	// EntityID 标识产生该采样的实体。
	EntityID string `json:"entity_id"`
	// Timestamp is the sample time.
	//
	// Timestamp 是采样时间。
	Timestamp time.Time `json:"timestamp"`
	// Value is the numeric sample value.
	//
	// Value 是采样的数值。
	Value float64 `json:"value"`
	// Tags identify the logical series within a metric and entity.
	//
	// Tags 标识同一指标和实体下的逻辑序列。
	Tags map[string]string `json:"tags,omitempty"`
	// Labels carry extra metadata that does not define series identity.
	//
	// Labels 携带不参与序列身份判定的额外元数据。
	Labels map[string]string `json:"labels,omitempty"`
}

// Validate checks whether the value is well formed.
//
// Validate 检查采样点是否包含必要字段。
func (p Point) Validate() error {
	if strings.TrimSpace(p.MetricName) == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if strings.TrimSpace(p.EntityID) == "" {
		return fmt.Errorf("%w: entity id is required", ErrInvalidArgument)
	}
	if p.Timestamp.IsZero() {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidArgument)
	}
	if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
		return fmt.Errorf("%w: value must be finite", ErrInvalidArgument)
	}
	return nil
}

// normalized returns a canonical form of the value.
//
// normalized 将采样点时间规范化为 UTC，并补齐空标签和标签说明 map。
func (p Point) normalized() Point {
	p.Timestamp = p.Timestamp.UTC()
	if p.Tags == nil {
		p.Tags = map[string]string{}
	}
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	return p
}

// Query loads raw metric points matching a query.
//
// Query 描述原始点查询条件，包括指标、实体、时间范围、标签和分页。
type Query struct {
	// MetricName restricts the query to one metric.
	//
	// MetricName 将查询限定到单个指标。
	MetricName string `json:"metric_name"`
	// EntityID optionally restricts the query to one entity.
	//
	// EntityID 可选地将查询限定到单个实体。
	EntityID string `json:"entity_id,omitempty"`
	// Start is the inclusive query start time.
	//
	// Start 是包含边界的查询起始时间。
	Start time.Time `json:"start"`
	// End is the inclusive query end time.
	//
	// End 是包含边界的查询结束时间。
	End time.Time `json:"end"`
	// Tags filters points by exact tag key/value matches.
	//
	// Tags 按标签键值精确匹配过滤采样点。
	Tags map[string]string `json:"tags,omitempty"`
	// Limit limits the number of raw points returned.
	//
	// Limit 限制返回的原始点数量。
	Limit int `json:"limit,omitempty"`
	// Offset skips this many raw points before returning results.
	//
	// Offset 在返回结果前跳过指定数量的原始点。
	Offset int `json:"offset,omitempty"`
	// Order controls chronological result ordering.
	//
	// Order 控制结果的时间顺序。
	Order Order `json:"order,omitempty"`
}

// Validate checks whether the value is well formed.
//
// Validate 检查原始查询条件是否合法。
func (q Query) Validate() error {
	if strings.TrimSpace(q.MetricName) == "" {
		return fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if q.Start.IsZero() || q.End.IsZero() {
		return fmt.Errorf("%w: start and end time are required", ErrInvalidArgument)
	}
	if q.End.Before(q.Start) {
		return fmt.Errorf("%w: end time cannot be before start time", ErrInvalidArgument)
	}
	if q.Limit < 0 || q.Offset < 0 {
		return fmt.Errorf("%w: limit and offset cannot be negative", ErrInvalidArgument)
	}
	switch q.Order {
	case "", OrderAsc, OrderDesc:
	default:
		return fmt.Errorf("%w: unsupported order %q", ErrInvalidArgument, q.Order)
	}
	return nil
}

// normalized returns a canonical form of the value.
//
// normalized 将查询时间规范化为 UTC，并设置默认排序。
func (q Query) normalized() Query {
	q.Start = q.Start.UTC()
	q.End = q.End.UTC()
	if q.Order == "" {
		q.Order = OrderAsc
	}
	return q
}

// AggregateQuery describes a bucketed aggregate query.
//
// AggregateQuery 描述按固定时间间隔分桶后的聚合查询。
type AggregateQuery struct {
	// Query supplies the raw series filter and time window.
	//
	// Query 提供原始序列过滤条件和时间窗口。
	Query
	// Aggregation selects the bucket aggregation to compute.
	//
	// Aggregation 选择要计算的桶聚合方式。
	Aggregation Aggregation `json:"aggregation"`
	// Interval is the width of each aggregate bucket.
	//
	// Interval 是每个聚合桶的宽度。
	Interval time.Duration `json:"interval"`
	// FillEmpty emits zero-count buckets for empty intervals.
	//
	// FillEmpty 会为空时间段输出零计数桶。
	FillEmpty bool `json:"fill_empty,omitempty"`
	// BucketLimit and BucketOffset page over the produced aggregate buckets, not
	// the underlying raw points. They are applied consistently across every
	// backend and aggregation type. The embedded Query.Limit/Query.Offset are
	// ignored for aggregation (they describe raw-point paging, which would mean
	// something different depending on whether the aggregation is pushed down to
	// SQL or computed in memory).
	//
	// BucketLimit 和 BucketOffset 对生成的聚合桶分页，而不是对底层原始点分页。
	// 它们会在所有后端和聚合类型上保持一致。嵌入的 Query.Limit/Query.Offset
	// 在聚合中会被忽略，因为它们描述的是原始点分页；根据聚合是下推到 SQL 还是在
	// 内存计算，这会产生不同语义。
	BucketLimit int `json:"bucket_limit,omitempty"`
	// BucketOffset skips this many aggregate buckets before returning results.
	//
	// BucketOffset 在返回结果前跳过指定数量的聚合桶。
	BucketOffset int `json:"bucket_offset,omitempty"`
}

// Validate checks whether the value is well formed.
//
// Validate 检查聚合查询的时间间隔、分页和聚合类型是否合法。
func (q AggregateQuery) Validate() error {
	if err := q.Query.Validate(); err != nil {
		return err
	}
	if q.Interval <= 0 {
		return fmt.Errorf("%w: aggregate interval must be positive", ErrInvalidArgument)
	}
	if q.BucketLimit < 0 || q.BucketOffset < 0 {
		return fmt.Errorf("%w: bucket limit and offset cannot be negative", ErrInvalidArgument)
	}
	switch q.Aggregation {
	case AggAvg, AggMin, AggMax, AggSum, AggCount, AggFirst, AggLast, AggRate, AggStdDev:
	default:
		// Any percentile (p50, p95, p99, p99.9, ...) is also valid. The fixed
		// AggP50/AggP95/AggP99 constants fall through to here as well.
		if !isPercentile(q.Aggregation) {
			return fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidArgument, q.Aggregation)
		}
	}
	return nil
}

// AggregatePoint stores one aggregate bucket result.
//
// AggregatePoint 表示一个聚合桶的结果。
type AggregatePoint struct {
	// MetricName is the metric represented by the bucket.
	//
	// MetricName 是该桶代表的指标名称。
	MetricName string `json:"metric_name"`
	// EntityID is the entity represented by the bucket when one was requested.
	//
	// EntityID 是请求限定实体时该桶代表的实体。
	EntityID string `json:"entity_id,omitempty"`
	// Bucket is the bucket start time.
	//
	// Bucket 是桶起始时间。
	Bucket time.Time `json:"bucket"`
	// Value is the computed aggregate value.
	//
	// Value 是计算出的聚合值。
	Value float64 `json:"value"`
	// Count is the number of points represented by the bucket.
	//
	// Count 是该桶代表的点数量。
	Count int `json:"count"`
}

// Stats stores or computes summary statistics for a point series.
//
// Stats 表示一段原始点序列的统计摘要。
type Stats struct {
	// Count is the number of points in the series.
	//
	// Count 是序列中的点数量。
	Count int `json:"count"`
	// Min is the minimum value.
	//
	// Min 是最小值。
	Min float64 `json:"min"`
	// Max is the maximum value.
	//
	// Max 是最大值。
	Max float64 `json:"max"`
	// Avg is the arithmetic mean.
	//
	// Avg 是算术平均值。
	Avg float64 `json:"avg"`
	// Sum is the sum of all values.
	//
	// Sum 是所有值的总和。
	Sum float64 `json:"sum"`
	// P50 is the 50th percentile.
	//
	// P50 是第 50 百分位。
	P50 float64 `json:"p50"`
	// P95 is the 95th percentile.
	//
	// P95 是第 95 百分位。
	P95 float64 `json:"p95"`
	// P99 is the 99th percentile.
	//
	// P99 是第 99 百分位。
	P99 float64 `json:"p99"`
	// First is the first value in time order.
	//
	// First 是时间顺序上的第一个值。
	First float64 `json:"first"`
	// Last is the last value in time order.
	//
	// Last 是时间顺序上的最后一个值。
	Last float64 `json:"last"`
	// Rate is the reset-aware per-second rate.
	//
	// Rate 是可处理重置的每秒速率。
	Rate float64 `json:"rate"`
	// Start is the first point timestamp.
	//
	// Start 是第一个点的时间戳。
	Start time.Time `json:"start"`
	// End is the last point timestamp.
	//
	// End 是最后一个点的时间戳。
	End time.Time `json:"end"`
	// StdDev is the population standard deviation.
	//
	// StdDev 是总体标准差。
	StdDev float64 `json:"std_dev"`
}
