package api_v1

import (
	"net/http"
	"strings"

	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/accounts"

	"github.com/gin-gonic/gin"
)

var (
	requireAuthPaths = []string{
		// v1 API at 2025-12-06
		// "/api/rpc2",
		// "/ping",
		// "/api/login",
		// "/api/me",
		"/api/clients",
		"/api/nodes",
		// "/api/public",
		// "/api/oauth",
		// "/api/oauth_callback",
		// "/api/logout",
		// "/api/version",
		"/api/recent/",
		"/api/records/",
		"/api/task/",
		// "/api/clients/register",
		// "/api/clients/report",
		// "/api/clients/uploadBasicInfo",
		// "/api/clients/terminal",
		// "/api/clients/task/result",
		// "/api/admin/download/backup",
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
		path := c.Request.URL.Path
		// 如果不是 /api 开头的路径，直接放行
		if !strings.HasPrefix(path, "/api") {
			c.Next()
			return
		}

		// /api/clients/* 由 TokenAuthMiddleware 处理
		// /api/rpc2 由 api_rpc 处理
		// /api/admin/* 由 AdminAuthMiddleware 处理
		if strings.HasPrefix(path, "/api/clients/") ||
			strings.HasPrefix(path, "/api/admin") ||
			strings.HasPrefix(path, "/api/rpc2") {
			c.Next()
			return
		}

		require := false
		for _, p := range requireAuthPaths {
			if strings.HasPrefix(path, p) {
				require = true
				break
			}
		}

		if !require {
			c.Next()
			return
		}
		conf, err := conf.GetWithV1Format()
		if err != nil {
			resp.RespondError(c, http.StatusInternalServerError, "Failed to get configuration.")
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
			resp.RespondError(c, http.StatusUnauthorized, "Private site is enabled, please login first.")
			c.Abort()
			return
		}
		_, err = accounts.GetSession(session)
		if err != nil {
			resp.RespondError(c, http.StatusUnauthorized, "Private site is enabled, please login first.")
			c.Abort()
			return
		}

		c.Next()
	}
}
