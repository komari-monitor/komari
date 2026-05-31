package ws

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func UpgradeRequest(c *gin.Context, checkOrigin func(r *http.Request) bool) (*websocket.Conn, error) {
	// 升级到ws
	if !websocket.IsWebSocketUpgrade(c.Request) {
		return nil, fmt.Errorf("require websocket upgrade")
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: checkOrigin,
	}
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
