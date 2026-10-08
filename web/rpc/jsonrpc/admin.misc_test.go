package jsonrpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/metricstore"
)

func TestAdminGetLastRunVersion(t *testing.T) {
	result, rpcErr := adminGetLastRunVersion(context.Background(), nil)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		LastRunVersion *string `json:"last_run_version"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	want := dbcore.LastRunVersion()
	if (payload.LastRunVersion == nil) != (want == nil) ||
		(want != nil && *payload.LastRunVersion != *want) {
		t.Fatalf("unexpected last run version: %s", data)
	}
}

func TestMetricKeysTouched(t *testing.T) {
	if !metricKeysTouched(map[string]interface{}{metricstore.MetricDBDSNKey: "metrics.db"}) {
		t.Fatal("metric database DSN must trigger metric store validation")
	}
	for _, key := range []string{
		metricstore.MetricRollupMinuteRetentionMinutesKey,
		metricstore.MetricRollupFiveMinuteRetentionMinutesKey,
		metricstore.MetricRollupHourRetentionHoursKey,
	} {
		if !metricKeysTouched(map[string]interface{}{key: 1}) {
			t.Fatalf("%s must trigger metric store validation", key)
		}
	}
}

func TestValidateMetricRollupSettingChanges(t *testing.T) {
	if err := validateMetricRollupSettingChanges(map[string]interface{}{
		metricstore.MetricRollupMinuteRetentionMinutesKey:     float64(30),
		metricstore.MetricRollupFiveMinuteRetentionMinutesKey: float64(150),
		metricstore.MetricRollupHourRetentionHoursKey:         float64(300),
	}); err != nil {
		t.Fatalf("valid rollup settings rejected: %v", err)
	}

	for _, value := range []interface{}{float64(0), float64(-1), float64(1.5), "not-a-number"} {
		err := validateMetricRollupSettingChanges(map[string]interface{}{
			metricstore.MetricRollupMinuteRetentionMinutesKey: value,
		})
		if err == nil {
			t.Fatalf("value %#v should be rejected", value)
		}
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
