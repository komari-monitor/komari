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
	RegisterWithGroupAndMeta("switchAgentVersion", rpc.RoleAdmin, adminSwitchAgentVersion, &rpc.MethodMeta{
		Name:        "admin:switchAgentVersion",
		Summary:     "Request an agent version switch",
		Description: "Queues a fire-and-forget version switch for one online v2 agent.",
		Params: []rpc.ParamMeta{
			{Name: "uuid", Type: "string", Required: true, Description: "Target agent UUID"},
			{Name: "version", Type: "string", Required: true, Description: "latest, a version line such as 1.2.*, or an exact version"},
		},
		Returns: "{ queued: true }",
		Example: map[string]any{"uuid": "agent-uuid", "version": "1.2.*"},
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

func adminSwitchAgentVersion(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		UUID    string `json:"uuid"`
		Version string `json:"version"`
	}
	if err := req.BindParams(&params); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid and version are required", nil)
	}
	if strings.TrimSpace(params.UUID) == "" || strings.TrimSpace(params.Version) == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "uuid and version are required", nil)
	}
	if err := agent_runtime.SwitchAgentVersion(params.UUID, params.Version); err != nil {
		switch {
		case errors.Is(err, agent_runtime.ErrSwitchVersionInvalidParams):
			return nil, rpc.MakeError(rpc.InvalidParams, err.Error(), nil)
		case errors.Is(err, agent_runtime.ErrSwitchVersionOffline):
			return nil, rpc.MakeError(rpc.Unavailable, err.Error(), nil)
		case errors.Is(err, agent_runtime.ErrSwitchVersionUnsupported):
			return nil, rpc.MakeError(rpc.Unimplemented, err.Error(), nil)
		default:
			return nil, rpc.MakeError(rpc.InternalError, "failed to dispatch agent version switch", nil)
		}
	}
	return map[string]any{"queued": true}, nil
}
