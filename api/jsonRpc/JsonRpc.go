package jsonRpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/utils/rpc"
	"github.com/komari-monitor/komari/ws"
)

// OnInternalRequest 内部调用 RPC 方法，支持权限控制
// group: 调用者的权限分组 (guest/client/admin)
// method: RPC 方法名 (如 "common:getNodes")
// params: 方法参数
func OnInternalRequest(ctx context.Context, group string, method string, params interface{}) *rpc.JsonRpcResponse {
	// 检查私有站点
	PrivateSite, _ := config.GetAs[bool](config.PrivateSiteKey)

	if PrivateSite && group == "guest" {
		return rpc.ErrorResponse(nil, rpc.PermissionDenied, "Private site enabled, please login first", nil)
	}

	// 解析方法命名空间
	fc := strings.Split(method, ":")
	namespace := "common"
	if len(fc) >= 2 {
		namespace = fc[0]
	}

	// 权限检查
	allowed := false
	switch namespace {
	case "guest", "", "rpc", "common":
		allowed = true
	case "client":
		if group == "client" || group == "admin" {
			allowed = true
		}
	case "admin":
		if group == "admin" {
			allowed = true
		}
	}

	if !allowed {
		return rpc.ErrorResponse(nil, rpc.PermissionDenied, "Permission denied", nil)
	}

	// 构建 context meta
	meta := &rpc.ContextMeta{Permission: group}
	return rpc.CallWithContext(rpc.NewContextWithMeta(ctx, meta), nil, method, params)
}

// Json Rpc2 over websocket, /api/rpc2
func OnRpcRequest(c *gin.Context) {
	AllowCors, _ := config.GetAs[bool](config.AllowCorsKey)

	// GET -> WebSocket
	if c.Request.Method == http.MethodGet {
		_conn, err := ws.UpgradeRequest(c, func(r *http.Request) bool {
			if AllowCors {
				return true
			}
			return ws.CheckOrigin(r)
		})
		conn := ws.NewSafeConn(_conn)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
			return
		}
		permissionGroup := detectPermissionGroup(c)
		meta := buildContextMeta(c, permissionGroup)
		defer conn.Close()
		for {
			var req rpc.JsonRpcRequest
			err := conn.ReadJSON(&req)
			if err != nil {
				// JSON 解析
				var se *json.SyntaxError
				var ute *json.UnmarshalTypeError
				if errors.As(err, &se) || errors.As(err, &ute) {
					conn.WriteJSON(rpc.ErrorResponse(nil, rpc.InvalidRequest, "bad request: "+err.Error(), nil))
					continue
				}
				// 其它视为连接/IO 错误
				break
			}
			if jerr := req.Validate(); jerr != nil {
				conn.WriteJSON(jerr.ResponseWithID(req.ID))
				continue
			}
			dispatchByPermissionWithMeta(conn, permissionGroup, meta, &req)
		}
		return
	}

	// 否则按 HTTP POST JSON-RPC 处理 (支持单个或批量)
	if c.Request.Method != http.MethodPost {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}
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
	// 批量
	responses := make([]*rpc.JsonRpcResponse, 0, len(requests))
	for _, rreq := range requests {
		// 权限与命名空间
		fc := strings.Split(rreq.Method, ":")
		if len(fc) == 1 {
			fc[0] = "common"
		}
		allowed := false
		switch fc[0] {
		case "guest", "", "rpc", "common":
			allowed = true
		case "client":
			if permissionGroup == "client" || permissionGroup == "admin" {
				allowed = true
			}
		case "admin":
			if permissionGroup == "admin" {
				allowed = true
			}
		default:
			responses = append(responses, rpc.ErrorResponse(rreq.ID, 401, "Unauthorized", nil))
			continue
		}
		if !allowed {
			responses = append(responses, rpc.ErrorResponse(rreq.ID, 401, "Unauthorized", nil))
			continue
		}
		responses = append(responses, rpc.CallWithContext(rpc.NewContextWithMeta(context.TODO(), meta), rreq.ID, rreq.Method, rreq.Params))
	}
	// 单个请求直接对象，批量请求数组 (符合 JSON-RPC 2.0)
	if len(responses) == 1 {
		c.JSON(http.StatusOK, responses[0])
	} else {
		c.JSON(http.StatusOK, responses)
	}
}

// detectPermissionGroup 提取权限分组，与原逻辑保持一致
func detectPermissionGroup(c *gin.Context) string {
	permissionGroup := "guest"
	token := c.Query("Authorization")
	if _, err := clients.GetClientUUIDByToken(token); err == nil {
		permissionGroup = "client"
	}
	if session_token, _ := c.Cookie("session_token"); session_token != "" {
		if _, err := accounts.GetUserBySession(session_token); err == nil {
			permissionGroup = "admin"
		}
	}
	apiKey := c.GetHeader("Authorization")
	if len(apiKey) <= len("Bearer ") {
		return permissionGroup
	}
	cfg, _ := config.GetAs[string](config.ApiKeyKey, "")
	if len(cfg) > 8 && apiKey == "Bearer "+cfg {
		permissionGroup = "admin"
	}
	return permissionGroup
}

// buildContextMeta 从 gin.Context 构建 *rpc.ContextMeta
func buildContextMeta(c *gin.Context, permissionGroup string) *rpc.ContextMeta {
	meta := &rpc.ContextMeta{Permission: permissionGroup}
	// 提取客户端 token (query Authorization / Header Authorization Bearer token)
	token := c.Query("Authorization")
	if token == "" {
		// 兼容 header Bearer <token>
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
	// 提取用户 (session cookie)
	if session_token, _ := c.Cookie("session_token"); session_token != "" {
		if user, err := accounts.GetUserBySession(session_token); err == nil {
			meta.User = &user
			meta.UserUUID = user.UUID
		}
	}
	meta.RemoteIP = c.ClientIP()
	meta.UserAgent = c.GetHeader("User-Agent")
	return meta
}

// dispatchByPermissionWithMeta 与原函数类似，但会携带 meta 上下文给 handler
func dispatchByPermissionWithMeta(conn *ws.SafeConn, permissionGroup string, meta *rpc.ContextMeta, req *rpc.JsonRpcRequest) {
	fc := strings.Split(req.Method, ":")
	if len(fc) == 1 {
		fc[0] = "common"
	}
	ctx := rpc.NewContextWithMeta(context.TODO(), meta)
	switch fc[0] {
	case "guest", "", "rpc", "common":
		go conn.WriteJSON(rpc.CallWithContext(ctx, req.ID, req.Method, req.Params))
	case "client":
		if permissionGroup == "client" || permissionGroup == "admin" {
			go conn.WriteJSON(rpc.CallWithContext(ctx, req.ID, req.Method, req.Params))
		} else {
			conn.WriteJSON(rpc.ErrorResponse(req.ID, 401, "Unauthorized", nil))
		}
	case "admin":
		if permissionGroup == "admin" {
			go conn.WriteJSON(rpc.CallWithContext(ctx, req.ID, req.Method, req.Params))
		} else {
			conn.WriteJSON(rpc.ErrorResponse(req.ID, 401, "Unauthorized", nil))
		}
	default:
		conn.WriteJSON(rpc.ErrorResponse(req.ID, 401, "Unauthorized", nil))
	}
}

// registry holds method handlers keyed by "namespace:MethodName"
var (
	registryMu sync.RWMutex
	registry   = make(map[string]reflect.Value)
)

// Register 将以分组common注册
func Register(name string, cb rpc.Handler) error {
	return RegisterWithGroupAndMeta(name, "common", cb, &rpc.MethodMeta{
		Name:        name,
		Summary:     "This method does not provide a summary",
		Description: "This method does not provide a description",
	})
}

// RegisterWithGroup 将回调按分组注册。
// 传入函数:       Register("ns", someFunc) -> 注册 ns:SomeFunc
// group 为空时使用默认分组 "common"。
func RegisterWithGroupAndMeta(name, group string, cb rpc.Handler, meta *rpc.MethodMeta) error {
	return rpc.RegisterWithMeta(group+":"+name, cb, meta)
}

// GetRegisteredKeys 返回已注册的方法 key 列表 (调试用)。
func GetRegisteredKeys() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}
