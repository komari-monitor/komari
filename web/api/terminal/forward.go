package terminal

import (
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/web/connection"
)

// ForwardTerminal pumps frames between the browser and the agent until one of
// them ends the session. An unexpected break suspends only the broken side and
// tells the surviving one to wait for a reconnect; an explicit terminate closes
// the whole session.
//
// Each SafeConn spawns at most one reader goroutine across its lifetime: if an
// older peer ignores abort and stays connected across a reattach, its existing
// reader simply starts forwarding to the newly attached peer instead of racing
// a second concurrent ReadMessage on the same gorilla/websocket.Conn.
func ForwardTerminal(id string, browser, agent *connection.SafeConn) {
	if browser == nil || agent == nil {
		return
	}
	TerminalSessionsMutex.Lock()
	session := TerminalSessions[id]
	requesterIp, userUUID := "", ""
	var stop chan struct{}
	startBrowser, startAgent := false, false
	if session != nil && session.Browser == browser && session.Agent == agent {
		requesterIp, userUUID = session.RequesterIp, session.UserUUID
		stopForwardWaitLocked(session)
		stop = make(chan struct{})
		session.forwardStop = stop
		if session.browserPump != browser {
			session.browserPump = browser
			startBrowser = true
		}
		if session.agentPump != agent {
			session.agentPump = agent
			startAgent = true
		}
	}
	TerminalSessionsMutex.Unlock()
	if requesterIp == "" {
		return
	}

	auditlog.Log(requesterIp, userUUID, "established, terminal id:"+id, "terminal")
	establishedTime := time.Now()

	if startBrowser {
		go pumpBrowserFrames(id, browser)
	}
	if startAgent {
		go pumpAgentFrames(id, agent)
	}

	<-stop
	auditlog.Log(requesterIp, userUUID, "disconnected, terminal id:"+id+", duration:"+time.Since(establishedTime).String(), "terminal")
}

func currentAgentForBrowser(id string, browser *connection.SafeConn) (*connection.SafeConn, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil || session.Browser != browser {
		return nil, false
	}
	return session.Agent, true
}

func currentBrowserForAgent(id string, agent *connection.SafeConn) (*connection.SafeConn, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session := TerminalSessions[id]
	if session == nil || session.Agent != agent {
		return nil, false
	}
	return session.Browser, true
}

func pumpBrowserFrames(id string, browser *connection.SafeConn) {
	for {
		messageType, data, err := browser.ReadMessage()
		if err != nil {
			suspendBrowser(id, browser)
			return
		}
		if messageType == websocket.TextMessage {
			if control, ok := parseControl(data); ok {
				switch control.Type {
				case controlTypeHeartbeat:
					// 纯前端心跳，服务端消费掉，不转给被控端。
					continue
				case controlTypeTerminate, "close":
					// 浏览器主动结束：closeSession 会把 terminate 转给被控端。
					closeSession(id)
					return
				}
				agent, active := currentAgentForBrowser(id, browser)
				if !active {
					return
				}
				if agent == nil {
					continue
				}
				// resize 等其余控制帧保持文本帧原样透传（含旧版兼容）。
				if err = agent.WriteMessage(websocket.TextMessage, data); err != nil {
					suspendAgent(id, agent)
				}
				continue
			}
		}
		agent, active := currentAgentForBrowser(id, browser)
		if !active {
			return
		}
		if agent == nil {
			continue
		}
		if err = agent.WriteMessage(websocket.BinaryMessage, data); err != nil {
			suspendAgent(id, agent)
		}
	}
}

func pumpAgentFrames(id string, agent *connection.SafeConn) {
	for {
		messageType, data, err := agent.ReadMessage()
		if err != nil {
			suspendAgent(id, agent)
			return
		}
		if messageType == websocket.TextMessage {
			if control, ok := parseControl(data); ok {
				if control.Type == controlTypeTerminate {
					// 被控端结束（shell 退出等）：closeSession 会转给浏览器。
					closeSession(id)
					return
				}
				// 未知控制消息：忽略，前向兼容。
				continue
			}
		}
		browser, active := currentBrowserForAgent(id, agent)
		if !active {
			return
		}
		if browser == nil {
			continue
		}
		// 非控制文本帧（如旧版 Agent 的文本报错）按原帧类型转发，其余走二进制。
		if err = browser.WriteMessage(messageType, data); err != nil {
			suspendBrowser(id, browser)
		}
	}
}
