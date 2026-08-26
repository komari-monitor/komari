package terminal

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/web/connection"
)

func ForwardTerminal(id string, browser, agent *connection.SafeConn) {
	if browser == nil || agent == nil {
		return
	}
	TerminalSessionsMutex.Lock()
	session := TerminalSessions[id]
	requesterIp, userUUID := "", ""
	if session != nil && session.Browser == browser && session.Agent == agent {
		requesterIp, userUUID = session.RequesterIp, session.UserUUID
	}
	TerminalSessionsMutex.Unlock()
	if requesterIp == "" {
		return
	}

	auditlog.Log(requesterIp, userUUID, "established, terminal id:"+id, "terminal")
	established_time := time.Now()
	errChan := make(chan error, 1)

	go func() {
		for {
			messageType, data, err := browser.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}

			if messageType == websocket.TextMessage {
				var control struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &control) == nil {
					if control.Type == "heartbeat" {
						continue
					}
					if control.Type == "close" {
						_ = agent.WriteJSON(gin.H{"type": "close"})
						closeSession(id)
						errChan <- nil
						return
					}
				}
				if len(data) > 0 && data[0] == '{' {
					err = agent.WriteMessage(websocket.TextMessage, data)
				} else {
					err = agent.WriteMessage(websocket.BinaryMessage, data)
				}
			} else {
				err = agent.WriteMessage(websocket.BinaryMessage, data)
			}

			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	go func() {
		for {
			_, data, err := agent.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			err = browser.WriteMessage(websocket.BinaryMessage, data)
			if err != nil {
				errChan <- err
				return
			}
		}
	}()

	// 等待错误或主动关闭
	<-errChan
	suspendSession(id, browser, agent)
	disconnect_time := time.Now()
	auditlog.Log(requesterIp, userUUID, "disconnected, terminal id:"+id+", duration:"+disconnect_time.Sub(established_time).String(), "terminal")
}
