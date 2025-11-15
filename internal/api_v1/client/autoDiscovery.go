package client

import (
	"github.com/gin-gonic/gin"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/clients"
	"github.com/komari-monitor/komari/pkg/utils"
)

func RegisterClient(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		api.RespondError(c, 403, "Invalid AutoDiscovery Key")
		return
	}
	cfg, err := conf.GetWithV1Format()
	if err != nil {
		api.RespondError(c, 500, "Failed to get configuration: "+err.Error())
		return
	}

	if cfg.AutoDiscoveryKey == "" ||
		len(cfg.AutoDiscoveryKey) < 12 ||
		"Bearer "+cfg.AutoDiscoveryKey != auth {

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
