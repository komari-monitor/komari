package metricstore

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/metric"
)

// store_migration.go
//
// 面向 WebUI/API 的“存储后端迁移”支撑：把一个 metrics 源库（默认本地 SQLite
// ./data/metrics.db）中的全部指标数据搬运到当前正在运行的 metrics 目标库
// （通常是用户新配置并热重载后的 MySQL/PostgreSQL）。
//
// 与启动迁移（RunStartupMigration）的区别：
//   - 启动迁移在进程启动时自动、同步执行（komari.db → 当前 metrics 目标，或上一个
//     metrics 目标 → 当前目标）。
//   - 此处的 store 迁移由管理员在 WebUI 中「切换数据库后」手动触发，异步执行并可
//     查询进度 / 取消。数据以 upsert 写入，可安全重复执行。
//
// 典型流程：
//  1. 管理员在设置中把 metric_db_dsn 改为 MySQL/PostgreSQL，保存 → 后端连接测试
//     通过后热重载，当前 store 切到远端（此时远端为空）。
//  2. 管理员调用 startMetricMigration（不带 source 参数），系统以上一个 metrics
//     目标（默认 SQLite metrics.db）为源，把历史数据搬运到远端。
//  3. 完成后登记目标指纹，下次启动不再重复搬运。

// StoreMigrationProgress 描述一次 store-to-store 迁移的实时进度。
type StoreMigrationProgress struct {
	Status         string    `json:"status"`          // idle, running, completed, failed, canceled
	SourceDriver   string    `json:"source_driver"`   // 源库驱动
	SourceDSN      string    `json:"source_dsn"`      // 源库 DSN（脱敏后）
	TargetDriver   string    `json:"target_driver"`   // 目标库驱动
	TargetDSN      string    `json:"target_dsn"`      // 目标库 DSN（脱敏后）
	TotalMetrics   int       `json:"total_metrics"`   // 指标定义总数
	CurrentMetric  string    `json:"current_metric"`  // 当前正在搬运的指标名
	MetricsDone    int       `json:"metrics_done"`    // 已完成的指标数
	MigratedPoints int64     `json:"migrated_points"` // 已搬运的采样点数
	StartTime      time.Time `json:"start_time"`      // 开始时间
	EndTime        time.Time `json:"end_time"`        // 结束时间
	Error          string    `json:"error,omitempty"` // 错误信息
}

var (
	storeMigMu       sync.Mutex
	storeMigProgress StoreMigrationProgress
	storeMigCancel   context.CancelFunc
	storeMigDone     chan struct{}
)

// IsStoreMigrationRunning 报告是否有 store-to-store 迁移正在运行。
func IsStoreMigrationRunning() bool {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	return storeMigCancel != nil
}

// GetStoreMigrationProgress 返回当前 store-to-store 迁移进度快照。
func GetStoreMigrationProgress() StoreMigrationProgress {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	return storeMigProgress
}

// ResolveStoreMigrationSourceFingerprint 推断本次 store 迁移的源库指纹。
//
// 优先级：
//  1. 显式传入的 driver+dsn（driver 可留空，交由 DSN 推断）。
//  2. 已保存的上一个 metrics 目标指纹（MigrationTargetKey）。
//  3. 默认 SQLite（./data/metrics.db）。
func ResolveStoreMigrationSourceFingerprint(driver, dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn != "" {
		d := ResolveDriverFromConfig(driver, dsn)
		return fmt.Sprintf("%s|%s", d, dsn)
	}
	if saved, _ := config.GetAs[string](MigrationTargetKey, ""); saved != "" {
		return saved
	}
	return defaultSQLiteFingerprint()
}

// StartStoreMigration 异步启动一次 store-to-store 迁移：把源库（由 sourceDriver +
// sourceDSN 指定，留空则自动推断）的全部指标数据搬运到当前运行中的 metrics 目标库。
//
// 返回错误表示“未能启动”（例如已有迁移在跑、源与目标相同、目标未初始化等）；
// 迁移过程中的错误通过 GetStoreMigrationProgress().Status == "failed" 与 Error 暴露。
func StartStoreMigration(sourceDriver, sourceDSN string) error {
	dst := GetStore()
	if dst == nil {
		return fmt.Errorf("metric store not initialized")
	}

	cfg, err := config.GetManyAs[MetricStoreConfig]()
	if err != nil {
		return fmt.Errorf("failed to load metric store config: %w", err)
	}
	targetFP := targetFingerprint(cfg)
	sourceFP := ResolveStoreMigrationSourceFingerprint(sourceDriver, sourceDSN)

	if sourceFP == targetFP {
		return fmt.Errorf("source and target metrics database are the same (%s); nothing to migrate", targetFP)
	}

	srcCfg, err := configFromFingerprint(sourceFP, cfg)
	if err != nil {
		return fmt.Errorf("resolve source metrics store: %w", err)
	}

	storeMigMu.Lock()
	if storeMigCancel != nil {
		storeMigMu.Unlock()
		return fmt.Errorf("a store migration is already in progress")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	storeMigCancel = cancel
	storeMigDone = done
	storeMigProgress = StoreMigrationProgress{
		Status:       "running",
		SourceDriver: string(ResolveDriverFromConfig(srcCfg.Driver, srcCfg.DSN)),
		SourceDSN:    maskDSN(srcCfg.DSN),
		TargetDriver: string(ResolveDriverFromConfig(cfg.Driver, cfg.DSN)),
		TargetDSN:    maskDSN(cfg.DSN),
		StartTime:    time.Now(),
	}
	storeMigMu.Unlock()

	go runStoreMigration(ctx, cancel, done, srcCfg, cfg, dst, targetFP)
	return nil
}

// runStoreMigration 执行实际的搬运逻辑（在独立 goroutine 中）。
func runStoreMigration(ctx context.Context, cancel context.CancelFunc, done chan struct{}, srcCfg *MetricStoreConfig, cfg *MetricStoreConfig, dst *metric.Store, targetFP string) {
	defer func() {
		storeMigMu.Lock()
		storeMigCancel = nil
		storeMigDone = nil
		storeMigMu.Unlock()
		cancel()
		close(done)
	}()

	src, err := openSourceStore(ctx, srcCfg)
	if err != nil {
		finishStoreMigration("failed", fmt.Errorf("open source metrics store: %w", err))
		return
	}
	defer src.Close()

	observe := func(currentMetric string, metricIndex, totalMetrics int, addedPoints int64) {
		storeMigMu.Lock()
		storeMigProgress.CurrentMetric = currentMetric
		storeMigProgress.MetricsDone = metricIndex
		storeMigProgress.TotalMetrics = totalMetrics
		storeMigProgress.MigratedPoints += addedPoints
		storeMigMu.Unlock()
	}

	total, err := migrateBetweenStores(ctx, src, dst, observe)
	if err != nil {
		if ctx.Err() != nil {
			finishStoreMigration("canceled", nil)
			log.Printf("[store-migration] canceled after %d points", total)
			return
		}
		finishStoreMigration("failed", err)
		return
	}

	// 搬运成功：登记当前目标指纹，避免下次启动重复搬运。
	if err := config.Set(MigrationTargetKey, targetFP); err != nil {
		log.Printf("[store-migration] failed to persist migration target fingerprint: %v", err)
	}
	finishStoreMigration("completed", nil)
	log.Printf("[store-migration] completed via API (%d points) target=%s", total, targetFP)
}

// finishStoreMigration 统一收尾：设置终态状态、结束时间与错误信息。
func finishStoreMigration(status string, err error) {
	storeMigMu.Lock()
	defer storeMigMu.Unlock()
	storeMigProgress.Status = status
	storeMigProgress.EndTime = time.Now()
	if err != nil {
		storeMigProgress.Error = err.Error()
	}
	// 完成/取消时把当前指标标记为已全部处理，便于前端进度条归位。
	if status == "completed" && storeMigProgress.TotalMetrics > 0 {
		storeMigProgress.MetricsDone = storeMigProgress.TotalMetrics
	}
}

// CancelStoreMigration 请求取消正在运行的 store-to-store 迁移，并等待其退出。
// 由于写入是幂等 upsert，取消后可安全重新发起，不会产生重复数据。
func CancelStoreMigration() error {
	storeMigMu.Lock()
	cancel := storeMigCancel
	done := storeMigDone
	storeMigMu.Unlock()

	if cancel == nil {
		return fmt.Errorf("no store migration is currently running")
	}
	cancel()

	select {
	case <-done:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timed out waiting for store migration to cancel")
	}
}

// maskDSN 对 DSN 做粗粒度脱敏，避免在 API 响应/日志中泄露密码。
// 处理两类常见格式：URL（scheme://user:pass@host/...）与 key=value（password=...）。
func maskDSN(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	// key=value 形式：屏蔽 password=... 段。
	if strings.Contains(dsn, "password=") {
		parts := strings.Fields(dsn)
		for i, p := range parts {
			if strings.HasPrefix(strings.ToLower(p), "password=") {
				parts[i] = "password=***"
			}
		}
		return strings.Join(parts, " ")
	}
	// URL / user:pass@host 形式：屏蔽 user:pass@ 中的密码。
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		head := dsn[:at]
		tail := dsn[at:]
		if colon := strings.LastIndex(head, ":"); colon >= 0 {
			// 保留 scheme://user，屏蔽密码。
			return head[:colon] + ":***" + tail
		}
	}
	return dsn
}
