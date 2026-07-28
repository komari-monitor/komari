package notifier

import "testing"

func TestUpdateOnlineStateTracksConnectionBeforeNotificationsAreEnabled(t *testing.T) {
	clientID := "notification-disabled-online-client"
	clientStates.Delete(clientID)
	t.Cleanup(func() { clientStates.Delete(clientID) })

	if shouldNotify := updateOnlineState(clientID, 42); shouldNotify {
		t.Fatal("first connection should not send an online notification")
	}

	state := getOrInitState(clientID)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.connectionID != 42 {
		t.Fatalf("connectionID = %d, want 42", state.connectionID)
	}
	if state.isFirstConnection {
		t.Fatal("first connection should be recorded")
	}
	if !state.isConnExist {
		t.Fatal("connection should be marked as present")
	}
}
