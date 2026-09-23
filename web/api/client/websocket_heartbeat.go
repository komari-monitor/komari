package client

import (
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/web/connection"
)

const (
	v2WebSocketReadWait  = 75 * time.Second
	v2WebSocketWriteWait = 10 * time.Second
)

func setWebSocketPingHandler(conn *connection.SafeConn, readWait, writeWait time.Duration) {
	conn.SetPingHandler(func(data string) error {
		if err := conn.SetReadDeadline(time.Now().Add(readWait)); err != nil {
			return err
		}
		return conn.WriteControl(
			websocket.PongMessage,
			[]byte(data),
			time.Now().Add(writeWait),
		)
	})
}
