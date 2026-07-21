package metricstore

import (
	"fmt"
	logger "github.com/komari-monitor/komari/utils/log"
	"strings"

	"github.com/komari-monitor/komari/pkg/config"
)

// migration.go
//
// 启动阶段的 metrics 数据迁移。这里只处理 metrics 存储后端切换时的数据搬运
// （例如默认 SQLite ./data/metrics.db 切换到 MySQL/PostgreSQL）。旧 komari.db
// 监控表导入属于一次性迁移，见 pkg/migrations.RunMetricStoreMigrations。

// targetFingerprint 返回当前 metrics 目标库的指纹（driver + 归一化 DSN），
// 用于判断 metrics 存储后端是否发生变化（例如从 SQLite 切换到 MySQL/PostgreSQL）。
func targetFingerprint(cfg *MetricStoreConfig) string {
	driver := ResolveDriverFromConfig(cfg.Driver, cfg.DSN)
	dsn := strings.TrimSpace(cfg.DSN)
	return fmt.Sprintf("%s|%s", driver, dsn)
}

// RunStartupMigration 在服务启动时检查 metrics 存储后端是否发生变化，
// 若变化则把上一个 metrics 目标库的全部数据搬运到当前目标。
//
// 判定依据 MigrationTargetKey（记录“数据当前完整所在的 metrics 目标指纹”）：
//   - saved == current：数据已在当前目标，无需处理。
//   - saved == ""：无历史目标记录。老快照的 metrics 数据固定落在默认 SQLite
//     （./data/metrics.db）；若当前目标不是默认 SQLite（例如已切到 MySQL/PostgreSQL），
//     把默认 SQLite 中可能存在的历史数据搬运过来，否则仅登记指纹。
//   - saved != current：后端已切换，把上一个目标（saved）的数据搬运到当前目标。
//
// 搬运以 upsert 写入，幂等，中断后重启可安全重跑。任一失败都返回错误，
// 调用方应让启动失败并打印明确错误。
func RunStartupMigration() error {
	s := GetStore()
	if s == nil {
		return fmt.Errorf("metric store not initialized")
	}

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}

	current := targetFingerprint(cfg)
	saved, _ := config.GetAs[string](MigrationTargetKey, "")

	// 数据已完整位于当前目标：无需搬运。
	if saved == current {
		return nil
	}

	// 上一个目标：优先使用已保存指纹；无记录时按老快照默认 SQLite 推断。
	prev := saved
	if prev == "" {
		prev = defaultSQLiteFingerprint()
	}

	if prev != current {
		if err := migrateFromPreviousStore(prev, cfg, s); err != nil {
			return err
		}
	}

	if err := config.Set(MigrationTargetKey, current); err != nil {
		logger.Errorf("metricstore", "Failed to persist migration target fingerprint: %v", err)
	}
	return nil
}
