package logger

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// GinLogger records completed requests. Query values are intentionally omitted
// because they commonly contain credentials or other sensitive data.
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		message := fmt.Sprintf("%d %s %s | %s | %s",
			c.Writer.Status(), c.Request.Method, c.Request.URL.Path, c.ClientIP(), time.Since(start))
		if len(c.Errors) > 0 {
			message += " | " + c.Errors.String()
		}

		switch {
		case c.Writer.Status() >= 500:
			Error("gin", message)
		case c.Writer.Status() >= 400:
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
