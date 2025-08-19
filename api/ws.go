package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/common"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/config"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/ws"
)

func GetClients(c *gin.Context) {
	// 升级到ws
	if !websocket.IsWebSocketUpgrade(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	cfg, _ := config.Get()
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if cfg.AllowCors {
				return true
			}
			return ws.CheckOrigin(r)
		},
	}
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket"})
		return
	}
	defer conn.Close()

	// 请求
	for {
		var resp struct {
			Online []string                  `json:"online"` // 已建立连接的客户端uuid列表
			Data   map[string]*common.Report `json:"data"`   // 最后上报的数据
		}
		resp.Online = []string{}
		_, data, err := conn.ReadMessage()
		if err != nil {
			//log.Println("Error reading message:", err)
			return
		}
		message := string(data)

		uuID := ""
		if message != "get" { // 非请求全部内容
			if strings.HasPrefix(message, "get ") {
				uuID = strings.TrimSpace(strings.TrimPrefix(message, "get "))
			} else {
				conn.WriteJSON(gin.H{"status": "error", "error": "Invalid message"})
				continue
			}
		}

		// 登录状态检查
		isLogin := false
		session, _ := c.Cookie("session_token")
		_, err = accounts.GetUserBySession(session)
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
		}
		// 已建立连接的客户端uuid列表
		for key := range ws.GetConnectedClients() {
			if uuID != "" { // 请求特定服务器信息
				if !(!isLogin && hiddenMap[key]) && key == uuID { // 未登录且 Hidden 或 不符合请求的特定服务器 -> 跳过
					resp.Online = append(resp.Online, key)
				}
			} else {
				if !(!isLogin && hiddenMap[key]) { // 未登录且 Hidden -> 跳过
					resp.Online = append(resp.Online, key)
				}
			}
		}
		// 清除UUID，简化报告单
		resp.Data = ws.GetLatestReport()
		if !isLogin { // 未登录过滤 Hidden 的节点报告
			for uuid := range resp.Data {
				if hiddenMap[uuid] {
					delete(resp.Data, uuid)
				}
			}
		}

		if uuID != "" { // 过滤不符合请求的特定服务器
			for key := range resp.Data {
				if key != uuID {
					delete(resp.Data, key)
				}
			}
		}

		for _, report := range resp.Data { // 不暴露 uuid
			report.UUID = ""
			if report.CPU.Usage == 0 {
				report.CPU.Usage = 0.01
			}
		}

		err = conn.WriteJSON(gin.H{"status": "success", "data": resp})
		if err != nil {
			return
		}
	}

}
