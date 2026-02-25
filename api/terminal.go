package api

import (
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/ws"
)

func RequestTerminal(c *gin.Context) {
	uuid := c.Param("uuid")
	user_uuid, _ := c.Get("uuid")
	_, err := clients.GetClientByUUID(uuid)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"status":  "error",
			"message": "Client not found",
		})
		return
	}

	// 检查终端会话资源限制
	rm := GetResourceManager()
	if !rm.TryAcquireTerminalSession() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "error",
			"message": "Terminal session limit reached, please try again later",
		})
		return
	}

	// 建立ws
	if !websocket.IsWebSocketUpgrade(c.Request) {
		rm.ReleaseTerminalSession()
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Require WebSocket upgrade"})
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: ws.CheckOrigin,
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		rm.ReleaseTerminalSession()
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

	// 使用原子标记防止重复清理
	var cleanedUp atomic.Bool

	// 确保在连接关闭时释放资源
	cleanupSession := func() {
		// 防止重复清理
		if cleanedUp.Swap(true) {
			return
		}

		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()
		rm.ReleaseTerminalSession()
		if session.Agent != nil {
			session.Agent.Close()
		}
		if session.Browser != nil {
			session.Browser.Close()
		}
	}

	conn.SetCloseHandler(func(code int, text string) error {
		log.Println("Terminal connection closed:", code, text)
		cleanupSession()
		return nil
	})

	if ws.GetConnectedClients()[uuid] == nil {
		conn.WriteMessage(1, []byte("Client offline!"))
		cleanupSession()
		return
	}
	err = ws.GetConnectedClients()[uuid].WriteJSON(gin.H{
		"message":    "terminal",
		"request_id": id,
	})
	if err != nil {
		cleanupSession()
		return
	}
	conn.WriteMessage(1, []byte("Waiting for agent connection..."))
	// 如果没有连接上，则关闭连接
	time.AfterFunc(30*time.Second, func() {
		TerminalSessionsMutex.Lock()
		// 再次检查 session 状态
		if s, exists := TerminalSessions[id]; exists && s.Agent == nil {
			cleanupSession()
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

	rm := GetResourceManager()

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

	// 释放终端会话资源
	rm.ReleaseTerminalSession()
}
