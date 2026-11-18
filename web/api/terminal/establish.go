package terminal

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
)

func EstablishConnection(c *gin.Context) {
	sessionID := c.Query("id")
	TerminalSessionsMutex.Lock()
	session, exists := TerminalSessions[sessionID]
	TerminalSessionsMutex.Unlock()
	authenticatedUUID, _ := c.Get("client_uuid")
	authenticatedString, hasAuthenticatedUUID := authenticatedUUID.(string)
	if !exists || session == nil ||
		(hasAuthenticatedUUID && authenticatedString != "" && authenticatedString != session.UUID) {
		if (!exists || session == nil) &&
			peekTerminatedSession(sessionID, hasAuthenticatedUUID, authenticatedString) &&
			api.IsWebSocketUpgrade(c) {
			if conn, err := api.UpgradeSafeConn(c, api.EnableWebSocketCompression); err == nil {
				consumeTerminatedSession(sessionID)
				notifyTerminate(conn)
				_ = conn.Close()
				return
			}
		}
		c.JSON(404, gin.H{"status": "error", "error": "Session not found"})
		return
	}
	// Upgrade the connection to WebSocket
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c, api.EnableWebSocketCompression)
	if err != nil {
		return
	}
	_, ok := attachAgent(sessionID, conn)
	if !ok {
		// 会话已经不在了：明确告知终态，被控端不必重连。
		notifyTerminate(conn)
		_ = conn.Close()
		return
	}
	maybeStartForwarding(sessionID)
}
