package terminal

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/clients"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils"
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
	if _, err := clients.GetClientByUUID(uuid); err != nil {
		c.JSON(400, gin.H{
			"status":  "error",
			"message": "Client not found",
		})
		return
	}
	id := strings.TrimSpace(c.Query("request_id"))
	clientID := strings.TrimSpace(c.Query("client_id"))
	reattach := id != ""
	if !reattach {
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
		allowed, alreadyAttached := canAttachBrowserLocked(session, uuid, userID, isAPIKey, clientID)
		TerminalSessionsMutex.Unlock()
		if !allowed {
			// 会话已过期或已被其他浏览器占用时也走 terminate，浏览器据此明确不再重连。
			if api.IsWebSocketUpgrade(c) {
				if conn, err := api.UpgradeSafeConn(c, api.EnableWebSocketCompression); err == nil {
					notifyTerminate(conn)
					_ = conn.Close()
					return
				}
			}
			if alreadyAttached {
				c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "Terminal session is already attached"})
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "Terminal session not found"})
			return
		}
	}

	// 建立ws
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c, api.EnableWebSocketCompression)
	if err != nil {
		return
	}

	if reattach {
		_, hasAgent, ok := attachBrowserWithClientID(id, uuid, userID, isAPIKey, conn, clientID)
		if !ok {
			notifyTerminate(conn)
			_ = conn.Close()
			return
		}
		notifyAssigned(conn, id)
		if !hasAgent && !hasAttachedAgent(id) && !dispatchTerminalRequest(uuid, id) {
			closeSession(id)
			return
		}
		maybeStartForwarding(id)
		return
	}

	// 新建一个终端连接
	id = utils.GenerateRandomString(32)
	session := &TerminalSession{
		UserUUID:        userID,
		UUID:            uuid,
		Browser:         conn,
		BrowserClientID: clientID,
		Agent:           nil,
		RequesterIp:     c.ClientIP(),
		CreatedAt:       time.Now(),
	}

	TerminalSessionsMutex.Lock()
	pruneTerminatedSessionsLocked(session.CreatedAt)
	delete(terminatedSessions, id)
	TerminalSessions[id] = session
	session.browserPump = conn
	scheduleCleanup(id, session)
	TerminalSessionsMutex.Unlock()
	go pumpBrowserFrames(id, conn)
	// assigned 让浏览器先拿到 request_id，此后任何一侧掉线都能用它重连。
	notifyAssigned(conn, id)
	if !dispatchTerminalRequest(uuid, id) {
		closeSession(id)
		return
	}
	// 终端随后开启：被控端附着时 attachAgent 会发 ready，转发随即开始。
}
