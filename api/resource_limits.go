package api

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// ResourceLimits 资源限制配置
type ResourceLimits struct {
	MaxTerminalSessions int32 // 最大终端会话数
	MaxWebSocketConns   int32 // 最大 WebSocket 连接数
	MaxRecordsCacheSize int   // 最大记录缓存大小（客户端数量）
}

// DefaultResourceLimits 返回默认资源限制
func DefaultResourceLimits() *ResourceLimits {
	return &ResourceLimits{
		MaxTerminalSessions: 100,
		MaxWebSocketConns:   500,
		MaxRecordsCacheSize: 10000,
	}
}

// LoadResourceLimitsFromEnv 从环境变量加载资源限制
func LoadResourceLimitsFromEnv() *ResourceLimits {
	limits := DefaultResourceLimits()

	if v := os.Getenv("KOMARI_MAX_TERMINAL_SESSIONS"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil && i > 0 {
			limits.MaxTerminalSessions = int32(i)
		}
	}

	if v := os.Getenv("KOMARI_MAX_WEBSOCKET_CONNS"); v != "" {
		if i, err := strconv.ParseInt(v, 10, 32); err == nil && i > 0 {
			limits.MaxWebSocketConns = int32(i)
		}
	}

	if v := os.Getenv("KOMARI_MAX_RECORDS_CACHE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			limits.MaxRecordsCacheSize = i
		}
	}

	return limits
}

// ResourceManager 资源管理器
type ResourceManager struct {
	limits             *ResourceLimits
	terminalSessions   int32 // 当前终端会话数
	webSocketConns     int32 // 当前 WebSocket 连接数
	terminalSessionsMu sync.RWMutex
}

// globalResourceManager 全局资源管理器实例
var globalResourceManager = &ResourceManager{
	limits: LoadResourceLimitsFromEnv(),
}

// GetResourceManager 获取全局资源管理器
func GetResourceManager() *ResourceManager {
	return globalResourceManager
}

// GetLimits 获取资源限制配置
func (rm *ResourceManager) GetLimits() *ResourceLimits {
	return rm.limits
}

// TryAcquireTerminalSession 尝试获取终端会话资源
// 返回 true 表示成功，false 表示已达到限制
func (rm *ResourceManager) TryAcquireTerminalSession() bool {
	current := atomic.LoadInt32(&rm.terminalSessions)
	if current >= rm.limits.MaxTerminalSessions {
		log.Printf("[ResourceLimit] Terminal session limit reached: %d/%d", current, rm.limits.MaxTerminalSessions)
		return false
	}
	atomic.AddInt32(&rm.terminalSessions, 1)
	return true
}

// ReleaseTerminalSession 释放终端会话资源
func (rm *ResourceManager) ReleaseTerminalSession() {
	atomic.AddInt32(&rm.terminalSessions, -1)
}

// GetTerminalSessionCount 获取当前终端会话数
func (rm *ResourceManager) GetTerminalSessionCount() int32 {
	return atomic.LoadInt32(&rm.terminalSessions)
}

// TryAcquireWebSocketConn 尝试获取 WebSocket 连接资源
// 返回 true 表示成功，false 表示已达到限制
func (rm *ResourceManager) TryAcquireWebSocketConn() bool {
	current := atomic.LoadInt32(&rm.webSocketConns)
	if current >= rm.limits.MaxWebSocketConns {
		log.Printf("[ResourceLimit] WebSocket connection limit reached: %d/%d", current, rm.limits.MaxWebSocketConns)
		return false
	}
	atomic.AddInt32(&rm.webSocketConns, 1)
	return true
}

// ReleaseWebSocketConn 释放 WebSocket 连接资源
func (rm *ResourceManager) ReleaseWebSocketConn() {
	atomic.AddInt32(&rm.webSocketConns, -1)
}

// GetWebSocketConnCount 获取当前 WebSocket 连接数
func (rm *ResourceManager) GetWebSocketConnCount() int32 {
	return atomic.LoadInt32(&rm.webSocketConns)
}

// ResourceLimitMiddleware 创建资源限制中间件
func ResourceLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查是否是 WebSocket 升级请求
		if IsWebSocketUpgrade(c) {
			rm := GetResourceManager()
			if !rm.TryAcquireWebSocketConn() {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"status": "error",
					"error":  "server busy, please try again later",
				})
				return
			}

			// 设置资源释放钩子
			c.Set("ws_resource_acquired", true)
		}
		c.Next()
	}
}

// IsWebSocketUpgrade 检查是否是 WebSocket 升级请求
func IsWebSocketUpgrade(c *gin.Context) bool {
	return c.GetHeader("Upgrade") == "websocket" ||
		c.GetHeader("Connection") == "Upgrade" ||
		c.Query("ws") == "1"
}

// ReleaseWebSocketResource 释放 WebSocket 资源（用于连接关闭时）
func ReleaseWebSocketResource(c *gin.Context) {
	if acquired, exists := c.Get("ws_resource_acquired"); exists && acquired.(bool) {
		GetResourceManager().ReleaseWebSocketConn()
	}
}
