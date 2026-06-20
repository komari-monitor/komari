package metric

import (
	"strings"
	"testing"
)

// TestDialectsGenerateBackendSpecificSQL verifies backend-specific SQL rendering.
//
// TestDialectsGenerateBackendSpecificSQL 验证不同数据库后端生成各自的 SQL。
func TestDialectsGenerateBackendSpecificSQL(t *testing.T) {
	tests := []struct {
		name        string
		driver      Driver
		placeholder string
		upsert      string
		jsonType    string
	}{
		{
			name:        "sqlite",
			driver:      DriverSQLite,
			placeholder: "?",
			upsert:      "ON CONFLICT(metric_name, entity_id, tags_hash, ts_nano)",
			jsonType:    "TEXT",
		},
		{
			name:        "mysql",
			driver:      DriverMySQL,
			placeholder: "?",
			upsert:      "ON DUPLICATE KEY UPDATE",
			jsonType:    "JSON",
		},
		{
			name:        "postgresql",
			driver:      DriverPostgreSQL,
			placeholder: "$1",
			upsert:      "ON CONFLICT(metric_name, entity_id, tags_hash, ts_nano)",
			jsonType:    "JSONB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDialect(tt.driver)
			if got := d.placeholder(1); got != tt.placeholder {
				t.Fatalf("placeholder: expected %q, got %q", tt.placeholder, got)
			}
			if got := d.jsonType(); got != tt.jsonType {
				t.Fatalf("json type: expected %q, got %q", tt.jsonType, got)
			}
			sql := d.upsertPointSQL(tables{points: "metric_points"}, 2)
			if !strings.Contains(sql, "metric_points") {
				t.Fatalf("upsert sql should reference metric_points: %s", sql)
			}
			if !strings.Contains(sql, tt.upsert) {
				t.Fatalf("upsert sql should contain %q: %s", tt.upsert, sql)
			}
		})
	}
}
