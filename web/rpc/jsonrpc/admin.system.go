package jsonrpc

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/database/tasks"

	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/geoip"
	"github.com/komari-monitor/komari/utils/messageSender"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"gorm.io/gorm"
)

// admin.system.go
// 系统/运维类 RPC2 方法（admin 命名空间）：日志、远程执行、测试。

func init() {
	RegisterWithGroupAndMeta("getLogs", rpc.RoleAdmin, adminGetLogs, &rpc.MethodMeta{
		Name:    "admin:getLogs",
		Summary: "Get audit logs (paged, optionally filtered by message type)",
		Params: []rpc.ParamMeta{
			{Name: "limit", Type: "string", Description: "Page size (default 100)"},
			{Name: "page", Type: "string", Description: "One-based page number (default 1)"},
			{Name: "msg_type", Type: "string", Description: "Optional exact message type filter"},
		},
		Returns: "{ logs: Log[], total: number }",
	})
	reg("exec", adminExec, "Execute a command on clients")

	reg("testSendMessage", adminTestSendMessage, "Send a test notification")
	reg("testGeoip", adminTestGeoip, "Test GeoIP lookup")
	// 远程命令执行属敏感操作：除 admin 角色外，还需通过敏感操作二次验证。
	rpc.MarkSensitive("admin:exec")
}

func adminGetLogs(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Limit   string `json:"limit"`
		Page    string `json:"page"`
		MsgType string `json:"msg_type"`
	}
	req.BindParams(&params)
	if params.Limit == "" {
		params.Limit = "100"
	}
	if params.Page == "" {
		params.Page = "1"
	}
	limitInt, err := strconv.Atoi(params.Limit)
	if err != nil || limitInt <= 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid limit: "+params.Limit, nil)
	}
	pageInt, err := strconv.Atoi(params.Page)
	if err != nil || pageInt <= 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid page: "+params.Page, nil)
	}
	db := dbcore.GetDBInstance()
	logs, total, err := queryAdminLogs(db, limitInt, pageInt, params.MsgType)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to retrieve logs: "+err.Error(), nil)
	}
	return map[string]any{"logs": logs, "total": total}, nil
}

func queryAdminLogs(db *gorm.DB, limit, page int, msgType string) ([]models.Log, int64, error) {
	var logs []models.Log
	var total int64
	offset := (page - 1) * limit
	countQuery := filterAdminLogsByMessageType(db.Model(&models.Log{}), msgType)
	logsQuery := filterAdminLogsByMessageType(db.Model(&models.Log{}), msgType)
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := logsQuery.Order("time desc").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func filterAdminLogsByMessageType(query *gorm.DB, msgType string) *gorm.DB {
	if msgType = strings.TrimSpace(msgType); msgType != "" {
		return query.Where("msg_type = ?", msgType)
	}
	return query
}

func adminExec(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Command string   `json:"command"`
		Clients []string `json:"clients"`
	}

	req.BindParams(&params)
	if strings.TrimSpace(params.Command) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Command cannot be empty", nil)
	}
	if len(params.Clients) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "clients is required", nil)
	}

	var onlineClients, queuedClients, offlineClients []string
	for _, uuid := range params.Clients {
		if client := agent_runtime.GetConnectedClients()[uuid]; client != nil {
			onlineClients = append(onlineClients, uuid)
		} else if agent_runtime.IsAgentOnline(uuid) {
			queuedClients = append(queuedClients, uuid)
		} else {
			offlineClients = append(offlineClients, uuid)
		}
	}
	if len(onlineClients) == 0 && len(queuedClients) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "No clients connected", nil)
	}
	taskId := utils.GenerateRandomString(16)
	taskClients := append(append([]string{}, onlineClients...), queuedClients...)
	taskClients = append(taskClients, offlineClients...)
	if err := tasks.CreateTask(taskId, taskClients, params.Command); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to create task: "+err.Error(), nil)
	}
	for _, uuid := range onlineClients {
		legacy := struct {
			Message string `json:"message"`
			Command string `json:"command"`
			TaskId  string `json:"task_id"`
		}{Message: "exec", Command: params.Command, TaskId: taskId}
		payload, _ := json.Marshal(legacy)
		if agent_runtime.IsV2Client(uuid) {
			payload, _ = json.Marshal(v2.Request{JSONRPC: v2.Version, Method: v2.MethodAgentExec, Params: v2.ExecParams{TaskID: taskId, Command: params.Command}})
		}
		client := agent_runtime.GetConnectedClients()[uuid]
		if client == nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Client connection is null: "+uuid, nil)
		}
		if err := client.WriteMessage(websocket.TextMessage, payload); err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "Client connection is broke: "+uuid, nil)
		}
	}
	for _, uuid := range queuedClients {
		agent_runtime.DispatchV2Event(uuid, v2.MethodAgentExec, v2.ExecParams{TaskID: taskId, Command: params.Command})
	}
	actor, ip := auditActor(ctx)
	auditlog.Log(ip, actor, "REC, task id: "+taskId, "warn")
	if len(offlineClients) > 0 {
		for _, uuid := range offlineClients {
			tasks.SaveTaskResult(taskId, uuid, "Client offline!", -1, time.Now().UTC())
		}
	}
	return map[string]any{
		"task_id":        taskId,
		"clients":        onlineClients,
		"queued_clients": queuedClients,
	}, nil
}

func adminTestSendMessage(_ context.Context, _ *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	err := messageSender.SendEvent(models.EventMessage{
		Event:   "Test",
		Time:    time.Now().UTC(),
		Message: "This is a test message from Komari.",
	})
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to send message: "+err.Error(), nil)
	}
	return nil, nil
}

func adminTestGeoip(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		IP string `json:"ip"`
	}
	req.BindParams(&params)
	ip := params.IP
	if ip == "" {
		if meta := rpc.MetaFromContext(ctx); meta != nil {
			ip = meta.RemoteIP
		}
	}
	cfg, err := config.GetAs[bool](config.GeoIpEnabledKey, false)
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get configuration: "+err.Error(), nil)
	}
	if !cfg {
		return nil, rpc.MakeError(rpc.InvalidParams, "GeoIP is not enabled in the configuration.", nil)
	}
	record, err := geoip.GetGeoInfo(net.ParseIP(ip))
	if err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to get GeoIP record: "+err.Error(), nil)
	}
	return record, nil
}
