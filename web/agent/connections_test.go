package agent

import (
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/komari-monitor/komari/protocol/v1"
)

func TestRecordReportKeepsLatestAndShortRecentWindow(t *testing.T) {
	mu.Lock()
	previousLatest := latestReport
	previousRecent := recentReports
	latestReport = make(map[string]*v1.Report)
	recentReports = make(map[string][]v1.Report)
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		latestReport = previousLatest
		recentReports = previousRecent
		mu.Unlock()
	})

	now := time.Now().UTC()
	RecordReport(v1.Report{UUID: "node-a", UpdatedAt: now.Add(-2 * time.Minute), CPU: v1.CPUReport{Usage: 10}})
	report := v1.Report{
		UUID: "node-a", UpdatedAt: now.Add(-30 * time.Second), CPU: v1.CPUReport{Usage: 20},
		Disks:      []v1.DiskMount{{Mountpoint: "/", Total: 100, Used: 40}},
		Extensions: v1.ReportExtensions{"example": json.RawMessage(`{"health":"healthy"}`)},
	}
	RecordReport(report)
	RecordReport(v1.Report{UUID: "node-a", UpdatedAt: now.Add(-45 * time.Second), CPU: v1.CPUReport{Usage: 15}})
	report.Disks[0].Used = 99
	report.Extensions["example"][0] = '['

	recent := GetRecentReports("node-a")
	if len(recent) != 2 || recent[0].CPU.Usage != 15 || recent[1].CPU.Usage != 20 {
		t.Fatalf("recent reports = %#v", recent)
	}
	if recent[1].Disks != nil || recent[1].Extensions != nil {
		t.Fatalf("optional latest status leaked into recent reports: %#v", recent[1])
	}
	recent[0].CPU.Usage = 99
	if got := GetRecentReports("node-a"); len(got) != 2 || got[0].CPU.Usage != 15 {
		t.Fatalf("recent report cache was mutated through returned slice: %#v", got)
	}

	latest := GetLatestReport()
	if latest["node-a"] == nil || latest["node-a"].CPU.Usage != 20 {
		t.Fatalf("latest report = %#v", latest["node-a"])
	}
	latest["node-a"].CPU.Usage = 99
	latest["node-a"].Disks[0].Used = 99
	latest["node-a"].Extensions["example"][0] = '['
	if got := GetLatestReport()["node-a"]; got == nil || got.CPU.Usage != 20 {
		t.Fatalf("latest report cache was mutated through returned map: %#v", got)
	}
	if got := GetLatestReport()["node-a"]; got.Disks[0].Used != 40 || string(got.Extensions["example"]) != `{"health":"healthy"}` {
		t.Fatalf("latest optional status was mutated through returned map: %#v", got)
	}

	DeleteLatestReport("node-a")
	if len(GetRecentReports("node-a")) != 0 || GetLatestReport()["node-a"] != nil {
		t.Fatal("deleting latest report did not clear runtime report state")
	}
}
