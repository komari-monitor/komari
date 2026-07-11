package jsonrpc

import "testing"

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
