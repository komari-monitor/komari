package api_v1

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database"
)

func GetPublicSettings(c *gin.Context) {
	p, e := database.GetPublicInfo()
	if e != nil {
		resp.RespondError(c, 500, e.Error())
		return
	}
	resp.RespondSuccess(c, p)
}
