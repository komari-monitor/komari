package public

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

func GetClientRecentRecords(c *gin.Context) {
	uuid := c.Param("uuid")

	if uuid == "" {
		api.RespondError(c, 400, "UUID is required")
		return
	}

	// 登录状态检查
	isLogin := false
	session, _ := c.Cookie("session_token")
	_, err := accounts.GetUserBySession(session)
	if err == nil {
		isLogin = true
	}

	// 仅在未登录时需要 Hidden 信息做过滤
	hiddenMap := map[string]bool{}
	if !isLogin {
		var hiddenClients []models.Client
		db := dbcore.GetDBInstance()
		_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
		for _, cli := range hiddenClients {
			hiddenMap[cli.UUID] = true
		}

		if hiddenMap[uuid] {
			api.RespondError(c, 400, "UUID is required") //防止未登录用户获取隐藏客户端数据
			return
		}
	}

	records, _ := api.Records.Get(uuid)
	api.RespondSuccess(c, records)
}
