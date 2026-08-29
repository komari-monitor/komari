package terminal

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
	logger "github.com/komari-monitor/komari/utils/log"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"github.com/komari-monitor/komari/web/api"
)

func dispatchTerminalRequest(uuid, id string) bool {
	return agent_runtime.DispatchV2Event(uuid, v2.MethodAgentTerminal, v2.TerminalRequestParams{RequestID: id})
}

func RequestTerminal(c *gin.Context) {
	uuid := c.Param("uuid")
	userUUID, _ := c.Get("uuid")
	userID, _ := userUUID.(string)
	_, isAPIKey := c.Get("api_key")
	_, err := clients.GetClientByUUID(uuid)
	if err != nil {
		c.JSON(400, gin.H{
			"status":  "error",
			"message": "Client not found",
		})
		return
	}
	id := strings.TrimSpace(c.Query("request_id"))
	if id == "" {
		// Only a new terminal request is a sensitive operation. Reattaching an
		// existing session is authorized by its owner below and must not require
		// a fresh (and potentially expired) TOTP code.
		if err := api.VerifySensitive2FA(c); err != nil {
			api.RespondError(c, http.StatusUnauthorized, err.Error())
			return
		}
	} else {
		TerminalSessionsMutex.Lock()
		session := TerminalSessions[id]
		allowed := session != nil && session.UUID == uuid && (isAPIKey || session.UserUUID == userID)
		TerminalSessionsMutex.Unlock()
		if !allowed {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Terminal session not found"})
			return
		}
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

	if id != "" {
		session, ok := attachBrowser(id, userID, isAPIKey, conn)
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
		if session.Agent == nil {
			conn.WriteMessage(1, []byte("等待被控端连接 waiting for agent...\n"))
		}
		maybeStartForwarding(id)
		return
	}

	// 新建一个终端连接
	id = utils.GenerateRandomString(32)
	session := &TerminalSession{
		UserUUID:    userID,
		UUID:        uuid,
		Browser:     conn,
		Agent:       nil,
		RequesterIp: c.ClientIP(),
	}

	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = session
	scheduleCleanup(id, session)
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
	//auditlog.Log(c.ClientIP(), userID, "request, terminal id:"+id+",client:"+session.UUID, "terminal")
}
