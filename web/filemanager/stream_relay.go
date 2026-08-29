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
	logger "github.com/komari-monitor/komari/utils/log"
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
	success := err == nil
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
	if success {
		// A successful producer already closes the PipeWriter after the exact
		// byte count. Do not close the reader side here: a consumer may still be
		// draining the final bytes, and CloseWithError would turn a normal EOF
		// into io.ErrClosedPipe. The writer's EOF is enough to finish readers.
		_ = transfer.writer.CloseWithError(io.EOF)
	} else {
		_ = transfer.reader.CloseWithError(err)
		_ = transfer.writer.CloseWithError(err)
	}
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
		logger.Warnf("file-transfer", "agent stream authentication failed method=%s path=%s remote=%s: %v", c.Request.Method, c.Request.URL.Path, c.ClientIP(), err)
		_ = c.Error(err)
		c.String(http.StatusUnauthorized, err.Error())
		return
	}

	switch c.Request.Method {
	case http.MethodGet:
		if transfer.direction != streamUploadDirection {
			logger.Warnf("file-transfer", "agent stream method rejected id=%s direction=%s method=%s", transfer.id, transfer.direction, c.Request.Method)
			c.String(http.StatusMethodNotAllowed, "transfer direction does not allow GET")
			return
		}
		serveUploadTransfer(c, transfer)
	case http.MethodPost:
		if transfer.direction == streamUploadDirection {
			if c.Request.ContentLength > 0 {
				transfer.close(errors.New("upload transfer request must not contain a body"))
				logger.Warnf("file-transfer", "agent upload stream sent an unexpected request body id=%s content_length=%d", transfer.id, c.Request.ContentLength)
				_ = c.Error(errors.New("invalid upload transfer request"))
				c.String(http.StatusBadRequest, "invalid upload transfer request")
				return
			}
			serveUploadTransfer(c, transfer)
			return
		}
		if c.Request.ContentLength >= 0 && c.Request.ContentLength != transfer.size {
			transfer.close(fmt.Errorf("download stream content length %d, want %d", c.Request.ContentLength, transfer.size))
			logger.Warnf("file-transfer", "agent download stream length mismatch id=%s got=%d want=%d", transfer.id, c.Request.ContentLength, transfer.size)
			_ = c.Error(fmt.Errorf("download stream content length %d, want %d", c.Request.ContentLength, transfer.size))
			c.String(http.StatusBadRequest, "invalid transfer content length")
			return
		}
		if err := transfer.markConnected(); err != nil {
			logger.Warnf("file-transfer", "agent download stream connection rejected id=%s: %v", transfer.id, err)
			_ = c.Error(err)
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
			logger.Errorf("file-transfer", "agent download stream failed id=%s received=%d want=%d: %v", transfer.id, written, transfer.size, copyErr)
			_ = c.Error(copyErr)
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
		logger.Warnf("file-transfer", "agent upload stream connection rejected id=%s: %v", transfer.id, err)
		_ = c.Error(err)
		c.String(http.StatusConflict, err.Error())
		return
	}
	committed := false
	commitHeaders := func() {
		if committed {
			return
		}
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Length", strconv.FormatInt(transfer.size, 10))
		c.Header("Content-Encoding", "identity")
		c.Header("Cache-Control", "no-store, no-transform")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		committed = true
	}
	written, copyErr := copyExactStream(c.Writer, transfer.reader, transfer.size, true, commitHeaders)
	if copyErr == nil && written != transfer.size {
		copyErr = fmt.Errorf("upload stream ended after %d of %d bytes", written, transfer.size)
	}
	if copyErr != nil {
		transfer.close(copyErr)
		logger.Errorf("file-transfer", "agent upload stream failed id=%s sent=%d want=%d: %v", transfer.id, written, transfer.size, copyErr)
		_ = c.Error(copyErr)
		if !committed {
			c.String(http.StatusBadRequest, copyErr.Error())
		}
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
		rememberDownloadChunkSize(clientUUID, chunkSize)
		return nil
	}
	if chunkSize <= 0 || chunkSize > MaxTransferChunkSize {
		return fmt.Errorf("invalid download chunk size %d", chunkSize)
	}
	end := start + size
	offset := start
	chunkIndex := int64(0)
	currentChunkSize := chunkSize
	c.Set(downloadChunkSizeContextKey, currentChunkSize)
	// Keep the upper bound from the most recent 413 response. The lower bound
	// is the configured 1 MiB floor; each retry chooses the midpoint so a
	// restrictive proxy is discovered in logarithmic time.
	adaptiveUpperBound := chunkSize
	for offset < end {
		length := min(currentChunkSize, end-offset)
		var chunkErr error
		var lastCopyResult streamCopyResult
		for {
			resized := false
			chunkErr = nil
			for attempt := 1; attempt <= streamChunkAttempts; attempt++ {
				commit := commitHeaders
				if chunkIndex > 0 {
					commit = nil
				}
				copyResult, streamErr := streamDownloadChunk(c, clientUUID, path, fileSize, modifiedAt, offset, length, chunkIndex, commit)
				lastCopyResult = copyResult
				if copyResult.err == nil && copyResult.bytes == length {
					// The request has proven that this logical block size is
					// accepted. Remember it immediately so concurrent/follow-up
					// downloads do not repeat a known probe.
					rememberDownloadChunkSize(clientUUID, currentChunkSize)
					chunkErr = nil
					break
				}
				// streamDownloadChunk returns the Agent/RPC error separately when a
				// relay pipe was closed as a consequence of that error. Prefer it so
				// the caller sees the real status/body instead of io.ErrClosedPipe.
				chunkErr = streamErr
				if chunkErr == nil {
					chunkErr = copyResult.err
				}
				if chunkErr == nil {
					chunkErr = fmt.Errorf("download chunk %d returned %d of %d bytes", chunkIndex, copyResult.bytes, length)
				}
				logger.Warnf("file-transfer", "download chunk failed client=%s path=%q chunk=%d offset=%d length=%d attempt=%d/%d bytes=%d: %v", clientUUID, path, chunkIndex, offset, length, attempt, streamChunkAttempts, copyResult.bytes, chunkErr)

				// A 413 is a deterministic request-size rejection. Retry the same
				// offset with a smaller logical block before considering normal
				// transient retry limits. No browser bytes may have been committed
				// for this chunk when this branch is taken.
				if copyResult.bytes == 0 && isPayloadTooLargeError(chunkErr) {
					adaptiveUpperBound = min(adaptiveUpperBound, length)
					if next, ok := nextReducedTransferChunkSize(length, adaptiveUpperBound); ok && next < length {
						previousLength := length
						currentChunkSize = next
						c.Set(downloadChunkSizeContextKey, currentChunkSize)
						length = min(currentChunkSize, end-offset)
						logger.Warnf("file-transfer", "reducing download chunk size after 413 client=%s path=%q from=%d to=%d", clientUUID, path, previousLength, currentChunkSize)
						resized = true
						break
					}
				}
				if copyResult.bytes == 0 && isNonRetryableStreamError(chunkErr) {
					return chunkErr
				}
				if copyResult.bytes == 0 && attempt == streamChunkAttempts && isChunkTransportError(chunkErr) {
					adaptiveUpperBound = min(adaptiveUpperBound, length)
					if next, ok := nextReducedTransferChunkSize(length, adaptiveUpperBound); ok && next < length {
						previousLength := length
						currentChunkSize = next
						c.Set(downloadChunkSizeContextKey, currentChunkSize)
						length = min(currentChunkSize, end-offset)
						logger.Warnf("file-transfer", "reducing download chunk size after transport failure client=%s path=%q from=%d to=%d: %v", clientUUID, path, previousLength, currentChunkSize, chunkErr)
						resized = true
						break
					}
				}
				// Once a byte of this logical chunk reached the browser, retrying
				// would overlap an already committed HTTP response. Only retry when
				// the failure happened before the first byte.
				if copyResult.bytes > 0 || attempt == streamChunkAttempts || c.Request.Context().Err() != nil {
					break
				}
				time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
			}
			if resized {
				// Retry this offset using the newly selected block size. The
				// attempt counter is intentionally reset for the resized request.
				continue
			}
			break
		}
		if chunkErr != nil {
			// A downstream browser/proxy can close a long response after some
			// bytes were delivered. The current HTTP response cannot be resumed,
			// but the next Range request can avoid repeating the same oversized
			// logical block by remembering a smaller probe size.
			if lastCopyResult.bytes > 0 && isClientDisconnectError(lastCopyResult.err) {
				if next, ok := nextReducedTransferChunkSize(currentChunkSize, currentChunkSize); ok && next < currentChunkSize {
					rememberDownloadChunkSize(clientUUID, next)
					logger.Warnf("file-transfer", "reducing download chunk size after downstream disconnect client=%s from=%d to=%d", clientUUID, currentChunkSize, next)
				}
			}
			return chunkErr
		}
		offset += length
		chunkIndex++
	}
	rememberDownloadChunkSize(clientUUID, currentChunkSize)
	return nil
}

// nextReducedTransferChunkSize returns the next 1 MiB-aligned midpoint below
// a rejected request size. upperBound is exclusive and is tightened after
// every 413, allowing the caller to converge without probing below 1 MiB.
func nextReducedTransferChunkSize(failedSize, upperBound int64) (int64, bool) {
	if failedSize <= MinTransferChunkSize {
		return 0, false
	}
	high := failedSize
	if upperBound > 0 && upperBound < high {
		high = upperBound
	}
	if high <= MinTransferChunkSize {
		return MinTransferChunkSize, true
	}
	candidate := MinTransferChunkSize + (high-MinTransferChunkSize)/2
	candidate = (candidate / MinTransferChunkSize) * MinTransferChunkSize
	if candidate < MinTransferChunkSize {
		candidate = MinTransferChunkSize
	}
	if candidate >= failedSize {
		candidate = failedSize - 1
	}
	if candidate < MinTransferChunkSize {
		return 0, false
	}
	return candidate, true
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
		_, callErr := Call(ctx, clientUUID, "download_stream", args, CallOptions{Timeout: streamCallTimeout})
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
	// The bytes already delivered to the browser are authoritative. The Agent
	// may report an error only while reading the final JSON acknowledgement
	// after the complete body has arrived (for example after a proxy reset).
	if copyResult.err == nil && copyResult.bytes == length {
		return copyResult, nil
	}
	if copyResult.err != nil {
		if callErr != nil && isRelayPipeError(copyResult.err) {
			return copyResult, callErr
		}
		return copyResult, copyResult.err
	}
	if callErr != nil {
		return copyResult, callErr
	}
	if copyResult.bytes != length {
		return copyResult, fmt.Errorf("download stream returned %d of %d bytes", copyResult.bytes, length)
	}
	return copyResult, nil
}

func isRelayPipeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, io.EOF) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "closed pipe") || strings.Contains(message, "file transfer is closed")
}

func isClientDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"broken pipe",
		"connection reset by peer",
		"connection reset",
		"write: closed network connection",
		"client disconnected",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isChunkTransportError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		" 502 ",
		" 503 ",
		" 504 ",
		"broken pipe",
		"connection reset",
		"unexpected eof",
		"timeout",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func isNonRetryableStreamError(err error) bool {
	if err == nil {
		return false
	}
	if isUnsupportedAgentFileOperation(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, status := range []string{" 400 ", " 401 ", " 403 ", " 404 ", " 405 ", " 409 ", " 410 ", " 411 ", " 413 ", " 415 ", " 422 ", " 501 "} {
		if strings.Contains(message, status) {
			return true
		}
	}
	return false
}

func isPayloadTooLargeError(err error) bool {
	if err == nil {
		return false
	}
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		" 413 ",
		" 413",
		"413 ",
		"413:",
		"status 413",
		"status code 413",
		"request entity too large",
		"payload too large",
		"content too large",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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
			// Response headers must be installed before the first Write; net/http
			// commits them as soon as the write starts.
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
