package public

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/clients"
)

func GetNodesInformation(c *gin.Context) {
	clientList, err := clients.GetAllClientBasicInfo()
	if err != nil {
		api.RespondError(c, 500, "Failed to retrieve client information: "+err.Error())
		return
	}
	isLogin := false
	session, _ := c.Cookie("session_token")
	_, err = accounts.GetUserBySession(session)
	if err == nil {
		isLogin = true
	}

	// 过滤掉 Hidden 的客户端，并清理需要隐藏的字段
	j := 0
	for i := 0; i < len(clientList); i++ {
		if clientList[i].Hidden && !isLogin { // 不返回 Hidden 客户端
			continue
		}
		clientList[i].IPv4 = ""
		clientList[i].IPv6 = ""
		clientList[i].Remark = "" // 私有备注不展示
		clientList[i].Version = ""
		clientList[i].Token = ""
		clientList[j] = clientList[i]
		j++
	}
	clientList = clientList[:j]

	api.RespondSuccess(c, clientList)
}
