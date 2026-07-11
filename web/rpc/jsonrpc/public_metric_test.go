package jsonrpc

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/metric"
)

func TestSplitPublicMetricSeriesKeepsTagSeries(t *testing.T) {
	base := publicMetricSeries{
		MetricKey: "gpu.device.usage",
		EntityID:  "node-a",
		Points: []publicMetricPoint{
			{Time: "2026-06-18T00:00:00Z", Value: publicMetricValue(10), Count: 2, Tags: map[string]string{"device_index": "0"}},
			{Time: "2026-06-18T00:00:00Z", Value: publicMetricValue(80), Count: 2, Tags: map[string]string{"device_index": "1"}},
			{Time: "2026-06-18T00:01:00Z", Value: publicMetricValue(20), Count: 2, Tags: map[string]string{"device_index": "0"}},
		},
	}

	got := splitPublicMetricSeries(base)
	if len(got) != 2 {
		t.Fatalf("expected 2 tag series, got %d: %#v", len(got), got)
	}
	if got[0].Tags["device_index"] != "0" || got[0].Count != 2 {
		t.Fatalf("unexpected first series: %#v", got[0])
	}
	if got[0].Tag["device_index"] != "0" {
		t.Fatalf("tag alias was not set on first series: %#v", got[0])
	}
	if got[1].Tags["device_index"] != "1" || got[1].Count != 1 {
		t.Fatalf("unexpected second series: %#v", got[1])
	}
	if got[0].Points[0].Tags["device_index"] != "0" || got[1].Points[0].Tags["device_index"] != "1" {
		t.Fatalf("point tags were not preserved: %#v", got)
	}
	if got[0].Points[0].Tag["device_index"] != "0" || got[1].Points[0].Tag["device_index"] != "1" {
		t.Fatalf("point tag aliases were not preserved: %#v", got)
	}
}

func TestPublicMetricJSONIncludesTagAlias(t *testing.T) {
	payload, err := json.Marshal(publicMetricSeries{
		MetricKey: "ping.loss",
		EntityID:  "node-a",
		Tag:       map[string]string{"task_id": "7"},
		Tags:      map[string]string{"task_id": "7"},
		Points: []publicMetricPoint{{
			Time:  "2026-06-18T00:00:00Z",
			Value: publicMetricValue(0),
			Tag:   map[string]string{"task_id": "7"},
			Tags:  map[string]string{"task_id": "7"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal series: %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"tag":{"task_id":"7"}`) {
		t.Fatalf("series tag alias missing: %s", text)
	}
	if !strings.Contains(text, `"tags":{"task_id":"7"}`) {
		t.Fatalf("series tags missing: %s", text)
	}
}

func TestPublicMetricFillEmptyEmitsNullForEmptyBuckets(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	points, err := metric.AggregatePoints([]metric.Point{
		{MetricName: "cpu.usage", EntityID: "node-a", Timestamp: base, Value: 10},
		{MetricName: "cpu.usage", EntityID: "node-a", Timestamp: base.Add(2 * time.Minute), Value: 30},
	}, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "cpu.usage",
			EntityID:   "node-a",
			Start:      base,
			End:        base.Add(2 * time.Minute),
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
		FillEmpty:   true,
	})
	if err != nil {
		t.Fatalf("aggregate points: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 buckets with fill enabled, got %d: %#v", len(points), points)
	}
	if points[1].Count != 0 {
		t.Fatalf("middle bucket should be empty, got %#v", points[1])
	}

	payload, err := json.Marshal(publicMetricPoint{
		Time:  points[1].Bucket.Format(time.RFC3339Nano),
		Value: publicAggregateMetricValue(points[1], true),
		Count: points[1].Count,
	})
	if err != nil {
		t.Fatalf("marshal point: %v", err)
	}
	if !strings.Contains(string(payload), `"value":null`) {
		t.Fatalf("empty bucket should serialize as null, got %s", payload)
	}
}

func TestPublicMetricFillEmptyDisabledKeepsOnlyExistingBuckets(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	points, err := metric.AggregatePoints([]metric.Point{
		{MetricName: "cpu.usage", EntityID: "node-a", Timestamp: base, Value: 10},
		{MetricName: "cpu.usage", EntityID: "node-a", Timestamp: base.Add(2 * time.Minute), Value: 30},
	}, metric.AggregateQuery{
		Query: metric.Query{
			MetricName: "cpu.usage",
			EntityID:   "node-a",
			Start:      base,
			End:        base.Add(2 * time.Minute),
		},
		Aggregation: metric.AggAvg,
		Interval:    time.Minute,
	})
	if err != nil {
		t.Fatalf("aggregate points: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected only existing buckets by default, got %d: %#v", len(points), points)
	}
	for _, point := range points {
		if publicAggregateMetricValue(point, false) == nil {
			t.Fatalf("non-fill query should keep numeric values: %#v", point)
		}
	}
}

func TestPublicPingMetricFillEmptyMapsMinusOneToNull(t *testing.T) {
	for _, metricName := range []string{metricstore.MetricPingLatency, metricstore.MetricPingLoss} {
		if value := publicRawMetricValue(metricName, -1, true); value != nil {
			t.Fatalf("raw %s -1 should become null when fill_empty is enabled, got %v", metricName, *value)
		}
	}
	if value := publicAggregateMetricValue(metric.AggregatePoint{
		MetricName: metricstore.MetricPingLatency,
		Value:      -1,
		Count:      1,
	}, true); value != nil {
		t.Fatalf("downsampled ping -1 should become null when fill_empty is enabled, got %v", *value)
	}
}

func TestPublicPingMetricMinusOneIsPreservedWithoutFillEmpty(t *testing.T) {
	value := publicRawMetricValue(metricstore.MetricPingLatency, -1, false)
	if value == nil || *value != -1 {
		t.Fatalf("raw ping -1 should be preserved when fill_empty is disabled, got %v", value)
	}

	nonPing := publicRawMetricValue("temperature", -1, true)
	if nonPing == nil || *nonPing != -1 {
		t.Fatalf("negative values from non-ping metrics must be preserved, got %v", nonPing)
	}
}

func TestPublicPingStatsFromAggregateGroupsUsesTaskNamesAndLossMetric(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	taskMap := map[string]models.PingTask{
		"1": {Id: 1, Name: "Tokyo ICMP", Type: "icmp", Interval: 60},
	}
	groups := publicPingMetricAggregateGroups{
		Avg: map[string][]metric.AggregatePoint{
			"1": {
				{Bucket: base, Count: 2, Value: 20},
				{Bucket: base.Add(time.Minute), Count: 2, Value: 40},
			},
		},
		Min: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 12}},
		},
		Max: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 92}},
		},
		Last: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base.Add(time.Minute), Count: 1, Value: 44}},
		},
		P50: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 30}},
		},
		P99: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 80}},
		},
		StdDev: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 8}},
		},
		Loss: map[string][]metric.AggregatePoint{
			"1": {{Bucket: base, Count: 4, Value: 0.25}},
		},
		LossAvailable: true,
	}

	stats := publicPingStatsFromAggregateGroups("node-a", groups, taskMap, nil)
	if len(stats) != 1 {
		t.Fatalf("expected one stat, got %#v", stats)
	}
	got := stats[0]
	if got.Name != "Tokyo ICMP" || got.Type != "icmp" || got.Interval != 60 {
		t.Fatalf("task metadata not applied: %#v", got)
	}
	if got.Total != 4 || got.Valid != 3 {
		t.Fatalf("unexpected totals: %#v", got)
	}
	if got.Loss != 25 || got.LossApproximate {
		t.Fatalf("loss should come from ping.loss metric: %#v", got)
	}
	if got.Min == nil || *got.Min != 12 || got.Max == nil || *got.Max != 92 || got.Avg == nil || *got.Avg != 30 {
		t.Fatalf("latency stats mismatch: %#v", got)
	}
	if math.Abs(got.P99P50Ratio-1.6666666666666667) > 0.000001 {
		t.Fatalf("unexpected volatility ratio: %#v", got)
	}
}

func TestPublicPingMetricStatsIncludesZeroVolatility(t *testing.T) {
	payload, err := json.Marshal(publicPingMetricTaskStats{
		EntityID:    "node-a",
		TaskID:      "1",
		Total:       1,
		Valid:       1,
		P99P50Ratio: 0,
	})
	if err != nil {
		t.Fatalf("marshal ping stats: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal ping stats: %v", err)
	}
	if value, ok := decoded["p99_p50_ratio"]; !ok || value != float64(0) {
		t.Fatalf("zero volatility must be present, got %s", payload)
	}
}

func TestMetricDownsampleIntervalFloorsToStandardInterval(t *testing.T) {
	got := metricDownsampleInterval(30*24*time.Hour, 500)
	if got != time.Hour {
		t.Fatalf("30d/500 should floor to 1h, got %s", got)
	}

	got = metricDownsampleInterval(time.Hour, 500)
	if got != 5*time.Second {
		t.Fatalf("1h/500 should floor to 5s, got %s", got)
	}
}
