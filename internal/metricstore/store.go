package metricstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	logger "github.com/komari-monitor/komari/utils/log"

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
)

var ErrCompactInProgress = errors.New("metric store compact already in progress")

// ErrStructureUpgradeRequired reports that the configured store must be
// migrated by the restricted startup guide before it can be opened normally.
var ErrStructureUpgradeRequired = errors.New("metric store structure upgrade is required")

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
// func OpenStore(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
// 	return openStore(ctx, cfg)
// }

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
	clearStoreClosing()

	logger.Infof("metricstore", "Metric store initialized successfully (driver=%s)", ResolveDriverFromConfig(cfg.Driver, cfg.DSN))
	return nil
}

// RecoverStore opens, persists, and activates a replacement store selected
// from the restricted recovery page. It records that target as the current
// manual-migration source, but does not copy data from the unavailable store.
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
	restructureRequired, err := structureUpgradeRequiredForConfig(ctx, &recovered)
	if err != nil {
		return err
	}
	if restructureRequired {
		target := targetFingerprint(&recovered)
		if err := config.SetMany(map[string]any{
			MetricDBDriverKey:  recovered.Driver,
			MetricDBDSNKey:     recovered.DSN,
			MigrationTargetKey: target,
		}); err != nil {
			return fmt.Errorf("save recovered metric store config: %w", err)
		}
		logger.Infof("metricstore", "Metric store connection recovered; structure upgrade is required (driver=%s)", recovered.Driver)
		return nil
	}
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
// 搬运到新目标（如 MySQL/PostgreSQL）。跨库数据迁移必须由管理员手动启动。
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

	// Do not let the normal hot-reload path run AutoMigrate against an old
	// point-backed schema. That schema must go through the authenticated
	// structure-upgrade guide first; otherwise index creation can reference
	// columns such as resolution_id that do not exist yet.
	checkCfg, err := buildMetricConfig(cfg, false)
	if err != nil {
		return err
	}
	checkStore, err := metric.Open(ctx, checkCfg)
	if err != nil {
		return err
	}
	needsRestructure, err := checkStore.NeedsRestructure(ctx)
	_ = checkStore.Close()
	if err != nil {
		return fmt.Errorf("inspect metric store structure: %w", err)
	}
	if needsRestructure {
		return fmt.Errorf("%w before hot reload", ErrStructureUpgradeRequired)
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
