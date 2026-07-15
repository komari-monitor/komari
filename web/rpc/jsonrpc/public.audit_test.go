package jsonrpc

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

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

	got := normalizeVisitorAuditEvent(strings.Repeat("访", visitorAuditMaxEventLen+10))
	if utf8.RuneCountInString(got) != visitorAuditMaxEventLen || !utf8.ValidString(got) {
		t.Fatalf("expected a valid %d-rune event, got %q", visitorAuditMaxEventLen, got)
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
	message, err := buildVisitorAuditMessage(visitorAuditMessage{
		Event:     "page_view",
		Path:      strings.Repeat("\x01中文", visitorAuditMaxPathLen),
		Route:     strings.Repeat("路由", visitorAuditMaxRouteLen),
		Target:    strings.Repeat("目标", visitorAuditMaxTargetLen),
		UserAgent: strings.Repeat("浏览器", visitorAuditMaxUserAgentLen),
		Detail:    map[string]any{"data": strings.Repeat("详情", visitorAuditMaxDetailLen)},
	})
	if err != nil {
		t.Fatalf("buildVisitorAuditMessage returned error: %v", err)
	}
	if message == "" || len(message) > visitorAuditMaxMessageLen || !utf8.ValidString(message) {
		t.Fatalf("unexpected message length %d", len(message))
	}
	encoded, ok := strings.CutPrefix(message, visitorAuditMessagePrefix)
	if !ok {
		t.Fatalf("message missing prefix: %q", message)
	}
	var decoded visitorAuditMessage
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("message JSON is invalid: %v", err)
	}
	if decoded.Event != "page_view" {
		t.Fatalf("unexpected event after bounding message: %q", decoded.Event)
	}
}

func TestBuildVisitorAuditMessageBoundsUserAgentByRunes(t *testing.T) {
	message, err := buildVisitorAuditMessage(visitorAuditMessage{
		Event:     "page_view",
		UserAgent: strings.Repeat("访", visitorAuditMaxUserAgentLen+20),
	})
	if err != nil {
		t.Fatalf("buildVisitorAuditMessage returned error: %v", err)
	}
	encoded, _ := strings.CutPrefix(message, visitorAuditMessagePrefix)
	var decoded visitorAuditMessage
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("message JSON is invalid: %v", err)
	}
	if got := utf8.RuneCountInString(decoded.UserAgent); got != visitorAuditMaxUserAgentLen {
		t.Fatalf("user agent length = %d, want %d", got, visitorAuditMaxUserAgentLen)
	}
}

func TestTruncateStringPreservesUTF8(t *testing.T) {
	got := truncateString("中文路径测试", 4)
	if got != "中..." || !utf8.ValidString(got) {
		t.Fatalf("truncateString returned %q", got)
	}
}

func TestVisitorAuditRateLimiter(t *testing.T) {
	limiter := newVisitorAuditRateLimiter()
	now := time.Unix(1000, 0)
	for i := 0; i < visitorAuditRateBurst; i++ {
		if !limiter.Allow("192.0.2.1", now) {
			t.Fatalf("request %d within burst was rejected", i+1)
		}
	}
	if limiter.Allow("192.0.2.1", now) {
		t.Fatal("request over burst was allowed")
	}
	if !limiter.Allow("192.0.2.1", now.Add(2*time.Second)) {
		t.Fatal("token was not refilled at 30 requests per minute")
	}

	cleanupAt := now.Add(2*time.Second + visitorAuditLimiterEntryTTL)
	if !limiter.Allow("192.0.2.2", cleanupAt) {
		t.Fatal("request from a new IP was rejected")
	}
	if _, ok := limiter.entries["192.0.2.1"]; ok {
		t.Fatal("stale limiter entry was not cleaned up")
	}
}

func TestVisitorAuditRateLimiterCapsTrackedIPs(t *testing.T) {
	limiter := newVisitorAuditRateLimiter()
	now := time.Unix(1000, 0)
	for i := 0; i < visitorAuditRateMaxEntries; i++ {
		if !limiter.Allow(strconv.Itoa(i), now) {
			t.Fatalf("IP %d was rejected before the limiter reached capacity", i)
		}
	}
	if limiter.Allow("overflow", now) {
		t.Fatal("new IP was allowed after the limiter reached capacity")
	}
	if !limiter.Allow("0", now) {
		t.Fatal("tracked IP was rejected after the limiter reached capacity")
	}
}
