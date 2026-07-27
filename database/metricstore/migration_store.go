package metricstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/komari-monitor/komari/pkg/metric"
	logger "github.com/komari-monitor/komari/utils/log"
)

const storeMigrationBatchSize = 500

// configFromFingerprint 从目标指纹（driver|dsn）重建一个 MetricStoreConfig，
// 用于以只读方式打开“上一次的 metrics 目标库”。表前缀、保留天数、连接数等
// 沿用当前配置（切换后端时这些通常不变）。
func configFromFingerprint(fingerprint string, base *MetricStoreConfig) (*MetricStoreConfig, error) {
	idx := strings.Index(fingerprint, "|")
	if idx < 0 {
		return nil, fmt.Errorf("invalid target fingerprint: %q", fingerprint)
	}
	driver := fingerprint[:idx]
	dsn := fingerprint[idx+1:]
	if strings.TrimSpace(driver) == "" {
		return nil, fmt.Errorf("empty driver in target fingerprint: %q", fingerprint)
	}
	return &MetricStoreConfig{
		Driver:       driver,
		DSN:          dsn,
		TablePrefix:  base.TablePrefix,
		MaxOpenConns: base.MaxOpenConns,
		MaxIdleConns: base.MaxIdleConns,
	}, nil
}

// openSourceStore 打开一个已存在的 metrics 目标库作为数据搬运的源库读取。
//
// 使用 autoMigrate=true 确保规范化表和索引存在；创建语句不会删除已有数据。
// 当源库文件/表不存在（例如老快照记录了 completed 但 metrics.db 缺失）时，
// 创建空表可让后续 ListMetrics 返回空集，把“无历史可迁移”识别为正常情况。
func openSourceStore(ctx context.Context, cfg *MetricStoreConfig) (*metric.Store, error) {
	metricCfg, err := buildMetricConfig(cfg, true)
	if err != nil {
		return nil, err
	}
	return metric.Open(ctx, metricCfg)
}

// defaultSQLiteFingerprint 返回默认 SQLite metrics 库（./data/metrics.db）的目标指纹。
// 老快照的 metrics 数据固定落在该 SQLite 文件，用于在缺失指纹时推断上一个源库。
func defaultSQLiteFingerprint() string {
	return targetFingerprint(&MetricStoreConfig{Driver: "sqlite", DSN: "./data/metrics.db"})
}

// migrateFromPreviousStore 打开由 prevFingerprint 指定的上一个 metrics 目标库作为源，
// 把其中的全部指标定义和持久化 rollup 搬运到当前目标 dst（例如 SQLite
// metrics.db → MySQL/PostgreSQL）。
//
// 该过程幂等：dst 以 (series, resolution, labels, bucket) upsert 写入，中断后重启可安全重跑。
func migrateFromPreviousStore(prevFingerprint string, cfg *MetricStoreConfig, dst *metric.Store) error {
	prevCfg, err := configFromFingerprint(prevFingerprint, cfg)
	if err != nil {
		return fmt.Errorf("parse previous metrics target %q: %w", prevFingerprint, err)
	}

	ctx := context.Background()
	src, err := openSourceStore(ctx, prevCfg)
	if err != nil {
		return fmt.Errorf("open previous metrics store (%s): %w", prevFingerprint, err)
	}
	defer src.Close()

	logger.Infof("metricstore", "Migrating metrics from previous store %s to current target...", prevFingerprint)
	total, err := MigrateBetweenStores(ctx, src, dst)
	if err != nil {
		return fmt.Errorf("store-to-store metrics migration failed: %w", err)
	}
	logger.Infof("metricstore", "Store-to-store metrics migration completed (%d rollups)", total)
	return nil
}

// storeMigrationObserver 在 store-to-store 迁移过程中接收进度回调。
//   - currentMetric：当前正在搬运的指标名。
//   - metricIndex：该指标在全部指标中的序号（0 起），即已完成的指标数。
//   - totalMetrics：指标定义总数。
//   - addedPoints：本次新写入目标库的 rollup 行数（用于外部累计）。
type storeMigrationObserver func(currentMetric string, metricIndex, totalMetrics int, addedPoints int64)

// MigrateBetweenStores 把 src metric store 的全部指标定义与持久化 rollup 搬运到 dst。
//
// 用于 metrics 后端切换（例如默认 SQLite metrics.db → MySQL/PostgreSQL）：
//   - 先在 dst 建立/更新全部指标定义；
//   - 再按指标流式读取源库 rollup 并分批写入 dst。
//
// rollup 在 dst 以 (series, resolution, labels, bucket) upsert 写入，因此整个
// 过程可安全重试。返回搬运的 rollup 行数。
func MigrateBetweenStores(ctx context.Context, src, dst *metric.Store) (int64, error) {
	return migrateBetweenStores(ctx, src, dst, nil)
}

// migrateBetweenStores 是 MigrateBetweenStores 的内部实现，额外接受一个可选的进度
// 观察者 observe（为 nil 时行为与旧版本完全一致）。WebUI/API 手动触发的迁移传入
// 回调以实时更新进度。
func migrateBetweenStores(ctx context.Context, src, dst *metric.Store, observe storeMigrationObserver) (int64, error) {
	if src == nil || dst == nil {
		return 0, fmt.Errorf("source or destination metric store is nil")
	}

	defs, err := src.ListMetrics(ctx)
	if err != nil {
		return 0, fmt.Errorf("list source metrics: %w", err)
	}

	var total int64
	for i, def := range defs {
		if observe != nil {
			// 进入下一个指标：已完成 i 个指标。
			observe(def.Name, i, len(defs), 0)
		}
		// 目标库先建立指标定义，保证后续写入的指标存在。
		if err := dst.UpsertMetric(ctx, def); err != nil {
			return total, fmt.Errorf("upsert metric %q on target: %w", def.Name, err)
		}
		if def.RetentionDays == 0 {
			continue
		}
		var migrated int64
		_, err := src.ExportRollups(ctx, def.Name, storeMigrationBatchSize, func(batch []metric.PersistedRollup) error {
			if err := dst.ImportRollups(ctx, batch); err != nil {
				return fmt.Errorf("write metric %q rollup batch to target: %w", def.Name, err)
			}
			count := int64(len(batch))
			migrated += count
			total += count
			if observe != nil {
				observe(def.Name, i, len(defs), count)
			}
			return nil
		})
		if err != nil {
			return total, fmt.Errorf("export metric %q rollups: %w", def.Name, err)
		}
		if migrated > 0 {
			logger.Infof("metricstore", "[store-migration] metric %q: migrated %d rollups", def.Name, migrated)
		}
	}

	return total, nil
}
