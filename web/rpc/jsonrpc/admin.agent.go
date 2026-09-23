package jsonrpc

import (
	"context"
	"errors"
	"strings"

	"github.com/komari-monitor/komari/pkg/rpc"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
)

func init() {
	RegisterWithGroupAndMeta("getAgentStartupConfig", rpc.RoleAdmin, adminGetAgentStartupConfig, &rpc.MethodMeta{
		Name:        "admin:getAgentStartupConfig",
		Summary:     "Get all effective agent startup settings as a flat object",
		Description: "Includes credentials and default values without redaction. Requires an online updated agent.",
		Params:      []rpc.ParamMeta{{Name: "uuid", Type: "string", Description: "Agent UUID"}},
		Returns:     "Flat startup configuration object",
	})
	rpc.MarkSensitive("admin:getAgentStartupConfig")
}

func adminGetAgentStartupConfig(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID string `json:"uuid"`
	}
	if err := req.BindParams(&params); err != nil || strings.TrimSpace(params.UUID) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid is required", nil)
	}
	result, err := agent_runtime.GetStartupConfig(ctx, params.UUID)
	switch {
	case err == nil:
		return result, nil
	case errors.Is(err, agent_runtime.ErrStartupConfigOffline):
		return nil, rpc.MakeError(rpc.Unavailable, err.Error(), nil)
	case errors.Is(err, agent_runtime.ErrStartupConfigTimeout):
		return nil, rpc.MakeError(rpc.DeadlineExceeded, err.Error(), nil)
	case errors.Is(err, context.Canceled):
		return nil, rpc.MakeError(rpc.Cancelled, "request canceled", nil)
	default:
		return nil, rpc.MakeError(rpc.InternalError, "failed to get agent startup configuration", nil)
	}
}
