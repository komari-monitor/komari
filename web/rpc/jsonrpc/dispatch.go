package jsonrpc

import (
	"context"

	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// privateSiteLoginWhitelist 私有站点模式下仍允许匿名访问的方法白名单。
// 这些方法返回登录页渲染所需的元信息(站点配置、版本、当前登录态占位)。
// 不在此白名单的 public:* 方法(如 getNodesInformation)会被私有站点拦截。
var privateSiteLoginWhitelist = map[string]bool{
	"public:getMe":             true,
	"public:getPublicSettings": true,
	"public:getVersion":        true,
}

// Dispatch 是所有传输入口的统一分发点：私有站点检查 → 权限校验 → 执行方法。
// ctx 携带可选的取消/超时；meta 为调用者身份元数据（Principal 为权威来源）。
// 始终返回完整的 JsonRpcResponse（包含错误）。
func Dispatch(ctx context.Context, meta *rpc.ContextMeta, req *rpc.JsonRpcRequest) *rpc.JsonRpcResponse {
	if ctx == nil {
		ctx = context.Background()
	}
	if meta == nil {
		meta = &rpc.ContextMeta{Principal: rpc.NewAnonymousPrincipal()}
	}
	// 保证 Principal 与 Permission 字段双向同步(后者用于向后兼容)。
	if meta.Principal == nil {
		if meta.Permission != "" {
			meta.Principal = rpc.PrincipalFromRole(meta.Permission)
		} else {
			meta.Principal = rpc.NewAnonymousPrincipal()
		}
	}
	if meta.Permission == "" {
		meta.Permission = meta.Principal.PrimaryRole()
	}

	// 私有站点：未认证访客一律拒绝，但放行登录页所需的元信息接口(见 issue #567)。
	if meta.Principal.Type == rpc.PrincipalAnonymous && !privateSiteLoginWhitelist[req.Method] {
		if privateSite, _ := config.GetAs[bool](config.PrivateSiteKey); privateSite {
			return rpc.ErrorResponse(req.ID, rpc.PermissionDenied, "Private site enabled, please login first", nil)
		}
	}

	// 命名空间权限校验:基于 Principal 的能力集(集合成员语义)。
	if !rpc.CheckPrincipal(meta.Principal, req.Method) {
		return rpc.ErrorResponse(req.ID, rpc.PermissionDenied, "Permission denied", nil)
	}

	return rpc.CallWithContext(rpc.NewContextWithMeta(ctx, meta), req.ID, req.Method, req.Params)
}

// OnInternalRequest 内部调用 RPC 方法（如服务端代码代发请求），仅携带权限分组。
// group: 调用者权限分组 (guest/client/admin)；method: "namespace:method"；params: 参数。
func OnInternalRequest(ctx context.Context, group string, method string, params interface{}) *rpc.JsonRpcResponse {
	meta := &rpc.ContextMeta{Permission: group}
	req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: method, Params: params}
	return Dispatch(ctx, meta, req)
}
