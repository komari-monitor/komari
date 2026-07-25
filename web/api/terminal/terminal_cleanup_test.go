package terminal

import "testing"

func TestCloseClientSessionsOnlyRemovesMatchingClient(t *testing.T) {
	TerminalSessionsMutex.Lock()
	original := TerminalSessions
	TerminalSessions = map[string]*TerminalSession{
		"session-a": {UUID: "client-a"},
		"session-b": {UUID: "client-b"},
	}
	TerminalSessionsMutex.Unlock()
	t.Cleanup(func() {
		TerminalSessionsMutex.Lock()
		TerminalSessions = original
		TerminalSessionsMutex.Unlock()
	})

	CloseClientSessions("client-a")
	TerminalSessionsMutex.Lock()
	defer TerminalSessionsMutex.Unlock()
	if _, exists := TerminalSessions["session-a"]; exists {
		t.Fatal("deleted client session remains")
	}
	if _, exists := TerminalSessions["session-b"]; !exists {
		t.Fatal("unrelated client session was removed")
	}
}
