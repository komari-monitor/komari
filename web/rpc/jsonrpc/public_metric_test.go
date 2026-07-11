package jsonrpc

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/database/metricstore"
)

func TestSplitPublicMetricSeriesKeepsTagSeries(t *testing.T) {
	base := publicMetricSeries{
		MetricKey: "gpu.device.usage",
		EntityID:  "node-a",
		Points: []publicMetricPoint{
			{Time: "2026-06-18T00:00:00Z", Value: 10, Count: 2, Tags: map[string]string{"device_index": "0"}},
			{Time: "2026-06-18T00:00:00Z", Value: 80, Count: 2, Tags: map[string]string{"device_index": "1"}},
			{Time: "2026-06-18T00:01:00Z", Value: 20, Count: 2, Tags: map[string]string{"device_index": "0"}},
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

func TestMetricRollupCompatibleIntervalClampsOldWindowToFinestTier(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	got := metricRollupCompatibleInterval(now.Add(-time.Hour), now, 5*time.Second)
	if got != metricstore.DefaultRollupFinestTier {
		t.Fatalf("old window should clamp to finest rollup tier, got %s", got)
	}

	got = metricRollupCompatibleInterval(now.Add(-5*time.Minute), now, 5*time.Second)
	if got != 5*time.Second {
		t.Fatalf("recent window should keep raw interval, got %s", got)
	}
}
