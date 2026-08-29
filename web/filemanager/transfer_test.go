package filemanager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

func TestFormatDownloadContentDisposition(t *testing.T) {
	regular := formatDownloadContentDisposition("inline", "中文 文档.docx", false)
	if !strings.Contains(regular, "filename=download.docx") {
		t.Fatalf("regular disposition = %q, want an ASCII fallback", regular)
	}
	if !strings.Contains(regular, "filename*=UTF-8''") {
		t.Fatalf("regular disposition = %q, want an RFC 5987 filename", regular)
	}

	office := formatDownloadContentDisposition("inline", "中文 文档.docx", true)
	if !strings.Contains(office, "filename=download.docx") {
		t.Fatalf("Office disposition = %q, want an ASCII fallback", office)
	}
	if strings.Contains(office, "filename*=") {
		t.Fatalf("Office disposition = %q, must not include filename*", office)
	}
}

func TestRespondTransferErrorMarksUnsupportedAgentAsNonRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/file", nil)
	respondTransferError(context, errors.New("unsupported file operation: upload_stream"))
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotImplemented)
	}
	if len(context.Errors) == 0 {
		t.Fatal("transfer error was not recorded in gin context")
	}
}

func TestRespondTransferErrorPreservesPayloadTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/file", nil)
	respondTransferError(context, errors.New("download_stream: file transfer endpoint returned 413 Request Entity Too Large"))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestNextReducedTransferChunkSize(t *testing.T) {
	megabyte := int64(1024 * 1024)
	tests := []struct {
		name   string
		failed int64
		upper  int64
		want   int64
		wantOK bool
	}{
		{name: "default", failed: 25 * megabyte, upper: 25 * megabyte, want: 13 * megabyte, wantOK: true},
		{name: "second probe", failed: 13 * megabyte, upper: 25 * megabyte, want: 7 * megabyte, wantOK: true},
		{name: "floor", failed: 2 * megabyte, upper: 2 * megabyte, want: megabyte, wantOK: true},
		{name: "already at floor", failed: megabyte, upper: megabyte, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := nextReducedTransferChunkSize(test.failed, test.upper)
			if ok != test.wantOK || (ok && got != test.want) {
				t.Fatalf("nextReducedTransferChunkSize(%d, %d) = %d, %v; want %d, %v", test.failed, test.upper, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestDownloadChunkSizeRequestUsesSaferCachedValue(t *testing.T) {
	downloadChunkMu.Lock()
	downloadChunkSizes = map[string]int64{"client": 4 * 1024 * 1024}
	downloadChunkMu.Unlock()
	t.Cleanup(func() {
		downloadChunkMu.Lock()
		downloadChunkSizes = make(map[string]int64)
		downloadChunkMu.Unlock()
	})
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest(http.MethodGet, "/file?chunk_size=26214400", nil)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	if got, want := downloadChunkSizeForRequest(context, "client"), int64(4*1024*1024); got != want {
		t.Fatalf("download chunk size = %d, want %d", got, want)
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
