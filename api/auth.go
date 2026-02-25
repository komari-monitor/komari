package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/config"
)

const (
	// BearerPrefix 是 API Key 的 Bearer 前缀
	BearerPrefix = "Bearer "
)

// ValidateAPIKey 验证 API Key 是否有效
// 返回 true 表示验证通过，false 表示验证失败
// API Key 格式: "Bearer {key}"
func ValidateAPIKey(apiKey string) bool {
	if apiKey == "" {
		return false
	}

	// 从配置中获取 API Key
	configApiKey, err := config.GetAs[string](config.ApiKeyKey, "")
	if err != nil {
		return false
	}

	// API Key 配置为空或长度不足时不验证
	if configApiKey == "" || len(configApiKey) < 12 {
		return false
	}

	// 验证格式为 "Bearer {key}"
	return apiKey == BearerPrefix+configApiKey
}

// ValidateAPIKeyFromRequest 从请求中验证 API Key
// 如果验证通过，设置 api_key 到上下文并返回 true
func ValidateAPIKeyFromRequest(c *gin.Context) bool {
	apiKey := c.GetHeader("Authorization")
	if ValidateAPIKey(apiKey) {
		c.Set("api_key", apiKey)
		return true
	}
	return false
}

// RequireAPIKey 创建一个仅验证 API Key 的中间件
func RequireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !ValidateAPIKeyFromRequest(c) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"status": "error",
				"error":  "invalid or missing API key",
			})
			return
		}
		c.Next()
	}
}
