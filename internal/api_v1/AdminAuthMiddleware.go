package api_v1

import (
	"net/http"

	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/accounts"

	"github.com/gin-gonic/gin"
)

func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// API key authentication
		apiKey := c.GetHeader("Authorization")
		if isApiKeyValid(apiKey) {
			c.Set("api_key", apiKey)
			c.Next()
			return
		}
		// session-based authentication
		session, err := c.Cookie("session_token")
		if err != nil {
			resp.RespondError(c, http.StatusUnauthorized, "Unauthorized.")
			c.Abort()
			return
		}

		// Komari is a single user system
		uuid, err := accounts.GetSession(session)
		if err != nil {
			resp.RespondError(c, http.StatusUnauthorized, "Unauthorized.")
			c.Abort()
			return
		}
		accounts.UpdateLatest(session, c.Request.UserAgent(), c.ClientIP())
		// 将 session 和 用户 UUID 传递到后续处理器
		c.Set("session", session)
		c.Set("uuid", uuid)

		c.Next()
	}
}
