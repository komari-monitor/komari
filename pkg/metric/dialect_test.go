package metric

import "testing"

// TestDialectsGenerateBackendSpecificSQL verifies backend-specific SQL rendering.
//
// TestDialectsGenerateBackendSpecificSQL 验证不同数据库后端生成各自的 SQL。
func TestDialectsGenerateBackendSpecificSQL(t *testing.T) {
	tests := []struct {
		name        string
		driver      Driver
		placeholder string
		jsonType    string
		blobType    string
	}{
		{
			name:        "sqlite",
			driver:      DriverSQLite,
			placeholder: "?",
			jsonType:    "TEXT",
			blobType:    "BLOB",
		},
		{
			name:        "mysql",
			driver:      DriverMySQL,
			placeholder: "?",
			jsonType:    "JSON",
			blobType:    "LONGBLOB",
		},
		{
			name:        "postgresql",
			driver:      DriverPostgreSQL,
			placeholder: "$1",
			jsonType:    "JSONB",
			blobType:    "BYTEA",
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
			if got := d.blobType(); got != tt.blobType {
				t.Fatalf("blob type: expected %q, got %q", tt.blobType, got)
			}
		})
	}
}
