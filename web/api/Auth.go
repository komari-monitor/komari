package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

const (
	RoleAdmin  = "admin"
	RoleClient = "client"
	RoleGuest  = "guest"
)

// IdentityMiddleware 统一身份识别中间件，在路由栈最外层运行。
// 负责识别当前请求者身份（Admin / Client / Guest），并写入 Context。
// 身份识别统一委托给 IdentifyPrincipal;同时保留旧的 c.Set 键(role/uuid/
// api_key/session/client_uuid)以兼容现有 handler 与中间件。
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := IdentifyPrincipal(c)
		SetPrincipal(c, p)

		// 写入兼容字段。
		c.Set("role", p.PrimaryRole())
		switch p.Type {
		case rpc.PrincipalAPIKey:
			// 旧逻辑:API Key 时记录裸 key 与固定占位 uuid。
			apiKey := c.GetHeader("Authorization")
			c.Set("api_key", apiKey[len("Bearer "):])
			c.Set("uuid", "00000000-0000-0000-0000-000000000000")
		case rpc.PrincipalUser:
			if session, err := c.Cookie("session_token"); err == nil && session != "" {
				c.Set("session", session)
				accounts.UpdateLatest(session, c.Request.UserAgent(), c.ClientIP())
			}
			c.Set("uuid", p.UserUUID)
		case rpc.PrincipalAgent:
			c.Set("client_uuid", p.ClientUUID)
		}

		c.Next()
	}
}

// RequireRole 声明式权限校验中间件，仅允许指定角色通过。
func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		current := GetRole(c)
		for _, role := range allowedRoles {
			if current == role {
				c.Next()
				return
			}
		}
		RespondError(c, http.StatusUnauthorized, "Unauthorized.")
		c.Abort()
	}
}

// GetRole 获取当前请求的角色
func GetRole(c *gin.Context) string {
	if role := c.GetString("role"); role != "" {
		return role
	}
	return RoleGuest
}

// --- 私有站点访问控制 ---

var publicPaths = []string{
	"/ping",
	"/api/public",
	"/api/login",
	"/api/me",
	"/api/oauth",
	"/api/oauth_callback",
	"/api/version",
	"/api/recent",
	"/api/admin",    // 由 RequireRole 处理
	"/api/clients/", // 由 RequireRole 处理
	"/api/preview/", // 预览令牌校验后放行
}

// PrivateSiteMiddleware 私有站点访问控制。
// 依赖 IdentityMiddleware 已设置的 role，对未认证的访客在私有站点模式下进行拦截。
func PrivateSiteMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 已认证用户直接放行
		if GetRole(c) != RoleGuest {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// 公开路径直接放行
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		// 非 API 路径直接放行（静态资源等）
		if !strings.HasPrefix(path, "/api") {
			c.Next()
			return
		}

		// 非私有站点直接放行
		privateSite, err := config.GetAs[bool](config.PrivateSiteKey, false)
		if err != nil {
			RespondError(c, http.StatusInternalServerError, "Failed to get configuration.")
			c.Abort()
			return
		}
		if !privateSite {
			c.Next()
			return
		}

		// 临时访问许可
		if hasTempAccess(c) {
			c.Next()
			return
		}

		RespondError(c, http.StatusUnauthorized, "Private site is enabled, please login first.")
		c.Abort()
	}
}

func hasTempAccess(c *gin.Context) bool {
	tempKey, err := c.Cookie("temp_key")
	if err != nil {
		return false
	}
	allowKey, err := config.GetAs[string]("tempory_share_token", "")
	if err != nil || allowKey == "" || tempKey != allowKey {
		return false
	}
	expireAt, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
	return err == nil && expireAt >= time.Now().Unix()
}

func extractClientToken(c *gin.Context) string {
	if token := c.Query("token"); token != "" {
		return token
	}
	// rpc2 约定:agent 经 ?Authorization=<token> 传入 client token。
	if token := c.Query("Authorization"); token != "" {
		return token
	}

	if c.Request.Method != http.MethodGet {
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return ""
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var body struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(bodyBytes, &body) == nil {
			return body.Token
		}
	}
	return ""
}

func isApiKeyValid(apiKey string) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil || len(apiKeyConfig) < 12 {
		return false
	}
	return apiKey == "Bearer "+apiKeyConfig
}
