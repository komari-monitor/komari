package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/clients"
	"gorm.io/gorm"
)

const (
	RoleAdmin  = "admin"
	RoleClient = "client"
	RoleGuest  = "guest"
)

// IdentityMiddleware 统一身份识别中间件，在路由栈最外层运行。
// 负责识别当前请求者身份（Admin / Client / Guest），并写入 Context。
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. API Key 认证
		apiKey := c.GetHeader("Authorization")
		if isApiKeyValid(apiKey) {
			c.Set("role", RoleAdmin)
			c.Set("api_key", apiKey[7:])
			c.Set("uuid", "00000000-0000-0000-0000-000000000000") // API Key
			c.Next()
			return
		}

		// 2. Session 认证
		session, err := c.Cookie("session_token")
		if err == nil && session != "" {
			uuid, err := accounts.GetSession(session)
			if err == nil {
				c.Set("role", RoleAdmin)
				c.Set("session", session)
				c.Set("uuid", uuid)
				accounts.UpdateLatest(session, c.Request.UserAgent(), c.ClientIP())
				c.Next()
				return
			}
		}

		// 3. Client Token 认证
		token := extractClientToken(c)
		if token != "" {
			uuid, err := checkTokenAndGetUUID(token)
			if err == nil && uuid != "" {
				c.Set("role", RoleClient)
				c.Set("client_uuid", uuid)
				c.Next()
				return
			}
		}

		// 4. 未识别身份，标记为访客
		c.Set("role", RoleGuest)
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
	role, exists := c.Get("role")
	if !exists {
		return RoleGuest
	}
	if s, ok := role.(string); ok {
		return s
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
	expireAt, err := config.GetAs[int64]("tempory_share_token_expire_at", 0)
	if err != nil {
		return false
	}
	allowKey, err := config.GetAs[string]("tempory_share_token", "")
	if err != nil {
		return false
	}
	if allowKey == "" || tempKey != allowKey {
		return false
	}
	return expireAt >= time.Now().Unix()
}

func extractClientToken(c *gin.Context) string {
	token := c.Query("token")
	if token != "" {
		return token
	}

	if c.Request.Method != http.MethodGet {
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return ""
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var bodyMap map[string]interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
				if tokenVal, exists := bodyMap["token"]; exists {
					if str, ok := tokenVal.(string); ok && str != "" {
						return str
					}
				}
			}
		}
	}

	return ""
}

func checkTokenAndGetUUID(token string) (string, error) {
	uuid, err := clients.GetClientUUIDByToken(token)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return uuid, nil
}

func isApiKeyValid(apiKey string) bool {
	apiKeyConfig, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil {
		return false
	}

	if apiKeyConfig == "" || len(apiKeyConfig) < 12 {
		return false
	}
	return apiKey == "Bearer "+apiKeyConfig
}
