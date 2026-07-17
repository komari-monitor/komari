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
	if got[1].Tags["device_index"] != "1" || got[1].Count != 1 {
		t.Fatalf("unexpected second series: %#v", got[1])
	}
	if got[0].Points[0].Tags["device_index"] != "0" || got[1].Points[0].Tags["device_index"] != "1" {
		t.Fatalf("point tags were not preserved: %#v", got)
	}
}

func TestPublicMetricJSONIncludesOnlyTags(t *testing.T) {
	payload, err := json.Marshal(publicMetricSeries{
		MetricKey: "ping.loss",
		EntityID:  "node-a",
		Tags:      map[string]string{"task_id": "7"},
		Points: []publicMetricPoint{{
			Time:  "2026-06-18T00:00:00Z",
			Value: publicMetricValue(0),
			Tags:  map[string]string{"task_id": "7"},
		}},
	})
	if err != nil {
		t.Fatalf("marshal series: %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"tags":{"task_id":"7"}`) {
		t.Fatalf("series tags missing: %s", text)
	}
	if strings.Contains(text, `"tag":`) {
		t.Fatalf("legacy tag field should not be serialized: %s", text)
	}
}

func TestAdaptiveFillPublicMetricSeriesUsesObservedInterval(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	series := publicMetricSeries{
		MetricKey:       "cpu.usage",
		EntityID:        "node-a",
		FillEmpty:       true,
		IntervalSeconds: 1,
		Tags:            map[string]string{"core": "0"},
	}
	for i := 0; i < 10; i++ {
		series.Points = append(series.Points, publicMetricPoint{
			Time:  base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
			Value: publicMetricValue(float64(i)),
		})
	}

	got := adaptiveFillPublicMetricSeries(series, base, base.Add(9*time.Minute))
	if got.IntervalSeconds != 60 {
		t.Fatalf("expected observed 60s interval, got %v", got.IntervalSeconds)
	}
	if len(got.Points) != 10 {
		t.Fatalf("regular sparse series should not gain null buckets, got %#v", got.Points)
	}
	for _, point := range got.Points {
		if point.Value == nil {
			t.Fatalf("regular sparse series gained a null point: %#v", got.Points)
		}
	}
}

func TestAdaptiveFillPublicMetricSeriesAddsCompactGapsAndBounds(t *testing.T) {
	base := time.Date(2026, 6, 18, 0, 0, 0, 0, time.UTC)
	tags := map[string]string{"device_index": "0"}
	series := publicMetricSeries{
		MetricKey:       "gpu.device.usage",
		EntityID:        "node-a",
		IntervalSeconds: 1,
		Tags:            tags,
		Points: []publicMetricPoint{
			{Time: base.Format(time.RFC3339Nano), Value: publicMetricValue(10)},
			{Time: base.Add(time.Minute).Format(time.RFC3339Nano), Value: publicMetricValue(20)},
			{Time: base.Add(2 * time.Minute).Format(time.RFC3339Nano), Value: publicMetricValue(30)},
			{Time: base.Add(4 * time.Minute).Format(time.RFC3339Nano), Value: publicMetricValue(40)},
		},
	}
	start := base.Add(-30 * time.Second)
	end := base.Add(4*time.Minute + 30*time.Second)
	got := adaptiveFillPublicMetricSeries(series, start, end)
	if got.IntervalSeconds != 60 {
		t.Fatalf("expected observed 60s interval, got %v", got.IntervalSeconds)
	}
	if len(got.Points) != 7 {
		t.Fatalf("expected four values, one gap and two bounds, got %#v", got.Points)
	}
	wantNullTimes := map[string]bool{
		start.Format(time.RFC3339Nano):                     true,
		base.Add(3 * time.Minute).Format(time.RFC3339Nano): true,
		end.Format(time.RFC3339Nano):                       true,
	}
	for _, point := range got.Points {
		if point.Value != nil {
			continue
		}
		if !wantNullTimes[point.Time] {
			t.Fatalf("unexpected null point at %s: %#v", point.Time, got.Points)
		}
		if point.Tags["device_index"] != "0" {
			t.Fatalf("adaptive null point lost tags: %#v", point)
		}
		delete(wantNullTimes, point.Time)
	}
	if len(wantNullTimes) != 0 {
		t.Fatalf("missing expected null points: %#v", wantNullTimes)
	}
}

func TestPublicPingMetricFillEmptyMapsMinusOneToNull(t *testing.T) {
	for _, metricName := range []string{metricstore.MetricPingLatency, metricstore.MetricPingLoss} {
		if value := publicRawMetricValue(metricName, -1, true); value != nil {
			t.Fatalf("raw %s -1 should become null when fill_empty is enabled, got %v", metricName, *value)
		}
	}
	if value := publicRawMetricValue(metricstore.MetricPingLatency, -1, true); value != nil {
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

func TestMetricDownsampleIntervalCeilsToStandardInterval(t *testing.T) {
	got := metricDownsampleInterval(30*24*time.Hour, 500)
	if got != 2*time.Hour {
		t.Fatalf("30d/500 should ceil to 2h, got %s", got)
	}

	got = metricDownsampleInterval(time.Hour, 500)
	if got != 10*time.Second {
		t.Fatalf("1h/500 should ceil to 10s, got %s", got)
	}

	got = metricDownsampleInterval(1000*24*time.Hour, 10)
	if got != 100*24*time.Hour {
		t.Fatalf("ranges beyond the standard table should ceil to whole days, got %s", got)
	}
}
