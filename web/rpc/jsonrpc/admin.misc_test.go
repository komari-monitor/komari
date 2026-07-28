package jsonrpc

import (
	"testing"

	"github.com/komari-monitor/komari/database/metricstore"
)

func TestMetricKeysTouched(t *testing.T) {
	if !metricKeysTouched(map[string]interface{}{metricstore.MetricDBDSNKey: "metrics.db"}) {
		t.Fatal("metric database DSN must trigger metric store validation")
	}
}

func TestRemoveRetiredLowResourceMode(t *testing.T) {
	cfg := map[string]interface{}{
		"low_resource_mode": true,
		"sitename":          "Komari",
	}

	removeRetiredLowResourceMode(cfg)

	if _, ok := cfg["low_resource_mode"]; ok {
		t.Fatal("retired low resource mode must not be persisted")
	}
	if cfg["sitename"] != "Komari" {
		t.Fatal("unrelated settings must be preserved")
	}
}
