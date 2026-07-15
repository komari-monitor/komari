package metricstore

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/komari-monitor/komari/pkg/metric"
)

// storeMigrationWindow 是 store-to-store 迁移时按时间分窗查询的窗口大小。
// 采样点按时间窗口分批拉取，避免一次性把整段序列读入内存。
const storeMigrationWindow = 6 * time.Hour

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
		Driver:              driver,
		DSN:                 dsn,
		DownsamplingEnabled: base.DownsamplingEnabled,
		TablePrefix:         base.TablePrefix,
		MaxOpenConns:        base.MaxOpenConns,
		MaxIdleConns:        base.MaxIdleConns,
	}, nil
}

// openSourceStore 打开一个已存在的 metrics 目标库作为数据搬运的源库读取。
//
// 使用 autoMigrate=true：GORM 的 AutoMigrate 只新增表/列/索引、从不删除数据，
// 因此对真实旧库幂等无害；而当源库文件/表不存在（例如老快照记录了 completed
// 但 metrics.db 缺失）时，可创建空表让后续 ListMetrics 返回空集而非报
// "no such table"，从而把“无历史可迁移”识别为正常情况而非错误。
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
// 把其中的全部指标搬运到当前目标 dst（例如 SQLite metrics.db → MySQL/PostgreSQL）。
//
// 该过程幂等：dst 以 (metric, entity, tags, ts) upsert 写入，中断后重启可安全重跑。
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

	log.Printf("Migrating metrics from previous store %s to current target...", prevFingerprint)
	total, err := MigrateBetweenStores(ctx, src, dst)
	if err != nil {
		return fmt.Errorf("store-to-store metrics migration failed: %w", err)
	}
	log.Printf("Store-to-store metrics migration completed (%d points)", total)
	return nil
}

// storeMigrationObserver 在 store-to-store 迁移过程中接收进度回调。
//   - currentMetric：当前正在搬运的指标名。
//   - metricIndex：该指标在全部指标中的序号（0 起），即已完成的指标数。
//   - totalMetrics：指标定义总数。
//   - addedPoints：本次新写入目标库的采样点数（用于外部累计）。
type storeMigrationObserver func(currentMetric string, metricIndex, totalMetrics int, addedPoints int64)

// MigrateBetweenStores 把 src metric store 的全部指标定义与采样点搬运到 dst。
//
// 用于 metrics 后端切换（例如默认 SQLite metrics.db → MySQL/PostgreSQL）：
//   - 先在 dst 建立/更新全部指标定义；
//   - 再按指标、按时间窗口分批读取源库采样点并写入 dst。
//
// 采样点在 dst 以 (metric_name, entity_id, tags, ts) upsert 写入，窗口边界重叠也
// 幂等，因此整个过程可安全重试。返回搬运的采样点总数。
func MigrateBetweenStores(ctx context.Context, src, dst *metric.Store) (int64, error) {
	return migrateBetweenStores(ctx, src, dst, nil)
}

// migrateBetweenStores 是 MigrateBetweenStores 的内部实现，额外接受一个可选的进度
// 观察者 observe（为 nil 时行为与旧版本完全一致）。启动迁移走 nil，WebUI/API 触发
// 的迁移传入回调以实时更新进度。
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
		retentionDays := def.RetentionDays
		if retentionDays == 0 {
			// UpsertMetric retains its legacy default for omitted retention. Persist
			// an explicit zero through SetMetricRetention immediately afterwards.
			def.RetentionDays = 1
		}
		if err := dst.UpsertMetric(ctx, def); err != nil {
			return total, fmt.Errorf("upsert metric %q on target: %w", def.Name, err)
		}
		if retentionDays == 0 {
			if _, err := dst.SetMetricRetention(ctx, def.Name, 0); err != nil {
				return total, fmt.Errorf("disable metric %q on target: %w", def.Name, err)
			}
			continue
		}

		earliest, latest, ok, err := metricTimeBounds(ctx, src, def.Name)
		if err != nil {
			return total, fmt.Errorf("resolve time bounds for metric %q: %w", def.Name, err)
		}
		if !ok {
			// 该指标只有定义没有数据，跳过。
			continue
		}

		var migrated int64
		for windowStart := earliest; !windowStart.After(latest); windowStart = windowStart.Add(storeMigrationWindow) {
			select {
			case <-ctx.Done():
				return total, ctx.Err()
			default:
			}

			windowEnd := windowStart.Add(storeMigrationWindow)
			points, err := src.Query(ctx, metric.Query{
				MetricName: def.Name,
				Start:      windowStart,
				End:        windowEnd,
				Order:      metric.OrderAsc,
			})
			if err != nil {
				return total, fmt.Errorf("query metric %q window [%s,%s): %w", def.Name, windowStart, windowEnd, err)
			}
			if len(points) == 0 {
				continue
			}
			if err := dst.WriteBatch(ctx, points); err != nil {
				return total, fmt.Errorf("write metric %q batch to target: %w", def.Name, err)
			}
			migrated += int64(len(points))
			total += int64(len(points))
			if observe != nil {
				observe(def.Name, i, len(defs), int64(len(points)))
			}
		}
		if migrated > 0 {
			log.Printf("[store-migration] metric %q: migrated %d points", def.Name, migrated)
		}
	}

	return total, nil
}

// metricTimeBounds 返回某指标在 src 中最早/最晚采样时间。ok=false 表示无数据。
// 通过升/降序各取一条采样点定位边界，避免把整段序列读入内存。
func metricTimeBounds(ctx context.Context, src *metric.Store, name string) (time.Time, time.Time, bool, error) {
	wide := metric.Query{
		MetricName: name,
		Start:      time.Unix(0, 0),
		End:        time.Now().Add(24 * time.Hour),
	}

	asc := wide
	asc.Order = metric.OrderAsc
	asc.Limit = 1
	first, err := src.Query(ctx, asc)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if len(first) == 0 {
		return time.Time{}, time.Time{}, false, nil
	}

	desc := wide
	desc.Order = metric.OrderDesc
	desc.Limit = 1
	last, err := src.Query(ctx, desc)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if len(last) == 0 {
		return time.Time{}, time.Time{}, false, nil
	}

	return first[0].Timestamp, last[0].Timestamp, true, nil
}
