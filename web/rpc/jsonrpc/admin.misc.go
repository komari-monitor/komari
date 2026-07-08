package jsonrpc

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// admin.misc.go
// 杂项 admin RPC2 方法：会话管理、设置、客户端排序。

func parseUintKey(s string) (uint, error) {
	v, err := strconv.ParseUint(s, 10, 64)
	return uint(v), err
}

func init() {
	RegisterWithGroupAndMeta("getSessions", rpc.RoleAdmin, adminGetSessions, &rpc.MethodMeta{
		Name:    "admin:getSessions",
		Summary: "List all login sessions",
		Returns: "{ current: string, data: Session[] }",
	})
	RegisterWithGroupAndMeta("deleteSession", rpc.RoleAdmin, adminDeleteSession, &rpc.MethodMeta{
		Name:    "admin:deleteSession",
		Summary: "Delete a session by token",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("deleteAllSessions", rpc.RoleAdmin, adminDeleteAllSessions, &rpc.MethodMeta{
		Name:    "admin:deleteAllSessions",
		Summary: "Delete all sessions",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("getSettings", rpc.RoleAdmin, adminGetSettings, &rpc.MethodMeta{
		Name:    "admin:getSettings",
		Summary: "Get all settings",
		Returns: "object",
	})
	RegisterWithGroupAndMeta("editSettings", rpc.RoleAdmin, adminEditSettings, &rpc.MethodMeta{
		Name:    "admin:editSettings",
		Summary: "Update settings (partial)",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("clearAllRecords", rpc.RoleAdmin, adminClearAllRecords, &rpc.MethodMeta{
		Name:    "admin:clearAllRecords",
		Summary: "Delete all load and ping records",
		Returns: "null",
	})
	RegisterWithGroupAndMeta("orderClients", rpc.RoleAdmin, adminOrderClients, &rpc.MethodMeta{
		Name:    "admin:orderClients",
		Summary: "Reorder clients (map of uuid->weight)",
		Returns: "null",
	})
}

func adminGetSessions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	ss, err := accounts.GetAllSessions()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve sessions: "+err.Error(), nil)
	}
	current := ""
	if meta := rpc.MetaFromContext(ctx); meta != nil {
		current = meta.SessionToken
	}
	return map[string]any{"current": current, "data": ss}, nil
}

func adminDeleteSession(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Session string `json:"session"`
	}
	req.BindParams(&params)
	if params.Session == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "session is required", nil)
	}
	if err := accounts.DeleteSession(params.Session); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete session: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "delete session", "info")
	return nil, nil
}

func adminDeleteAllSessions(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	if err := accounts.DeleteAllSessions(); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to delete all sessions: "+err.Error(), nil)
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "delete all sessions", "warn")
	return nil, nil
}

func adminGetSettings(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	cst, err := config.GetAll()
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get settings: "+err.Error(), nil)
	}
	return cst, nil
}

// metricStoreConfigKeys 是与 metrics 独立数据库相关、需要触发连接测试 + 热重载的配置键。
//
// 注意：metric_store_enabled 已废弃（metric store 始终启用），不再纳入此集合。
var metricStoreConfigKeys = map[string]struct{}{
	metricstore.MetricDBDriverKey:      {},
	metricstore.MetricDBDSNKey:         {},
	metricstore.MetricRetentionDaysKey: {},
	metricstore.MetricTablePrefixKey:   {},
	metricstore.MetricMaxOpenConnsKey:  {},
	metricstore.MetricMaxIdleConnsKey:  {},
}

// metricKeysTouched 判断本次设置变更是否涉及 metrics 数据库相关键。
func metricKeysTouched(cfg map[string]interface{}) bool {
	for key := range cfg {
		if _, ok := metricStoreConfigKeys[key]; ok {
			return true
		}
	}
	return false
}

func adminEditSettings(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	cfg := make(map[string]interface{})
	if err := req.BindParams(&cfg); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing request body: "+err.Error(), nil)
	}

	// 若本次修改涉及 metrics 数据库配置，则在落库前先用「当前配置 + 本次改动」
	// 合并出的目标配置做一次连接测试。metric store 始终启用，只要触及 metrics
	// 相关键就做连接测试，避免把明显无效的连接串保存给用户。
	touchedMetric := metricKeysTouched(cfg)
	if touchedMetric {
		// 数据库类型不再由前端显式选择，而是根据 DSN 自动推断后写回配置，
		// 使后续连接测试、热重载和初始化都使用一致的 driver。
		if v, ok := cfg[metricstore.MetricDBDSNKey]; ok {
			if dsn, ok := v.(string); ok {
				dsn = strings.TrimSpace(dsn)
				cfg[metricstore.MetricDBDSNKey] = dsn
				if driver, inferred := metricstore.InferDriverFromDSN(dsn); inferred {
					cfg[metricstore.MetricDBDriverKey] = string(driver)
				}
			}
		}

		merged, err := mergedMetricConfig(cfg)
		if err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to resolve metric store config: "+err.Error(), nil)
		}
		testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := metricstore.TestConnection(testCtx, merged); err != nil {
			cancel()
			return nil, rpc.MakeError(rpc.InvalidParams,
				"Metrics database connection test failed: "+err.Error(), nil)
		}
		cancel()
	}

	if err := config.SetMany(cfg); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to update settings: "+err.Error(), nil)
	}

	// 配置已落库，热重载 metric store（无需重启）。连接已在上面验证过，
	// 这里再次失败属异常情况，回报给用户。
	if touchedMetric {
		reloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := metricstore.Reload(reloadCtx); err != nil {
			cancel()
			return nil, rpc.MakeError(rpc.InternalError,
				"Settings saved but metrics database hot reload failed: "+err.Error(), nil)
		}
		cancel()
	}

	message := "update settings: "
	for key := range cfg {
		message += key + ", "
	}
	if len(message) > 2 {
		message = message[:len(message)-2]
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, message, "info")
	return nil, nil
}

// mergedMetricConfig 读取当前持久化的 metric store 配置，并把本次请求中涉及的
// metrics 相关键覆盖上去，得到「即将生效」的目标配置，用于落库前的连接测试。
func mergedMetricConfig(cfg map[string]interface{}) (*metricstore.MetricStoreConfig, error) {
	merged, err := config.GetManyAs[metricstore.MetricStoreConfig]()
	if err != nil {
		return nil, err
	}

	if v, ok := cfg[metricstore.MetricDBDriverKey]; ok {
		if s, ok := v.(string); ok {
			merged.Driver = s
		}
	}

	if v, ok := cfg[metricstore.MetricDBDSNKey]; ok {
		if s, ok := v.(string); ok {
			merged.DSN = s
		}
	}
	if v, ok := cfg[metricstore.MetricRetentionDaysKey]; ok {
		merged.RetentionDays = toInt(v, merged.RetentionDays)
	}
	if v, ok := cfg[metricstore.MetricTablePrefixKey]; ok {
		if s, ok := v.(string); ok {
			merged.TablePrefix = s
		}
	}
	if v, ok := cfg[metricstore.MetricMaxOpenConnsKey]; ok {
		merged.MaxOpenConns = toInt(v, merged.MaxOpenConns)
	}
	if v, ok := cfg[metricstore.MetricMaxIdleConnsKey]; ok {
		merged.MaxIdleConns = toInt(v, merged.MaxIdleConns)
	}

	return merged, nil
}

// toInt 将 JSON 解码得到的任意值（通常是 float64 或 string）转换为 int，失败时返回 fallback。
func toInt(v any, fallback int) int {

	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	case string:
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return fallback
}

func adminClearAllRecords(ctx context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {

	records.DeleteAll()
	tasks.DeleteAllPingRecords()
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "clear all records", "info")
	return nil, nil
}

func adminOrderClients(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var order map[string]int
	if err := req.BindParams(&order); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid or missing request body: "+err.Error(), nil)
	}
	db := dbcore.GetDBInstance()
	for uuid, weight := range order {
		if err := db.Model(&models.Client{}).Where("uuid = ?", uuid).Update("weight", weight).Error; err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to update client weight: "+err.Error(), nil)
		}
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "order clients", "info")
	return nil, nil
}
