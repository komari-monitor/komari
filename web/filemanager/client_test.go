package filemanager

import (
	"testing"

	v2 "github.com/komari-monitor/komari/protocol/v2"
)

func TestResolveMatchesRequestAndClient(t *testing.T) {
	pendingMu.Lock()
	pending = make(map[string]pendingCall)
	response := make(chan v2.FileResult, 1)
	pending["request"] = pendingCall{uuid: "client-a", response: response}
	pendingMu.Unlock()

	if Resolve(v2.FileResult{UUID: "client-b", RequestID: "request", OK: true}) {
		t.Fatal("resolved a response from the wrong client")
	}
	if !Resolve(v2.FileResult{UUID: "client-a", RequestID: "request", OK: true}) {
		t.Fatal("did not resolve the matching response")
	}
	result := <-response
	if result.UUID != "client-a" || !result.OK {
		t.Fatalf("unexpected response: %+v", result)
	}
	if Resolve(v2.FileResult{UUID: "client-a", RequestID: "request", OK: true}) {
		t.Fatal("resolved the same request twice")
	}
}
