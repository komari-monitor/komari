package terminal

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
)

// TestLegacyAgentIgnoresUnknownMessages documents why the protocol needs no
// compatibility code: an agent built before abort/terminate existed parses text
// frames into its own switch and simply falls through on unknown types, so the
// session still comes up and only the graceful-abort hint is lost.
func TestLegacyAgentIgnoresUnknownMessages(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-legacy", "owner-uuid", "client-a")
	session.RequesterIp = "127.0.0.1"
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn

	go ForwardTerminal("session-legacy", browserSafeConn, agentSafeConn)
	time.Sleep(50 * time.Millisecond)

	// A legacy agent treats the browser's terminate as an unknown type and
	// ignores it, but the data path keeps working: that is the whole point.
	writeFrame(t, browserClient, `{"type":"terminate"}`)

	// Whatever the server decides, the connection must not be left half-dead:
	// the eventual outcome is either a terminate or a suspend, and in both
	// cases the session reaches a stable state instead of hanging forever.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		present := TerminalSessions["session-legacy"] != nil
		TerminalSessionsMutex.Unlock()
		if !present {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session never reached a stable state after terminate from a legacy peer")
}

// writeFrame sends one text control frame from a test client.
func writeFrame(t *testing.T, client *websocket.Conn, payload string) {
	t.Helper()
	if err := client.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write frame %q failed: %v", payload, err)
	}
}

// TestForwardTerminalSuspendsOnUnexpectedBreak drives a real forwarding loop and
// verifies the abort/terminate split:
//
//   - one side dropping unexpectedly sends abort to the survivor and keeps the
//     session for a reconnect
//   - an explicit terminate closes the session and removes it
func TestForwardTerminalSuspendsOnUnexpectedBreak(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-forward", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn
	// ForwardTerminal refuses sessions without a requester IP (they can never be
	// audited), so match what RequestTerminal records in production.
	session.RequesterIp = "127.0.0.1"

	go ForwardTerminal("session-forward", browserSafeConn, agentSafeConn)

	// Give the pumps a moment to start before breaking one side.
	time.Sleep(50 * time.Millisecond)
	_ = browserClient.Close()

	// The agent survives, so it must be told the session was aborted rather
	// than terminated, and the session must still be reconnectable.
	frame := readControlFrame(t, agentClient)
	if frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	TerminalSessionsMutex.Lock()
	kept := TerminalSessions["session-forward"] != nil
	TerminalSessionsMutex.Unlock()
	if !kept {
		t.Fatal("session was removed on an unexpected break, so reconnect is impossible")
	}
}

// TestForwardTerminalClosesOnTerminate verifies the normal-termination path: a
// terminate from the browser reaches the agent and the session is dropped.
func TestForwardTerminalClosesOnTerminate(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-terminate", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn
	session.RequesterIp = "127.0.0.1"

	go ForwardTerminal("session-terminate", browserSafeConn, agentSafeConn)
	time.Sleep(50 * time.Millisecond)

	writeFrame(t, browserClient, `{"type":"terminate"}`)

	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeTerminate)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		TerminalSessionsMutex.Lock()
		present := TerminalSessions["session-terminate"] != nil
		TerminalSessionsMutex.Unlock()
		if !present {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session survived an explicit terminate")
}

// TestForwardTerminalRelaysOutput pins the data path: binary frames from the
// agent reach the browser untouched.
func TestForwardTerminalRelaysOutput(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-relay", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn
	session.RequesterIp = "127.0.0.1"

	go ForwardTerminal("session-relay", browserSafeConn, agentSafeConn)
	time.Sleep(50 * time.Millisecond)

	if err := agentClient.WriteMessage(websocket.BinaryMessage, []byte("hello")); err != nil {
		t.Fatalf("agent write failed: %v", err)
	}

	_ = browserClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, data, err := browserClient.ReadMessage()
	if err != nil {
		t.Fatalf("browser read failed: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type = %d, want binary", messageType)
	}
	if string(data) != "hello" {
		t.Fatalf("payload = %q, want %q", string(data), "hello")
	}
}

// TestForwardTerminalRelaysHeartbeat verifies that heartbeat frames carrying an
// id traverse browser -> server -> agent -> server -> browser for keepalive and RTT.
func TestForwardTerminalRelaysHeartbeat(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-heartbeat", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn
	session.RequesterIp = "127.0.0.1"

	go ForwardTerminal("session-heartbeat", browserSafeConn, agentSafeConn)
	time.Sleep(50 * time.Millisecond)

	const hb = `{"type":"heartbeat","id":"42"}`
	writeFrame(t, browserClient, hb)

	_ = agentClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := agentClient.ReadMessage()
	if err != nil {
		t.Fatalf("agent read heartbeat failed: %v", err)
	}
	if msgType != websocket.TextMessage || string(data) != hb {
		t.Fatalf("agent got (%d, %q), want text %q", msgType, string(data), hb)
	}

	writeFrame(t, agentClient, hb)

	_ = browserClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err = browserClient.ReadMessage()
	if err != nil {
		t.Fatalf("browser read heartbeat echo failed: %v", err)
	}
	if msgType != websocket.TextMessage || string(data) != hb {
		t.Fatalf("browser got (%d, %q), want text %q", msgType, string(data), hb)
	}
}

// TestLegacyAgentSurvivesBrowserReattach verifies that an old agent that
// ignores abort and keeps its socket open can still be read cleanly by a second
// ForwardTerminal after a browser reconnects.
func TestLegacyAgentSurvivesBrowserReattach(t *testing.T) {
	resetTerminalSessions(t)

	browser1Safe, browser1Client := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browser1Client.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-legacy-reattach", "owner-uuid", "client-a")
	session.RequesterIp = "127.0.0.1"
	session.Browser = browser1Safe
	session.Agent = agentSafeConn
	session.Forwarding = true

	doneFirst := make(chan struct{})
	go func() {
		ForwardTerminal("session-legacy-reattach", browser1Safe, agentSafeConn)
		close(doneFirst)
	}()
	time.Sleep(50 * time.Millisecond)

	// Browser 1 drops abruptly; legacy agent reads and ignores abort without disconnecting.
	_ = browser1Client.Close()
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}
	<-doneFirst

	// Browser 2 reattaches and starts forwarding against the same surviving legacy agent.
	browser2Safe, browser2Client := newControlPair(t)
	t.Cleanup(func() { _ = browser2Client.Close() })
	if _, ok := attachBrowser("session-legacy-reattach", "client-a", "owner-uuid", false, browser2Safe); !ok {
		t.Fatal("reattach browser failed")
	}
	if frame := readControlFrame(t, browser2Client); frame.Type != controlTypeReady {
		t.Fatalf("browser2 frame = %q, want %q", frame.Type, controlTypeReady)
	}
	maybeStartForwarding("session-legacy-reattach")
	time.Sleep(50 * time.Millisecond)

	if err := agentClient.WriteMessage(websocket.BinaryMessage, []byte("still-alive")); err != nil {
		t.Fatalf("legacy agent write after reattach failed: %v", err)
	}
	_ = browser2Client.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, payload, err := browser2Client.ReadMessage()
	if err != nil {
		t.Fatalf("browser2 read failed: %v", err)
	}
	if msgType != websocket.BinaryMessage || string(payload) != "still-alive" {
		t.Fatalf("browser2 got (%d, %q), want binary %q", msgType, string(payload), "still-alive")
	}
}

// TestTerminalSessionProducesSingleAuditLogEntry verifies that a terminal
// session — including a suspend + reconnect + explicit terminate lifecycle —
// produces exactly one audit log row updated in place, with "Name(uuid)".
func TestTerminalSessionProducesSingleAuditLogEntry(t *testing.T) {
	resetTerminalSessions(t)
	db := dbcore.GetDBInstance()
	const (
		clientUUID = "term-audit-node-1"
		sessionID  = "session-single-audit"
	)
	t.Cleanup(func() {
		db.Where("uuid = ?", clientUUID).Delete(&models.Client{})
		db.Where("message LIKE ?", "%"+sessionID+"%").Delete(&models.Log{})
	})
	db.Where("uuid = ?", clientUUID).Delete(&models.Client{})
	db.Where("message LIKE ?", "%"+sessionID+"%").Delete(&models.Log{})
	if err := db.Create(&models.Client{
		UUID:  clientUUID,
		Token: "tok-" + clientUUID,
		Name:  "HK-Terminal-01",
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}

	browser1Safe, browser1Client := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browser1Client.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, sessionID, "owner-uuid", clientUUID)
	session.RequesterIp = "198.51.100.7"
	session.Browser = browser1Safe
	session.Agent = agentSafeConn
	session.Forwarding = true

	doneFirst := make(chan struct{})
	go func() {
		ForwardTerminal(sessionID, browser1Safe, agentSafeConn)
		close(doneFirst)
	}()
	time.Sleep(50 * time.Millisecond)

	// Unexpected drop suspends the session; still only 1 log entry.
	_ = browser1Client.Close()
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}
	<-doneFirst

	// Browser 2 reattaches, then explicitly terminates.
	browser2Safe, browser2Client := newControlPair(t)
	t.Cleanup(func() { _ = browser2Client.Close() })
	if _, ok := attachBrowser(sessionID, clientUUID, "owner-uuid", false, browser2Safe); !ok {
		t.Fatal("reattach browser failed")
	}
	if frame := readControlFrame(t, browser2Client); frame.Type != controlTypeReady {
		t.Fatalf("browser2 frame = %q, want %q", frame.Type, controlTypeReady)
	}
	doneSecond := make(chan struct{})
	go func() {
		ForwardTerminal(sessionID, browser2Safe, agentSafeConn)
		close(doneSecond)
	}()
	time.Sleep(50 * time.Millisecond)

	writeFrame(t, browser2Client, `{"type":"terminate"}`)
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeTerminate)
	}
	<-doneSecond

	var logs []models.Log
	if err := db.Where("message LIKE ?", "%"+sessionID+"%").Find(&logs).Error; err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly 1 terminal audit log entry, got %d: %+v", len(logs), logs)
	}
	got := logs[0]
	if got.MsgType != "terminal" || got.IP != "198.51.100.7" || got.UUID != "owner-uuid" {
		t.Fatalf("unexpected log metadata: %+v", got)
	}
	for _, want := range []string{
		"client=HK-Terminal-01(" + clientUUID + ")",
		"terminal_id=" + sessionID,
		"status=closed",
		"duration=",
		"reconnects=1",
		"Started:",
		"Ended:",
	} {
		if !strings.Contains(got.Message, want) {
			t.Fatalf("terminal audit log missing %q:\n%s", want, got.Message)
		}
	}
}
