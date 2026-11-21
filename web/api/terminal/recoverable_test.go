package terminal

import (
	"testing"
	"time"
)

func setTestSessionRetention(t *testing.T, d time.Duration) {
	t.Helper()
	TerminalSessionsMutex.Lock()
	prev := terminalSessionRetention
	terminalSessionRetention = d
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() {
		TerminalSessionsMutex.Lock()
		terminalSessionRetention = prev
		TerminalSessionsMutex.Unlock()
	})
}

func setTestTerminalKeepalive(t *testing.T, pingInterval, readWait, writeWait time.Duration) {
	t.Helper()
	prevPing, prevRead, prevWrite := terminalPingInterval, terminalReadWait, terminalWriteWait
	terminalPingInterval = pingInterval
	terminalReadWait = readWait
	terminalWriteWait = writeWait
	t.Cleanup(func() {
		terminalPingInterval = prevPing
		terminalReadWait = prevRead
		terminalWriteWait = prevWrite
	})
}

func resetTerminatedSessions(t *testing.T) {
	t.Helper()
	TerminalSessionsMutex.Lock()
	terminatedSessions = make(map[string]terminatedSessionEntry)
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() {
		TerminalSessionsMutex.Lock()
		terminatedSessions = make(map[string]terminatedSessionEntry)
		TerminalSessionsMutex.Unlock()
	})
}

// TestSuspendedSessionDoesNotExtendCleanupOnAgentChurn verifies the core
// regression: once the browser disconnects, repeated agent disconnects and
// reconnects (such as Nginx 60s / Cloudflare 100s idle timeouts) must NOT keep
// resetting the 5-minute retention timer forever.
func TestSuspendedSessionDoesNotExtendCleanupOnAgentChurn(t *testing.T) {
	resetTerminalSessions(t)
	resetTerminatedSessions(t)
	setTestSessionRetention(t, 120*time.Millisecond)

	browserSafe, browserClient := newControlPair(t)
	agent1Safe, agent1Client := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agent1Client.Close() })

	session := newSession(t, "session-agent-churn", "owner-uuid", "client-a")
	session.Browser = browserSafe
	session.Agent = agent1Safe
	session.CreatedAt = time.Now()

	// Browser leaves unexpectedly -> starts the single 120ms retention countdown.
	suspendBrowser("session-agent-churn", browserSafe)
	if frame := readControlFrame(t, agent1Client); frame.Type != controlTypeAbort {
		t.Fatalf("agent1 frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	// Simulate reverse-proxy idle drops + agent reconnects every 35ms. Before
	// the fix, every suspendAgent and attachAgent reset CleanupTimer to a fresh
	// 120ms, keeping the session alive indefinitely.
	latestAgentSafe := agent1Safe
	latestAgentClient := agent1Client
	for i := 0; i < 3; i++ {
		time.Sleep(30 * time.Millisecond)
		suspendAgent("session-agent-churn", latestAgentSafe)

		nextSafe, nextClient := newControlPair(t)
		t.Cleanup(func() { _ = nextClient.Close() })
		if _, ok := attachAgent("session-agent-churn", nextSafe); !ok {
			// Session already expired on schedule — that's what we want!
			break
		}
		latestAgentSafe = nextSafe
		latestAgentClient = nextClient
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		_, present := TerminalSessions["session-agent-churn"]
		TerminalSessionsMutex.Unlock()
		if !present {
			// Ensure the expired session is no longer listed as recoverable and
			// is recorded in terminatedSessions for any late-reconnecting agent.
			if list := ListRecoverableSessions("owner-uuid", false, ""); len(list) != 0 {
				t.Fatalf("expected no recoverable sessions after expiry, got %+v", list)
			}
			if !peekTerminatedSession("session-agent-churn", true, "client-a") {
				t.Fatal("expired session was not recorded in terminatedSessions")
			}
			// If an agent was attached at expiry, it should receive terminate.
			_ = latestAgentClient.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("suspended session survived past retention window due to agent disconnect/reconnect churn")
}

// TestSuspendedSessionDoesNotExtendCleanupOnBrowserChurn verifies the mirror
// case: when the agent is gone, repeated browser reattaches must not reset the
// retention deadline indefinitely.
func TestSuspendedSessionDoesNotExtendCleanupOnBrowserChurn(t *testing.T) {
	resetTerminalSessions(t)
	resetTerminatedSessions(t)
	setTestSessionRetention(t, 120*time.Millisecond)

	browser1Safe, browser1Client := newControlPair(t)
	agentSafe, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browser1Client.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-browser-churn", "owner-uuid", "client-a")
	session.Browser = browser1Safe
	session.BrowserClientID = "tab-1"
	session.Agent = agentSafe

	suspendAgent("session-browser-churn", agentSafe)
	if frame := readControlFrame(t, browser1Client); frame.Type != controlTypeAbort {
		t.Fatalf("browser1 frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	currentBrowser := browser1Safe
	for i := 0; i < 3; i++ {
		time.Sleep(30 * time.Millisecond)
		suspendBrowser("session-browser-churn", currentBrowser)

		nextSafe, nextClient := newControlPair(t)
		t.Cleanup(func() { _ = nextClient.Close() })
		if _, _, ok := attachBrowserWithClientID("session-browser-churn", "client-a", "owner-uuid", false, nextSafe, "tab-1"); !ok {
			break
		}
		currentBrowser = nextSafe
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		_, present := TerminalSessions["session-browser-churn"]
		TerminalSessionsMutex.Unlock()
		if !present {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("suspended session survived past retention window due to browser reconnect churn")
}

// TestResumedSessionSurvivesOriginalDeadlineAndGetsFreshWindow verifies that
// once both peers reattach, the prior cleanup timer is cancelled (including any
// stale callback race) and a subsequent disconnect receives a brand-new window.
func TestResumedSessionSurvivesOriginalDeadlineAndGetsFreshWindow(t *testing.T) {
	resetTerminalSessions(t)
	resetTerminatedSessions(t)
	setTestSessionRetention(t, 100*time.Millisecond)

	browser1Safe, browser1Client := newControlPair(t)
	agentSafe, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browser1Client.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-resume-fresh", "owner-uuid", "client-a")
	session.Browser = browser1Safe
	session.Agent = agentSafe

	suspendBrowser("session-resume-fresh", browser1Safe)
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	// Reattach before expiry.
	time.Sleep(30 * time.Millisecond)
	browser2Safe, browser2Client := newControlPair(t)
	t.Cleanup(func() { _ = browser2Client.Close() })
	if _, hasAgent, ok := attachBrowserWithClientID("session-resume-fresh", "client-a", "owner-uuid", false, browser2Safe, "tab-2"); !ok || !hasAgent {
		t.Fatalf("expected reattach with live agent, got ok=%v hasAgent=%v", ok, hasAgent)
	}
	if frame := readControlFrame(t, browser2Client); frame.Type != controlTypeReady {
		t.Fatalf("browser2 frame = %q, want %q", frame.Type, controlTypeReady)
	}
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeReady {
		t.Fatalf("agent frame after browser reattach = %q, want %q", frame.Type, controlTypeReady)
	}

	// Wait past the original 100ms window — resumed session must stay alive.
	time.Sleep(130 * time.Millisecond)
	TerminalSessionsMutex.Lock()
	stillAlive := TerminalSessions["session-resume-fresh"] == session
	TerminalSessionsMutex.Unlock()
	if !stillAlive {
		t.Fatal("resumed session was prematurely expired by old cleanup timer")
	}

	// A second disconnect gets a fresh 100ms retention window and then expires.
	suspendBrowser("session-resume-fresh", browser2Safe)
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame on second suspend = %q, want %q", frame.Type, controlTypeAbort)
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		_, present := TerminalSessions["session-resume-fresh"]
		TerminalSessionsMutex.Unlock()
		if !present {
			if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
				t.Fatalf("agent frame on expiry = %q, want %q", frame.Type, controlTypeTerminate)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session did not expire after second disconnect")
}

// TestListRecoverableSessionsExcludesExpiredDeadline ensures an already-past
// cleanupDeadline is never surfaced even if the timer goroutine hasn't run yet.
func TestListRecoverableSessionsExcludesExpiredDeadline(t *testing.T) {
	resetTerminalSessions(t)

	session := newSession(t, "session-past-deadline", "owner-uuid", "client-a")
	session.cleanupDeadline = time.Now().Add(-time.Second)

	if list := ListRecoverableSessions("owner-uuid", false, ""); len(list) != 0 {
		t.Fatalf("expected expired session to be hidden, got %+v", list)
	}
}

// TestTerminateRecoverableSessionsWithClientID verifies that:
//   - empty clientID protects any session attached to an active browser
//   - matching clientID allows a closing tab to terminate its own half-open session
//   - mismatched clientID protects another tab's attached session
//   - terminatedSessions is recorded even when an agent socket was attached
func TestTerminateRecoverableSessionsWithClientID(t *testing.T) {
	resetTerminalSessions(t)
	resetTerminatedSessions(t)

	browserSafe, browserClient := newControlPair(t)
	agentSafe, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-client-id", "owner-uuid", "client-a")
	session.Browser = browserSafe
	session.BrowserClientID = "tab-owner"
	session.Agent = agentSafe

	// Dialog terminate (no client_id) must not touch an actively attached session.
	if got := TerminateRecoverableSessions("owner-uuid", false, []string{"session-client-id"}); len(got) != 0 {
		t.Fatalf("expected empty terminated list without client_id, got %v", got)
	}

	// Another tab's client_id must also not kill it.
	if got := TerminateRecoverableSessionsWithClientID("owner-uuid", false, []string{"session-client-id"}, "other-tab"); len(got) != 0 {
		t.Fatalf("expected empty terminated list for wrong client_id, got %v", got)
	}

	// Same tab's client_id terminates it cleanly and notifies both sides.
	got := TerminateRecoverableSessionsWithClientID("owner-uuid", false, []string{"session-client-id"}, "tab-owner")
	if len(got) != 1 || got[0] != "session-client-id" {
		t.Fatalf("terminated = %v, want [session-client-id]", got)
	}
	if frame := readControlFrame(t, browserClient); frame.Type != controlTypeTerminate {
		t.Fatalf("browser frame = %q, want %q", frame.Type, controlTypeTerminate)
	}
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeTerminate)
	}
	if !peekTerminatedSession("session-client-id", true, "client-a") {
		t.Fatal("terminated session was not recorded in terminatedSessions for agent reconnect safety")
	}
}

// TestTerminalKeepaliveDetectsSilentBrowserDrop verifies that a silently dead
// browser connection (no TCP FIN/RST, no Pong replies) hits the read deadline
// and transitions the session into suspended state promptly.
func TestTerminalKeepaliveDetectsSilentBrowserDrop(t *testing.T) {
	resetTerminalSessions(t)
	setTestTerminalKeepalive(t, 25*time.Millisecond, 80*time.Millisecond, 50*time.Millisecond)

	browserSafe, browserClient := newControlPair(t)
	agentSafe, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	// Note: browserClient intentionally never calls ReadMessage, so it will NOT
	// respond to WebSocket Ping frames — simulating a silently frozen/sleeping peer.
	session := newSession(t, "session-silent-browser", "owner-uuid", "client-a")
	session.RequesterIp = "127.0.0.1"
	session.Browser = browserSafe
	session.Agent = agentSafe

	go ForwardTerminal("session-silent-browser", browserSafe, agentSafe)

	// Agent should receive abort once browser hits readWait timeout.
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	TerminalSessionsMutex.Lock()
	detached := TerminalSessions["session-silent-browser"] != nil && TerminalSessions["session-silent-browser"].Browser == nil
	TerminalSessionsMutex.Unlock()
	if !detached {
		t.Fatal("expected silent browser to be suspended after read deadline")
	}
}
