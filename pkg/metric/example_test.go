package metric_test

import (
	"context"
	"fmt"
	"log"
	"strings"
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

// Example_agentFleetLifecycle demonstrates a realistic Komari-style fleet:
// 50 agents report system metrics every second, ping tasks are represented as a
// tagged ping metric, and lifecycle events clean up task and agent series.
//
// Example_agentFleetLifecycle 演示贴近 Komari 的实际场景：50 个 agent 每秒上报系统
// 指标，ping task 用带标签的 ping 指标表示，并通过生命周期事件清理 task 和 agent 序列。
func Example_agentFleetLifecycle() {
	ctx := context.Background()
	base := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)

	store, err := metric.Open(ctx, metric.SQLite(
		"file:metric-agent-fleet?mode=memory&cache=shared",
		metric.WithRollupPolicy(metric.RollupPolicy{
			RawRetention: 2 * time.Minute,
			Tiers: []metric.RollupTier{
				{Interval: time.Minute, Retention: 24 * time.Hour},
				{Interval: 5 * time.Minute, Retention: 30 * 24 * time.Hour},
			},
		}),
	))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	for _, def := range []metric.Definition{
		{Name: "cpu.usage", Type: metric.TypeGauge, Unit: "%"},
		{Name: "mem.usage", Type: metric.TypeGauge, Unit: "%"},
		{Name: "disk.usage", Type: metric.TypeGauge, Unit: "%"},
		{Name: "swap.usage", Type: metric.TypeGauge, Unit: "%"},
		{Name: "net.in.bytes", Type: metric.TypeCounter, Unit: "bytes"},
		{Name: "net.out.bytes", Type: metric.TypeCounter, Unit: "bytes"},
		{Name: "process.count", Type: metric.TypeGauge, Unit: "count"},
		{Name: "gpu.usage", Type: metric.TypeGauge, Unit: "%"},
		{Name: "ping.latency_ms", Type: metric.TypeGauge, Unit: "ms"},
	} {
		if err := store.UpsertMetric(ctx, def); err != nil {
			log.Fatal(err)
		}
	}

	agents := makeAgents(50)
	pingTasks := []string{"pingtask1", "pingtask2"}
	for sec := 0; sec < 120; sec++ {
		ts := base.Add(time.Duration(sec) * time.Second)

		switch sec {
		case 45:
			// A ping task is deleted by the admin. Because task_id is a tag, one
			// DeleteSeries removes that task's records for every agent, including
			// already materialized rollups.
			if _, err := store.DeleteSeries(ctx, metric.Query{
				MetricName: "ping.latency_ms",
				Tags:       map[string]string{"task_id": "pingtask2"},
			}); err != nil {
				log.Fatal(err)
			}
			pingTasks = []string{"pingtask1"}
		case 70:
			// The admin clears all ping task records while keeping the task
			// definitions themselves. Deleting the metric data is the simplest
			// reset; UpsertMetric recreates the definition before new samples land.
			if err := store.DeleteMetric(ctx, "ping.latency_ms"); err != nil {
				log.Fatal(err)
			}
			if err := store.UpsertMetric(ctx, metric.Definition{
				Name: "ping.latency_ms", Type: metric.TypeGauge, Unit: "ms",
			}); err != nil {
				log.Fatal(err)
			}
		case 85:
			// An agent is removed. DeleteEntity clears every metric series for
			// that agent, raw and rollup, across the whole store.
			if _, err := store.DeleteEntity(ctx, "agent-007"); err != nil {
				log.Fatal(err)
			}
			agents = removeAgent(agents, "agent-007")
		case 95:
			// A new agent is added. No schema change is needed; the first write
			// creates its series naturally.
			agents = append(agents, "agent-051")
		}

		points := make([]metric.Point, 0, len(agents)*(7+2+len(pingTasks)))
		for _, agentID := range agents {
			points = append(points, systemPoints(agentID, ts, sec)...)
			for _, gpu := range []string{"0", "1"} {
				points = append(points, metric.Point{
					MetricName: "gpu.usage",
					EntityID:   agentID,
					Timestamp:  ts,
					Value:      gpuUsage(agentID, gpu, sec),
					Tags:       map[string]string{"device_index": gpu},
				})
			}
			for _, taskID := range pingTasks {
				points = append(points, metric.Point{
					MetricName: "ping.latency_ms",
					EntityID:   agentID,
					Timestamp:  ts,
					Value:      pingLatency(agentID, taskID, sec),
					Tags:       map[string]string{"task_id": taskID},
				})
			}
		}
		if err := store.WriteBatch(ctx, points); err != nil {
			log.Fatal(err)
		}
		if sec%30 == 29 {
			if _, err := store.Compact(ctx, ts); err != nil {
				log.Fatal(err)
			}
		}
	}

	now := base.Add(2 * time.Hour)
	if _, err := store.Compact(ctx, now); err != nil {
		log.Fatal(err)
	}

	activeCPU, err := store.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "cpu.usage",
			Start:      base,
			End:        base.Add(2 * time.Minute),
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		log.Fatal(err)
	}
	deletedAgentCPU, err := store.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "cpu.usage",
			EntityID:   "agent-007",
			Start:      base,
			End:        base.Add(2 * time.Minute),
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		log.Fatal(err)
	}
	task1, err := store.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "ping.latency_ms",
			Start:      base,
			End:        base.Add(2 * time.Minute),
			Tags:       map[string]string{"task_id": "pingtask1"},
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		log.Fatal(err)
	}
	task2, err := store.Series(ctx, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "ping.latency_ms",
			Start:      base,
			End:        base.Add(2 * time.Minute),
			Tags:       map[string]string{"task_id": "pingtask2"},
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	}, now)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("agents now=%d\n", len(agents))
	fmt.Printf("fleet cpu buckets=%d\n", len(nonEmpty(activeCPU)))
	fmt.Printf("deleted agent buckets=%d\n", len(nonEmpty(deletedAgentCPU)))
	fmt.Printf("pingtask1 buckets=%d\n", len(nonEmpty(task1)))
	fmt.Printf("pingtask2 buckets=%d\n", len(nonEmpty(task2)))

	// Output:
	// agents now=50
	// fleet cpu buckets=2
	// deleted agent buckets=0
	// pingtask1 buckets=1
	// pingtask2 buckets=0
}

func makeAgents(n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("agent-%03d", i))
	}
	return out
}

func removeAgent(agents []string, deleted string) []string {
	out := agents[:0]
	for _, agentID := range agents {
		if agentID != deleted {
			out = append(out, agentID)
		}
	}
	return out
}

func systemPoints(agentID string, ts time.Time, sec int) []metric.Point {
	id := numericSuffix(agentID)
	return []metric.Point{
		{MetricName: "cpu.usage", EntityID: agentID, Timestamp: ts, Value: float64((id*3 + sec) % 100)},
		{MetricName: "mem.usage", EntityID: agentID, Timestamp: ts, Value: 35 + float64((id+sec)%40)},
		{MetricName: "disk.usage", EntityID: agentID, Timestamp: ts, Value: 50 + float64(id%20)/2},
		{MetricName: "swap.usage", EntityID: agentID, Timestamp: ts, Value: float64((id + sec/10) % 12)},
		{MetricName: "net.in.bytes", EntityID: agentID, Timestamp: ts, Value: float64(sec * (1000 + id))},
		{MetricName: "net.out.bytes", EntityID: agentID, Timestamp: ts, Value: float64(sec * (700 + id))},
		{MetricName: "process.count", EntityID: agentID, Timestamp: ts, Value: float64(90 + id%25)},
	}
}

func gpuUsage(agentID, deviceIndex string, sec int) float64 {
	id := numericSuffix(agentID)
	device := 0
	if deviceIndex == "1" {
		device = 37
	}
	return float64((id*5 + device + sec*2) % 100)
}

func pingLatency(agentID, taskID string, sec int) float64 {
	id := numericSuffix(agentID)
	offset := 0
	if strings.HasSuffix(taskID, "2") {
		offset = 35
	}
	return float64(20 + (id+sec+offset)%80)
}

func numericSuffix(agentID string) int {
	var n int
	for _, r := range agentID {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
		}
	}
	return n
}

func nonEmpty(points []metric.AggregatePoint) []metric.AggregatePoint {
	out := points[:0]
	for _, p := range points {
		if p.Count > 0 {
			out = append(out, p)
		}
	}
	return out
}
