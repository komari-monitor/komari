package security

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/config"
)

func CorsMiddleware(initialEnabled bool, initialAllowedOrigins string) gin.HandlerFunc {
	var mu sync.RWMutex
	enabled := initialEnabled
	allowedOrigins := initialAllowedOrigins

	config.Subscribe(func(event config.ConfigEvent) {
		mu.Lock()
		defer mu.Unlock()
		if ok, t := config.IsChangedT[bool](event, config.CorsOriginCheckEnabledKey); ok {
			enabled = t
		}
		if ok, t := config.IsChangedT[string](event, config.CorsAllowedOriginsKey); ok {
			allowedOrigins = t
		}
	})

	return func(c *gin.Context) {
		if !isAPIRequestPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		mu.RLock()
		corsEnabled := enabled
		corsAllowedOrigins := allowedOrigins
		mu.RUnlock()

		if !corsEnabled {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		allowOrigin := ""
		if origin != "" && (IsAPIKeyRequest(c.Request) ||
			OriginMatchesHost(origin, c.Request.Host) ||
			OriginInAllowlist(origin, corsAllowedOrigins)) {
			allowOrigin = origin
		}

		authorizationPreflight := origin != "" && allowOrigin == "" && IsAuthorizationPreflight(c.Request)
		if authorizationPreflight {
			allowOrigin = origin
		}

		if allowOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowOrigin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, X-CSRF-Token, X-Requested-With, Set-Cookie, X-2FA-Code, X-Two-Factor-Code")
			c.Header("Access-Control-Expose-Headers", "Content-Length, Authorization, Set-Cookie")
			if !authorizationPreflight {
				c.Header("Access-Control-Allow-Credentials", "true")
			}
			c.Header("Access-Control-Max-Age", "43200") // 12 hours
		}

		if c.Request.Method == http.MethodOptions {
			if allowOrigin != "" {
				c.AbortWithStatus(http.StatusNoContent)
			} else {
				c.AbortWithStatus(http.StatusForbidden)
			}
			return
		}

		if origin != "" && allowOrigin == "" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}

func isAPIRequestPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
