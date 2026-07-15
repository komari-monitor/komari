package metricstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

// 指标名称常量
const (
	MetricCPU            = "cpu.usage"
	MetricGPU            = "gpu.usage"        // 实体级平均 GPU 使用率（对应 Record.Gpu）
	MetricGPUDeviceUsage = "gpu.device.usage" // 每设备 GPU 利用率（带 device_index 标签）
	MetricGPUMem         = "gpu.memory.used"
	MetricGPUMemTotal    = "gpu.memory.total"
	MetricGPUTemp        = "gpu.temperature"
	MetricRAM            = "memory.used"
	MetricRAMTotal       = "memory.total"
	MetricSwap           = "swap.used"
	MetricSwapTotal      = "swap.total"
	MetricLoad           = "load.average"
	MetricTemp           = "temperature"
	MetricDisk           = "disk.used"
	MetricDiskTotal      = "disk.total"
	MetricNetIn          = "net.in.rate"
	MetricNetOut         = "net.out.rate"
	MetricNetTotalUp     = "net.total.up"
	MetricNetTotalDown   = "net.total.down"
	MetricTrafficUp      = "traffic.up"
	MetricTrafficDown    = "traffic.down"
	MetricProcess        = "process.count"
	MetricConnections    = "connections.tcp"
	MetricConnectionsUDP = "connections.udp"
	MetricPingLatency    = "ping.latency_ms"
	MetricPingLoss       = "ping.loss"
)

var (
	store            *metric.Store
	storeFingerprint string
	storeMu          sync.RWMutex
	storeOnce        sync.Once
	storeOperations  = newStoreOperationGate()
	compactAt        int
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
	Driver              string `json:"metric_db_driver" default:"sqlite"`           // 数据库类型: sqlite, mysql, postgresql
	DSN                 string `json:"metric_db_dsn" default:"./data/metrics.db"`   // 数据库连接串
	DownsamplingEnabled bool   `json:"metric_downsampling_enabled" default:"false"` // 是否启用分层降采样
	TablePrefix         string `json:"metric_table_prefix" default:"metric_"`       // 表名前缀
	MaxOpenConns        int    `json:"metric_max_open_conns" default:"25"`          // 最大连接数
	MaxIdleConns        int    `json:"metric_max_idle_conns" default:"5"`           // 最大空闲连接数
}

// MetricStoreConfigKeys 配置键
//
// MetricStoreEnabledKey 已废弃：metric store 始终启用，保留常量仅用于清理旧配置。
const (
	MetricStoreEnabledKey        = "metric_store_enabled" // Deprecated: metric store 始终启用
	MetricDBDriverKey            = "metric_db_driver"
	MetricDBDSNKey               = "metric_db_dsn"
	MetricDownsamplingEnabledKey = "metric_downsampling_enabled"
	MetricTablePrefixKey         = "metric_table_prefix"
	MetricMaxOpenConnsKey        = "metric_max_open_conns"
	MetricMaxIdleConnsKey        = "metric_max_idle_conns"
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
	if cfg.DownsamplingEnabled {
		opts = append(opts, metric.WithRollupPolicy(defaultRollupPolicy()))
	}

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
		opts = append(opts,
			metric.WithMaxOpenConns(1),
			metric.WithMaxIdleConns(1),
			metric.WithSQLiteReadPool(4),
		)
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
	metricCfg, err := buildMetricConfig(cfg, true)
	if err != nil {
		return nil, err
	}

	s, err := metric.Open(ctx, metricCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open metric store: %w", err)
	}

	if err := createMetricDefinitions(ctx, s); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to create metric definitions: %w", err)
	}

	return s, nil
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

// InitializeStore 初始化 metric store（启动时调用，仅执行一次）。
func InitializeStore() error {
	var initErr error
	storeOnce.Do(func() {
		cfg, err := config.GetManyAs[MetricStoreConfig]()
		if err != nil {
			initErr = fmt.Errorf("failed to load metric store config: %w", err)
			return
		}

		// metric store 始终启用；未配置时默认 SQLite（./data/metrics.db）。
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		s, err := openStore(ctx, cfg)
		if err != nil {
			initErr = err
			return
		}

		storeMu.Lock()
		store = s
		storeFingerprint = targetFingerprint(cfg)
		storeMu.Unlock()
		clearStoreClosing()

		log.Printf("Metric store initialized successfully (driver=%s)", ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
	})

	return initErr
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

	if old != nil {
		if cerr := old.Close(); cerr != nil {
			log.Printf("Failed to close previous metric store on reload: %v", cerr)
		}
	}

	log.Printf("Metric store reloaded successfully (driver=%s)", ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
	return nil
}

// GetStore 获取 metric store 实例（如果未启用返回 nil）
func GetStore() *metric.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return store
}

// IsEnabled 检查 metric store 是否已启用
func IsEnabled() bool {
	return GetStore() != nil
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
	if !storeOperations.TryAcquire() {
		return 0, ErrCompactInProgress
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
	for i := 0; i < len(defs); i++ {
		idx := (start + i) % len(defs)
		n, err := activeStore.CompactMetric(ctx, defs[idx].Name, now)
		if err != nil {
			compactAt = idx
			return total, fmt.Errorf("compact metric %q: %w", defs[idx].Name, err)
		}
		total += n
		compactAt = (idx + 1) % len(defs)
	}
	if _, err := activeStore.CleanupExpired(ctx, now); err != nil {
		return total, fmt.Errorf("clean up expired raw metrics: %w", err)
	}
	return total, nil
}

// CloseStore closes the metric store after stopping any API-triggered migration.
func CloseStore() error {
	return CloseStoreContext(context.Background())
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

const defaultBuiltinMetricRetentionDays = 30

// createMetricDefinitions creates built-in definitions with explicit policies.
func createMetricDefinitions(ctx context.Context, s *metric.Store) error {
	definitions := []metric.Definition{
		{Name: MetricCPU, Type: metric.TypeGauge, Unit: "%", Description: "CPU usage percentage", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricGPU, Type: metric.TypeGauge, Unit: "%", Description: "GPU usage percentage", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricGPUDeviceUsage, Type: metric.TypeGauge, Unit: "%", Description: "Per-device GPU utilization", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricGPUMem, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory used", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricGPUMemTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "GPU memory total", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricGPUTemp, Type: metric.TypeGauge, Unit: "°C", Description: "GPU temperature", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricRAM, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM used", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricRAMTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "RAM total", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricSwap, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap used", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricSwapTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "Swap total", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricLoad, Type: metric.TypeGauge, Unit: "", Description: "System load average", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricTemp, Type: metric.TypeGauge, Unit: "°C", Description: "System temperature", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricDisk, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk used", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricDiskTotal, Type: metric.TypeGauge, Unit: "bytes", Description: "Disk total", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricNetIn, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network in rate", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricNetOut, Type: metric.TypeGauge, Unit: "bytes/s", Description: "Network out rate", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricNetTotalUp, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total upload", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricNetTotalDown, Type: metric.TypeCounter, Unit: "bytes", Description: "Network total download", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricTrafficUp, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic upload delta", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricTrafficDown, Type: metric.TypeGauge, Unit: "bytes", Description: "Traffic download delta", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricProcess, Type: metric.TypeGauge, Unit: "count", Description: "Process count", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricConnections, Type: metric.TypeGauge, Unit: "count", Description: "TCP connections", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricConnectionsUDP, Type: metric.TypeGauge, Unit: "count", Description: "UDP connections", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPingLatency, Type: metric.TypeGauge, Unit: "ms", Description: "Ping latency", RetentionDays: defaultBuiltinMetricRetentionDays},
		{Name: MetricPingLoss, Type: metric.TypeGauge, Unit: "ratio", Description: "Ping packet loss indicator", RetentionDays: defaultBuiltinMetricRetentionDays},
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

	return nil
}

// WriteRecord 将 models.Record 写入 metric store
func WriteRecord(ctx context.Context, rec models.Record) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client

	points := []metric.Point{
		{MetricName: MetricCPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Cpu)},
		{MetricName: MetricGPU, EntityID: entityID, Timestamp: ts, Value: float64(rec.Gpu)},
		{MetricName: MetricRAM, EntityID: entityID, Timestamp: ts, Value: float64(rec.Ram)},
		{MetricName: MetricRAMTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.RamTotal)},
		{MetricName: MetricSwap, EntityID: entityID, Timestamp: ts, Value: float64(rec.Swap)},
		{MetricName: MetricSwapTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.SwapTotal)},
		{MetricName: MetricLoad, EntityID: entityID, Timestamp: ts, Value: float64(rec.Load)},
		{MetricName: MetricTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temp)},
		{MetricName: MetricDisk, EntityID: entityID, Timestamp: ts, Value: float64(rec.Disk)},
		{MetricName: MetricDiskTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.DiskTotal)},
		{MetricName: MetricNetIn, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetIn)},
		{MetricName: MetricNetOut, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetOut)},
		{MetricName: MetricNetTotalUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalUp)},
		{MetricName: MetricNetTotalDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.NetTotalDown)},
		{MetricName: MetricTrafficUp, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficUp)},
		{MetricName: MetricTrafficDown, EntityID: entityID, Timestamp: ts, Value: float64(rec.TrafficDown)},
		{MetricName: MetricProcess, EntityID: entityID, Timestamp: ts, Value: float64(rec.Process)},
		{MetricName: MetricConnections, EntityID: entityID, Timestamp: ts, Value: float64(rec.Connections)},
		{MetricName: MetricConnectionsUDP, EntityID: entityID, Timestamp: ts, Value: float64(rec.ConnectionsUdp)},
	}

	return s.WriteBatch(ctx, points)
}

// WriteGPURecord 将 models.GPURecord 写入 metric store
func WriteGPURecord(ctx context.Context, rec models.GPURecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client
	tags := map[string]string{
		"device_index": fmt.Sprintf("%d", rec.DeviceIndex),
		"device_name":  rec.DeviceName,
	}

	points := []metric.Point{
		{MetricName: MetricGPUMem, EntityID: entityID, Timestamp: ts, Value: float64(rec.MemUsed), Tags: tags},
		{MetricName: MetricGPUMemTotal, EntityID: entityID, Timestamp: ts, Value: float64(rec.MemTotal), Tags: tags},
		{MetricName: MetricGPUDeviceUsage, EntityID: entityID, Timestamp: ts, Value: float64(rec.Utilization), Tags: tags},
		{MetricName: MetricGPUTemp, EntityID: entityID, Timestamp: ts, Value: float64(rec.Temperature), Tags: tags},
	}

	return s.WriteBatch(ctx, points)
}

// WritePingRecord 将 ping 记录写入 metric store
func WritePingRecord(ctx context.Context, rec models.PingRecord) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	ts := rec.Time.ToTime()
	entityID := rec.Client
	tags := map[string]string{
		"task_id": fmt.Sprintf("%d", rec.TaskId),
	}

	loss := 0.0
	if rec.Value < 0 {
		loss = 1
	}
	points := []metric.Point{
		{
			MetricName: MetricPingLatency,
			EntityID:   entityID,
			Timestamp:  ts,
			Value:      float64(rec.Value),
			Tags:       tags,
		},
		{
			MetricName: MetricPingLoss,
			EntityID:   entityID,
			Timestamp:  ts,
			Value:      loss,
			Tags:       tags,
		},
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

	interval := recordSeriesInterval(s, start, end, time.Now())
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

var loadRecordMetricNames = []string{
	MetricCPU, MetricGPU, MetricRAM, MetricRAMTotal, MetricSwap, MetricSwapTotal,
	MetricLoad, MetricTemp, MetricDisk, MetricDiskTotal, MetricNetIn, MetricNetOut,
	MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
	MetricProcess, MetricConnections, MetricConnectionsUDP,
}

var recordDownsampleStandardIntervals = []time.Duration{
	time.Second,
	5 * time.Second,
	10 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	3 * time.Hour,
	6 * time.Hour,
	12 * time.Hour,
	24 * time.Hour,
}

type recordSeriesKey struct {
	client string
	ts     int64
}

func getRecordsByClientAndTimeFromSeries(ctx context.Context, s *metric.Store, clientUUID string, start, end time.Time) ([]models.Record, error) {
	now := time.Now()
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
			Aggregation: metric.AggAvg,
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
					Time:   models.FromTime(point.Bucket),
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
	return floorRecordDownsampleInterval(interval)
}

func floorRecordDownsampleInterval(interval time.Duration) time.Duration {
	out := time.Second
	for _, candidate := range recordDownsampleStandardIntervals {
		if candidate > interval {
			break
		}
		out = candidate
	}
	return out
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
	case MetricRAMTotal:
		rec.RamTotal = int64(value)
	case MetricSwap:
		rec.Swap = int64(value)
	case MetricSwapTotal:
		rec.SwapTotal = int64(value)
	case MetricLoad:
		rec.Load = float32(value)
	case MetricTemp:
		rec.Temp = float32(value)
	case MetricDisk:
		rec.Disk = int64(value)
	case MetricDiskTotal:
		rec.DiskTotal = int64(value)
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
		return records[i].Time.ToTime().Before(records[j].Time.ToTime())
	})
}

// GetLatestTrafficBefore 查询每个客户端在指定时间之前最新的累计流量（用于计算增量基线）
func GetLatestTrafficBefore(ctx context.Context, clientUUIDs []string, before time.Time) (map[string]models.Record, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	result := make(map[string]models.Record, len(clientUUIDs))
	end := before.Add(-time.Nanosecond)
	for _, uuid := range clientUUIDs {
		if uuid == "" {
			continue
		}
		upPts, err := s.Query(ctx, metric.Query{
			MetricName: MetricNetTotalUp,
			EntityID:   uuid,
			Start:      time.Unix(0, 0),
			End:        end,
			Order:      metric.OrderDesc,
			Limit:      1,
		})
		if err != nil {
			return nil, err
		}
		if len(upPts) == 0 {
			continue
		}
		rec := models.Record{
			Client:     uuid,
			Time:       models.FromTime(upPts[0].Timestamp),
			NetTotalUp: int64(upPts[0].Value),
		}
		downPts, err := s.Query(ctx, metric.Query{
			MetricName: MetricNetTotalDown,
			EntityID:   uuid,
			Start:      time.Unix(0, 0),
			End:        end,
			Order:      metric.OrderDesc,
			Limit:      1,
		})
		if err != nil {
			return nil, err
		}
		if len(downPts) > 0 {
			rec.NetTotalDown = int64(downPts[0].Value)
		}
		result[uuid] = rec
	}
	return result, nil
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
					Time:        models.FromTime(p.Timestamp),
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

// GetPingRecords 从 metric store 查询 ping 记录
func GetPingRecords(ctx context.Context, clientUUID string, taskID int, start, end time.Time) ([]models.PingRecord, error) {
	s := GetStore()
	if s == nil {
		return nil, fmt.Errorf("metric store not enabled")
	}

	query := metric.Query{
		MetricName: MetricPingLatency,
		Start:      start,
		End:        end,
		Order:      metric.OrderDesc,
	}

	if clientUUID != "" {
		query.EntityID = clientUUID
	}

	if taskID >= 0 {
		query.Tags = map[string]string{"task_id": fmt.Sprintf("%d", taskID)}
	}

	points, err := s.Query(ctx, query)
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
			Time:   models.FromTime(p.Timestamp),
			Value:  int(p.Value),
		})
	}

	return records, nil
}

// recordMetricNames 是负载/系统类指标（不含 ping）。ping 使用独立的保留策略，
// 因此负载记录的批量删除与保留清理都不应波及 ping.latency_ms。
var recordMetricNames = []string{
	MetricCPU, MetricGPU, MetricRAM, MetricRAMTotal, MetricSwap, MetricSwapTotal,
	MetricLoad, MetricTemp, MetricDisk, MetricDiskTotal, MetricNetIn, MetricNetOut,
	MetricNetTotalUp, MetricNetTotalDown, MetricTrafficUp, MetricTrafficDown,
	MetricProcess, MetricConnections, MetricConnectionsUDP,
	MetricGPUDeviceUsage, MetricGPUMem, MetricGPUMemTotal, MetricGPUTemp,
}

// farFuture 返回一个足够远的未来时间，用于以 DeleteBefore 语义清空某指标的全部数据。
func farFuture() time.Time {
	return time.Now().Add(24 * 365 * time.Hour)
}

// DeleteAllRecords 删除所有负载/系统类记录（保留指标定义，不含 ping）。
func DeleteAllRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	for _, metricName := range recordMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			log.Printf("Failed to delete metric %s: %v", metricName, err)
		}
	}

	return nil
}

// DeleteRecordsBefore 删除指定时间之前的负载/系统类记录（不含 ping）。
func DeleteRecordsBefore(ctx context.Context, before time.Time) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}

	for _, metricName := range recordMetricNames {
		if _, err := s.DeleteBefore(ctx, metricName, before); err != nil {
			log.Printf("Failed to delete old metric %s: %v", metricName, err)
		}
	}

	return nil
}

// DeleteAllPingRecords 删除全部 ping 记录（保留指标定义）。
func DeleteAllPingRecords(ctx context.Context) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, metricName := range []string{MetricPingLatency, MetricPingLoss} {
		if _, err := s.DeleteBefore(ctx, metricName, farFuture()); err != nil {
			return fmt.Errorf("failed to delete ping records: %w", err)
		}
	}
	return nil
}

// DeletePingRecordsBefore 删除指定时间之前的 ping 记录。
func DeletePingRecordsBefore(ctx context.Context, before time.Time) error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not enabled")
	}
	for _, metricName := range []string{MetricPingLatency, MetricPingLoss} {
		if _, err := s.DeleteBefore(ctx, metricName, before); err != nil {
			return fmt.Errorf("failed to delete ping records before %s: %w", before, err)
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
		for _, metricName := range []string{MetricPingLatency, MetricPingLoss} {
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
