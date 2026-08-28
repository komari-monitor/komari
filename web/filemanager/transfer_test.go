package filemanager

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestUploadSessionLimitAndRelease(t *testing.T) {
	resetUploadSessionsForTest()
	t.Cleanup(resetUploadSessionsForTest)

	sessions := make([]*uploadSession, 0, maxConcurrentUploads)
	for index := 0; index < maxConcurrentUploads; index++ {
		session, created, err := acquireUploadSession("", "client", "/tmp/file", 100, 0)
		if err != nil || !created {
			t.Fatalf("create session %d: created=%v err=%v", index, created, err)
		}
		sessions = append(sessions, session)
	}
	if _, _, err := acquireUploadSession("", "client", "/tmp/extra", 100, 0); !errors.Is(err, ErrTooManyUploads) {
		t.Fatalf("fifth session error = %v, want %v", err, ErrTooManyUploads)
	}

	finishUploadSession(sessions[0])
	if _, created, err := acquireUploadSession("", "client", "/tmp/reused", 100, 0); err != nil || !created {
		t.Fatalf("slot was not released: created=%v err=%v", created, err)
	}
}

func TestUploadSessionMustMatchRequest(t *testing.T) {
	resetUploadSessionsForTest()
	t.Cleanup(resetUploadSessionsForTest)

	session, _, err := acquireUploadSession("", "client-a", "/tmp/file", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := acquireUploadSession(session.ID, "client-b", "/tmp/file", 100, 0); err == nil {
		t.Fatal("session accepted a different client")
	}
	if _, _, err := acquireUploadSession(session.ID, "client-a", "/tmp/other", 100, 0); err == nil {
		t.Fatal("session accepted a different path")
	}
	if _, _, err := acquireUploadSession(session.ID, "client-a", "/tmp/file", 101, 0); err == nil {
		t.Fatal("session accepted a different size")
	}
	if _, _, err := acquireUploadSession(session.ID, "client-a", "", 0, 0); err != nil {
		t.Fatalf("continuation request was rejected: %v", err)
	}
}

func TestParseSingleByteRange(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		size       int64
		start, end int64
		ok         bool
	}{
		{name: "closed", header: "bytes=2-5", size: 10, start: 2, end: 5, ok: true},
		{name: "open", header: "bytes=7-", size: 10, start: 7, end: 9, ok: true},
		{name: "suffix", header: "bytes=-3", size: 10, start: 7, end: 9, ok: true},
		{name: "clamped", header: "bytes=8-99", size: 10, start: 8, end: 9, ok: true},
		{name: "multiple", header: "bytes=0-1,4-5", size: 10, ok: false},
		{name: "outside", header: "bytes=10-", size: 10, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, ok := parseSingleByteRange(test.header, test.size)
			if ok != test.ok || (ok && (start != test.start || end != test.end)) {
				t.Fatalf("parseSingleByteRange(%q, %d) = %d-%d, %v", test.header, test.size, start, end, ok)
			}
		})
	}
}

func TestIfRangeAllowsRange(t *testing.T) {
	modifiedAt := time.Date(2026, time.August, 28, 12, 0, 0, 123456789, time.UTC)
	etag := `"100-200"`
	date := modifiedAt.Format(http.TimeFormat)

	for _, test := range []struct {
		name  string
		value string
		allow bool
	}{
		{name: "missing", value: "", allow: true},
		{name: "matching etag", value: etag, allow: true},
		{name: "matching date", value: date, allow: true},
		{name: "stale date", value: modifiedAt.Add(-time.Minute).Format(http.TimeFormat), allow: false},
		{name: "invalid", value: "not-a-validator", allow: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ifRangeAllowsRange(test.value, etag, modifiedAt); got != test.allow {
				t.Fatalf("ifRangeAllowsRange(%q) = %v, want %v", test.value, got, test.allow)
			}
		})
	}
}

func resetUploadSessionsForTest() {
	uploadMu.Lock()
	uploadSessions = make(map[string]*uploadSession)
	for len(uploadSlots) > 0 {
		<-uploadSlots
	}
	uploadMu.Unlock()
}
