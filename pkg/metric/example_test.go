package metric_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

// Example_rollupTags demonstrates tagged rollups with automatic series routing.
//
// Example_rollupTags 演示带标签的 rollup 以及 Series 自动路由读取。
func Example_rollupTags() {
	ctx := context.Background()
	base := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)

	store, err := metric.Open(ctx, metric.SQLite(
		"file:metric-example?mode=memory&cache=shared",
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: 2 * time.Minute,
			Tiers: []metric.RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
			},
		}),
	))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateMetric(ctx, metric.Definition{
		Name: "gpu.usage",
		Type: metric.TypeGauge,
		Unit: "%",
	}); err != nil {
		log.Fatal(err)
	}

	var points []metric.Point
	for i := 0; i < 10; i++ {
		ts := base.Add(time.Duration(i) * time.Second)
		points = append(points,
			metric.Point{
				MetricName: "gpu.usage",
				EntityID:   "host-1",
				Timestamp:  ts,
				Value:      float64(10 + i),
				Tags:       map[string]string{"device_index": "0"},
			},
			metric.Point{
				MetricName: "gpu.usage",
				EntityID:   "host-1",
				Timestamp:  ts,
				Value:      float64(80 + i),
				Tags:       map[string]string{"device_index": "1"},
			},
		)
	}
	if err := store.WriteBatch(ctx, points); err != nil {
		log.Fatal(err)
	}

	// Compact builds one rollup series per tag set. Raw points older than the
	// policy's RawRetention are deleted after their rollups are materialized.
	now := base.Add(time.Hour)
	if _, err := store.Compact(ctx, now); err != nil {
		log.Fatal(err)
	}

	series, err := store.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "gpu.usage",
			EntityID:   "host-1",
			Start:      base,
			End:        base.Add(time.Minute),
			Tags:       map[string]string{"device_index": "0"},
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("device 0: count=%d avg=%.1f\n", series[0].Count, series[0].Value)

	// Output:
	// device 0: count=10 avg=14.5
}
