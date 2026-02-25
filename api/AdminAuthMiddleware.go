package api

import (
	"net/http"

	"github.com/komari-monitor/komari/database/accounts"

	"github.com/gin-gonic/gin"
)

func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// API key authentication
		if ValidateAPIKeyFromRequest(c) {
			c.Next()
			return
		}

		// session-based authentication
		session, err := c.Cookie("session_token")
		if err != nil {
			RespondError(c, http.StatusUnauthorized, "Unauthorized.")
			c.Abort()
			return
		}

		// Komari is a single user system
		uuid, err := accounts.GetSession(session)
		if err != nil {
			RespondError(c, http.StatusUnauthorized, "Unauthorized.")
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
