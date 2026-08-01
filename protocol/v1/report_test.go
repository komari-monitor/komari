package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportOptionalStatusFieldsRoundTrip(t *testing.T) {
	payload := []byte(`{
		"disk":{"total":100,"used":40},
		"disks":[{"name":"sda1","device":"/dev/sda1","mountpoint":"/","filesystem":"ext4","total":100,"used":40}],
		"extensions":{"example.storage":{"health":"healthy"}}
	}`)

	var report Report
	if err := json.Unmarshal(payload, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Disks) != 1 || report.Disks[0].Mountpoint != "/" || report.Disks[0].Used != 40 {
		t.Fatalf("unexpected disk mounts: %#v", report.Disks)
	}
	if got := string(report.Extensions["example.storage"]); !strings.Contains(got, `"health":"healthy"`) {
		t.Fatalf("unexpected extension: %s", got)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(encoded), `"disks"`) || !strings.Contains(string(encoded), `"extensions"`) {
		t.Fatalf("optional status fields were dropped: %s", encoded)
	}
}

func TestReportOmitsEmptyOptionalStatusFields(t *testing.T) {
	encoded, err := json.Marshal(Report{})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if strings.Contains(string(encoded), `"disks"`) || strings.Contains(string(encoded), `"extensions"`) {
		t.Fatalf("empty optional status fields should be omitted: %s", encoded)
	}
}
