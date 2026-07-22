package metricstore

import (
	"context"
	"errors"
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

var (
	store             *metric.Store
	storeFingerprint  string
	storeMu           sync.RWMutex
	storeInitMu       sync.Mutex
	storeOperations   = newStoreOperationGate()
	compactOperations = newStoreOperationGate()
	compactAt         int
)

var ErrCompactInProgress = errors.New("metric store compact already in progress")

const (
	// DefaultRollupRawRetention keeps a short hot raw window; older samples are
	// served from rollups after compaction.
	DefaultRollupRawRetention = 15 * time.Minute
	DefaultRollupFinestTier   = time.Minute
)

// MetricStoreConfig 保存 metric store 配置。
//
// 注意：metric store 现在始终启用（旧的 metric_store_enabled 开关已废弃）。
// 未显式配置时默认使用 SQLite（./data/metrics.db）。
type MetricStoreConfig struct {
	Driver          string `json:"metric_db_driver" default:"sqlite"`         // 数据库类型: sqlite, mysql, postgresql
	DSN             string `json:"metric_db_dsn" default:"./data/metrics.db"` // 数据库连接串
	LowResourceMode bool   `json:"low_resource_mode"`                         // 低资源模式由首次探测或后台设置决定
	TablePrefix     string `json:"metric_table_prefix" default:"metric_"`     // 表名前缀
	MaxOpenConns    int    `json:"metric_max_open_conns" default:"25"`        // 最大连接数
	MaxIdleConns    int    `json:"metric_max_idle_conns" default:"5"`         // 最大空闲连接数
}

// MetricStoreConfigKeys 配置键
//
// MetricStoreEnabledKey 已废弃：metric store 始终启用，保留常量仅用于清理旧配置。
const (
	MetricStoreEnabledKey = "metric_store_enabled" // Deprecated: metric store 始终启用
	MetricDBDriverKey     = "metric_db_driver"
	MetricDBDSNKey        = "metric_db_dsn"
	MetricTablePrefixKey  = "metric_table_prefix"
	MetricMaxOpenConnsKey = "metric_max_open_conns"
	MetricMaxIdleConnsKey = "metric_max_idle_conns"
	// MigrationTargetKey 记录上一次成功完成启动迁移的目标指纹（driver+dsn）。
	// 当目标发生变化（例如从 SQLite 切换到 MySQL/PostgreSQL）时，启动迁移会
	// 重新执行，把上一个目标库的数据搬运到新的目标 metrics 库。
	MigrationTargetKey = "metric_migration_target"
)

// buildMetricConfig 根据 MetricStoreConfig 构造底层 metric.Config。
// autoMigrate 控制是否在 Open 时自动建表：正式初始化/热加载时为 true，
// 仅做连接测试时为 false（不写入 schema，避免对目标库产生副作用）。
func buildMetricConfig(cfg *MetricStoreConfig, autoMigrate bool) (metric.Config, error) {
	driver := ResolveDriverFromConfig(cfg.Driver, cfg.DSN)

	tablePrefix := cfg.TablePrefix
	if tablePrefix == "" {
		tablePrefix = "metric_"
	}
	opts := []metric.Option{
		metric.WithTablePrefix(tablePrefix),
		metric.WithAutoMigrate(autoMigrate),
	}
	opts = append(opts, metric.WithRollupPolicy(defaultRollupPolicy()))

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
		if cfg.LowResourceMode {
			opts = append(opts,
				metric.WithSQLiteProfile(metric.SQLiteProfileBalanced),
				metric.WithSQLiteCacheSizeKB(8*1024),
				metric.WithSQLiteMMapSize(0),
				metric.WithSQLiteTempStoreMemory(false),
				metric.WithSQLiteReadPool(0),
			)
		} else {
			opts = append(opts, metric.WithSQLiteReadPool(4))
		}
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
	return metric.RollupPolicy{
		RawRetention: DefaultRollupRawRetention,
		Tiers: []metric.RollupTier{
			{Interval: DefaultRollupFinestTier, Retention: 48 * time.Hour},
			{Interval: 5 * time.Minute, Retention: 14 * 24 * time.Hour},
			{Interval: time.Hour, Retention: 14 * 24 * time.Hour},
		},
	}
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

// openStore 按配置打开 metric store 并创建指标定义。
func openStore(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
	return openStoreWithDefaultRetention(ctx, cfg, defaultBuiltinMetricRetentionDays)
}

func openStoreWithDefaultRetention(ctx context.Context, cfg *MetricStoreConfig, defaultRetentionDays int) (*metric.Store, error) {
	metricCfg, err := buildMetricConfig(cfg, true)
	if err != nil {
		return nil, err
	}

	s, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open metric store: %w", err)
	}

	if err := createMetricDefinitionsWithDefaultRetention(ctx, s, defaultRetentionDays); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to create metric definitions: %w", err)
	}

	return s, nil
}

// OpenStore opens an isolated metric store using the supplied configuration.
// It is used by the pre-start upgrade flow before the process-wide store is
// initialized. The caller owns the returned store and must close it.
func OpenStore(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
	return openStore(ctx, cfg)
}

// OpenStoreForMigration opens an isolated target and uses the legacy data span
// as the initial retention for definitions that do not exist yet. Existing
// definitions keep their configured retention, including an explicit zero.
func OpenStoreForMigration(ctx context.Context, cfg *MetricStoreConfig, legacyRetentionDays int) (*metric.Store, error) {
	if legacyRetentionDays < defaultBuiltinMetricRetentionDays {
		legacyRetentionDays = defaultBuiltinMetricRetentionDays
	}
	return openStoreWithDefaultRetention(ctx, cfg, legacyRetentionDays)
}

// TestConnection 使用给定配置尝试连接 metrics 数据库（不影响当前运行的 store）。
// 仅打开连接并 Ping，不执行自动建表，连接成功后立即关闭。失败时返回可读错误。
func TestConnection(ctx context.Context, cfg *MetricStoreConfig) error {
	metricCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return err
	}

	s, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return err
	}
	defer s.Close()

	return s.Ping(ctx)
}

// InitializeStore 初始化 metric store（启动时调用，可在失败后重试）。
func InitializeStore() error {
	storeInitMu.Lock()
	defer storeInitMu.Unlock()

	// A previous failed connection must remain retryable. The old sync.Once
	// implementation consumed the one-time call on failure and returned nil on
	// every later call while the store was still nil.
	storeMu.RLock()
	initialized := store != nil
	storeMu.RUnlock()
	if initialized {
		return nil
	}

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}

	// metric store 始终启用；未配置时默认 SQLite（./data/metrics.db）。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}

	storeMu.Lock()
	store = s
	storeFingerprint = targetFingerprint(cfg)
	storeMu.Unlock()
	setLowResourceMode(cfg.LowResourceMode)
	clearStoreClosing()

	logger.Infof("metricstore", "Metric store initialized successfully (driver=%s)", ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
	return nil
}

// RecoverStore opens, persists, and activates a replacement store selected
// from the restricted recovery page. Marking the opened target as current
// makes the normal startup migration a no-op: recovery reconnects only and
// never copies data from the unavailable store.
func RecoverStore(ctx context.Context, cfg *MetricStoreConfig) error {
	if cfg == nil {
		return fmt.Errorf("metric store recovery config is nil")
	}

	storeInitMu.Lock()
	defer storeInitMu.Unlock()
	if err := storeOperations.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for metric store operations before recovery: %w", err)
	}
	defer storeOperations.Release()
	if isStoreClosing() {
		return ErrStoreBusy
	}

	recovered := *cfg
	recovered.DSN = strings.TrimSpace(recovered.DSN)
	recovered.Driver = string(ResolveDriverFromConfig(recovered.Driver, recovered.DSN))
	s, err := openStore(ctx, &recovered)
	if err != nil {
		return err
	}

	target := targetFingerprint(&recovered)
	if err := config.SetMany(map[string]any{
		MetricDBDriverKey:  recovered.Driver,
		MetricDBDSNKey:     recovered.DSN,
		MigrationTargetKey: target,
	}); err != nil {
		_ = s.Close()
		return fmt.Errorf("save recovered metric store config: %w", err)
	}

	storeMu.Lock()
	old := store
	store = s
	storeFingerprint = target
	storeMu.Unlock()
	setLowResourceMode(recovered.LowResourceMode)
	clearStoreClosing()

	if old != nil {
		if closeErr := old.Close(); closeErr != nil {
			logger.Errorf("metricstore", "Failed to close previous metric store during recovery: %v", closeErr)
		}
	}
	logger.Infof("metricstore", "Metric store recovered successfully (driver=%s)", recovered.Driver)
	return nil
}

// Reload 根据最新配置热重载 metric store，无需重启进程。
// metric store 始终启用：用新配置打开并建表（内部已 Ping 校验连接），
// 成功后再替换运行中的 store，最后关闭旧实例。任何失败都会保留旧 store 不变。
//
// 注意：Reload 只切换运行中的连接，不会把旧目标（如 SQLite）中的历史数据
// 搬运到新目标（如 MySQL/PostgreSQL）。跨库数据迁移由启动迁移
// （RunStartupMigration）在下次启动时按目标指纹自动完成。
func Reload(ctx context.Context) error {
	if err := storeOperations.Acquire(ctx); err != nil {
		return fmt.Errorf("wait for metric store operations before reload: %w", err)
	}
	defer storeOperations.Release()
	if isStoreClosing() {
		return ErrStoreBusy
	}

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}

	// 用新配置打开并建表（内部已 Ping 校验连接）。
	s, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}

	storeMu.Lock()
	old := store
	store = s
	storeFingerprint = targetFingerprint(cfg)
	storeMu.Unlock()
	setLowResourceMode(cfg.LowResourceMode)

	if old != nil {
		if cerr := old.Close(); cerr != nil {
			logger.Errorf("metricstore", "Failed to close previous metric store on reload: %v", cerr)
		}
	}

	logger.Infof("metricstore", "Metric store reloaded successfully (driver=%s)", ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
	return nil
}

// GetStore 获取 metric store 实例（如果未启用返回 nil）
func GetStore() *metric.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

// RetentionSummary is the compatibility view of all persisted metric policies.
type RetentionSummary struct {
	AllPositive bool
	MaxDays     int
}

// GetRetentionSummary aggregates the active store's metric definitions. An
// empty definition set is not considered record-enabled.
func GetRetentionSummary(ctx context.Context) (RetentionSummary, error) {
	s := GetStore()
	if s == nil {
		return RetentionSummary{}, fmt.Errorf("metric store not initialized")
	}
	defs, err := s.ListMetrics(ctx)
	if err != nil {
		return RetentionSummary{}, err
	}
	return summarizeRetentionDefinitions(defs), nil
}

func summarizeRetentionDefinitions(defs []metric.Definition) RetentionSummary {
	if len(defs) == 0 {
		return RetentionSummary{}
	}
	summary := RetentionSummary{AllPositive: true}
	for _, def := range defs {
		if def.RetentionDays <= 0 {
			summary.AllPositive = false
		}
		if def.RetentionDays > summary.MaxDays {
			summary.MaxDays = def.RetentionDays
		}
	}
	return summary
}

func Compact(ctx context.Context, now time.Time) (int, error) {
	if !compactOperations.TryAcquire() {
		return 0, ErrCompactInProgress
	}
	defer compactOperations.Release()
	if err := storeOperations.Acquire(ctx); err != nil {
		return 0, fmt.Errorf("wait for metric store operations before compaction: %w", err)
	}
	defer storeOperations.Release()

	storeMu.RLock()
	activeStore := store
	storeMu.RUnlock()
	if activeStore == nil {
		return 0, fmt.Errorf("metric store not initialized")
	}

	defs, err := activeStore.ListMetrics(ctx)
	if err != nil {
		return 0, err
	}
	if len(defs) == 0 {
		compactAt = 0
		return 0, nil
	}
	if compactAt >= len(defs) {
		compactAt = 0
	}

	total := 0
	start := compactAt
	failedAt := -1
	var compactErrors []error
	for i := 0; i < len(defs); i++ {
		idx := (start + i) % len(defs)
		n, err := activeStore.CompactMetric(ctx, defs[idx].Name, now)
		if err != nil {
			if failedAt < 0 {
				failedAt = idx
			}
			compactErrors = append(compactErrors, fmt.Errorf("compact metric %q: %w", defs[idx].Name, err))
			continue
		}
		total += n
	}
	if _, err := activeStore.CleanupExpired(ctx, now); err != nil {
		compactErrors = append(compactErrors, fmt.Errorf("clean up expired raw metrics: %w", err))
	}
	if failedAt >= 0 {
		compactAt = failedAt
	} else {
		compactAt = start
	}
	return total, errors.Join(compactErrors...)
}

// CloseStoreContext stops the asynchronous store migration before taking the
// store write lock, so shutdown cannot wait forever on the migration's lease.
func CloseStoreContext(ctx context.Context) error {
	if err := stopStoreMigrationForClose(ctx); err != nil {
		clearStoreClosing()
		return err
	}
	if err := storeOperations.Acquire(ctx); err != nil {
		clearStoreClosing()
		return fmt.Errorf("wait for metric store operations before close: %w", err)
	}
	defer storeOperations.Release()

	storeMu.Lock()
	defer storeMu.Unlock()

	if store != nil {
		err := store.Close()
		store = nil
		storeFingerprint = ""
		return err
	}
	storeFingerprint = ""
	return nil
}

const defaultBuiltinMetricRetentionDays = 1

// createMetricDefinitions creates built-in definitions with explicit policies.
func createMetricDefinitions(ctx context.Context, s *metric.Store) error {
	return createMetricDefinitionsWithDefaultRetention(ctx, s, defaultBuiltinMetricRetentionDays)
}

// EnsureBuiltinMetricDefinitions registers definitions for the server's
// built-in report and ping writers before a standalone Store receives points.
func EnsureBuiltinMetricDefinitions(ctx context.Context, s *metric.Store) error {
	return createMetricDefinitions(ctx, s)
}

func createMetricDefinitionsWithDefaultRetention(ctx context.Context, s *metric.Store, defaultRetentionDays int) error {
	if defaultRetentionDays < defaultBuiltinMetricRetentionDays {
		defaultRetentionDays = defaultBuiltinMetricRetentionDays
	}
	definitions := []metric.Definition{
		{Name: MetricCPU, Type: metric.TypeGauge, Unit: "%", Description: "CPU usage percentage", RetentionDays: defaultRetentionDays},
		{Name: MetricGPU, Type: metric.TypeGauge, Unit: "%", Description: "GPU usage percentage", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUDeviceUsage, Type: metric.TypeGauge, Unit: "%", Description: "Per-device GPU utilization", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUMem, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory used", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUMemTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory total", RetentionDays: defaultRetentionDays},
		{Name: MetricGPUTemp, Type: metric.TypeGauge, Unit: "°C", Description: "GPU temperature", RetentionDays: defaultRetentionDays},
		{Name: MetricRAM, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM used", RetentionDays: defaultRetentionDays},
		{Name: MetricSwap, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap used", RetentionDays: defaultRetentionDays},
		{Name: MetricLoad, Type: metric.TypeGauge, Unit: "", Description: "System load average", RetentionDays: defaultRetentionDays},
		{Name: MetricDisk, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk used", RetentionDays: defaultRetentionDays},
		{Name: MetricNetIn, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network in rate", RetentionDays: defaultRetentionDays},
		{Name: MetricNetOut, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network out rate", RetentionDays: defaultRetentionDays},
		{Name: MetricNetTotalUp, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total upload", RetentionDays: defaultRetentionDays},
		{Name: MetricNetTotalDown, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total download", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficUp, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic upload delta", RetentionDays: defaultRetentionDays},
		{Name: MetricTrafficDown, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic download delta", RetentionDays: defaultRetentionDays},
		{Name: MetricProcess, Type: metric.TypeGauge, Unit: "count", Description: "Process count", RetentionDays: defaultRetentionDays},
		{Name: MetricConnections, Type: metric.TypeGauge, Unit: "count", Description: "TCP connections", RetentionDays: defaultRetentionDays},
		{Name: MetricConnectionsUDP, Type: metric.TypeGauge, Unit: "count", Description: "UDP connections", RetentionDays: defaultRetentionDays},
		{Name: MetricPingLatency, Type: metric.TypeGauge, Unit: "ms", Description: "Ping latency", RetentionDays: defaultRetentionDays},
		{Name: MetricPingLoss, Type: metric.TypeGauge, Unit: "ratio", Description: "Ping packet loss indicator", RetentionDays: defaultRetentionDays},
	}

	for _, def := range definitions {
		existing, err := s.GetMetric(ctx, def.Name)
		if err != nil && !errors.Is(err, metric.ErrNotFound) {
			return fmt.Errorf("failed to get metric %s: %w", def.Name, err)
		}
		if err == nil {
			if existing.RetentionDays == 0 {
				if _, err := s.SetMetricRetention(ctx, def.Name, 0); err != nil {
					return fmt.Errorf("failed to preserve disabled metric %s: %w", def.Name, err)
				}
				continue
			}
			def.RetentionDays = existing.RetentionDays
		}
		if err := s.UpsertMetric(ctx, def); err != nil {
			return fmt.Errorf("failed to create metric %s: %w", def.Name, err)
		}
	}
	for _, name := range obsoleteBuiltinMetricNames {
		if err := s.DeleteMetric(ctx, name); err != nil {
			return fmt.Errorf("failed to remove obsolete metric %s: %w", name, err)
		}
	}

	return nil
}

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	reportBatcherMu.Lock()
	worker := reportBatcher
	reportBatcherMu.Unlock()
	if worker != nil {
		return worker.enqueuePing(ctx, rec)
	}
	return writePingRecords(ctx, []models.PingRecord{rec})
}

func writePingRecords(ctx context.Context, records []models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if len(records) == 0 {
		return nil
	}

	points := make([]metric.Point, 0, len(records)*2)
	for _, rec := range records {
		tags := map[string]string{"task_id": fmt.Sprintf("%d", rec.TaskId)}
		loss := 0.0
		if rec.Value < 0 {
			loss = 1
		}
		points = append(points,
			metric.Point{
				MetricName: MetricPingLatency,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      float64(rec.Value),
				Tags:       tags,
			},
			metric.Point{
				MetricName: MetricPingLoss,
				EntityID:   rec.Client,
				Timestamp:  rec.Time,
				Value:      loss,
				Tags:       tags,
			},
		)
	}
	return s.WriteBatch(ctx, points)
}

// GetRecordsByClientAndTime 从 metric store 查询记录并重构为 models.Record
func GetRecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	return getRecordsByClientAndTimeFromSeries(ctx, s, clientUUID, start, end)
}

// GetRecordsByTime 从 metric store 查询所有客户端在时间范围内的记录
func GetRecordsByTime(ctx context.Context, start, end time.Time) ([]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	interval := recordSeriesInterval(s, start, end, time.Now().UTC())
	entityIDs, err := listRecordEntityIDs(ctx, s, start, end, interval)
	if err != nil {
		return nil, err
	}
	var records []models.Record
	for _, entityID := range entityIDs {
		items, err := getRecordsByClientAndTimeFromSeries(ctx, s, entityID, start, end)
		if err != nil {
			return nil, err
		}
		records = append(records, items...)
	}
	sortRecords(records)
	return records, nil
}

type recordSeriesKey struct {
	client string
	ts     int64
}

func getRecordsByClientAndTimeFromSeries(ctx context.Context, s *metric.Store, clientUUID string, start, end time.Time) ([]models.Record, error) {
	now := time.Now().UTC()
	interval := recordSeriesInterval(s, start, end, now)
	recordMap := make(map[recordSeriesKey]*models.Record)

	for _, metricName := range loadRecordMetricNames {
		points, err := s.Series(ctx, metric.AggregateQuery{
			Query: metric.Query{
				MetricName: metricName,
				EntityID:   clientUUID,
				Start:      start,
				End:        end,
				Order:      metric.OrderAsc,
			},
			Aggregation: recordMetricAggregation(metricName),
			Interval:    interval,
		}, now)
		if err != nil {
			return nil, fmt.Errorf("failed to query metric %s: %w", metricName, err)
		}
		for _, point := range points {
			entityID := point.EntityID
			if entityID == "" {
				entityID = clientUUID
			}
			key := recordSeriesKey{client: entityID, ts: point.Bucket.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.Record{
					Client: entityID,
					Time:   point.Bucket.UTC(),
				}
			}
			applyRecordMetricValue(recordMap[key], metricName, point.Value)
		}
	}

	records := make([]models.Record, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}
	sortRecords(records)
	return records, nil
}

func recordMetricAggregation(metricName string) metric.Aggregation {
	switch metricName {
	case MetricTrafficUp, MetricTrafficDown:
		return metric.AggSum
	case MetricNetTotalUp, MetricNetTotalDown:
		return metric.AggLast
	default:
		return metric.AggAvg
	}
}

func recordSeriesInterval(s *metric.Store, start, end, now time.Time) time.Duration {
	interval := recordDownsampleInterval(end.Sub(start), 500)
	return s.CompatibleSeriesInterval(start, now, interval)
}

func recordDownsampleInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 500
	}
	nanos := rangeDuration.Nanoseconds()
	if nanos <= 0 {
		return time.Second
	}
	interval := time.Duration((nanos + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.FloorStandardInterval(interval)
}

func listRecordEntityIDs(ctx context.Context, s *metric.Store, start, end time.Time, interval time.Duration) ([]string, error) {
	seen := make(map[string]struct{})
	for _, metricName := range loadRecordMetricNames {
		ids, err := s.EntityIDs(ctx, metric.Query{
			MetricName: metricName,
			Start:      start.Add(-interval),
			End:        end,
		})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func applyRecordMetricValue(rec *models.Record, metricName string, value float64) {
	switch metricName {
	case MetricCPU:
		rec.Cpu = float32(value)
	case MetricGPU:
		rec.Gpu = float32(value)
	case MetricRAM:
		rec.Ram = int64(value)
	case MetricSwap:
		rec.Swap = int64(value)
	case MetricLoad:
		rec.Load = float32(value)
	case MetricDisk:
		rec.Disk = int64(value)
	case MetricNetIn:
		rec.NetIn = int64(value)
	case MetricNetOut:
		rec.NetOut = int64(value)
	case MetricNetTotalUp:
		rec.NetTotalUp = int64(value)
	case MetricNetTotalDown:
		rec.NetTotalDown = int64(value)
	case MetricTrafficUp:
		rec.TrafficUp = int64(value)
	case MetricTrafficDown:
		rec.TrafficDown = int64(value)
	case MetricProcess:
		rec.Process = int(value)
	case MetricConnections:
		rec.Connections = int(value)
	case MetricConnectionsUDP:
		rec.ConnectionsUdp = int(value)
	}
}

func sortRecords(records []models.Record) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Client != records[j].Client {
			return records[i].Client < records[j].Client
		}
		return records[i].Time.Before(records[j].Time)
	})
}

// GetGPURecordsByClientAndTime 从 metric store 查询 GPU 记录
func GetGPURecordsByClientAndTime(ctx context.Context, clientUUID string, start, end time.Time) ([]models.GPURecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	// 查询 GPU 相关指标（每设备利用率使用独立指标 gpu.device.usage）
	gpuMetrics := []string{MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp}

	// 按设备索引和时间组织数据
	type gpuKey struct {
		deviceIndex int
		timestamp   int64
	}
	recordMap := make(map[gpuKey]*models.GPURecord)

	for _, metricName := range gpuMetrics {
		points, err := s.Query(ctx, metric.Query{
			MetricName: metricName,
			EntityID:   clientUUID,
			Start:      start,
			End:        end,
			Order:      metric.OrderAsc,
		})
		if err != nil {
			continue // GPU 数据可能不存在
		}

		for _, p := range points {
			deviceIndex := 0
			deviceName := ""
			if idx, ok := p.Tags["device_index"]; ok {
				fmt.Sscanf(idx, "%d", &deviceIndex)
			}
			if name, ok := p.Tags["device_name"]; ok {
				deviceName = name
			}

			key := gpuKey{deviceIndex: deviceIndex, timestamp: p.Timestamp.Unix()}
			if recordMap[key] == nil {
				recordMap[key] = &models.GPURecord{
					Client:      clientUUID,
					Time:        p.Timestamp.UTC(),
					DeviceIndex: deviceIndex,
					DeviceName:  deviceName,
				}
			}
			rec := recordMap[key]

			switch metricName {
			case MetricGPUDeviceUsage:
				rec.Utilization = float32(p.Value)
			case MetricGPUMem:
				rec.MemUsed = int64(p.Value)
			case MetricGPUMemTotal:
				rec.MemTotal = int64(p.Value)
			case MetricGPUTemp:
				rec.Temperature = int(p.Value)
			}
		}
	}

	// 转换为切片
	records := make([]models.GPURecord, 0, len(recordMap))
	for _, rec := range recordMap {
		records = append(records, *rec)
	}

	return records, nil
}

// GetPingRecords 从 metric store 查询兼容旧接口的 ping 记录。
//
// 旧接口过去直接读取 ping_records 的原始点。启用 metric rollup 后，较旧
// 的 raw 点会被压入 rollup 并删除，因此这里使用 Series 走与 queryMetrics
// 相同的 raw/rollup 混合读取路径，并保留 task_id 标签。
func GetPingRecords(ctx context.Context, clientUUID string, taskID int, start, end time.Time) ([]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	query := metric.Query{
		MetricName: MetricPingLatency,
		Start:      start,
		End:        end,
		Order:      metric.OrderAsc,
	}

	if clientUUID != "" {
		query.EntityID = clientUUID
	}

	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	interval := pingQueryInterval(end.Sub(start), 4000)
	interval = s.CompatibleSeriesInterval(start, time.Now().UTC(), interval)
	points, err := s.Series(ctx, metric.AggregateQuery{
		Query:          query,
		Aggregation:    metric.AggLast,
		Interval:       interval,
		PreserveSeries: true,
	}, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	records := make([]models.PingRecord, 0, len(points))
	for _, p := range points {
		taskIDVal := uint(0)
		if tid, ok := p.Tags["task_id"]; ok {
			var t uint64
			fmt.Sscanf(tid, "%d", &t)
			taskIDVal = uint(t)
		}

		records = append(records, models.PingRecord{
			Client: p.EntityID,
			TaskId: taskIDVal,
			Time:   p.Bucket.UTC(),
			Value:  int(p.Value),
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Time.After(records[j].Time)
	})

	return records, nil
}

func pingQueryInterval(rangeDuration time.Duration, maxPoints int) time.Duration {
	if maxPoints <= 0 {
		maxPoints = 4000
	}
	if rangeDuration <= 0 {
		return time.Second
	}
	interval := time.Duration((rangeDuration.Nanoseconds() + int64(maxPoints) - 1) / int64(maxPoints))
	if interval < time.Second {
		return time.Second
	}
	return metric.FloorStandardInterval(interval)
}

// farFuture 返回一个足够远的未来时间，用于以 DeleteBefore 语义清空某指标的全部数据。
func farFuture() time.Time {
	return time.Now().UTC().Add(24 * 365 * time.Hour)
}

// DeleteAllRecords 删除所有负载/系统类记录（保留指标定义，不含 ping）。
func DeleteAllRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	for _, metricName := range recordMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			logger.Errorf("metricstore", "Failed to delete metric %s: %v", metricName, err)
		}
	}
	clearReportTrafficStates()

	return nil
}

// DeleteAllPingRecords 删除全部 ping 记录（保留指标定义）。
func DeleteAllPingRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, metricName := range pingMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			return fmt.Errorf("failed to delete ping records: %w", err)
		}
	}
	return nil
}

// DeletePingRecordsByTask 删除指定任务（task_id）的全部 ping 记录。
func DeletePingRecordsByTask(ctx context.Context, taskIDs []uint) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, id := range taskIDs {
		for _, metricName := range pingMetricNames {
			if _, err := s.DeleteSeries(ctx, metric.Query{
				MetricName: metricName,
				Tags:       map[string]string{"task_id": fmt.Sprintf("%d", id)},
			}); err != nil {
				return fmt.Errorf("failed to delete ping records for task %d: %w", id, err)
			}
		}
	}
	return nil
}

// DeleteEntity 删除指定 agent 在所有指标下的历史数据。
func DeleteEntity(ctx context.Context, entityID string) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	if _, err := s.DeleteEntity(ctx, entityID); err != nil {
		return fmt.Errorf("failed to delete metric records for entity %s: %w", entityID, err)
	}
	deleteReportTrafficState(entityID)
	return nil
}

// DeleteEntityAsync clears one agent's metric history without delaying the
// client deletion response.
func DeleteEntityAsync(entityID string) {
	go func() {
		if err := DeleteEntity(context.Background(), entityID); err != nil {
			logger.Errorf("metricstore", "Failed to delete metric records for entity %s: %v", entityID, err)
		}
	}()
}

// DeleteMetricDataAsync clears disabled metric history without delaying an
// admin retention-policy update response.
func DeleteMetricDataAsync(metricName string) {
	go func() {
		s := GetStore()
		if s == nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: metric store not enabled", metricName)
			return
		}
		if _, err := s.DeleteMetricDataIfDisabled(context.Background(), metricName); err != nil {
			logger.Errorf("metricstore", "Failed to delete disabled metric %s: %v", metricName, err)
		}
	}()
}
