package clients

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/komari-monitor/komari/protocol/v1"
)

func TestReportVerifyAcceptsOptionalStatusFields(t *testing.T) {
	report := v1.Report{
		Disks: []v1.DiskMount{{
			Name: "sda1", Device: "/dev/sda1", Mountpoint: "/", Filesystem: "ext4", Total: 100, Used: 40,
		}},
		Extensions: v1.ReportExtensions{
			"example.storage": json.RawMessage(`{"health":"healthy"}`),
		},
	}
	if err := ReportVerify(report); err != nil {
		t.Fatalf("valid optional status fields were rejected: %v", err)
	}
}

func TestReportVerifyRejectsInvalidDiskMounts(t *testing.T) {
	oversizedPayload := make([]v1.DiskMount, 9)
	for i := range oversizedPayload {
		oversizedPayload[i] = v1.DiskMount{Mountpoint: "/data", Device: strings.Repeat("x", 4000)}
	}
	tests := []struct {
		name  string
		disks []v1.DiskMount
	}{
		{name: "negative used", disks: []v1.DiskMount{{Mountpoint: "/", Used: -1}}},
		{name: "negative total", disks: []v1.DiskMount{{Mountpoint: "/", Total: -1}}},
		{name: "too many", disks: make([]v1.DiskMount, maxReportDiskMounts+1)},
		{name: "oversized string", disks: []v1.DiskMount{{Mountpoint: strings.Repeat("x", maxReportDiskStringLength+1)}}},
		{name: "oversized payload", disks: oversizedPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ReportVerify(v1.Report{Disks: tt.disks}); err == nil {
				t.Fatal("invalid disk mounts were accepted")
			}
		})
	}
}

func TestReportVerifyRejectsInvalidExtensions(t *testing.T) {
	tests := []struct {
		name       string
		extensions v1.ReportExtensions
	}{
		{name: "invalid namespace", extensions: v1.ReportExtensions{"Example Status": json.RawMessage(`{}`)}},
		{name: "non-object value", extensions: v1.ReportExtensions{"example": json.RawMessage(`true`)}},
		{name: "invalid json", extensions: v1.ReportExtensions{"example": json.RawMessage(`{`)}},
		{name: "oversized payload", extensions: v1.ReportExtensions{
			"example": json.RawMessage(`{"value":"` + strings.Repeat("x", maxReportExtensionsBytes) + `"}`),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ReportVerify(v1.Report{Extensions: tt.extensions}); err == nil {
				t.Fatal("invalid extensions were accepted")
			}
		})
	}

	tooMany := make(v1.ReportExtensions, maxReportExtensionNamespaces+1)
	for i := 0; i < maxReportExtensionNamespaces+1; i++ {
		tooMany["example"+strings.Repeat("x", i)] = json.RawMessage(`{}`)
	}
	if err := ReportVerify(v1.Report{Extensions: tooMany}); err == nil {
		t.Fatal("too many extension namespaces were accepted")
	}
}
