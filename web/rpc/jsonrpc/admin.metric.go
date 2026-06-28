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
	reg("deleteLegacyTables", adminDeleteLegacyTables, "Delete legacy SQLite tables after migration")
}

func adminGetMetricMigrationStatus(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	status, err := metricstore.GetMigrationStatus()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	estimated, err := metricstore.EstimateMigrationSize()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, err.Error(), nil)
	}

	totalRecords := estimated["records"] + estimated["records_long_term"]
	totalGPU := estimated["gpu_records"] + estimated["gpu_records_long_term"]
	totalPing := estimated["ping_records"]

	return map[string]any{
		"status":                 status,
		"estimated_records":      totalRecords,
		"estimated_gpu_records":  totalGPU,
		"estimated_ping_records": totalPing,
		"tables":                 estimated,
	}, nil
}

func adminStartMetricMigration(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
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

	// 异步执行迁移
	go func() {
		_, _ = metricstore.MigrateFromLegacyTables(params.BatchSize)
	}()

	return map[string]any{
		"status":  "started",
		"message": "Migration started in background",
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
