package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/pkg/metric"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.metric.go
// Metrics 数据库迁移相关 RPC 方法（admin 命名空间）。
//
// 这些方法服务于「metrics 存储后端迁移」：把默认 SQLite（./data/metrics.db）中的
// 历史数据搬运到管理员新配置的 MySQL/PostgreSQL。此操作只会由管理员显式触发，
// 服务启动不会自动迁移数据。
//
// 典型使用顺序（WebUI）：
//  1. admin:editSettings 修改 metric_db_dsn 为 MySQL/PostgreSQL（后端会做连接测试
//     并热重载，当前 store 切到远端，此时远端为空）。
//  2. admin:startMetricMigration 触发把旧 SQLite metrics 数据搬运到远端。
//  3. 轮询 admin:getMetricMigrationStatus 展示进度。
//  4.（可选）admin:cancelMetricMigration 取消；因写入幂等，取消后可安全重发。

func init() {
	reg("listMetricDefinitions", adminListMetricDefinitions, "List metric definitions and retention policies")
	reg("updateMetricDefinition", adminUpdateMetricDefinition, "Update a metric definition")
	reg("getMetricMigrationStatus", adminGetMetricMigrationStatus, "Get metrics store migration status (SQLite -> MySQL/PostgreSQL)")
	reg("startMetricMigration", adminStartMetricMigration, "Start migrating metrics data from source SQLite to the current MySQL/PostgreSQL target")
	reg("cancelMetricMigration", adminCancelMetricMigration, "Cancel the currently running metrics store migration")
}

type metricDefinitionResponse struct {
	Name          string            `json:"name"`
	Description   any               `json:"description,omitempty"`
	Type          string            `json:"type"`
	Unit          string            `json:"unit,omitempty"`
	RetentionDays int               `json:"retention_days"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

func metricDescriptionValue(raw string) any {
	desc := strings.TrimSpace(raw)
	if desc == "" {
		return ""
	}
	var dict map[string]string
	if err := json.Unmarshal([]byte(desc), &dict); err == nil && len(dict) > 0 {
		return dict
	}
	return raw
}

func adminListMetricDefinitions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	defs, err := store.ListMetrics(ctx)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to list metric definitions: "+err.Error(), nil)
	}
	out := make([]metricDefinitionResponse, 0, len(defs))
	for _, def := range defs {
		out = append(out, metricDefinitionResponse{
			Name:          def.Name,
			Description:   metricDescriptionValue(def.Description),
			Type:          string(def.Type),
			Unit:          def.Unit,
			RetentionDays: def.RetentionDays,
			Metadata:      def.Metadata,
			CreatedAt:     def.CreatedAt,
			UpdatedAt:     def.UpdatedAt,
		})
	}
	return out, nil
}

func adminUpdateMetricDefinition(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Name          string `json:"name"`
		RetentionDays int    `json:"retention_days"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid request body: "+err.Error(), nil)
	}
	params.Name = strings.TrimSpace(params.Name)
	if params.Name == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "name is required", nil)
	}
	if params.RetentionDays < 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "retention_days must be a non-negative integer", nil)
	}
	store := metricstore.GetStore()
	if store == nil {
		return nil, rpc.MakeError(rpc.InternalError, "metric store not initialized", nil)
	}
	def, err := store.UpdateMetricRetention(ctx, params.Name, params.RetentionDays)
	if errors.Is(err, metric.ErrNotFound) {
		return nil, rpc.MakeError(rpc.InvalidParams, "metric not found: "+params.Name, nil)
	}
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to update metric definition: "+err.Error(), nil)
	}
	if params.RetentionDays == 0 {
		metricstore.DeleteMetricDataAsync(params.Name)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "update metric definition: "+params.Name, "info")

	return metricDefinitionResponse{
		Name:          def.Name,
		Description:   metricDescriptionValue(def.Description),
		Type:          string(def.Type),
		Unit:          def.Unit,
		RetentionDays: def.RetentionDays,
		Metadata:      def.Metadata,
		CreatedAt:     def.CreatedAt,
		UpdatedAt:     def.UpdatedAt,
	}, nil
}

// adminGetMetricMigrationStatus 返回当前 store-to-store 迁移进度快照。
//
// 返回字段：
//   - status:          idle | running | completed | failed | canceled
//   - is_running:      是否有迁移正在进行
//   - source_driver:   源库驱动（如 sqlite）
//   - source_dsn:      源库 DSN（脱敏）
//   - target_driver:   目标库驱动（如 mysql / postgresql）
//   - target_dsn:      目标库 DSN（脱敏）
//   - total_metrics:   指标定义总数
//   - metrics_done:    已完成的指标数
//   - current_metric:  当前正在搬运的指标名
//   - migrated_points: 已搬运的采样点数
//   - start_time / end_time / error
func adminGetMetricMigrationStatus(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	p := metricstore.GetStoreMigrationProgress()
	status := p.Status
	if status == "" {
		status = "idle"
	}
	return map[string]any{
		"status":          status,
		"is_running":      metricstore.IsStoreMigrationRunning(),
		"source_driver":   p.SourceDriver,
		"source_dsn":      p.SourceDSN,
		"target_driver":   p.TargetDriver,
		"target_dsn":      p.TargetDSN,
		"total_metrics":   p.TotalMetrics,
		"metrics_done":    p.MetricsDone,
		"current_metric":  p.CurrentMetric,
		"migrated_points": p.MigratedPoints,
		"start_time":      p.StartTime,
		"end_time":        p.EndTime,
		"error":           p.Error,
	}, nil
}

// adminStartMetricMigration 启动一次 store-to-store 迁移。
//
// 参数（均可选）：
//   - source_driver: 源库驱动；留空时由 source_dsn 推断。
//   - source_dsn:    源库 DSN；留空时使用「上一个已保存的 metrics 目标」，
//     再退化到默认 SQLite（./data/metrics.db）。
//
// 目标固定为“当前运行中的 metric store”（即 editSettings 切换并热重载后的库）。
func adminStartMetricMigration(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		SourceDriver string `json:"source_driver"`
		SourceDSN    string `json:"source_dsn"`
	}
	req.BindParams(&params)

	if err := metricstore.StartStoreMigration(strings.TrimSpace(params.SourceDriver), strings.TrimSpace(params.SourceDSN)); err != nil {
		return nil, rpc.MakeError(rpc.InvalidRequest, err.Error(), nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "start metrics store migration", "info")

	return map[string]any{
		"status":  "started",
		"message": "Metrics store migration started in background",
	}, nil
}

// adminCancelMetricMigration 取消正在运行的 store-to-store 迁移。
// 因写入是幂等 upsert，取消后可安全重新发起，不会产生重复数据。
func adminCancelMetricMigration(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if err := metricstore.CancelStoreMigration(); err != nil {
		return nil, rpc.MakeError(rpc.InvalidRequest, err.Error(), nil)
	}

	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "cancel metrics store migration", "warn")

	return map[string]any{
		"status":  "canceled",
		"message": "Metrics store migration canceled. It can be safely restarted.",
	}, nil
}
