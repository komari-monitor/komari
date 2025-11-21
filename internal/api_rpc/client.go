package api_rpc

import (
	"context"

	"github.com/komari-monitor/komari/pkg/rpc"
)

const GroupClient = "client"

func init() {
	RegisterWithGroupAndMeta("register", GroupClient, registerClient, &rpc.MethodMeta{
		Name:        "RegisterClient",
		Description: "Register a new client with the server (Auto discovery).",
		Params: []rpc.ParamMeta{
			{
				Name:        "name",
				Type:        "string",
				Description: "The name of the client to register.",
			},
		},
	})
}

func registerClient(ctx context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Name string `json:"name"`
	}
	req.BindParams(&params)
	return nil, rpc.MakeError(rpc.InternalError, "Not impl yet", nil)
}
