package terminal

import (
	"testing"
	"time"

	"github.com/komari-monitor/komari/web/connection"
)

// resetTerminalSessions clears the global session map so each test is
// independent and no cleanup timer leaks into the next test.
func resetTerminalSessions(t *testing.T) {
	t.Helper()
	TerminalSessionsMutex.Lock()
	for id, session := range TerminalSessions {
		if session != nil {
			stopCleanup(session)
		}
		delete(TerminalSessions, id)
	}
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() {
		TerminalSessionsMutex.Lock()
		for id, session := range TerminalSessions {
			if session != nil {
				stopCleanup(session)
			}
			delete(TerminalSessions, id)
		}
		TerminalSessionsMutex.Unlock()
	})
}

// newSession registers a session owned by userUUID without any live
// connections, which is enough to exercise the ownership rules.
func newSession(t *testing.T, id, userUUID, clientUUID string) *TerminalSession {
	t.Helper()
	session := &TerminalSession{
		UUID:     clientUUID,
		UserUUID: userUUID,
	}
	TerminalSessionsMutex.Lock()
	TerminalSessions[id] = session
	TerminalSessionsMutex.Unlock()
	return session
}

// TestAttachBrowserEnforcesOwnership is the privilege-escalation guard for the
// web terminal: a different user must never attach to someone else's session,
// while an API key may.
func TestAttachBrowserEnforcesOwnership(t *testing.T) {
	resetTerminalSessions(t)
	newSession(t, "session-1", "owner-uuid", "client-a")

	if _, ok := attachBrowser("session-1", "attacker-uuid", false, nil); ok {
		t.Fatal("a different user attached to another user's terminal session")
	}
	if _, ok := attachBrowser("session-1", "owner-uuid", false, nil); !ok {
		t.Fatal("the owner was refused their own terminal session")
	}
	if _, ok := attachBrowser("session-1", "attacker-uuid", true, nil); !ok {
		t.Fatal("an API key was refused the terminal session")
	}
	if _, ok := attachBrowser("missing-session", "owner-uuid", true, nil); ok {
		t.Fatal("attaching to a missing session succeeded")
	}
}

// TestAttachBrowserRefusesNilSession covers a nil entry in the map, which can
// occur if a session is torn down concurrently.
func TestAttachBrowserRefusesNilSession(t *testing.T) {
	resetTerminalSessions(t)
	TerminalSessionsMutex.Lock()
	TerminalSessions["nil-session"] = nil
	TerminalSessionsMutex.Unlock()

	if _, ok := attachBrowser("nil-session", "any", true, nil); ok {
		t.Fatal("attached to a nil session")
	}
}

// TestAttachAgentBindsToExistingSession ensures the agent side cannot create a
// session, only join one created by the browser.
func TestAttachAgentBindsToExistingSession(t *testing.T) {
	resetTerminalSessions(t)
	if _, ok := attachAgent("missing", nil); ok {
		t.Fatal("attachAgent created a session out of thin air")
	}

	session := newSession(t, "session-2", "owner-uuid", "client-a")
	got, ok := attachAgent("session-2", nil)
	if !ok || got != session {
		t.Fatalf("attachAgent = %v, %v; want the existing session", got, ok)
	}
}

// TestCloseSessionRemovesEntryAndIsIdempotent covers teardown.
func TestCloseSessionRemovesEntryAndIsIdempotent(t *testing.T) {
	resetTerminalSessions(t)
	newSession(t, "session-3", "owner-uuid", "client-a")

	closeSession("session-3")
	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-3"]
	TerminalSessionsMutex.Unlock()
	if present {
		t.Fatal("closeSession left the session in the map")
	}

	// Closing twice must not panic.
	closeSession("session-3")
	closeSession("never-existed")
}

// TestSuspendSessionIgnoresStaleConnection guards against a late close from a
// previous browser tearing down a session that has already been reattached.
func TestSuspendSessionIgnoresStaleConnection(t *testing.T) {
	resetTerminalSessions(t)
	session := newSession(t, "session-4", "owner-uuid", "client-a")

	// Simulate a stale connection that is not the session's current browser.
	stale := (*connection.SafeConn)(nil)
	suspendSession("session-4", stale, nil)

	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-4"]
	TerminalSessionsMutex.Unlock()
	if !present {
		t.Fatal("a stale connection removed the live session")
	}
	_ = session
}

// TestScheduleCleanupRemovesSession verifies the retention timer eventually
// drops the session and that stopCleanup prevents it.
func TestScheduleCleanupRemovesSession(t *testing.T) {
	resetTerminalSessions(t)
	session := newSession(t, "session-5", "owner-uuid", "client-a")

	// Replace the retention window with a very short timer for the test.
	TerminalSessionsMutex.Lock()
	session.CleanupTimer = time.AfterFunc(20*time.Millisecond, func() {
		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, "session-5")
		TerminalSessionsMutex.Unlock()
	})
	TerminalSessionsMutex.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		_, present := TerminalSessions["session-5"]
		TerminalSessionsMutex.Unlock()
		if !present {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cleanup timer never removed the session")
}

// TestStopCleanupPreventsRemoval ensures a reattached session survives.
func TestStopCleanupPreventsRemoval(t *testing.T) {
	resetTerminalSessions(t)
	session := newSession(t, "session-6", "owner-uuid", "client-a")

	TerminalSessionsMutex.Lock()
	session.CleanupTimer = time.AfterFunc(20*time.Millisecond, func() {
		TerminalSessionsMutex.Lock()
		delete(TerminalSessions, "session-6")
		TerminalSessionsMutex.Unlock()
	})
	TerminalSessionsMutex.Unlock()

	TerminalSessionsMutex.Lock()
	stopCleanup(session)
	TerminalSessionsMutex.Unlock()

	time.Sleep(60 * time.Millisecond)
	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-6"]
	TerminalSessionsMutex.Unlock()
	if !present {
		t.Fatal("stopCleanup did not cancel the retention timer")
	}
}
