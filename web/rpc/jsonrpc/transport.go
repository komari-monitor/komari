package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/clients"
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
// 优先采用 IdentityMiddleware 已确立的角色（c.GetString("role")），其与路由的 RequireRole 一致；
// 缺失时回退到 detectPermissionGroup 自行识别。再经统一 Dispatch 执行。
func CallFromGin(c *gin.Context, method string, params any) *rpc.JsonRpcResponse {
	group := c.GetString("role")
	if group == "" {
		group = detectPermissionGroup(c)
	}
	meta := buildContextMeta(c, group)
	// API Key 等身份下 buildContextMeta 拿不到用户 UUID，回退到中间件已识别的值，
	// 保证审计日志的 actor 与原有 REST 行为一致。
	if meta.UserUUID == "" {
		if v, ok := c.Get("uuid"); ok {
			if s, ok := v.(string); ok {
				meta.UserUUID = s
			}
		}
	}
	// 客户端 token 经 ?token= / body 传入时 buildContextMeta 取不到，
	// 回退到 IdentityMiddleware 已解析的 client_uuid。
	if meta.ClientUUID == "" {
		if v, ok := c.Get("client_uuid"); ok {
			if s, ok := v.(string); ok {
				meta.ClientUUID = s
			}
		}
	}
	req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, Method: method, Params: params}
	return Dispatch(c.Request.Context(), meta, req)
}

func serveWebSocket(c *gin.Context) {
	_conn, err := api.UpgradeWebSocket(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	conn := connection.NewSafeConn(_conn)
	defer conn.Close()

	permissionGroup := detectPermissionGroup(c)
	meta := buildContextMeta(c, permissionGroup)
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
		conn.WriteJSON(Dispatch(context.Background(), meta, &req))
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
	permissionGroup := detectPermissionGroup(c)
	meta := buildContextMeta(c, permissionGroup)

	responses := make([]*rpc.JsonRpcResponse, 0, len(requests))
	for _, rreq := range requests {
		responses = append(responses, Dispatch(c.Request.Context(), meta, rreq))
	}
	// 单条直接对象，批量数组（符合 JSON-RPC 2.0）。
	if len(responses) == 1 {
		c.JSON(http.StatusOK, responses[0])
	} else {
		c.JSON(http.StatusOK, responses)
	}
}

// detectPermissionGroup 识别请求者权限分组 (guest/client/admin)。
func detectPermissionGroup(c *gin.Context) string {
	permissionGroup := rpc.RoleGuest
	token := c.Query("Authorization")
	if _, err := clients.GetClientUUIDByToken(token); err == nil {
		permissionGroup = rpc.RoleClient
	}
	if sessionToken, _ := c.Cookie("session_token"); sessionToken != "" {
		if _, err := accounts.GetUserBySession(sessionToken); err == nil {
			permissionGroup = rpc.RoleAdmin
		}
	}
	apiKey := c.GetHeader("Authorization")
	if len(apiKey) <= len("Bearer ") {
		return permissionGroup
	}
	cfg, _ := config.GetAs[string](config.ApiKeyKey, "")
	if len(cfg) > 8 && apiKey == "Bearer "+cfg {
		permissionGroup = rpc.RoleAdmin
	}
	return permissionGroup
}

// buildContextMeta 从 gin.Context 构建 *rpc.ContextMeta。
func buildContextMeta(c *gin.Context, permissionGroup string) *rpc.ContextMeta {
	meta := &rpc.ContextMeta{Permission: permissionGroup}
	// 客户端 token：query Authorization 或 header Bearer。
	token := c.Query("Authorization")
	if token == "" {
		hAuth := c.GetHeader("Authorization")
		if strings.HasPrefix(hAuth, "Bearer ") {
			token = strings.TrimPrefix(hAuth, "Bearer ")
		}
	}
	if token != "" {
		if uuid, err := clients.GetClientUUIDByToken(token); err == nil {
			meta.ClientToken = token
			meta.ClientUUID = uuid
		}
	}
	// 用户身份：session cookie。
	if sessionToken, _ := c.Cookie("session_token"); sessionToken != "" {
		meta.SessionToken = sessionToken
		if user, err := accounts.GetUserBySession(sessionToken); err == nil {
			meta.User = &user
			meta.UserUUID = user.UUID
		}
	}
	meta.RemoteIP = c.ClientIP()
	meta.UserAgent = c.GetHeader("User-Agent")
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
