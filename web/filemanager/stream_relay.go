package filemanager

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	streamTransferTTL   = 2 * time.Minute
	streamCallTimeout   = 30 * time.Minute
	streamBufferSize    = 512 * 1024
	streamTokenHeader   = "X-Komari-Transfer-Token"
	streamChunkAttempts = 3
	maxStreamTransfers  = 32
)

type streamTransferDirection string

const (
	streamUploadDirection   streamTransferDirection = "upload"
	streamDownloadDirection streamTransferDirection = "download"
)

// streamTransfer is a one-shot, bounded relay between the public browser
// request and the Agent's outbound HTTP request. io.Pipe deliberately keeps
// the relay back-pressured so neither side can queue an unbounded body.
type streamTransfer struct {
	id         string
	token      string
	clientUUID string
	direction  streamTransferDirection
	size       int64
	expiresAt  time.Time

	reader   *io.PipeReader
	writer   *io.PipeWriter
	slotHeld bool

	mu        sync.Mutex
	connected bool
	closed    bool
}

var (
	streamTransfersMu sync.Mutex
	streamTransfers   = make(map[string]*streamTransfer)
	streamCleanupOnce sync.Once
	streamBufferPool  = sync.Pool{New: func() any { return make([]byte, streamBufferSize) }}
	streamSlots       = make(chan struct{}, maxStreamTransfers)
)

type streamCopyResult struct {
	bytes int64
	err   error
}

func newStreamTransfer(ctx context.Context, clientUUID string, direction streamTransferDirection, size int64) (*streamTransfer, error) {
	streamCleanupOnce.Do(startStreamTransferCleanup)
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case streamSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	reader, writer := io.Pipe()
	transfer := &streamTransfer{
		id:         uuid.NewString(),
		token:      uuid.NewString(),
		clientUUID: clientUUID,
		direction:  direction,
		size:       size,
		expiresAt:  time.Now().Add(streamTransferTTL),
		reader:     reader,
		writer:     writer,
		slotHeld:   true,
	}
	streamTransfersMu.Lock()
	streamTransfers[transfer.id] = transfer
	streamTransfersMu.Unlock()
	return transfer, nil
}

func startStreamTransferCleanup() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			var expired []*streamTransfer
			streamTransfersMu.Lock()
			for id, transfer := range streamTransfers {
				transfer.mu.Lock()
				connected := transfer.connected
				transfer.mu.Unlock()
				if !connected && now.After(transfer.expiresAt) {
					delete(streamTransfers, id)
					expired = append(expired, transfer)
				}
			}
			streamTransfersMu.Unlock()
			for _, transfer := range expired {
				transfer.close(errors.New("file transfer expired"))
			}
		}
	}()
}

func (transfer *streamTransfer) markConnected() error {
	transfer.mu.Lock()
	defer transfer.mu.Unlock()
	if transfer.closed {
		return errors.New("file transfer is closed")
	}
	if transfer.connected {
		return errors.New("file transfer is already connected")
	}
	transfer.connected = true
	return nil
}

func (transfer *streamTransfer) close(err error) {
	if err == nil {
		err = io.EOF
	}
	transfer.mu.Lock()
	if transfer.closed {
		transfer.mu.Unlock()
		return
	}
	transfer.closed = true
	slotHeld := transfer.slotHeld
	transfer.slotHeld = false
	transfer.mu.Unlock()

	streamTransfersMu.Lock()
	if current := streamTransfers[transfer.id]; current == transfer {
		delete(streamTransfers, transfer.id)
	}
	streamTransfersMu.Unlock()
	_ = transfer.reader.CloseWithError(err)
	_ = transfer.writer.CloseWithError(err)
	if slotHeld {
		<-streamSlots
	}
}

func lookupStreamTransfer(c *gin.Context) (*streamTransfer, error) {
	id := strings.TrimSpace(c.Param("id"))
	if headerID := strings.TrimSpace(c.GetHeader("X-Komari-Transfer-ID")); headerID != "" && headerID != id {
		return nil, errors.New("file transfer id does not match request")
	}
	token := strings.TrimSpace(c.GetHeader(streamTokenHeader))
	if token == "" {
		token = strings.TrimSpace(c.Query("transfer_token"))
	}
	if id == "" || token == "" {
		return nil, errors.New("file transfer credentials are required")
	}
	clientValue, ok := c.Get("client_uuid")
	clientID, ok := clientValue.(string)
	if !ok || clientID == "" {
		return nil, errors.New("agent identity is required")
	}

	streamTransfersMu.Lock()
	transfer := streamTransfers[id]
	streamTransfersMu.Unlock()
	if transfer == nil {
		return nil, errors.New("unknown or expired file transfer")
	}
	if subtle.ConstantTimeCompare([]byte(transfer.token), []byte(token)) != 1 || transfer.clientUUID != clientID {
		return nil, errors.New("invalid file transfer credentials")
	}
	if time.Now().After(transfer.expiresAt) {
		transfer.close(errors.New("file transfer expired"))
		return nil, errors.New("file transfer expired")
	}
	return transfer, nil
}

// AgentTransfer is the data-plane endpoint used by the Agent. POST is used for
// both directions: a download carries bytes in the request body, while an
// upload receives the browser bytes in the response body. GET remains an alias
// for upload so an interrupted rollout cannot strand an already-open session.
func AgentTransfer(c *gin.Context) {
	transfer, err := lookupStreamTransfer(c)
	if err != nil {
		c.String(http.StatusUnauthorized, err.Error())
		return
	}

	switch c.Request.Method {
	case http.MethodGet:
		if transfer.direction != streamUploadDirection {
			c.String(http.StatusMethodNotAllowed, "transfer direction does not allow GET")
			return
		}
		serveUploadTransfer(c, transfer)
	case http.MethodPost:
		if transfer.direction == streamUploadDirection {
			if c.Request.ContentLength > 0 {
				transfer.close(errors.New("upload transfer request must not contain a body"))
				c.String(http.StatusBadRequest, "invalid upload transfer request")
				return
			}
			serveUploadTransfer(c, transfer)
			return
		}
		if c.Request.ContentLength >= 0 && c.Request.ContentLength != transfer.size {
			transfer.close(fmt.Errorf("download stream content length %d, want %d", c.Request.ContentLength, transfer.size))
			c.String(http.StatusBadRequest, "invalid transfer content length")
			return
		}
		if err := transfer.markConnected(); err != nil {
			c.String(http.StatusConflict, err.Error())
			return
		}
		written, copyErr := copyExactStream(transfer.writer, c.Request.Body, transfer.size, false, nil)
		if copyErr == nil && written != transfer.size {
			copyErr = fmt.Errorf("download stream ended after %d of %d bytes", written, transfer.size)
		}
		if copyErr != nil {
			_ = transfer.writer.CloseWithError(copyErr)
			transfer.close(copyErr)
			c.String(http.StatusBadRequest, copyErr.Error())
			return
		}
		_ = transfer.writer.Close()
		c.JSON(http.StatusOK, gin.H{"received": written})
		transfer.close(nil)
	default:
		c.String(http.StatusMethodNotAllowed, "method not allowed")
	}
}

func serveUploadTransfer(c *gin.Context, transfer *streamTransfer) {
	if err := transfer.markConnected(); err != nil {
		c.String(http.StatusConflict, err.Error())
		return
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(transfer.size, 10))
	c.Header("Content-Encoding", "identity")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	written, copyErr := copyExactStream(c.Writer, transfer.reader, transfer.size, true, nil)
	if copyErr == nil && written != transfer.size {
		copyErr = fmt.Errorf("upload stream ended after %d of %d bytes", written, transfer.size)
	}
	if copyErr != nil {
		transfer.close(copyErr)
		return
	}
	transfer.close(nil)
}

// streamDownloadRelay opens one bounded transfer for a logical download
// chunk, then forwards its body directly to the browser response. A new
// transfer per logical chunk keeps retries and CDN request sizes bounded while
// the pipe keeps the memory footprint independent of that chunk size.
func streamDownloadRelay(c *gin.Context, clientUUID, path string, fileSize int64, modifiedAt time.Time, start, size, chunkSize int64, commitHeaders func()) error {
	if size <= 0 {
		return nil
	}
	if chunkSize <= 0 || chunkSize > MaxTransferChunkSize {
		return fmt.Errorf("invalid download chunk size %d", chunkSize)
	}
	end := start + size
	offset := start
	chunkIndex := int64(0)
	for offset < end {
		length := min(chunkSize, end-offset)
		var chunkErr error
		for attempt := 1; attempt <= streamChunkAttempts; attempt++ {
			commit := commitHeaders
			if chunkIndex > 0 {
				commit = nil
			}
			copyResult, callErr := streamDownloadChunk(c, clientUUID, path, fileSize, modifiedAt, offset, length, chunkIndex, commit)
			if copyResult.err == nil && copyResult.bytes == length {
				chunkErr = nil
				break
			}
			if copyResult.err != nil {
				chunkErr = copyResult.err
			} else {
				chunkErr = callErr
			}
			// Once a byte of this logical chunk reached the browser, retrying
			// would overlap an already committed HTTP response. Only retry when
			// the failure happened before the first byte.
			if copyResult.bytes > 0 || attempt == streamChunkAttempts || c.Request.Context().Err() != nil {
				break
			}
			time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
		}
		if chunkErr != nil {
			return chunkErr
		}
		offset += length
		chunkIndex++
	}
	return nil
}

func streamDownloadChunk(c *gin.Context, clientUUID, path string, fileSize int64, modifiedAt time.Time, offset, length, chunkIndex int64, commitHeaders func()) (streamCopyResult, error) {
	transfer, transferErr := newStreamTransfer(c.Request.Context(), clientUUID, streamDownloadDirection, length)
	if transferErr != nil {
		return streamCopyResult{}, transferErr
	}
	defer transfer.close(nil)
	args := map[string]any{
		"transfer_id":    transfer.id,
		"transfer_token": transfer.token,
		"path":           path,
		"offset":         offset,
		"length":         length,
		"chunk_index":    chunkIndex,
		"file_size":      fileSize,
	}
	if !modifiedAt.IsZero() {
		args["modified_at"] = modifiedAt.UTC().Format(time.RFC3339Nano)
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		_, callErr := Call(ctx, clientUUID, "download_stream", args, "", CallOptions{Timeout: streamCallTimeout})
		callDone <- callErr
	}()

	copyDone := make(chan streamCopyResult, 1)
	go func() {
		written, copyErr := copyExactStream(c.Writer, transfer.reader, length, true, commitHeaders)
		if copyErr == nil && written != length {
			copyErr = fmt.Errorf("download stream ended after %d of %d bytes", written, length)
		}
		copyDone <- streamCopyResult{bytes: written, err: copyErr}
	}()

	var copyResult streamCopyResult
	var callErr error
	copyFinished, callFinished := false, false
	contextDone := c.Request.Context().Done()
	for !copyFinished || !callFinished {
		select {
		case copyResult = <-copyDone:
			copyFinished = true
			if copyResult.err != nil {
				cancel()
				transfer.close(copyResult.err)
			}
		case callErr = <-callDone:
			callFinished = true
			if callErr != nil {
				cancel()
				transfer.close(callErr)
			}
		case <-contextDone:
			cancel()
			transfer.close(c.Request.Context().Err())
			contextDone = nil
		}
	}
	if copyResult.err != nil {
		return copyResult, copyResult.err
	}
	if copyResult.bytes != length {
		return copyResult, fmt.Errorf("download stream returned %d of %d bytes", copyResult.bytes, length)
	}
	// The data body is authoritative. The Agent's final JSON ACK can be lost
	// after the exact body has already reached the browser.
	return copyResult, nil
}

// copyStream copies at most limit bytes using one pooled buffer.
func copyStream(dst io.Writer, src io.Reader, limit int64, flush bool, onFirstWrite func()) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	buffer := streamBufferPool.Get().([]byte)
	defer streamBufferPool.Put(buffer)
	reader := io.Reader(src)
	if limit > 0 {
		reader = io.LimitReader(src, limit)
	}
	var total int64
	first := true
	for {
		read, readErr := reader.Read(buffer)
		if read > 0 {
			if first && onFirstWrite != nil {
				onFirstWrite()
				first = false
			}
			written, writeErr := dst.Write(buffer[:read])
			if writeErr != nil {
				return total + int64(written), writeErr
			}
			if written != read {
				return total + int64(written), io.ErrShortWrite
			}
			total += int64(written)
			if flush {
				if flusher, ok := dst.(http.Flusher); ok {
					flusher.Flush()
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

// copyExactStream copies exactly expected bytes and probes one additional byte
// so an oversized request is rejected without forwarding the extra byte.
func copyExactStream(dst io.Writer, src io.Reader, expected int64, flush bool, onFirstWrite func()) (int64, error) {
	if expected < 0 {
		return 0, errors.New("negative expected stream size")
	}
	written, err := copyStream(dst, src, expected, flush, onFirstWrite)
	if err != nil {
		return written, err
	}
	if written != expected {
		return written, fmt.Errorf("stream ended after %d of %d bytes", written, expected)
	}
	var extra [1]byte
	read, readErr := src.Read(extra[:])
	if read > 0 {
		return written, fmt.Errorf("stream exceeds %d bytes", expected)
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return written, readErr
	}
	return written, nil
}
