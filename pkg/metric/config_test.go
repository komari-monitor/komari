package metric

import (
	"testing"
	"time"
)

// TestBackendBuildersApplyOptions verifies backend builders apply options.
//
// TestBackendBuildersApplyOptions 验证各后端构造器会正确应用选项。
func TestBackendBuildersApplyOptions(t *testing.T) {
	cfg := SQLite(
		"file:metrics.db?cache=shared",
		WithTablePrefix("x_metric_"),
		WithDefaultRetention(180),
		WithAutoMigrate(false),
		WithMaxOpenConns(10),
		WithMaxIdleConns(2),
		WithConnMaxLifetime(30*time.Minute),
		WithConnectTimeout(2*time.Second),
	)

	if cfg.Driver != DriverSQLite {
		t.Fatalf("expected sqlite driver, got %q", cfg.Driver)
	}
	if cfg.TablePrefix != "x_metric_" {
		t.Fatalf("table prefix option was not applied: %q", cfg.TablePrefix)
	}
	if cfg.DefaultRetentionDays != 180 {
		t.Fatalf("default retention option was not applied: %d", cfg.DefaultRetentionDays)
	}
	if cfg.AutoMigrate {
		t.Fatalf("auto migrate option was not applied")
	}
	if cfg.MaxOpenConns != 10 || cfg.MaxIdleConns != 2 {
		t.Fatalf("pool options were not applied: %#v", cfg)
	}
	if cfg.ConnMaxLifetime != 30*time.Minute || cfg.ConnectTimeout != 2*time.Second {
		t.Fatalf("timeout options were not applied: %#v", cfg)
	}
}

// TestSQLiteInDirBuildsFileConfig verifies SQLiteInDir file configuration.
//
// TestSQLiteInDirBuildsFileConfig 验证 SQLiteInDir 会生成预期的文件数据库配置。
func TestSQLiteInDirBuildsFileConfig(t *testing.T) {
	cfg := SQLiteInDir(
		"data/metrics",
		WithSQLiteProfile(SQLiteProfilePerformance),
		WithSQLiteCacheSizeKB(128*1024),
	)

	if cfg.Driver != DriverSQLite {
		t.Fatalf("expected sqlite driver, got %q", cfg.Driver)
	}
	if cfg.MaxOpenConns != 1 || cfg.MaxIdleConns != 1 {
		t.Fatalf("sqlite directory config should default to a single connection: %#v", cfg)
	}
	if cfg.SQLite.PerformanceProfile != SQLiteProfilePerformance {
		t.Fatalf("sqlite profile option was not applied: %q", cfg.SQLite.PerformanceProfile)
	}
	if cfg.SQLite.CacheSizeKB != 128*1024 {
		t.Fatalf("sqlite cache option was not applied: %d", cfg.SQLite.CacheSizeKB)
	}
	if cfg.DSN != "file:data/metrics/metrics.db?cache=shared&mode=rwc" {
		t.Fatalf("unexpected sqlite dir dsn: %q", cfg.DSN)
	}
}
