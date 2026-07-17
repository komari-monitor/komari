package metricstore

import (
	"context"
	"errors"
	"testing"

	"github.com/komari-monitor/komari/pkg/metric"
)

func TestInspectCompressionUsesActiveStore(t *testing.T) {
	s, err := metric.Open(context.Background(), metric.SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	installTestStore(t, s)

	status, err := InspectCompression(context.Background())
	if err != nil {
		t.Fatalf("inspect compression: %v", err)
	}
	if status.Available || status.Driver != metric.DriverSQLite {
		t.Fatalf("unexpected compression status: %#v", status)
	}
}

func TestConfigureCompressionReportsBusyStore(t *testing.T) {
	s, err := metric.Open(context.Background(), metric.SQLite(":memory:"))
	if err != nil {
		t.Fatalf("open metric store: %v", err)
	}
	installTestStore(t, s)

	if !storeOperations.TryAcquire() {
		t.Fatal("acquire metric store operation gate")
	}
	defer storeOperations.Release()

	_, err = ConfigureCompression(context.Background(), metric.CompressionConfig{})
	if !errors.Is(err, ErrStoreBusy) {
		t.Fatalf("configure compression error = %v, want %v", err, ErrStoreBusy)
	}
}
