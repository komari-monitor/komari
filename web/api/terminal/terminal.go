package terminal

import (
	"sync"
	"time"

	"github.com/komari-monitor/komari/web/connection"
)

type TerminalSession struct {
	UUID        string
	UserUUID    string
	Browser     *connection.SafeConn
	Agent       *connection.SafeConn
	RequesterIp string
	Forwarding  bool
	// browserPump / agentPump 确保同一条 SafeConn 至多只启动一个读循环协程；
	// forwardStop 在单边断线或正常结束时关闭，唤醒当前活跃的 ForwardTerminal。
	browserPump *connection.SafeConn
	agentPump   *connection.SafeConn
	forwardStop chan struct{}
	// CleanupTimer bounds how long a suspended session waits for a reconnect.
	CleanupTimer *time.Timer
}

var TerminalSessionsMutex = &sync.Mutex{}
var TerminalSessions = make(map[string]*TerminalSession)

// 与 v2 事件队列 TTL 对齐：意外断开后，会话在这段时间内可被重连复用。
const terminalSessionRetention = 5 * time.Minute

// scheduleCleanup 在会话下次被回收时清理。重连成功会取消它。
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

		// 等不到重连：告诉仍在线的对端这是终态，别再等了。
		if browser != nil {
			notifyTerminate(browser)
			_ = browser.Close()
		}
		if agent != nil {
			notifyTerminate(agent)
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

func suspendSide(id string, conn *connection.SafeConn, matchBrowser bool) {
	if conn == nil {
		return
	}
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil ||
		(matchBrowser && session.Browser != conn) ||
		(!matchBrowser && session.Agent != conn) {
		TerminalSessionsMutex.Unlock()
		return
	}
	var survivor *connection.SafeConn
	clientUUID := ""
	if matchBrowser {
		session.Browser = nil
		survivor = session.Agent
	} else {
		session.Agent = nil
		survivor = session.Browser
		if survivor != nil {
			clientUUID = session.UUID
		}
	}
	session.Forwarding = false
	stopForwardWaitLocked(session)
	scheduleCleanup(id, session)
	TerminalSessionsMutex.Unlock()

	_ = conn.Close()
	// 持锁期间不写 socket：写入可能阻塞，会拖住整个会话表。
	if survivor != nil {
		notifyAbort(survivor)
	}
	// 浏览器仍在线而被控端掉线时，主动重发终端请求唤醒被控端（兼容不自动重连的旧版 Agent）。
	if clientUUID != "" {
		dispatchTerminalRequest(clientUUID, id)
	}
}

func suspendBrowser(id string, conn *connection.SafeConn) { suspendSide(id, conn, true) }
func suspendAgent(id string, conn *connection.SafeConn)   { suspendSide(id, conn, false) }

// closeSession 结束会话：给两端发 terminate 并清理。terminate 是终态，
// 收到的一方不会再重连，所以这里可以直接关闭连接。
func closeSession(id string) {
	var browser, agent *connection.SafeConn

	TerminalSessionsMutex.Lock()
	if session, ok := TerminalSessions[id]; ok && session != nil {
		stopCleanup(session)
		stopForwardWaitLocked(session)
		browser, agent = session.Browser, session.Agent
		delete(TerminalSessions, id)
	}
	TerminalSessionsMutex.Unlock()

	if browser != nil {
		notifyTerminate(browser)
		_ = browser.Close()
	}
	if agent != nil {
		notifyTerminate(agent)
		_ = agent.Close()
	}
}

func attachBrowser(id, clientUUID, userUUID string, apiKey bool, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	if session.UUID != clientUUID || (!apiKey && session.UserUUID != userUUID) {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	oldBrowser := session.Browser
	oldAgent := session.Agent
	session.Browser = conn
	session.Forwarding = false
	startPump := conn != nil && session.browserPump != conn
	if startPump {
		session.browserPump = conn
	}
	stopForwardWaitLocked(session)
	if oldAgent != nil {
		stopCleanup(session)
	} else {
		scheduleCleanup(id, session)
	}
	TerminalSessionsMutex.Unlock()

	if oldBrowser != nil && oldBrowser != conn {
		_ = oldBrowser.Close()
	}
	if startPump {
		go pumpBrowserFrames(id, conn)
	}
	// 重连的浏览器要立刻知道自己回到了哪一步：被控端已在位就算就绪。
	if oldAgent != nil {
		notifyReady(conn)
	}
	return session, true
}

func attachAgent(id string, conn *connection.SafeConn) (*TerminalSession, bool) {
	TerminalSessionsMutex.Lock()
	session, ok := TerminalSessions[id]
	if !ok || session == nil {
		TerminalSessionsMutex.Unlock()
		return nil, false
	}
	oldAgent := session.Agent
	oldBrowser := session.Browser
	session.Agent = conn
	session.Forwarding = false
	startPump := conn != nil && session.agentPump != conn
	if startPump {
		session.agentPump = conn
	}
	stopForwardWaitLocked(session)
	if oldBrowser != nil {
		stopCleanup(session)
	} else {
		scheduleCleanup(id, session)
	}
	TerminalSessionsMutex.Unlock()

	if oldAgent != nil && oldAgent != conn {
		_ = oldAgent.Close()
	}
	if startPump {
		go pumpAgentFrames(id, conn)
	}
	// 被控端就位：浏览器可以停止等待，转发随即开始。
	if oldBrowser != nil {
		notifyReady(oldBrowser)
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

func stopForwardWaitLocked(session *TerminalSession) {
	if session.forwardStop != nil {
		close(session.forwardStop)
		session.forwardStop = nil
	}
}
