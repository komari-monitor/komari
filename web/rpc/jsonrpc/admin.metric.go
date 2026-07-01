package jsonrpc

import (
	"context"

	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.metric.go
// Metrics 数据库迁移相关 RPC 方法（admin 命名空间）

func init() {
	reg("getMetricMigrationStatus", adminGetMetricMigrationStatus, "Get metric migration status and estimated data size")
	reg("startMetricMigration", adminStartMetricMigration, "Start migrating legacy records to metric store")
	reg("pauseMetricMigration", adminPauseMetricMigration, "Pause the currently running migration")
	reg("resumeMetricMigration", adminResumeMetricMigration, "Resume a paused migration from the saved cursor")
	reg("deleteLegacyTables", adminDeleteLegacyTables, "Delete legacy SQLite tables after migration")
}

func adminGetMetricMigrationStatus(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	progress := metricstore.GetFullMigrationStatus()

	estimated, err := metricstore.EstimateMigrationSize()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	totalRecords := estimated["records"] + estimated["records_long_term"]
	totalGPU := estimated["gpu_records"] + estimated["gpu_records_long_term"]
	totalPing := estimated["ping_records"]

	isRunning := metricstore.IsMigrationRunning()

	return map[string]any{
		"status":                 progress.Status,
		"estimated_records":       totalRecords,
		"estimated_gpu_records":   totalGPU,
		"estimated_ping_records":  totalPing,
		"tables":                 estimated,
		"is_running":             isRunning,
		"can_resume":             progress.CanResume,
		"migrated_records":       progress.MigratedRecords,
		"migrated_ping":          progress.MigratedPing,
	}, nil
}

func adminStartMetricMigration(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		BatchSize int  `json:"batch_size"`
		Resume    bool `json:"resume"` // 是否从暂停点恢复
	}
	req.BindParams(&params)

	if params.BatchSize <= 0 {
		params.BatchSize = 500
	}

	if params.BatchSize > 5000 {
		return nil, rpc.MakeError(rpc.InvalidParams, "batch_size cannot exceed 5000", nil)
	}

	// 检查迁移是否已在运行
	if metricstore.IsMigrationRunning() {
		return nil, rpc.MakeError(rpc.InvalidRequest, "Migration is already in progress", nil)
	}

	// 检查状态：只有 not_started / failed / paused 可以开始新迁移
	status, _ := metricstore.GetMigrationStatus()
	if status == "in_progress" {
		return nil, rpc.MakeError(rpc.InvalidRequest, "Migration is already running", nil)
	}

	// 异步执行迁移
	go func() {
		_, _ = metricstore.MigrateFromLegacyTables(params.BatchSize, params.Resume)
	}()

	action := "started"
	if params.Resume {
		action = "resumed"
	}

	return map[string]any{
		"status":  "started",
		"message": "Migration " + action + " in background",
	}, nil
}

func adminPauseMetricMigration(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// 检查迁移是否正在运行
	if !metricstore.IsMigrationRunning() {
		return nil, rpc.MakeError(rpc.InvalidRequest, "No migration is currently running", nil)
	}

	if err := metricstore.PauseMigration(); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	return map[string]any{
		"status":  "paused",
		"message": "Migration paused successfully. You can resume it later.",
	}, nil
}

func adminResumeMetricMigration(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// 检查是否有迁移正在运行
	if metricstore.IsMigrationRunning() {
		return nil, rpc.MakeError(rpc.InvalidRequest, "Migration is already running", nil)
	}

	// 检查状态是否为 paused
	status, err := metricstore.GetMigrationStatus()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	if status != "paused" {
		return nil, rpc.MakeError(rpc.InvalidRequest, "No paused migration to resume. Current status: "+status, nil)
	}

	var params struct {
		BatchSize int `json:"batch_size"`
	}
	req.BindParams(&params)

	if params.BatchSize <= 0 {
		params.BatchSize = 500
	}

	if params.BatchSize > 5000 {
		return nil, rpc.MakeError(rpc.InvalidParams, "batch_size cannot exceed 5000", nil)
	}

	// 异步执行恢复迁移（resume=true 从保存的游标继续）
	go func() {
		_, _ = metricstore.MigrateFromLegacyTables(params.BatchSize, true)
	}()

	return map[string]any{
		"status":  "resumed",
		"message": "Migration resumed from saved cursor",
	}, nil
}

func adminDeleteLegacyTables(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	// 检查迁移状态
	status, err := metricstore.GetMigrationStatus()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	if status != "completed" {
		return nil, rpc.MakeError(rpc.InvalidRequest, "Migration must be completed before deleting legacy tables", nil)
	}

	if err := metricstore.DeleteLegacyTables(); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	return map[string]any{
		"status":  "success",
		"message": "Legacy tables deleted successfully",
	}, nil
}
