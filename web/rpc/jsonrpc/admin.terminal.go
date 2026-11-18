package jsonrpc

import (
	"context"
	"strings"

	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/api/terminal"
)

func init() {
	RegisterWithGroupAndMeta("listRecoverableTerminalSessions", rpc.RoleAdmin, adminListRecoverableTerminalSessions, &rpc.MethodMeta{
		Name:        "admin:listRecoverableTerminalSessions",
		Summary:     "List recoverable terminal sessions where the browser disconnected",
		Description: "Returns request IDs of terminal sessions whose browser side is disconnected while the session remains retained (agent connected or waiting reconnect).",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: false, Description: "Optional client UUID filter"},
		},
		Returns: "RecoverableSession[] ({ request_id, uuid, agent_connected })",
	})
	RegisterWithGroupAndMeta("terminateTerminalSessions", rpc.RoleAdmin, adminTerminateTerminalSessions, &rpc.MethodMeta{
		Name:        "admin:terminateTerminalSessions",
		Summary:     "Terminate recoverable terminal sessions",
		Description: "Terminates browser-disconnected terminal sessions owned by the caller and sends terminate to connected agents.",
		Params: []rpc.ParamMeta{
			{Name: "request_ids", Type: "string[]", Required: false, Description: "Terminal request IDs to terminate"},
			{Name: "request_id", Type: "string", Required: false, Description: "Single terminal request ID to terminate"},
		},
		Returns: "{ terminated: string[] }",
	})
}

func callerTerminalIdentity(ctx context.Context) (userUUID string, isAPIKey bool) {
	meta := rpc.MetaFromContext(ctx)
	if meta == nil {
		return "", false
	}
	if meta.Principal != nil {
		if meta.Principal.IsAPIKey {
			return "", true
		}
		if meta.Principal.UserUUID != "" {
			return meta.Principal.UserUUID, false
		}
	}
	return meta.UserUUID, false
}

func adminListRecoverableTerminalSessions(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	if req != nil {
		if err := req.BindParams(&params); err != nil {
			return nil, rpc.MakeError(rpc.InvalidParams, "invalid params: "+err.Error(), nil)
		}
	}
	userUUID, isAPIKey := callerTerminalIdentity(ctx)
	return terminal.ListRecoverableSessions(userUUID, isAPIKey, strings.TrimSpace(params.UUID)), nil
}

func adminTerminateTerminalSessions(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		RequestIDs []string `json:"request_ids"`
		RequestID  string   `json:"request_id"`
	}
	if req == nil || req.BindParams(&params) != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "request_ids or request_id is required", nil)
	}
	ids := make([]string, 0, len(params.RequestIDs)+1)
	for _, id := range params.RequestIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	if trimmed := strings.TrimSpace(params.RequestID); trimmed != "" {
		ids = append(ids, trimmed)
	}
	if len(ids) == 0 {
		return nil, rpc.MakeError(rpc.InvalidParams, "request_ids or request_id is required", nil)
	}
	userUUID, isAPIKey := callerTerminalIdentity(ctx)
	terminated := terminal.TerminateRecoverableSessions(userUUID, isAPIKey, ids)
	return map[string]any{"terminated": terminated}, nil
}
