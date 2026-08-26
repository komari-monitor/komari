package terminal

import (
	"sync"
	"time"

	"github.com/komari-monitor/komari/web/connection"
)

type TerminalSession struct {
	UUID         string
	UserUUID     string
	Browser      *connection.SafeConn
	Agent        *connection.SafeConn
	RequesterIp  string
	Forwarding   bool
	CleanupTimer *time.Timer
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)

const terminalSessionRetention = 30 * time.Second

func scheduleCleanup(id string, session *TerminalSession) {
	if session.CleanupTimer != nil {
		session.CleanupTimer.Stop()
	}
	session.CleanupTimer = time.AfterFunc(terminalSessionRetention, func() {
		var browser, agent *connection.SafeConn

		TerminalSessionsMutex.Lock()
		if current, ok := TerminalSessions[id]; !ok || current != session {
			TerminalSessionsMutex.Unlock()
			return
		}
		browser, agent = session.Browser, session.Agent
		delete(TerminalSessions, id)
		TerminalSessionsMutex.Unlock()

		if browser != nil {
			_ = browser.Close()
		}
		if agent != nil {
			_ = agent.Close()
		}
	})
}

func stopCleanup(session *TerminalSession) {
	if session.CleanupTimer != nil {
		session.CleanupTimer.Stop()
		session.CleanupTimer = nil
	}
}

func suspendSession(id string, browser, agent *connection.SafeConn) {
	var otherBrowser, otherAgent *connection.SafeConn

	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil ||
		(browser != nil && session.Browser != browser) ||
		(agent != nil && session.Agent != agent) {
		TerminalSessionsMutex.Unlock()
		return
	}
	otherBrowser, otherAgent = session.Browser, session.Agent
	session.Browser = nil
	session.Agent = nil
	session.Forwarding = false
	scheduleCleanup(id, session)
	TerminalSessionsMutex.Unlock()

	if otherBrowser != nil {
		_ = otherBrowser.Close()
	}
	if otherAgent != nil {
		_ = otherAgent.Close()
	}
}

func closeSession(id string) {
	var browser, agent *connection.SafeConn

	TerminalSessionsMutex.Lock()
	if session, ok := TerminalSessions[id]; ok && session != nil {
		stopCleanup(session)
		browser, agent = session.Browser, session.Agent
		delete(TerminalSessions, id)
	}
	TerminalSessionsMutex.Unlock()

	if browser != nil {
		_ = browser.Close()
	}
	if agent != nil {
		_ = agent.Close()
	}
}

func attachBrowser(id string, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		return nil, false
	}
	if session.Browser != nil && session.Browser != conn {
		_ = session.Browser.Close()
	}
	session.Browser = conn
	session.Forwarding = false
	if session.Agent != nil {
		stopCleanup(session)
	}
	return session, true
}

func attachAgent(id string, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		return nil, false
	}
	if session.Agent != nil && session.Agent != conn {
		_ = session.Agent.Close()
	}
	session.Agent = conn
	session.Forwarding = false
	if session.Browser != nil {
		stopCleanup(session)
	}
	return session, true
}

func maybeStartForwarding(id string) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil || session.Browser == nil || session.Agent == nil || session.Forwarding {
		TerminalSessionsMutex.Unlock()
		return
	}
	session.Forwarding = true
	browser, agent := session.Browser, session.Agent
	TerminalSessionsMutex.Unlock()
	go ForwardTerminal(id, browser, agent)
}
