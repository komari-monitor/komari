package metric

import (
	"fmt"
	"strings"
	"time"
)

type Driver string

const (
	DriverSQLite     Driver = "sqlite"
	DriverMySQL      Driver = "mysql"
	DriverPostgreSQL Driver = "postgresql"
)

type MetricType string

const (
	TypeGauge     MetricType = "gauge"
	TypeCounter   MetricType = "counter"
	TypeHistogram MetricType = "histogram"
	TypeSummary   MetricType = "summary"
)

type Aggregation string

const (
	AggAvg    Aggregation = "avg"
	AggMin    Aggregation = "min"
	AggMax    Aggregation = "max"
	AggSum    Aggregation = "sum"
	AggCount  Aggregation = "count"
	AggP50    Aggregation = "p50"
	AggP95    Aggregation = "p95"
	AggP99    Aggregation = "p99"
	AggFirst  Aggregation = "first"
	AggLast   Aggregation = "last"
	AggRate   Aggregation = "rate"
	AggStdDev Aggregation = "stddev"
)

type Order string

const (
	OrderAsc  Order = "asc"
	OrderDesc Order = "desc"
)

type Definition struct {
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	Type          MetricType        `json:"type"`
	Unit          string            `json:"unit,omitempty"`
	RetentionDays int               `json:"retention_days,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at,omitempty"`
}

func (d Definition) withDefaults(defaultRetentionDays int) Definition {
	if d.Type == "" {
		d.Type = TypeGauge
	}
	if d.RetentionDays == 0 {
		d.RetentionDays = defaultRetentionDays
	}
	return d
}

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

type Point struct {
	MetricName string            `json:"metric_name"`
	EntityID   string            `json:"entity_id"`
	Timestamp  time.Time         `json:"timestamp"`
	Value      float64           `json:"value"`
	Tags       map[string]string `json:"tags,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

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
	return nil
}

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

type Query struct {
	MetricName string            `json:"metric_name"`
	EntityID   string            `json:"entity_id,omitempty"`
	Start      time.Time         `json:"start"`
	End        time.Time         `json:"end"`
	Tags       map[string]string `json:"tags,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Offset     int               `json:"offset,omitempty"`
	Order      Order             `json:"order,omitempty"`
}

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

func (q Query) normalized() Query {
	q.Start = q.Start.UTC()
	q.End = q.End.UTC()
	if q.Order == "" {
		q.Order = OrderAsc
	}
	return q
}

type AggregateQuery struct {
	Query
	Aggregation Aggregation   `json:"aggregation"`
	Interval    time.Duration `json:"interval"`
	FillEmpty   bool          `json:"fill_empty,omitempty"`
	// BucketLimit and BucketOffset page over the produced aggregate buckets, not
	// the underlying raw points. They are applied consistently across every
	// backend and aggregation type. The embedded Query.Limit/Query.Offset are
	// ignored for aggregation (they describe raw-point paging, which would mean
	// something different depending on whether the aggregation is pushed down to
	// SQL or computed in memory).
	BucketLimit  int `json:"bucket_limit,omitempty"`
	BucketOffset int `json:"bucket_offset,omitempty"`
}

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
	case AggAvg, AggMin, AggMax, AggSum, AggCount, AggP50, AggP95, AggP99, AggFirst, AggLast, AggRate, AggStdDev:
	default:
		return fmt.Errorf("%w: unsupported aggregation %q", ErrInvalidArgument, q.Aggregation)
	}
	return nil
}

type AggregatePoint struct {
	MetricName string    `json:"metric_name"`
	EntityID   string    `json:"entity_id,omitempty"`
	Bucket     time.Time `json:"bucket"`
	Value      float64   `json:"value"`
	Count      int       `json:"count"`
}

type Stats struct {
	Count  int       `json:"count"`
	Min    float64   `json:"min"`
	Max    float64   `json:"max"`
	Avg    float64   `json:"avg"`
	Sum    float64   `json:"sum"`
	P50    float64   `json:"p50"`
	P95    float64   `json:"p95"`
	P99    float64   `json:"p99"`
	First  float64   `json:"first"`
	Last   float64   `json:"last"`
	Rate   float64   `json:"rate"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	StdDev float64   `json:"std_dev"`
}
