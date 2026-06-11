package jsonrpc

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// bridge.go
// 声明式路由桥：把一个 gin 路由直接绑定到 RPC2 方法，无需手写 handler。
// 负责从 gin 请求装配参数、调用 RPC、并按指定渲染器把响应映射回原有 HTTP/JSON 契约。

// renderKind 决定成功响应如何映射回 HTTP body。
type renderKind int

const (
	// renderStandard: { "status":"success", "message":<msg>, "data":<result> }（api.Respond 约定）
	renderStandard renderKind = iota
	// renderFlat: 把 result(map) 平铺到顶层，并附加 { "status":"success" }
	renderFlat
	// renderRaw: 直接输出 result 作为 body，无任何包装
	renderRaw
)

type bindConfig struct {
	render      renderKind
	successMsg  string
	pathParams  []string // 合并到参数对象的路径参数名
	queryParams []string // 合并到参数对象的查询参数名
}

// BindOption 配置 Bind 行为。
type BindOption func(*bindConfig)

// WithFlat 使成功响应把 result(map) 平铺到顶层（如 { status, uuid, token }）。
func WithFlat() BindOption { return func(c *bindConfig) { c.render = renderFlat } }

// WithRaw 使成功响应直接输出 result，无包装（用于 agent 裸 JSON 接口）。
func WithRaw() BindOption { return func(c *bindConfig) { c.render = renderRaw } }

// WithMessage 使成功响应带固定 message（standard 渲染）。
func WithMessage(msg string) BindOption {
	return func(c *bindConfig) { c.successMsg = msg }
}

// WithPath 声明要合并进参数对象的路径参数（gin c.Param）。
func WithPath(names ...string) BindOption {
	return func(c *bindConfig) { c.pathParams = append(c.pathParams, names...) }
}

// WithQuery 声明要合并进参数对象的查询参数（gin c.Query）。
func WithQuery(names ...string) BindOption {
	return func(c *bindConfig) { c.queryParams = append(c.queryParams, names...) }
}

// Bind 返回一个 gin.HandlerFunc，把请求转发到指定 RPC 方法。
func Bind(method string, opts ...BindOption) gin.HandlerFunc {
	cfg := &bindConfig{render: renderStandard}
	for _, o := range opts {
		o(cfg)
	}
	return func(c *gin.Context) {
		params, ok := assembleParams(c, cfg)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Invalid or missing request body"})
			return
		}
		resp := CallFromGin(c, method, params)
		renderResponse(c, cfg, resp)
	}
}

// assembleParams 从 body + path + query 装配 RPC 参数。
// body 为 JSON 对象时与 path/query 合并为一个对象；body 为数组（且无 path/query）时直接透传数组。
// 返回 ok=false 表示请求体存在但无法解析为合法 JSON（畸形 body）。
func assembleParams(c *gin.Context, cfg *bindConfig) (any, bool) {
	var bodyVal any
	if c.Request.Body != nil {
		if raw, err := io.ReadAll(c.Request.Body); err == nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, &bodyVal); err != nil {
				return nil, false
			}
		}
	}

	// 数组 body 且无附加参数：直接透传。
	if arr, ok := bodyVal.([]any); ok && len(cfg.pathParams) == 0 && len(cfg.queryParams) == 0 {
		return arr, true
	}

	obj := map[string]any{}
	if m, ok := bodyVal.(map[string]any); ok {
		for k, v := range m {
			obj[k] = v
		}
	}
	for _, name := range cfg.pathParams {
		if v := c.Param(name); v != "" {
			obj[name] = v
		}
	}
	for _, name := range cfg.queryParams {
		if v := c.Query(name); v != "" {
			obj[name] = v
		}
	}
	return obj, true
}

// rpcErrorHTTPStatus 将 JSON-RPC 错误码映射为合适的 HTTP 状态码。
func rpcErrorHTTPStatus(code int) int {
	switch code {
	case rpc.InvalidParams, rpc.InvalidRequest, rpc.ParseError:
		return http.StatusBadRequest
	case rpc.PermissionDenied, rpc.Unauthenticated:
		return http.StatusUnauthorized
	case rpc.NotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func renderResponse(c *gin.Context, cfg *bindConfig, resp *rpc.JsonRpcResponse) {
	if resp.Error != nil {
		// 统一错误形状：{ status:"error", message } —— 与 api.RespondError 一致。
		c.JSON(rpcErrorHTTPStatus(resp.Error.Code), gin.H{"status": "error", "message": resp.Error.Message})
		return
	}
	switch cfg.render {
	case renderRaw:
		c.JSON(http.StatusOK, resp.Result)
	case renderFlat:
		out := gin.H{"status": "success"}
		if m, ok := resp.Result.(map[string]any); ok {
			for k, v := range m {
				out[k] = v
			}
		}
		c.JSON(http.StatusOK, out)
	default: // renderStandard
		// 与 api.Response 一致：data 为 nil 时省略该字段（omitempty 语义）。
		out := gin.H{"status": "success", "message": cfg.successMsg}
		if resp.Result != nil {
			out["data"] = resp.Result
		}
		c.JSON(http.StatusOK, out)
	}
}
