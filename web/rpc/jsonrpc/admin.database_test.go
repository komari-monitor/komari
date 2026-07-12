package jsonrpc

import (
	"testing"

	"github.com/komari-monitor/komari/pkg/metric"
)

func TestLocalDatabaseTotalRequiresTwoKnownLocalSizes(t *testing.T) {
	mainSize, monitoringSize := int64(10), int64(15)
	main := databaseStorageStatus{Location: databaseLocationLocal, Size: &mainSize}
	monitoring := databaseStorageStatus{Location: databaseLocationLocal, Size: &monitoringSize}

	total := localDatabaseTotal(main, monitoring)
	if total == nil || *total != 25 {
		t.Fatalf("local total = %v, want 25", total)
	}

	monitoring.Location = databaseLocationExternal
	if total := localDatabaseTotal(main, monitoring); total != nil {
		t.Fatalf("external monitoring database should not produce a local total: %d", *total)
	}

	monitoring.Location = databaseLocationLocal
	monitoring.Size = nil
	if total := localDatabaseTotal(main, monitoring); total != nil {
		t.Fatalf("unknown monitoring size should not produce a local total: %d", *total)
	}
}

func TestDatabaseLocationForDriver(t *testing.T) {
	tests := []struct {
		driver metric.Driver
		want   string
	}{
		{driver: metric.DriverSQLite, want: databaseLocationLocal},
		{driver: metric.DriverMySQL, want: databaseLocationExternal},
		{driver: metric.DriverPostgreSQL, want: databaseLocationExternal},
		{driver: "", want: ""},
	}
	for _, test := range tests {
		if got := databaseLocationForDriver(test.driver); got != test.want {
			t.Errorf("databaseLocationForDriver(%q) = %q, want %q", test.driver, got, test.want)
		}
	}
}

func TestDatabaseMaintenanceResponsePreservesLegacyMainSizes(t *testing.T) {
	before, after := int64(100), int64(60)
	for _, test := range []struct {
		name              string
		mainSuccess       bool
		monitoringSuccess bool
		wantAllSucceeded  bool
	}{
		{name: "both succeed", mainSuccess: true, monitoringSuccess: true, wantAllSucceeded: true},
		{name: "main fails", mainSuccess: false, monitoringSuccess: true, wantAllSucceeded: false},
		{name: "monitoring fails", mainSuccess: true, monitoringSuccess: false, wantAllSucceeded: false},
		{name: "both fail", mainSuccess: false, monitoringSuccess: false, wantAllSucceeded: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := newDatabaseMaintenanceResponse(
				databaseMaintenanceResult{Before: &before, After: &after, Success: test.mainSuccess},
				databaseMaintenanceResult{Success: test.monitoringSuccess},
			)
			if response.AllSucceeded != test.wantAllSucceeded {
				t.Fatalf("all_succeeded = %t, want %t", response.AllSucceeded, test.wantAllSucceeded)
			}
			if response.Before != before || response.After != after || response.Size != after {
				t.Fatalf("legacy sizes changed: before=%d after=%d size=%d", response.Before, response.After, response.Size)
			}
		})
	}
}
