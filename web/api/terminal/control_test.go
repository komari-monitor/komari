package terminal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	agent_runtime "github.com/komari-monitor/komari/web/agent"
	"github.com/komari-monitor/komari/web/connection"
)

// newControlPair wires a real server/client WebSocket pair so the control
// messages are exercised through SafeConn exactly as they are in production.
func newControlPair(t *testing.T) (*connection.SafeConn, *websocket.Conn) {
	t.Helper()

	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		accepted <- conn
	}))
	t.Cleanup(server.Close)

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	serverConn := <-accepted
	t.Cleanup(func() { _ = serverConn.Close() })

	return connection.NewSafeConn(serverConn), client
}

// readControlFrame reads one frame and requires it to be a JSON control
// message. Raw terminal text on the wire would fail here, which is the point:
// the browser distinguishes output from control messages by frame type.
func readControlFrame(t *testing.T, client *websocket.Conn) controlFrame {
	t.Helper()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("reading control frame failed: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("control frame message type = %d, want text", messageType)
	}
	var frame controlFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatalf("control frame is not JSON (%q): %v", string(data), err)
	}
	return frame
}

// TestControlFramesAreJSONAndTyped pins the wire format of every message the
// server sends to a browser.
func TestControlFramesAreJSONAndTyped(t *testing.T) {
	safeConn, client := newControlPair(t)

	cases := []controlFrame{
		{Type: controlTypeAssigned, RequestID: "abc"},
		{Type: controlTypeReady},
		{Type: controlTypeAbort},
		{Type: controlTypeTerminate},
	}
	for _, want := range cases {
		writeControl(safeConn, want)
		if got := readControlFrame(t, client); got != want {
			t.Fatalf("control frame = %+v, want %+v", got, want)
		}
	}
}

// TestAttachAgentNotifiesBrowser is the regression guard for the waiting toast:
// the agent attaching must produce a ready frame, otherwise the browser has no
// event to drop its "waiting for agent" hint.
func TestAttachAgentNotifiesBrowser(t *testing.T) {
	resetTerminalSessions(t)

	browserServerConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = agentClient.Close() })

	newSession(t, "session-notify", "owner-uuid", "client-a")
	if _, ok := attachBrowser("session-notify", "client-a", "owner-uuid", false, browserServerConn); !ok {
		t.Fatal("owner could not attach to their own session")
	}
	if _, ok := attachAgent("session-notify", agentSafeConn); !ok {
		t.Fatal("agent could not attach to the session")
	}

	if frame := readControlFrame(t, browserClient); frame.Type != controlTypeReady {
		t.Fatalf("frame after agent attach = %q, want %q", frame.Type, controlTypeReady)
	}
}

// TestAttachBrowserNotifiesReattachedAgent covers the reconnect path: a browser
// reattaching to a session whose agent is still attached must be told
// immediately, otherwise its waiting toast would hang until output arrives.
func TestAttachBrowserNotifiesReattachedAgent(t *testing.T) {
	resetTerminalSessions(t)

	browserServerConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = agentClient.Close() })

	newSession(t, "session-reattach", "owner-uuid", "client-a")
	if _, ok := attachAgent("session-reattach", agentSafeConn); !ok {
		t.Fatal("agent could not attach to the session")
	}
	if _, ok := attachBrowser("session-reattach", "client-a", "owner-uuid", false, browserServerConn); !ok {
		t.Fatal("owner could not reattach")
	}

	if frame := readControlFrame(t, browserClient); frame.Type != controlTypeReady {
		t.Fatalf("frame on reattach = %q, want %q", frame.Type, controlTypeReady)
	}
}

// TestSuspendBrowserAbortsAgent is the core of the abort/terminate split: when
// the browser drops unexpectedly, the surviving agent is told the session is
// suspended (abort) rather than ended (terminate), and the session survives.
func TestSuspendBrowserAbortsAgent(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })

	session := newSession(t, "session-abort", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn

	suspendBrowser("session-abort", browserSafeConn)

	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeAbort {
		t.Fatalf("agent frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-abort"]
	sessionStillHasAgent := TerminalSessions["session-abort"].Agent != nil
	TerminalSessionsMutex.Unlock()
	if !present {
		t.Fatal("suspendBrowser removed the session instead of retaining it")
	}
	if !sessionStillHasAgent {
		t.Fatal("suspendBrowser cleared the agent side")
	}
}

// TestSuspendAgentAbortsBrowser is the mirror case: the agent dropping must
// abort the browser and re-dispatch the terminal request so the agent reconnects.
func TestSuspendAgentAbortsBrowser(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = agentClient.Close() })

	agent_runtime.MarkV2Client("client-a")
	_ = agent_runtime.TakeV2Events("client-a", nil, 0)

	session := newSession(t, "session-abort2", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn

	suspendAgent("session-abort2", agentSafeConn)

	if frame := readControlFrame(t, browserClient); frame.Type != controlTypeAbort {
		t.Fatalf("browser frame = %q, want %q", frame.Type, controlTypeAbort)
	}

	events := agent_runtime.TakeV2Events("client-a", nil, 0)
	if len(events) == 0 || events[len(events)-1].Method != v2.MethodAgentTerminal {
		t.Fatalf("expected agent.terminal.request event after suspendAgent, got %+v", events)
	}

	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-abort2"]
	TerminalSessionsMutex.Unlock()
	if !present {
		t.Fatal("suspendAgent removed the session instead of retaining it")
	}
}

// TestCloseSessionTerminatesBothSides verifies terminate reaches both peers and
// that the session is removed immediately, unlike suspend.
func TestCloseSessionTerminatesBothSides(t *testing.T) {
	resetTerminalSessions(t)

	browserSafeConn, browserClient := newControlPair(t)
	agentSafeConn, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browserClient.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, "session-end", "owner-uuid", "client-a")
	session.Browser = browserSafeConn
	session.Agent = agentSafeConn

	closeSession("session-end")

	if frame := readControlFrame(t, browserClient); frame.Type != controlTypeTerminate {
		t.Fatalf("browser frame = %q, want terminate", frame.Type)
	}
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
		t.Fatalf("agent frame = %q, want terminate", frame.Type)
	}

	TerminalSessionsMutex.Lock()
	_, present := TerminalSessions["session-end"]
	TerminalSessionsMutex.Unlock()
	if present {
		t.Fatal("closeSession left the session in the map")
	}
}

// TestWriteControlIgnoresNilConn ensures a session whose peer already went away
// does not panic.
func TestWriteControlIgnoresNilConn(t *testing.T) {
	notifyAssigned(nil, "abc")
	notifyReady(nil)
	notifyAbort(nil)
	notifyTerminate(nil)
}

// readAvailableControlFrames drains control frames for a short bounded window.
// It is used only after all server-side control writes in an interleaving have
// completed, so a missing frame is evidence of absence rather than slowness.
func readAvailableControlFrames(client *websocket.Conn, window time.Duration) []controlFrame {
	frames := make([]controlFrame, 0, 4)
	_ = client.SetReadDeadline(time.Now().Add(window))
	for {
		_, data, err := client.ReadMessage()
		if err != nil {
			break
		}
		if frame, ok := parseControl(data); ok {
			frames = append(frames, frame)
		}
	}
	return frames
}

func assertNoReadyThenAbort(t *testing.T, frames []controlFrame) {
	t.Helper()
	seenReady := false
	for _, frame := range frames {
		if frame.Type == controlTypeReady {
			seenReady = true
		}
		if seenReady && frame.Type == controlTypeAbort {
			t.Fatalf("stale abort followed ready in control sequence: %+v", frames)
		}
	}
}

// TestConcurrentBrowserReattachDoesNotOverwriteReady is a bidirectional
// interleaving regression test for MF-2. An abort caused by B1 may precede the
// new ready, but it must never be written after that ready.
func TestConcurrentBrowserReattachDoesNotOverwriteReady(t *testing.T) {
	for trial := 0; trial < 15; trial++ {
		resetTerminalSessions(t)
		id := "session-interleave-browser"
		browser1Safe, browser1Client := newControlPair(t)
		agentSafe, agentClient := newControlPair(t)
		browser2Safe, browser2Client := newControlPair(t)
		t.Cleanup(func() { _ = browser1Client.Close() })
		t.Cleanup(func() { _ = browser2Client.Close() })
		t.Cleanup(func() { _ = agentClient.Close() })

		session := newSession(t, id, "owner-uuid", "client-a")
		session.Browser = browser1Safe
		session.BrowserClientID = "tab-1"
		session.Agent = agentSafe

		suspendDone := make(chan struct{})
		go func() {
			suspendBrowser(id, browser1Safe)
			close(suspendDone)
		}()
		attachDone := make(chan bool, 1)
		go func() {
			_, _, ok := attachBrowserWithClientID(id, "client-a", "owner-uuid", false, browser2Safe, "tab-1")
			attachDone <- ok
		}()
		<-suspendDone
		if ok := <-attachDone; !ok {
			t.Fatalf("trial %d: browser reattach failed", trial)
		}
		assertNoReadyThenAbort(t, readAvailableControlFrames(agentClient, 15*time.Millisecond))
	}
}

// TestConcurrentAgentReattachDoesNotOverwriteReady is the mirror interleaving:
// after the browser receives ready for a replacement agent, no stale abort from
// the old agent's suspend path may overtake it.
func TestConcurrentAgentReattachDoesNotOverwriteReady(t *testing.T) {
	for trial := 0; trial < 15; trial++ {
		resetTerminalSessions(t)
		id := "session-interleave-agent"
		browserSafe, browserClient := newControlPair(t)
		agent1Safe, agent1Client := newControlPair(t)
		agent2Safe, agent2Client := newControlPair(t)
		t.Cleanup(func() { _ = browserClient.Close() })
		t.Cleanup(func() { _ = agent1Client.Close() })
		t.Cleanup(func() { _ = agent2Client.Close() })

		agent_runtime.MarkV2Client("client-a")
		_ = agent_runtime.TakeV2Events("client-a", nil, 0)
		session := newSession(t, id, "owner-uuid", "client-a")
		session.Browser = browserSafe
		session.Agent = agent1Safe

		suspendDone := make(chan struct{})
		go func() {
			suspendAgent(id, agent1Safe)
			close(suspendDone)
		}()
		attachDone := make(chan bool, 1)
		go func() {
			_, ok := attachAgent(id, agent2Safe)
			attachDone <- ok
		}()
		<-suspendDone
		if ok := <-attachDone; !ok {
			t.Fatalf("trial %d: agent reattach failed", trial)
		}
		assertNoReadyThenAbort(t, readAvailableControlFrames(browserClient, 15*time.Millisecond))
	}
}

// TestCloseSessionIfCurrentIgnoresStaleTerminate checks that a terminate from
// a replaced browser generation cannot close the reattached session, while the
// current browser generation still can.
func TestCloseSessionIfCurrentIgnoresStaleTerminate(t *testing.T) {
	resetTerminalSessions(t)
	id := "session-stale-terminate"
	browser1Safe, browser1Client := newControlPair(t)
	browser2Safe, browser2Client := newControlPair(t)
	agentSafe, agentClient := newControlPair(t)
	t.Cleanup(func() { _ = browser1Client.Close() })
	t.Cleanup(func() { _ = browser2Client.Close() })
	t.Cleanup(func() { _ = agentClient.Close() })

	session := newSession(t, id, "owner-uuid", "client-a")
	session.Browser = browser1Safe
	session.BrowserClientID = "tab-1"
	session.Agent = agentSafe

	if _, _, ok := attachBrowserWithClientID(id, "client-a", "owner-uuid", false, browser2Safe, "tab-1"); !ok {
		t.Fatal("browser reattach failed")
	}
	if frame := readControlFrame(t, browser2Client); frame.Type != controlTypeReady {
		t.Fatalf("browser2 frame = %q, want ready", frame.Type)
	}
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeReady {
		t.Fatalf("agent frame = %q, want ready", frame.Type)
	}

	closeSessionIfCurrent(id, browser1Safe)
	TerminalSessionsMutex.Lock()
	present := TerminalSessions[id] == session
	TerminalSessionsMutex.Unlock()
	if !present {
		t.Fatal("stale browser terminate closed the reattached session")
	}
	closeSessionIfCurrent(id, browser2Safe)
	if frame := readControlFrame(t, agentClient); frame.Type != controlTypeTerminate {
		t.Fatalf("agent frame after current terminate = %q, want terminate", frame.Type)
	}
}

// TestParseControl pins how inbound text frames are classified: only JSON with
// a type is a control message, everything else is terminal content.
func TestParseControl(t *testing.T) {
	cases := []struct {
		name string
		data string
		ok   bool
		typ  string
	}{
		{"heartbeat", `{"type":"heartbeat"}`, true, controlTypeHeartbeat},
		{"terminate", `{"type":"terminate"}`, true, controlTypeTerminate},
		{"resize", `{"type":"resize","cols":80,"rows":24}`, true, controlTypeResize},
		{"unknown type", `{"type":"whatever"}`, true, "whatever"},
		{"no type", `{"cols":80}`, false, ""},
		{"not json", "ls -la\r", false, ""},
		{"empty", "", false, ""},
	}
	for _, tc := range cases {
		frame, ok := parseControl([]byte(tc.data))
		if ok != tc.ok {
			t.Fatalf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
		}
		if ok && frame.Type != tc.typ {
			t.Fatalf("%s: type = %q, want %q", tc.name, frame.Type, tc.typ)
		}
	}
}
