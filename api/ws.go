package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/common"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/accounts"
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
	AllowCors, _ := config.GetAs[bool](config.AllowCorsKey, false)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if AllowCors {
				return true
			}
			return ws.CheckOrigin(r)
		},
	}
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Failed to upgrade to WebSocket." + err.Error()})
		return
	}
	defer conn.Close()

	// 初始化用户信息
	var (
		isLogin    = false
		hiddenMap  = map[string]bool{}
		session, _ = c.Cookie("session_token")
	)

	// 登录状态检查
	_, err = accounts.GetUserBySession(session)
	if err == nil {
		isLogin = true
	}

	// 仅在未登录时需要 Hidden 信息做过滤
	if !isLogin {
		var hiddenClients []models.Client
		db := dbcore.GetDBInstance()
		_ = db.Select("uuid").Where("hidden = ?", true).Find(&hiddenClients).Error
		for _, cli := range hiddenClients {
			hiddenMap[cli.UUID] = true
		}
	}

	// 请求
	for {
		var resp struct {
			Online []string                 `json:"online"` // 已建立连接的客户端uuid列表
			Data   map[string]common.Report `json:"data"`   // 最后上报的数据
		}

		resp.Online = []string{}
		resp.Data = map[string]common.Report{}

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

		// 在线客户端uuid列表（WebSocket 与非 WebSocket）
		for _, key := range ws.GetAllOnlineUUIDs() {
			if !isLogin && hiddenMap[key] {
				continue
			}
			if uuID != "" && key != uuID {
				continue
			}
			resp.Online = append(resp.Online, key)
		}

		//过往节点数据信息
		for key, report := range ws.GetLatestReport() {
			if !isLogin && hiddenMap[key] {
				continue
			}
			if uuID != "" && key != uuID {
				continue
			}

			report.UUID = "" // 不暴露 uuid
			if report.CPU.Usage == 0 {
				report.CPU.Usage = 0.01
			}
			resp.Data[key] = *report
		}

		err = conn.WriteJSON(gin.H{"status": "success", "data": resp})
		if err != nil {
			return
		}
	}
}
