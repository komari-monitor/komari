package terminal

import (
	"sort"
	"strings"
	"time"

	"github.com/komari-monitor/komari/web/connection"
)

// RecoverableSession describes a terminal session whose browser side has
// disconnected while the session remains retained on the server (either with
// the agent still connected or waiting for agent reconnect).
type RecoverableSession struct {
	RequestID      string `json:"request_id"`
	UUID           string `json:"uuid"`
	AgentConnected bool   `json:"agent_connected"`
}

type terminatedSessionEntry struct {
	uuid      string
	expiresAt time.Time
}

var terminatedSessions = make(map[string]terminatedSessionEntry)

func pruneTerminatedSessionsLocked(now time.Time) {
	for id, entry := range terminatedSessions {
		if !entry.expiresAt.After(now) {
			delete(terminatedSessions, id)
		}
	}
}

func recordTerminatedSessionLocked(id, clientUUID string) {
	if id == "" || clientUUID == "" {
		return
	}
	now := time.Now()
	pruneTerminatedSessionsLocked(now)
	terminatedSessions[id] = terminatedSessionEntry{
		uuid:      clientUUID,
		expiresAt: now.Add(terminalSessionRetention),
	}
}

func peekTerminatedSession(id string, hasAuth bool, authUUID string) bool {
	if id == "" {
		return false
	}
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	pruneTerminatedSessionsLocked(time.Now())
	entry, ok := terminatedSessions[id]
	if !ok {
		return false
	}
	if hasAuth && authUUID != "" && authUUID != entry.uuid {
		return false
	}
	return true
}

func consumeTerminatedSession(id string) {
	if id == "" {
		return
	}
	TerminalSessionsMutex.Lock()
	delete(terminatedSessions, id)
	TerminalSessionsMutex.Unlock()
}

// ListRecoverableSessions returns terminal sessions where Browser == nil
// (web<x>server<->agent or web<x>server<x>agent) owned by the caller.
func ListRecoverableSessions(userUUID string, isAPIKey bool, clientUUID string) []RecoverableSession {
	type sortableSession struct {
		item      RecoverableSession
		createdAt time.Time
	}

	clientUUID = strings.TrimSpace(clientUUID)

	TerminalSessionsMutex.Lock()
	items := make([]sortableSession, 0)
	for id, session := range TerminalSessions {
		if session == nil || session.Browser != nil {
			continue
		}
		if !isAPIKey && session.UserUUID != userUUID {
			continue
		}
		if clientUUID != "" && session.UUID != clientUUID {
			continue
		}
		items = append(items, sortableSession{
			item: RecoverableSession{
				RequestID:      id,
				UUID:           session.UUID,
				AgentConnected: session.Agent != nil,
			},
			createdAt: session.CreatedAt,
		})
	}
	TerminalSessionsMutex.Unlock()

	sort.Slice(items, func(i, j int) bool {
		if !items[i].createdAt.Equal(items[j].createdAt) {
			return items[i].createdAt.Before(items[j].createdAt)
		}
		return items[i].item.RequestID < items[j].item.RequestID
	})

	result := make([]RecoverableSession, len(items))
	for i, item := range items {
		result[i] = item.item
	}
	return result
}

// TerminateRecoverableSessions closes recoverable (Browser == nil) sessions
// owned by the caller and sends terminate to attached agents. Sessions that
// already have an active browser attached are left untouched.
func TerminateRecoverableSessions(userUUID string, isAPIKey bool, requestIDs []string) []string {
	if len(requestIDs) == 0 {
		return []string{}
	}

	type terminatedAuditTarget struct {
		id      string
		session *TerminalSession
	}
	seen := make(map[string]struct{}, len(requestIDs))
	terminated := make([]string, 0, len(requestIDs))
	agents := make([]*connection.SafeConn, 0, len(requestIDs))
	auditTargets := make([]terminatedAuditTarget, 0, len(requestIDs))

	TerminalSessionsMutex.Lock()
	for _, rawID := range requestIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		session := TerminalSessions[id]
		if session == nil || session.Browser != nil {
			continue
		}
		if !isAPIKey && session.UserUUID != userUUID {
			continue
		}

		stopCleanup(session)
		if session.auditEndReason == "" {
			session.auditEndReason = "terminated"
		}
		stopForwardWaitLocked(session)
		if session.Agent != nil {
			agents = append(agents, session.Agent)
		} else if session.UUID != "" {
			recordTerminatedSessionLocked(id, session.UUID)
		}
		delete(TerminalSessions, id)
		terminated = append(terminated, id)
		auditTargets = append(auditTargets, terminatedAuditTarget{id: id, session: session})
	}
	TerminalSessionsMutex.Unlock()

	for _, target := range auditTargets {
		finalizeSessionAudit(target.id, target.session)
	}

	for _, agent := range agents {
		notifyTerminate(agent)
		_ = agent.Close()
	}

	return terminated
}
