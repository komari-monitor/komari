package metricstore

import (
	"fmt"
	"strings"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

const (
	// DefaultRollupRawRetention documents the fixed in-memory exact-sample
	// window. Samples older than one minute are losslessly byte-encoded;
	// older history is served by the persisted rollup ladder.
	DefaultRollupRawRetention = 10 * time.Minute
	DefaultRollupFinestTier   = time.Minute
	defaultRollupPointLimit   = 600

	defaultRollupMinuteRetentionMinutes     = defaultRollupPointLimit
	defaultRollupFiveMinuteRetentionMinutes = 5 * defaultRollupPointLimit
	defaultRollupHourRetentionHours         = defaultRollupPointLimit
	defaultRollupDayRetentionDays           = 100 * 365
)

// MetricStoreConfig 保存 metric store 配置。
//
// 注意：metric store 现在始终启用（旧的 metric_store_enabled 开关已废弃）。
// 未显式配置时默认使用 SQLite（./data/metrics.db）。
type MetricStoreConfig struct {
	Driver       string `json:"metric_db_driver" default:"sqlite"`         // 数据库类型: sqlite, mysql, postgresql
	DSN          string `json:"metric_db_dsn" default:"./data/metrics.db"` // 数据库连接串
	TablePrefix  string `json:"metric_table_prefix" default:"metric_"`     // 表名前缀
	MaxOpenConns int    `json:"metric_max_open_conns" default:"25"`        // 最大连接数
	MaxIdleConns int    `json:"metric_max_idle_conns" default:"5"`         // 最大空闲连接数
	// RollupMinuteRetentionMinutes controls the persisted 1-minute bucket window.
	RollupMinuteRetentionMinutes int `json:"metric_rollup_minute_retention_minutes" default:"600"`
	// RollupFiveMinuteRetentionMinutes controls the persisted 5-minute bucket window.
	RollupFiveMinuteRetentionMinutes int `json:"metric_rollup_five_minute_retention_minutes" default:"3000"`
	// RollupHourRetentionHours controls the persisted 1-hour bucket window.
	RollupHourRetentionHours int `json:"metric_rollup_hour_retention_hours" default:"600"`
}

// MetricStoreConfigKeys 配置键
const (
	MetricDBDriverKey                         = "metric_db_driver"
	MetricDBDSNKey                            = "metric_db_dsn"
	MetricTablePrefixKey                      = "metric_table_prefix"
	MetricMaxOpenConnsKey                     = "metric_max_open_conns"
	MetricMaxIdleConnsKey                     = "metric_max_idle_conns"
	MetricRollupMinuteRetentionMinutesKey     = "metric_rollup_minute_retention_minutes"
	MetricRollupFiveMinuteRetentionMinutesKey = "metric_rollup_five_minute_retention_minutes"
	MetricRollupHourRetentionHoursKey         = "metric_rollup_hour_retention_hours"
	// MigrationTargetKey 记录上一次成功完成手动迁移的目标指纹（driver+dsn），
	// 用于在下一次管理员手动迁移时推断默认源库。
	MigrationTargetKey = "metric_migration_target"
)

func targetFingerprint(cfg *MetricStoreConfig) string {
	driver := ResolveDriverFromConfig(cfg.Driver, cfg.DSN)
	dsn := strings.TrimSpace(cfg.DSN)
	return fmt.Sprintf("%s|%s", driver, dsn)
}

// buildMetricConfig 根据 MetricStoreConfig 构造底层 metric.Config。
// autoMigrate 控制是否在 Open 时自动建表：正式初始化/热加载时为 true，
// 仅做连接测试时为 false（不写入 schema，避免对目标库产生副作用）。
func buildMetricConfig(cfg *MetricStoreConfig, autoMigrate bool) (metric.Config, error) {
	if cfg == nil {
		return metric.Config{}, fmt.Errorf("metric store config is nil")
	}
	driver := ResolveDriverFromConfig(cfg.Driver, cfg.DSN)

	tablePrefix := cfg.TablePrefix
	if tablePrefix == "" {
		tablePrefix = "metric_"
	}
	opts := []metric.Option{
		metric.WithTablePrefix(tablePrefix),
		metric.WithAutoMigrate(autoMigrate),
	}
	policy, err := rollupPolicyFromConfig(cfg)
	if err != nil {
		return metric.Config{}, err
	}
	opts = append(opts, metric.WithRollupPolicy(policy))

	switch driver {
	case metric.DriverSQLite:
		dsn := cfg.DSN
		if dsn == "" || dsn == "./data/metrics.db" {
			// 注意：刻意不使用 cache=shared。SQLite 共享缓存模式使用表级锁，
			// 当一个连接持有读锁、另一个连接尝试写入时会立即返回
			// SQLITE_LOCKED（"database table is locked"），且 busy_timeout
			// 对共享缓存的表级锁无效，迁移期间与前台查询/实时写入并发时必然报错。
			// _txlock=immediate 让写事务开始即获取写锁，避免锁升级死锁。
			dsn = "file:./data/metrics.db?mode=rwc&_txlock=immediate"
		} else {
			// 用户自定义 DSN 时，剥离 cache=shared，避免上述表级锁问题。
			dsn = stripSharedCache(dsn)
		}
		// SQLite 串行化写入：固定单写连接以避免 "database is locked" 竞争，
		// 同时启用独立的 WAL 只读连接池提升前台查询并发（写仍走单主连接）。
		// 这里刻意忽略 cfg.MaxOpenConns/MaxIdleConns —— 对 SQLite 而言多写连接
		// 只会引入锁竞争而非提升吞吐。
		opts = append(opts, metric.WithMaxOpenConns(1), metric.WithMaxIdleConns(1))
		opts = append(opts, metric.WithSQLiteReadPool(2))
		return metric.SQLite(dsn, opts...), nil
	case metric.DriverMySQL:
		opts = append(opts,
			metric.WithMaxOpenConns(cfg.MaxOpenConns),
			metric.WithMaxIdleConns(cfg.MaxIdleConns),
		)
		return metric.MySQL(cfg.DSN, opts...), nil
	case metric.DriverPostgreSQL:
		opts = append(opts,
			metric.WithMaxOpenConns(cfg.MaxOpenConns),
			metric.WithMaxIdleConns(cfg.MaxIdleConns),
		)
		return metric.PostgreSQL(cfg.DSN, opts...), nil
	default:
		return metric.Config{}, fmt.Errorf("unsupported metric database driver: %s", cfg.Driver)
	}
}

func defaultRollupPolicy() metric.RollupPolicy {
	return rollupPolicyFromValues(
		defaultRollupMinuteRetentionMinutes,
		defaultRollupFiveMinuteRetentionMinutes,
		defaultRollupHourRetentionHours,
	)
}

func rollupPolicyFromConfig(cfg *MetricStoreConfig) (metric.RollupPolicy, error) {
	if cfg == nil {
		return metric.RollupPolicy{}, fmt.Errorf("metric store config is nil")
	}

	minuteRetention := cfg.RollupMinuteRetentionMinutes
	fiveMinuteRetention := cfg.RollupFiveMinuteRetentionMinutes
	hourRetention := cfg.RollupHourRetentionHours
	// Configs constructed by older callers do not have the new fields. Treat
	// their zero values as omitted so recovery and migration remain compatible.
	if minuteRetention == 0 {
		minuteRetention = defaultRollupMinuteRetentionMinutes
	}
	if fiveMinuteRetention == 0 {
		fiveMinuteRetention = defaultRollupFiveMinuteRetentionMinutes
	}
	if hourRetention == 0 {
		hourRetention = defaultRollupHourRetentionHours
	}
	if minuteRetention < 0 || fiveMinuteRetention < 0 || hourRetention < 0 {
		return metric.RollupPolicy{}, fmt.Errorf("metric rollup retention values must be positive integers")
	}

	minuteDuration, err := rollupDuration(minuteRetention, time.Minute)
	if err != nil {
		return metric.RollupPolicy{}, err
	}
	fiveMinuteDuration, err := rollupDuration(fiveMinuteRetention, time.Minute)
	if err != nil {
		return metric.RollupPolicy{}, err
	}
	hourDuration, err := rollupDuration(hourRetention, time.Hour)
	if err != nil {
		return metric.RollupPolicy{}, err
	}

	policy := rollupPolicyFromDurations(minuteDuration, fiveMinuteDuration, hourDuration)
	if err := policy.Validate(); err != nil {
		return metric.RollupPolicy{}, fmt.Errorf("invalid metric rollup retention policy: %w", err)
	}
	return policy, nil
}

func rollupPolicyFromValues(minuteRetentionMinutes, fiveMinuteRetentionMinutes, hourRetentionHours int) metric.RollupPolicy {
	return rollupPolicyFromDurations(
		time.Duration(minuteRetentionMinutes)*time.Minute,
		time.Duration(fiveMinuteRetentionMinutes)*time.Minute,
		time.Duration(hourRetentionHours)*time.Hour,
	)
}

func rollupPolicyFromDurations(minuteRetention, fiveMinuteRetention, hourRetention time.Duration) metric.RollupPolicy {
	return metric.RollupPolicy{
		RawRetention: DefaultRollupRawRetention,
		Tiers: []metric.RollupTier{
			{Interval: time.Minute, Retention: minuteRetention},
			{Interval: 5 * time.Minute, Retention: fiveMinuteRetention},
			{Interval: time.Hour, Retention: hourRetention},
			// Daily buckets form the terminal tier. They remain available until
			// the metric's own retention policy removes them.
			{Interval: 24 * time.Hour, Retention: time.Duration(defaultRollupDayRetentionDays) * 24 * time.Hour},
		},
		Compression: 30,
	}
}

func rollupDuration(value int, unit time.Duration) (time.Duration, error) {
	maxDurationValue := int64((time.Duration(1<<63 - 1)) / unit)
	if value <= 0 || int64(value) > maxDurationValue {
		return 0, fmt.Errorf("metric rollup retention value must be a positive duration")
	}
	return time.Duration(value) * unit, nil
}

// ResolveDriverFromConfig 根据 DSN 自动推断 metrics 数据库类型；当 DSN 不能可靠
// 识别时回退到旧配置中的 driver，以兼容已有配置和非常规 DSN。
func ResolveDriverFromConfig(configuredDriver, dsn string) metric.Driver {
	if driver, ok := InferDriverFromDSN(dsn); ok {
		return driver
	}

	switch driver := metric.Driver(strings.ToLower(strings.TrimSpace(configuredDriver))); driver {
	case metric.DriverSQLite, metric.DriverMySQL, metric.DriverPostgreSQL:
		return driver
	default:
		return metric.DriverSQLite
	}
}

// InferDriverFromDSN 尽量根据常见 DSN 格式推断数据库类型。
// 返回 ok=false 表示格式不够明确，调用方应使用已有配置作为兜底。
func InferDriverFromDSN(dsn string) (metric.Driver, bool) {
	raw := strings.TrimSpace(dsn)
	if raw == "" {
		return metric.DriverSQLite, true
	}
	lower := strings.ToLower(raw)

	// PostgreSQL URL DSN: postgres://... 或 postgresql://...
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		return metric.DriverPostgreSQL, true
	}

	// SQLite 常见文件/内存 DSN。
	if raw == ":memory:" || strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "sqlite://") || strings.HasPrefix(lower, "sqlite3://") {
		return metric.DriverSQLite, true
	}

	// MySQL URL（虽然 go-sql-driver/mysql 原生 DSN 通常不是 URL，但这里用于给出
	// 类型推断；连接测试仍会校验 DSN 是否可被驱动接受）。
	if strings.HasPrefix(lower, "mysql://") {
		return metric.DriverMySQL, true
	}

	// PostgreSQL 关键字/值 DSN: host=... user=... dbname=...
	if looksLikePostgreSQLKeyValueDSN(lower) {
		return metric.DriverPostgreSQL, true
	}

	// go-sql-driver/mysql DSN: user:pass@tcp(host:3306)/db、user@unix(...)/db、user:pass@/db 等。
	if looksLikeMySQLDSN(lower) {
		return metric.DriverMySQL, true
	}

	// SQLite 路径：./data/metrics.db、/var/lib/metrics.sqlite3、metrics.sqlite 等。
	if looksLikeSQLitePath(lower) {
		return metric.DriverSQLite, true
	}

	return "", false
}

func looksLikePostgreSQLKeyValueDSN(lower string) bool {
	if !strings.Contains(lower, "=") || strings.Contains(lower, "://") {
		return false
	}
	keys := []string{"host=", "user=", "password=", "dbname=", "port=", "sslmode="}
	matched := 0
	for _, key := range keys {
		if strings.Contains(lower, key) {
			matched++
		}
	}
	// dbname= 基本是 PostgreSQL libpq DSN 的强特征；否则至少匹配两个常见键。
	return strings.Contains(lower, "dbname=") || matched >= 2
}

func looksLikeMySQLDSN(lower string) bool {
	if strings.Contains(lower, "://") || strings.Contains(lower, " ") {
		return false
	}
	if strings.Contains(lower, "@tcp(") || strings.Contains(lower, "@unix(") || strings.Contains(lower, "@/") {
		return true
	}
	// user:pass@host/db、user@host/db 这类虽然不是推荐格式，但也明显偏 MySQL。
	return strings.Contains(lower, "@") && strings.Contains(lower, "/")
}

func looksLikeSQLitePath(lower string) bool {
	path := lower
	if idx := strings.IndexAny(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return strings.HasSuffix(path, ".db") || strings.HasSuffix(path, ".sqlite") || strings.HasSuffix(path, ".sqlite3")
}

// stripSharedCache 从 SQLite DSN 中移除 cache=shared 参数，避免共享缓存模式下的
// 表级锁（SQLITE_LOCKED "database table is locked"）。其它参数保持不变。
func stripSharedCache(dsn string) string {
	if !strings.Contains(dsn, "cache=shared") {
		return dsn
	}
	idx := strings.Index(dsn, "?")
	if idx < 0 {
		return dsn
	}
	base := dsn[:idx]
	query := dsn[idx+1:]
	parts := strings.Split(query, "&")
	kept := parts[:0]
	for _, p := range parts {
		if p == "cache=shared" {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		return base
	}
	return base + "?" + strings.Join(kept, "&")
}
