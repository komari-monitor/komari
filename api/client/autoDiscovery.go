package client

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/utils"
)

func RegisterClient(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		api.RespondError(c, 403, "Invalid AutoDiscovery Key")
		return
	}
	AutoDiscoveryKey, err := config.GetAs[string](config.AutoDiscoveryKeyKey, "")
	if err != nil {
		api.RespondError(c, 500, "Failed to get AutoDiscovery Key: "+err.Error())
		return
	}
	if AutoDiscoveryKey == "" ||
		len(AutoDiscoveryKey) < 12 ||
		"Bearer "+AutoDiscoveryKey != auth {

		api.RespondError(c, 403, "Invalid AutoDiscovery Key")
		return
	}
	name := c.Query("name")
	if name == "" {
		name = utils.GenerateRandomString(8)
	}
	name = "Auto-" + name
	uuid, token, err := clients.CreateClientWithName(name)
	if err != nil {
		api.RespondError(c, 500, "Failed to create client: "+err.Error())
		return
	}
	api.RespondSuccess(c, gin.H{"uuid": uuid, "token": token})
}
