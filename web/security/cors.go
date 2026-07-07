package security

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/pkg/config"
)

// CorsController 保存 CORS 中间件的可热更新状态。
//
// 相比过去把 config.Subscribe 直接埋在中间件闭包里，这里把「当前配置」抽成显式对象：
//   - 需要热更新的状态集中在 controller 上，由外部（启动生命周期里的 reload 管理器）
//     统一调用 Update，避免订阅逻辑散落在中间件内部。
//   - Middleware() 只负责读取当前状态并执行 CORS 校验，不再自行订阅配置事件。
type CorsController struct {
	mu             sync.RWMutex
	enabled        bool
	allowedOrigins string
}

// NewCorsController 使用初始配置构造一个 CORS 控制器。
func NewCorsController(enabled bool, allowedOrigins string) *CorsController {
	return &CorsController{
		enabled:        enabled,
		allowedOrigins: allowedOrigins,
	}
}

// Update 根据配置事件刷新 CORS 相关状态。返回是否有字段发生变化。
func (ctrl *CorsController) Update(event config.ConfigEvent) bool {
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	changed := false
	if ok, t := config.IsChangedT[bool](event, config.CorsOriginCheckEnabledKey); ok {
		ctrl.enabled = t
		changed = true
	}
	if ok, t := config.IsChangedT[string](event, config.CorsAllowedOriginsKey); ok {
		ctrl.allowedOrigins = t
		changed = true
	}
	return changed
}

func (ctrl *CorsController) snapshot() (bool, string) {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.enabled, ctrl.allowedOrigins
}

// Middleware 返回读取当前控制器状态的 gin 中间件。
func (ctrl *CorsController) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAPIRequestPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		corsEnabled, corsAllowedOrigins := ctrl.snapshot()

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

// CorsMiddleware 保留原有签名：内部自建 controller 并自行订阅配置事件。
// 供测试以及不接入集中式 reload 管理器的场景使用。
func CorsMiddleware(initialEnabled bool, initialAllowedOrigins string) gin.HandlerFunc {
	ctrl := NewCorsController(initialEnabled, initialAllowedOrigins)
	config.Subscribe(func(event config.ConfigEvent) {
		ctrl.Update(event)
	})
	return ctrl.Middleware()
}

func isAPIRequestPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
