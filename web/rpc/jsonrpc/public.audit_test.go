package jsonrpc

import "testing"

func TestNormalizeVisitorAuditEvent(t *testing.T) {
	cases := map[string]string{
		"Page View":         "page_view",
		"node:open.detail":  "node:open.detail",
		"../../bad<script>": "....badscript",
		"":                  "",
	}

	for input, want := range cases {
		if got := normalizeVisitorAuditEvent(input); got != want {
			t.Fatalf("normalizeVisitorAuditEvent(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTrimVisitorAuditDetail(t *testing.T) {
	small := map[string]any{"route": "/", "ok": true}
	if got := trimVisitorAuditDetail(small); got == nil || got["route"] != "/" {
		t.Fatalf("expected small detail to be kept, got %#v", got)
	}

	large := map[string]any{"data": make([]byte, visitorAuditMaxDetailLen+1)}
	got := trimVisitorAuditDetail(large)
	if got == nil || got["truncated"] != true {
		t.Fatalf("expected large detail to be replaced by truncation marker, got %#v", got)
	}
}

func TestBuildVisitorAuditMessage(t *testing.T) {
	message, err := buildVisitorAuditMessage(visitorAuditMessage{Event: "page_view", Path: "/"})
	if err != nil {
		t.Fatalf("buildVisitorAuditMessage returned error: %v", err)
	}
	if message == "" || len(message) > visitorAuditMaxMessageLen {
		t.Fatalf("unexpected message length %d", len(message))
	}
}
