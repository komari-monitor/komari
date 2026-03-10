package public

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/utils"
)

func GetVersion(c *gin.Context) {
	api.RespondSuccess(c, gin.H{
		"version": utils.CurrentVersion,
		"hash":    utils.VersionHash,
	})
}
