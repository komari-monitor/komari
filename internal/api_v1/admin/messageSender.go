package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/messageSender"
	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

func GetMessageSenderProvider(c *gin.Context) {
	provider := c.Query("provider")
	if provider != "" {
		// 如果指定了provider，返回单个提供者的配置
		config, err := database.GetMessageSenderConfigByName(provider)
		if err != nil {
			resp.RespondError(c, 404, "Provider not found: "+err.Error())
			return
		}
		resp.RespondSuccess(c, config)
		return
	}
	// 否则返回所有提供者的配置项模板
	providers := factory.GetSenderConfigs()
	if len(providers) == 0 {
		resp.RespondError(c, 404, "No message sender providers found")
		return
	}
	resp.RespondSuccess(c, providers)
}

func SetMessageSenderProvider(c *gin.Context) {
	var senderConfig models.MessageSenderProvider
	if err := c.ShouldBindJSON(&senderConfig); err != nil {
		resp.RespondError(c, 400, "Invalid configuration: "+err.Error())
		return
	}
	if senderConfig.Name == "" {
		resp.RespondError(c, 400, "Provider name is required")
		return
	}
	_, exists := factory.GetConstructor(senderConfig.Name)
	if !exists {
		resp.RespondError(c, 404, "Provider not found: "+senderConfig.Name)
		return
	}
	if err := database.SaveMessageSenderConfig(&senderConfig); err != nil {
		resp.RespondError(c, 500, "Failed to save message sender provider configuration: "+err.Error())
		return
	}
	cfg, _ := conf.GetWithV1Format()
	// 正在使用，重载
	if cfg.NotificationMethod == senderConfig.Name {
		err := messageSender.LoadProvider(senderConfig.Name, senderConfig.Addition)
		if err != nil {
			resp.RespondError(c, 500, "Failed to load message sender provider: "+err.Error())
			return
		}
	}
	resp.RespondSuccess(c, gin.H{"message": "Message sender provider set successfully"})
}
