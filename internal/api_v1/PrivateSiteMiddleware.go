package api_v1

import (
	"net/http"

	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/config"

	"github.com/gin-gonic/gin"
)

var (
	publicPaths = []string{
		"/ping",
		"/api/public",
		"/api/login",
		"/api/me",
		"/api/oauth",
		"/api/oauth_callback",
		"/api/version",
		"/api/recent",
		"/api/admin",    // 由AdminAuthMiddleware处理
		"/api/clients/", // 由TokenAuthMiddleware处理
	}
)

func PrivateSiteMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// API key authentication
		apiKey := c.GetHeader("Authorization")
		if isApiKeyValid(apiKey) {
			c.Set("api_key", apiKey)
			c.Next()
			return
		}
		// 如果是公开的路径，直接放行
		for _, path := range publicPaths {
			if len(c.Request.URL.Path) >= len(path) && c.Request.URL.Path[:len(path)] == path {
				c.Next()
				return
			}
		}
		// 如果不是 /api，直接放行
		if len(c.Request.URL.Path) < 4 || c.Request.URL.Path[:4] != "/api" {
			c.Next()
			return
		}
		conf, err := config.Get()
		if err != nil {
			RespondError(c, http.StatusInternalServerError, "Failed to get configuration.")
			c.Abort()
			return
		}
		// 验证私有, 如果不是私有站点，直接放行
		if !conf.PrivateSite {
			c.Next()
			return
		}
		// 如果是私有站点，检查是否有 session
		session, err := c.Cookie("session_token")
		if err != nil {
			RespondError(c, http.StatusUnauthorized, "Private site is enabled, please login first.")
			c.Abort()
			return
		}
		_, err = accounts.GetSession(session)
		if err != nil {
			RespondError(c, http.StatusUnauthorized, "Unauthorized.")
			c.Abort()
			return
		}

		c.Next()
	}
}
