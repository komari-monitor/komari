package jsonrpc

import (
	"testing"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestGpuUsageFromReport(t *testing.T) {
	if gpuUsageFromReport(nil) != 0 {
		t.Fatalf("nil report should be 0")
	}
	if gpuUsageFromReport(&v2.Report{}) != 0 {
		t.Fatalf("missing GPU should be 0")
	}

	got := gpuUsageFromReport(&v2.Report{
		GPU: &v2.GPUDetailReport{
			Count:        1,
			AverageUsage: 87.5,
			DetailedInfo: []v2.GPUDeviceInfo{{
				Name:        "Phoenix1",
				Utilization: 87.5,
			}},
		},
	})
	if got != 87.5 {
		t.Fatalf("got %v, want 87.5", got)
	}
}
