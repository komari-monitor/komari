package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/connection"
)

// OnRpcRequest 是 /api/rpc2 的统一入口：GET 升级为 WebSocket，POST 处理单条/批量 JSON-RPC。
func OnRpcRequest(c *gin.Context) {
	// GET -> WebSocket
	if c.Request.Method == http.MethodGet {
		serveWebSocket(c)
		return
	}

	if c.Request.Method != http.MethodPost {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}
	servePost(c)
}

// CallFromGin 供传统 gin handler / 路由桥转调 RPC 方法。
// 复用 IdentityMiddleware 已识别的 principal；未识别时兜底调用 IdentifyPrincipal。
func CallFromGin(c *gin.Context, method string, params any) *rpc.JsonRpcResponse {
	meta := buildContextMeta(c)
	req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: method, Params: params}
	return dispatchWithSensitive(c.Request.Context(), c, meta, req)
}

// dispatchWithSensitive 在统一分发前对敏感方法补充二次验证，使各调用入口行为一致。
// 对已通过命名空间权限校验的敏感方法，要求调用方满足敏感操作 2FA（沿用
// api.VerifySensitive2FA 语义：API Key 放行、未配置 2FA 的账号沿用既有行为），
// 再交由 Dispatch 执行；Dispatch 仍为权威鉴权点。
//
// 若请求已被 RequireSensitive2FA 中间件校验过（sensitive_2fa_verified），则跳过，
// 避免在 body 被解析消费后重复读取校验。经 /api/rpc2 调用时无该中间件，2FA 码由
// X-2FA-Code 请求头（或 query）传入。
func dispatchWithSensitive(ctx context.Context, c *gin.Context, meta *rpc.ContextMeta, req *rpc.JsonRpcRequest) *rpc.JsonRpcResponse {
	if c != nil && meta != nil && !c.GetBool("sensitive_2fa_verified") &&
		rpc.IsSensitive(req.Method) && rpc.CheckPermission(meta.Permission, req.Method) {
		if err := api.VerifySensitive2FA(c); err != nil {
			return rpc.ErrorResponse(req.ID, rpc.PermissionDenied, err.Error(), nil)
		}
	}
	return Dispatch(ctx, meta, req)
}

func serveWebSocket(c *gin.Context) {
	_conn, err := api.UpgradeWebSocket(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	conn := connection.NewSafeConn(_conn)
	defer conn.Close()

	meta := buildContextMeta(c)
	for {
		var req rpc.JsonRpcRequest
		if err := conn.ReadJSON(&req); err != nil {
			var se *json.SyntaxError
			var ute *json.UnmarshalTypeError
			if errors.As(err, &se) || errors.As(err, &ute) {
				conn.WriteJSON(rpc.ErrorResponse(nil, rpc.InvalidRequest, "bad request: "+err.Error(), nil))
				continue
			}
			// 其它视为连接/IO 错误，结束循环
			break
		}
		if jerr := req.Validate(); jerr != nil {
			conn.WriteJSON(jerr.ResponseWithID(req.ID))
			continue
		}
		// 同步写：SafeConn 内部有锁，串行写避免响应乱序与并发竞态。
		conn.WriteJSON(dispatchWithSensitive(context.Background(), c, meta, &req))
	}
}

func servePost(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, rpc.ErrorResponse(nil, rpc.ParseError, "read body error", err.Error()))
		return
	}
	requests, jerr := rpc.ParseRequests(body)
	if jerr != nil {
		c.JSON(http.StatusBadRequest, jerr.Response())
		return
	}
	meta := buildContextMeta(c)

	responses := make([]*rpc.JsonRpcResponse, 0, len(requests))
	for _, rreq := range requests {
		responses = append(responses, dispatchWithSensitive(c.Request.Context(), c, meta, rreq))
	}
	// 单条直接对象，批量数组（符合 JSON-RPC 2.0）。
	if len(responses) == 1 {
		c.JSON(http.StatusOK, responses[0])
	} else {
		c.JSON(http.StatusOK, responses)
	}
}

// buildContextMeta 从 gin.Context 构建 *rpc.ContextMeta。
// 复用 IdentityMiddleware 已识别的 principal(api.GetPrincipal)；若未识别则兜底调用
// api.IdentifyPrincipal。填充 principal、Permission(兼容)、User、各 UUID、token 等字段。
func buildContextMeta(c *gin.Context) *rpc.ContextMeta {
	// 优先读取中间件已识别的 principal；未识别时兜底自行识别(如 /api/rpc2 请求)。
	p := api.GetPrincipal(c)
	if p == nil {
		p = api.IdentifyPrincipal(c)
	}

	meta := &rpc.ContextMeta{
		Principal:  p,
		Permission: p.PrimaryRole(), // 兼容现有 handler 与 Dispatch
		RemoteIP:   c.ClientIP(),
		UserAgent:  c.GetHeader("User-Agent"),
	}

	// 根据主体类型填充具体字段。
	switch p.Type {
	case rpc.PrincipalUser:
		meta.UserUUID = p.UserUUID
		if session, err := c.Cookie("session_token"); err == nil && session != "" {
			meta.SessionToken = session
			if user, err := accounts.GetUserBySession(session); err == nil {
				meta.User = &user
			}
		}
	case rpc.PrincipalAgent:
		meta.ClientUUID = p.ClientUUID
		// 尝试提取 client token(用于某些 handler 需要原始 token 的场景)。
		// 优先查询参数 ?Authorization=<token>，再尝试 Bearer header。
		if token := c.Query("Authorization"); token != "" {
			meta.ClientToken = token
		} else if auth := c.GetHeader("Authorization"); auth != "" && len(auth) > len("Bearer ") {
			meta.ClientToken = auth[len("Bearer "):]
		}
	}

	// 临时分享访问许可。
	meta.TempShareValid = hasTempShareAccess(c)
	return meta
}

// hasTempShareAccess 校验 temp_key cookie 是否为有效的临时分享访问许可。
func hasTempShareAccess(c *gin.Context) bool {
	tempKey, err := c.Cookie("temp_key")
	if err != nil || tempKey == "" {
		return false
	}
	expireAt, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
	if err != nil {
		return false
	}
	allowKey, err := config.GetAs[string]("tempory_share_token", "")
	if err != nil || allowKey == "" || tempKey != allowKey {
		return false
	}
	return expireAt >= time.Now().Unix()
}
