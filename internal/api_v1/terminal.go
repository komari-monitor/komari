package api_v1

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/clients"
	"github.com/komari-monitor/komari/internal/ws"
	"github.com/komari-monitor/komari/pkg/utils"
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
		conn.WriteMessage(1, []byte("Client offline!\n被控端离线!"))
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
	conn.WriteMessage(1, []byte("等待被控端连接 waiting for agent..."))
	// 如果没有连接上，则关闭连接
	time.AfterFunc(30*time.Second, func() {
		TerminalSessionsMutex.Lock()
		if session.Agent == nil {
			if session.Browser != nil {
				session.Browser.WriteMessage(1, []byte("被控端连接超时 timeout"))
				session.Browser.Close()
			}
			conn.Close()
			delete(TerminalSessions, id)
		}
		TerminalSessionsMutex.Unlock()
	})
	//auditlog.Log(c.ClientIP(), user_uuid.(string), "request, terminal id:"+id+",client:"+session.UUID, "terminal")
}

func ForwardTerminal(id string) {
	session, exists := TerminalSessions[id]

	if !exists || session == nil || session.Agent == nil || session.Browser == nil {
		return
	}
	auditlog.Log(session.RequesterIp, session.UserUUID, "established, terminal id:"+id, "terminal")
	established_time := time.Now()
	errChan := make(chan error, 1)

	go func() {
		for {
			messageType, data, err := session.Browser.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}

			if messageType == websocket.TextMessage {
				if session.Agent != nil && string(data[0:1]) == "{" {
					err = session.Agent.WriteMessage(websocket.TextMessage, data)
				} else if session.Agent != nil {
					err = session.Agent.WriteMessage(websocket.BinaryMessage, data)
				}
			} else if session.Agent != nil {
				// 二进制消息，原样传递
				err = session.Agent.WriteMessage(websocket.BinaryMessage, data)
			}

			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	go func() {
		for {
			_, data, err := session.Agent.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			if session.Browser != nil {
				err = session.Browser.WriteMessage(websocket.BinaryMessage, data)
				if err != nil {
					errChan <- err
					return
				}
			}
		}
	}()

	// 等待错误或主动关闭
	<-errChan
	// 关闭连接
	if session.Agent != nil {
		session.Agent.Close()
	}
	if session.Browser != nil {
		session.Browser.Close()
	}
	disconnect_time := time.Now()
	auditlog.Log(session.RequesterIp, session.UserUUID, "disconnected, terminal id:"+id+", duration:"+disconnect_time.Sub(established_time).String(), "terminal")
	TerminalSessionsMutex.Lock()
	delete(TerminalSessions, id)
	TerminalSessionsMutex.Unlock()
}
