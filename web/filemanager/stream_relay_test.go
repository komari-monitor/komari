package filemanager

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newAgentTransferTestContext(method, transferID, token, clientUUID string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/clients/transfer/"+url.PathEscape(transferID)+"?transfer_token="+url.QueryEscape(token), body)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = request
	context.Params = gin.Params{{Key: "id", Value: transferID}}
	context.Set("client_uuid", clientUUID)
	return context, recorder
}

func TestAgentTransferUploadRelaysBody(t *testing.T) {
	data := []byte("streamed upload data")
	transfer, err := newStreamTransfer(context.Background(), "agent-upload", streamUploadDirection, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transfer.close(nil) })
	context, recorder := newAgentTransferTestContext(http.MethodPost, transfer.id, transfer.token, transfer.clientUUID, nil)
	done := make(chan struct{})
	go func() {
		AgentTransfer(context)
		close(done)
	}()
	go func() {
		_, _ = transfer.writer.Write(data)
		_ = transfer.writer.Close()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AgentTransfer did not finish")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !bytes.Equal(recorder.Body.Bytes(), data) {
		t.Fatalf("body = %q, want %q", recorder.Body.Bytes(), data)
	}
}

func TestAgentTransferDownloadRelaysBody(t *testing.T) {
	data := []byte("streamed download data")
	transfer, err := newStreamTransfer(context.Background(), "agent-download", streamDownloadDirection, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transfer.close(nil) })
	context, recorder := newAgentTransferTestContext(http.MethodPost, transfer.id, transfer.token, transfer.clientUUID, bytes.NewReader(data))
	done := make(chan struct{})
	go func() {
		AgentTransfer(context)
		close(done)
	}()
	got, readErr := io.ReadAll(transfer.reader)
	if readErr != nil {
		t.Fatalf("read relayed body: %v", readErr)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AgentTransfer did not finish")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("body = %q, want %q", got, data)
	}
}

func TestCopyExactStreamRejectsOversizedBody(t *testing.T) {
	var destination bytes.Buffer
	written, err := copyExactStream(&destination, bytes.NewReader([]byte("12345")), 4, false, nil)
	if err == nil {
		t.Fatal("copyExactStream accepted an oversized body")
	}
	if written != 4 {
		t.Fatalf("written = %d, want 4", written)
	}
	if got := destination.String(); got != "1234" {
		t.Fatalf("destination = %q, want %q", got, "1234")
	}
}
