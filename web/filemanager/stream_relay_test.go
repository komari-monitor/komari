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

func TestAgentTransferDownloadKeepsNormalEOFForSlowConsumer(t *testing.T) {
	data := bytes.Repeat([]byte("slow-consumer-"), 4096)
	transfer, err := newStreamTransfer(context.Background(), "slow-agent", streamDownloadDirection, int64(len(data)))
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
	got := make([]byte, 0, len(data))
	buffer := make([]byte, 257)
	for {
		read, readErr := transfer.reader.Read(buffer)
		if read > 0 {
			got = append(got, buffer[:read]...)
			time.Sleep(time.Microsecond)
		}
		if readErr != nil {
			if readErr != io.EOF {
				t.Fatalf("slow consumer read: %v", readErr)
			}
			break
		}
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
		t.Fatalf("body length = %d, want %d", len(got), len(data))
	}
}

func TestAgentTransferRelaysLargeBodyOverHTTP(t *testing.T) {
	testAgentTransferOverHTTP(t, false)
}

func TestAgentTransferRelaysLargeBodyOverHTTP2(t *testing.T) {
	testAgentTransferOverHTTP(t, true)
}

func testAgentTransferOverHTTP(t *testing.T, http2 bool) {
	t.Helper()
	data := bytes.Repeat([]byte("komari-stream-"), (6*1024*1024)/len("komari-stream-"))
	transfer, err := newStreamTransfer(context.Background(), "large-agent", streamUploadDirection, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transfer.close(nil) })

	router := gin.New()
	router.POST("/api/clients/transfer/:id", func(c *gin.Context) {
		c.Set("client_uuid", transfer.clientUUID)
		AgentTransfer(c)
	})
	server := httptest.NewUnstartedServer(router)
	if http2 {
		server.EnableHTTP2 = true
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()
	client := server.Client()
	endpoint := server.URL + "/api/clients/transfer/" + transfer.id + "?transfer_token=" + url.QueryEscape(transfer.token)

	writeDone := make(chan struct{})
	go func() {
		_, _ = transfer.writer.Write(data)
		_ = transfer.writer.Close()
		close(writeDone)
	}()
	request, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("large stream writer did not finish")
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if response.ContentLength != int64(len(data)) {
		t.Fatalf("content length = %d, want %d", response.ContentLength, len(data))
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("body length = %d, want %d", len(got), len(data))
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
