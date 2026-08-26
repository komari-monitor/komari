package terminal

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
)

func EstablishConnection(c *gin.Context) {
	session_id := c.Query("id")
	TerminalSessionsMutex.Lock()
	session, exists := TerminalSessions[session_id]
	TerminalSessionsMutex.Unlock()
	authenticatedUUID, _ := c.Get("client_uuid")
	authenticatedString, hasAuthenticatedUUID := authenticatedUUID.(string)
	if !exists || session == nil || session.Browser == nil ||
		(hasAuthenticatedUUID && authenticatedString != "" && authenticatedString != session.UUID) {
		c.JSON(404, gin.H{"status": "error", "error": "Session not found"})
		return
	}
	// Upgrade the connection to WebSocket
	if !api.IsWebSocketUpgrade(c) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "Require WebSocket upgrade"})
		return
	}
	conn, err := api.UpgradeSafeConn(c)
	if err != nil {
		closeSession(session_id)
		return
	}
	_, ok := attachAgent(session_id, conn)
	if !ok {
		conn.Close()
		return
	}
	conn.SetCloseHandler(func(code int, text string) error {
		suspendSession(session_id, nil, conn)
		return nil
	})
	maybeStartForwarding(session_id)
}
