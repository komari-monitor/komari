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
	establishedTime := time.Now().UTC()
	TerminalSessionsMutex.Lock()
	session := TerminalSessions[id]
	requesterIp, userUUID, clientUUID := "", "", ""
	var (
		stop         chan struct{}
		prevDuration time.Duration
		connects     int
		startedAt    time.Time
	)
	startBrowser, startAgent := false, false
	if session != nil && session.Browser == browser && session.Agent == agent {
		requesterIp, userUUID, clientUUID = session.RequesterIp, session.UserUUID, session.UUID
		stopForwardWaitLocked(session)
		stop = make(chan struct{})
		session.forwardStop = stop
		session.activeForwarders++
		session.auditConnects++
		if session.auditStartedAt.IsZero() {
			session.auditStartedAt = establishedTime
		}
		prevDuration = session.auditDuration
		connects = session.auditConnects
		startedAt = session.auditStartedAt
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

	session.auditMu.Lock()
	if session.auditClient == "" && clientUUID != "" {
		session.auditClient = auditlog.FormatClientUUID(clientUUID)
	}
	activeMsg := formatTerminalAuditMessage(terminalAuditSnapshot{
		clientLabel: session.auditClient,
		terminalID:  id,
		status:      "active",
		duration:    prevDuration,
		connects:    connects,
		startedAt:   startedAt,
	})
	if session.auditLogID == 0 {
		session.auditLogID = auditlog.LogWithID(requesterIp, userUUID, activeMsg, "terminal")
	} else {
		auditlog.UpdateMessage(session.auditLogID, activeMsg)
	}
	session.auditMu.Unlock()

	if startBrowser {
		go pumpBrowserFrames(id, browser)
	}
	if startAgent {
		go pumpAgentFrames(id, agent)
	}

	<-stop
	segmentDuration := time.Since(establishedTime)

	session.auditMu.Lock()
	TerminalSessionsMutex.Lock()
	session.auditDuration += segmentDuration
	if session.activeForwarders > 0 {
		session.activeForwarders--
	}
	remainingForwarders := session.activeForwarders
	totalDuration := session.auditDuration
	finalConnects := session.auditConnects
	finalStarted := session.auditStartedAt
	stillRetained := TerminalSessions[id] == session
	endReason := session.auditEndReason
	TerminalSessionsMutex.Unlock()

	// Only write a suspended/final update if no newer ForwardTerminal took over.
	if session.auditLogID != 0 && remainingForwarders == 0 {
		status := "suspended"
		var endedAt time.Time
		if !stillRetained {
			status = endReason
			if status == "" {
				status = "closed"
			}
			endedAt = time.Now().UTC()
		}
		auditlog.UpdateMessage(session.auditLogID, formatTerminalAuditMessage(terminalAuditSnapshot{
			logID:       session.auditLogID,
			clientLabel: session.auditClient,
			terminalID:  id,
			status:      status,
			duration:    totalDuration,
			connects:    finalConnects,
			startedAt:   finalStarted,
			endedAt:     endedAt,
		}))
	}
	session.auditMu.Unlock()
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
				// heartbeat / resize 等控制帧保持文本帧原样透传（保活并支持端到端 RTT，含旧版兼容）。
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
				switch control.Type {
				case controlTypeTerminate:
					// 被控端结束（shell 退出等）：closeSession 会转给浏览器。
					closeSession(id)
					return
				case controlTypeHeartbeat:
					browser, active := currentBrowserForAgent(id, agent)
					if !active {
						return
					}
					if browser != nil {
						if err = browser.WriteMessage(websocket.TextMessage, data); err != nil {
							suspendBrowser(id, browser)
						}
					}
					continue
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
