package terminal

import (
	logger "github.com/komari-monitor/komari/utils/log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/connection"
)

func dispatchTerminalRequest(uuid, id string) bool {
	if agent_runtime.IsV2Client(uuid) {
		return agent_runtime.DispatchV2Event(uuid, v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: id})
	}
	agent := agent_runtime.GetConnectedClients()[uuid]
	if agent == nil {
		return false
	}
	return agent.WriteJSON(gin.H{
		"message":    "terminal",
		"request_id": id,
	}) == nil
}

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
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c)
	if err != nil {
		return
	}

	id := c.Query("request_id")
	if id != "" {
		session, ok := attachBrowser(id, conn)
		if !ok || session.UUID != uuid {
			conn.WriteMessage(1, []byte("Terminal session expired\n终端会话已过期\n"))
			conn.Close()
			return
		}
		conn.SetCloseHandler(func(code int, text string) error {
			logger.InfoArgs("terminal", "Terminal browser connection closed:", code, text)
			suspendSession(id, conn, nil)
			return nil
		})
		conn.WriteJSON(gin.H{"request_id": id})
		if !dispatchTerminalRequest(uuid, id) {
			conn.WriteMessage(1, []byte("Client offline!\n被控端离线!\n"))
			closeSession(id)
			return
		}
		conn.WriteMessage(1, []byte("等待被控端连接 waiting for agent...\n"))
		maybeStartForwarding(id)
		return
	}

	// 新建一个终端连接
	id = utils.GenerateRandomString(32)
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
		logger.InfoArgs("terminal", "Terminal browser connection closed:", code, text)
		suspendSession(id, conn, nil)
		return nil
	})
	conn.WriteJSON(gin.H{"request_id": id})
	if !dispatchTerminalRequest(uuid, id) {
		conn.Close()
		closeSession(id)
		return
	}
	conn.WriteMessage(1, []byte("等待被控端连接 waiting for agent...\n"))
	// 如果没有连接上，则关闭连接
	time.AfterFunc(30*time.Second, func() {
		var expired *connection.SafeConn
		TerminalSessionsMutex.Lock()
		if current, ok := TerminalSessions[id]; ok && current == session && current.Agent == nil {
			expired = current.Browser
			stopCleanup(session)
			delete(TerminalSessions, id)
		}
		TerminalSessionsMutex.Unlock()
		if expired != nil {
			expired.WriteMessage(1, []byte("被控端连接超时 timeout\n"))
			expired.Close()
		}
	})
	//auditlog.Log(c.ClientIP(), user_uuid.(string), "request, terminal id:"+id+",client:"+session.UUID, "terminal")
}
