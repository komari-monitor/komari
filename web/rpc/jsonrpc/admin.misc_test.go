package jsonrpc

import (
	"testing"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/internal/config"
)

func TestMetricKeysTouchedIgnoresLowResourceMode(t *testing.T) {
	if metricKeysTouched(map[string]interface{}{config.LowResourceModeKey: true}) {
		t.Fatal("low resource mode must not reload or reconfigure the metric store")
	}
	if !metricKeysTouched(map[string]interface{}{metricstore.MetricDBDSNKey: "metrics.db"}) {
		t.Fatal("metric database DSN must still trigger metric store validation")
	}
}
