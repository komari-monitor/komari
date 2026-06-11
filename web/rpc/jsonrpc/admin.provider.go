package jsonrpc

import (
	"context"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/utils/messageSender"
	msfactory "github.com/komari-monitor/komari/utils/messageSender/factory"
	"github.com/komari-monitor/komari/web/oauth"
	oauthfactory "github.com/komari-monitor/komari/web/oauth/factory"
)

// admin.provider.go
// 消息发送器与 OIDC 提供者配置 RPC2 方法（admin 命名空间）。

func init() {
	reg("getMessageSenderProvider", adminGetMessageSender, "Get message sender provider config or templates")
	reg("setMessageSenderProvider", adminSetMessageSender, "Set message sender provider config")
	reg("getOidcProvider", adminGetOidc, "Get OIDC provider config or templates")
	reg("setOidcProvider", adminSetOidc, "Set OIDC provider config")
}

func adminGetMessageSender(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Provider string `json:"provider"`
	}
	req.BindParams(&params)
	if params.Provider != "" {
		cfg, err := database.GetMessageSenderConfigByName(params.Provider)
		if err != nil {
			return nil, rpc.MakeError(rpc.NotFound, "Provider not found: "+err.Error(), nil)
		}
		return cfg, nil
	}
	providers := msfactory.GetSenderConfigs()
	if len(providers) == 0 {
		return nil, rpc.MakeError(rpc.NotFound, "No message sender providers found", nil)
	}
	return providers, nil
}

func adminSetMessageSender(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var senderConfig models.MessageSenderProvider
	if err := req.BindParams(&senderConfig); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid configuration: "+err.Error(), nil)
	}
	if senderConfig.Name == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Provider name is required", nil)
	}
	if _, exists := msfactory.GetConstructor(senderConfig.Name); !exists {
		return nil, rpc.MakeError(rpc.NotFound, "Provider not found: "+senderConfig.Name, nil)
	}
	if err := database.SaveMessageSenderConfig(&senderConfig); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to save message sender provider configuration: "+err.Error(), nil)
	}
	method, _ := config.GetAs[string](config.NotificationMethodKey, "none")
	if method == senderConfig.Name { // 正在使用，重载
		if err := messageSender.LoadProvider(senderConfig.Name, senderConfig.Addition); err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to load message sender provider: "+err.Error(), nil)
		}
	}
	return map[string]any{"message": "Message sender provider set successfully"}, nil
}

func adminGetOidc(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var params struct {
		Provider string `json:"provider"`
	}
	req.BindParams(&params)
	if params.Provider != "" {
		cfg, err := database.GetOidcConfigByName(params.Provider)
		if err != nil {
			return nil, rpc.MakeError(rpc.NotFound, "Provider not found: "+err.Error(), nil)
		}
		return cfg, nil
	}
	providers := oauthfactory.GetProviderConfigs()
	if len(providers) == 0 {
		return nil, rpc.MakeError(rpc.NotFound, "No OIDC providers found", nil)
	}
	return providers, nil
}

func adminSetOidc(_ context.Context, req *rpc.JsonRpcRequest) (any, *rpc.JsonRpcError) {
	var oidcConfig models.OidcProvider
	if err := req.BindParams(&oidcConfig); err != nil {
		return nil, rpc.MakeError(rpc.InvalidParams, "Invalid configuration: "+err.Error(), nil)
	}
	if oidcConfig.Name == "" {
		return nil, rpc.MakeError(rpc.InvalidParams, "Provider name is required", nil)
	}
	if _, exists := oauthfactory.GetConstructor(oidcConfig.Name); !exists {
		return nil, rpc.MakeError(rpc.NotFound, "Provider not found: "+oidcConfig.Name, nil)
	}
	if err := database.SaveOidcConfig(&oidcConfig); err != nil {
		return nil, rpc.MakeError(rpc.InternalError, "Failed to save OIDC provider configuration: "+err.Error(), nil)
	}
	provider, _ := config.GetAs[string](config.OAuthProviderKey, "github")
	if provider == oidcConfig.Name { // 正在使用，重载
		if err := oauth.LoadProvider(oidcConfig.Name, oidcConfig.Addition); err != nil {
			return nil, rpc.MakeError(rpc.InternalError, "Failed to load OIDC provider: "+err.Error(), nil)
		}
	}
	return map[string]any{"message": "OIDC provider set successfully"}, nil
}
