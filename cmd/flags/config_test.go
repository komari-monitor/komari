package flags

import "testing"

func TestNormalizeDatabaseType(t *testing.T) {
	tests := map[string]string{
		"":          DatabaseTypeSQLite,
		"sqlite":    DatabaseTypeSQLite,
		" SQLite ":  DatabaseTypeSQLite,
		"SQLITE":    DatabaseTypeSQLite,
		"postgres":  DatabaseTypePostgres,
		" postgres": DatabaseTypePostgres,
	}

	for input, want := range tests {
		if got := NormalizeDatabaseType(input); got != want {
			t.Fatalf("NormalizeDatabaseType(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestDatabaseDSNDefault 校验 PostgreSQL 连接 DSN 字段默认为零值，
// 确保未配置 KOMARI_DB_DSN 时调用方可以据此判断是否需要报错。
func TestDatabaseDSNDefault(t *testing.T) {
	if DatabaseDSN != "" {
		t.Fatalf("DatabaseDSN default = %q, want empty (should be configured via KOMARI_DB_DSN)", DatabaseDSN)
	}
}
