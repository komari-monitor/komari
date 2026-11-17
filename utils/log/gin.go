package logger

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// isAPIRequest checks if the request path targets an API or health check endpoint.
func isAPIRequest(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/api" || path == "/ping"
}

// GinLogger records completed requests. Query values are intentionally omitted
// because they commonly contain credentials or other sensitive data.
// 2xx and 304 successful responses for non-API requests (such as static assets and
// frontend pages) are suppressed; API requests and non-2xx responses (e.g. 4xx, 5xx)
// are always logged.
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		path := c.Request.URL.Path

		// 仅显示 API 请求和非 2xx/304 响应（如 4xx、5xx 异常），静默静态资源及前端页面正常请求
		isSuccess := (status >= 200 && status < 300) || status == 304
		if isSuccess && len(c.Errors) == 0 && !isAPIRequest(path) {
			return
		}

		message := fmt.Sprintf("%d %s %s | %s | %s",
			status, c.Request.Method, path, c.ClientIP(), time.Since(start))
		if len(c.Errors) > 0 {
			message += " | " + c.Errors.String()
		}

		switch {
		case status >= 500:
			Error("gin", message)
		case status >= 400:
			Warn("gin", message)
		default:
			Info("gin", message)
		}
	}
}

func GinRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				Error("http", "panic recovered",
					"error", fmt.Sprint(err),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
				)
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	}
}
