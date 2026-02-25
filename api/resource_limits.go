package api

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/config"
)

// 配置键名
const (
	ConfigKeyMaxTerminalSessions = "max_terminal_sessions"
	ConfigKeyMaxWebSocketConns   = "max_websocket_conns"
	ConfigKeyMaxRecordsCacheSize = "max_records_cache_size"
)

// ResourceLimits 资源限制配置
type ResourceLimits struct {
	MaxTerminalSessions int32 `json:"max_terminal_sessions"`  // 最大终端会话数
	MaxWebSocketConns   int32 `json:"max_websocket_conns"`    // 最大 WebSocket 连接数
	MaxRecordsCacheSize int   `json:"max_records_cache_size"` // 最大记录缓存大小
}

// DefaultResourceLimits 返回默认资源限制
func DefaultResourceLimits() *ResourceLimits {
	return &ResourceLimits{
		MaxTerminalSessions: 100,
		MaxWebSocketConns:   500,
		MaxRecordsCacheSize: 10000,
	}
}

// LoadResourceLimitsFromFlags 从命令行参数加载资源限制（仅作为初始值）
func LoadResourceLimitsFromFlags() *ResourceLimits {
	limits := DefaultResourceLimits()

	if flags.MaxTerminalSessions > 0 {
		limits.MaxTerminalSessions = int32(flags.MaxTerminalSessions)
	}
	if flags.MaxWebSocketConns > 0 {
		limits.MaxWebSocketConns = int32(flags.MaxWebSocketConns)
	}
	if flags.MaxRecordsCacheSize > 0 {
		limits.MaxRecordsCacheSize = flags.MaxRecordsCacheSize
	}

	return limits
}

// LoadResourceLimitsFromDB 从数据库加载资源限制配置
func LoadResourceLimitsFromDB() *ResourceLimits {
	limits := DefaultResourceLimits()

	if val, err := config.GetAs[int32](ConfigKeyMaxTerminalSessions, limits.MaxTerminalSessions); err == nil {
		limits.MaxTerminalSessions = val
	}
	if val, err := config.GetAs[int32](ConfigKeyMaxWebSocketConns, limits.MaxWebSocketConns); err == nil {
		limits.MaxWebSocketConns = val
	}
	if val, err := config.GetAs[int](ConfigKeyMaxRecordsCacheSize, limits.MaxRecordsCacheSize); err == nil {
		limits.MaxRecordsCacheSize = val
	}

	return limits
}

// SaveToDB 保存资源限制配置到数据库
func (r *ResourceLimits) SaveToDB() error {
	if err := config.Set(ConfigKeyMaxTerminalSessions, r.MaxTerminalSessions); err != nil {
		return err
	}
	if err := config.Set(ConfigKeyMaxWebSocketConns, r.MaxWebSocketConns); err != nil {
		return err
	}
	if err := config.Set(ConfigKeyMaxRecordsCacheSize, r.MaxRecordsCacheSize); err != nil {
		return err
	}
	return nil
}

// ResourceManager 资源管理器
type ResourceManager struct {
	limits           atomic.Pointer[ResourceLimits]
	terminalSessions int32 // 当前终端会话数
	webSocketConns   int32 // 当前 WebSocket 连接数
}

// globalResourceManager 全局资源管理器实例
var globalResourceManager = &ResourceManager{}

// InitResourceManager 初始化资源管理器（在命令行参数解析后调用）
func InitResourceManager() {
	// 先尝试从数据库加载，如果数据库中没有配置则使用命令行参数
	limits := LoadResourceLimitsFromDB()

	// 如果数据库中没有配置，保存命令行参数的默认值到数据库
	flagLimits := LoadResourceLimitsFromFlags()
	if limits.MaxTerminalSessions == 100 && flags.MaxTerminalSessions > 0 {
		limits.MaxTerminalSessions = flagLimits.MaxTerminalSessions
	}
	if limits.MaxWebSocketConns == 500 && flags.MaxWebSocketConns > 0 {
		limits.MaxWebSocketConns = flagLimits.MaxWebSocketConns
	}
	if limits.MaxRecordsCacheSize == 10000 && flags.MaxRecordsCacheSize > 0 {
		limits.MaxRecordsCacheSize = flagLimits.MaxRecordsCacheSize
	}

	globalResourceManager = &ResourceManager{}
	globalResourceManager.limits.Store(limits)

	// 订阅配置变更事件
	config.Subscribe(func(event config.ConfigEvent) {
		if event.IsChanged(ConfigKeyMaxTerminalSessions) ||
			event.IsChanged(ConfigKeyMaxWebSocketConns) ||
			event.IsChanged(ConfigKeyMaxRecordsCacheSize) {
			globalResourceManager.ReloadFromDB()
		}
	})
}

// GetResourceManager 获取全局资源管理器
func GetResourceManager() *ResourceManager {
	return globalResourceManager
}

// GetLimits 获取资源限制配置
func (rm *ResourceManager) GetLimits() *ResourceLimits {
	return rm.limits.Load()
}

// ReloadFromDB 从数据库重新加载配置
func (rm *ResourceManager) ReloadFromDB() {
	newLimits := LoadResourceLimitsFromDB()
	rm.limits.Store(newLimits)
}

// TryAcquireTerminalSession 尝试获取终端会话资源
// 返回 true 表示成功，false 表示已达到限制
func (rm *ResourceManager) TryAcquireTerminalSession() bool {
	current := atomic.LoadInt32(&rm.terminalSessions)
	limits := rm.limits.Load()
	if current >= limits.MaxTerminalSessions {
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
	limits := rm.limits.Load()
	if current >= limits.MaxWebSocketConns {
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

			// 设置资源释放钩子，使用 defer 确保在请求结束时释放资源
			// 无论请求是正常完成还是因为 panic 异常退出，都会执行释放
			defer rm.ReleaseWebSocketConn()
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
