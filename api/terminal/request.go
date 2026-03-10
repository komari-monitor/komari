package terminal

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/ws"
)

func RequestTerminal(c *gin.Context) {
	uuid := c.Param("uuid")
	user_uuid, _ := c.Get("uuid")
	_, err := clients.GetClientByUUID(uuid)
	if err != nil {
		c.JSON(400, gin.H{
			"status":  "error",
			"message": "Client not found",
		})
		return
	}
	// 建立ws
	if !websocket.IsWebSocketUpgrade(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Require WebSocket upgrade"})
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: ws.CheckOrigin,
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	// 新建一个终端连接
	id := utils.GenerateRandomString(32)
	session := &TerminalSession{
		UserUUID:    user_uuid.(string),
		UUID:        uuid,
		Browser:     conn,
		Agent:       nil,
		RequesterIp: c.ClientIP(),
	}

	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = session
	TerminalSessionsMutex.Unlock()
	conn.SetCloseHandler(func(code int, text string) error {
		log.Println("Terminal connection closed:", code, text)
		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()
		// 通知 Agent 关闭终端连接
		if session.Agent != nil {
			session.Agent.Close()
		}
		return nil
	})

	if ws.GetConnectedClients()[uuid] == nil {
		conn.WriteMessage(1, []byte("Client offline!\n被控端离线!\n"))
		conn.Close()
		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()
		return
	}
	err = ws.GetConnectedClients()[uuid].WriteJSON(gin.H{
		"message":    "terminal",
		"request_id": id,
	})
	if err != nil {
		conn.Close()
		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()
		return
	}
	conn.WriteMessage(1, []byte("等待被控端连接 waiting for agent...\n"))
	// 如果没有连接上，则关闭连接
	time.AfterFunc(30*time.Second, func() {
		TerminalSessionsMutex.Lock()
		if session.Agent == nil {
			if session.Browser != nil {
				session.Browser.WriteMessage(1, []byte("被控端连接超时 timeout\n"))
				session.Browser.Close()
			}
			conn.Close()
			delete(TerminalSessions, id)
		}
		TerminalSessionsMutex.Unlock()
	})
	//auditlog.Log(c.ClientIP(), user_uuid.(string), "request, terminal id:"+id+",client:"+session.UUID, "terminal")
}
