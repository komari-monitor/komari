package filemanager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/web/api"
)

const (
	DefaultTransferChunkSize int64 = 6 * 1024 * 1024
	MaxTransferChunkSize     int64 = 128 * 1024 * 1024
	MaxUploadSize            int64 = 8 * 1024 * 1024 * 1024
	minDownloadChunkSize     int64 = 1 * 1024 * 1024
	maxDownloadPrefetch      int   = 5
	downloadMemoryBudget     int64 = 512 * 1024 * 1024
	downloadMemoryReserve    int64 = 64 * 1024 * 1024
	uploadSessionTTL               = 15 * time.Minute
	previewTokenTTL                = 10 * time.Minute
	maxConcurrentUploads           = 4
)

var ErrTooManyUploads = errors.New("too many concurrent uploads")

type uploadSession struct {
	mu         sync.Mutex
	ID         string
	UUID       string
	Path       string
	Size       int64
	ChunkSize  int64
	NextOffset int64
	ExpiresAt  time.Time
	slotHeld   bool
}

type previewToken struct {
	ClientUUID string
	Path       string
	ExpiresAt  time.Time
}

var (
	uploadMu               sync.Mutex
	uploadSessions         = make(map[string]*uploadSession)
	uploadSlots            = make(chan struct{}, maxConcurrentUploads)
	cleanupOnce            sync.Once
	previewTokenMu         sync.Mutex
	previewTokens          = make(map[string]previewToken)
	downloadMemoryMu       sync.Mutex
	downloadMemoryReserved int64
)

type persistedUploadSession struct {
	ID         string    `json:"id"`
	UUID       string    `json:"uuid"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ChunkSize  int64     `json:"chunk_size"`
	NextOffset int64     `json:"next_offset"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func uploadSessionPath(id string) string {
	return filepath.Join("data", ".uploading", ".komari-upload-"+id+".json")
}

func Upload(c *gin.Context) {
	cleanupOnce.Do(startUploadCleanup)

	clientUUID := c.Param("uuid")
	operation := c.Query("operation")
	if operation == "" {
		operation = "init"
	}
	switch operation {
	case "init":
		initRemoteUpload(c, clientUUID)
	case "chunk":
		uploadRemoteChunk(c, clientUUID)
	case "merge":
		mergeRemoteUpload(c, clientUUID)
	case "cancel":
		cancelUpload(c, clientUUID)
	default:
		api.RespondError(c, http.StatusNotFound, "unknown upload operation")
	}
}

func initRemoteUpload(c *gin.Context, clientUUID string) {
	var request struct {
		Path      string `json:"path" binding:"required"`
		Size      int64  `json:"size"`
		ChunkSize int64  `json:"chunk_size"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	request.Path = strings.TrimSpace(request.Path)
	if request.Path == "" {
		api.RespondError(c, http.StatusBadRequest, "path is required")
		return
	}
	if request.Size < 0 || request.Size > MaxUploadSize {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("size must be between 0 and %d bytes", MaxUploadSize))
		return
	}
	chunkSize := request.ChunkSize
	if chunkSize == 0 {
		chunkSize = DefaultTransferChunkSize
	}
	if chunkSize <= 0 || chunkSize > MaxTransferChunkSize {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("chunk_size must be between 1 and %d bytes", MaxTransferChunkSize))
		return
	}
	if request.Size == 0 {
		_, err := Call(c.Request.Context(), clientUUID, "write", map[string]any{
			"path": request.Path,
		}, "", CallOptions{Timeout: 60 * time.Second})
		if err != nil {
			respondTransferError(c, err)
			return
		}
		auditFileTransfer(c, "upload", clientUUID, request.Path)
		api.RespondSuccess(c, gin.H{"complete": true, "chunk_size": chunkSize})
		return
	}

	session, _, err := acquireUploadSession("", clientUUID, request.Path, request.Size, 0)
	if err != nil {
		respondTransferError(c, err)
		return
	}
	session.mu.Lock()
	session.ChunkSize = chunkSize
	saveUploadSession(session)
	session.mu.Unlock()
	api.RespondSuccess(c, gin.H{
		"upload_id":        session.ID,
		"chunk_size":       chunkSize,
		"chunk_count":      uploadChunkCount(request.Size, chunkSize),
		"next_offset":      0,
		"next_chunk_index": 0,
		"complete":         false,
	})
}

func uploadRemoteChunk(c *gin.Context, clientUUID string) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxTransferChunkSize+(1<<20))
	uploadID := strings.TrimSpace(c.PostForm("upload_id"))
	index, err := strconv.ParseInt(c.PostForm("chunk_index"), 10, 64)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "chunk_index must be an integer")
		return
	}
	chunk, _, err := c.Request.FormFile("chunk_data")
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("get chunk data: %v", err))
		return
	}
	defer chunk.Close()

	session, _, err := acquireUploadSession(uploadID, clientUUID, "", 0, 0)
	if err != nil {
		respondTransferError(c, err)
		return
	}
	session.mu.Lock()
	chunkSize := session.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultTransferChunkSize
	}
	if chunkSize > MaxTransferChunkSize {
		session.mu.Unlock()
		api.RespondError(c, http.StatusBadRequest, "upload session chunk size exceeds the maximum")
		return
	}
	if index < 0 || index >= uploadChunkCount(session.Size, chunkSize) {
		session.mu.Unlock()
		api.RespondError(c, http.StatusBadRequest, "invalid chunk index")
		return
	}
	session.ExpiresAt = time.Now().Add(uploadSessionTTL)
	targetPath := session.Path
	sessionSize := session.Size
	sessionID := session.ID
	session.mu.Unlock()

	offset := index * chunkSize
	content, err := io.ReadAll(io.LimitReader(chunk, chunkSize+1))
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "failed to read upload chunk")
		return
	}
	expectedSize := min(chunkSize, sessionSize-offset)
	if int64(len(content)) != expectedSize {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("chunk %d has size %d, want %d", index, len(content), expectedSize))
		return
	}

	_, err = Call(c.Request.Context(), clientUUID, "upload_chunk", map[string]any{
		"path":        targetPath,
		"offset":      offset,
		"chunk_index": index,
		"chunk_count": uploadChunkCount(session.Size, chunkSize),
		"total_size":  sessionSize,
		"chunk_size":  chunkSize,
		"upload_id":   sessionID,
		"first":       index == 0,
		"final":       false,
	}, base64.StdEncoding.EncodeToString(content), CallOptions{Timeout: 90 * time.Second})
	if err != nil {
		session.mu.Lock()
		saveUploadSession(session)
		session.mu.Unlock()
		respondTransferError(c, err)
		return
	}

	session.mu.Lock()
	session.NextOffset = max(session.NextOffset, offset+expectedSize)
	nextOffset := session.NextOffset
	saveUploadSession(session)
	session.mu.Unlock()
	api.RespondSuccess(c, gin.H{
		"received":    true,
		"chunk_index": index,
		"next_offset": nextOffset,
	})
}

func mergeRemoteUpload(c *gin.Context, clientUUID string) {
	var request struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	session, _, err := acquireUploadSession(request.UploadID, clientUUID, "", 0, 0)
	if err != nil {
		respondTransferError(c, err)
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	chunkSize := session.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultTransferChunkSize
	}
	_, err = Call(c.Request.Context(), clientUUID, "upload_chunk", map[string]any{
		"path":        session.Path,
		"upload_id":   session.ID,
		"chunk_index": uploadChunkCount(session.Size, chunkSize),
		"chunk_count": uploadChunkCount(session.Size, chunkSize),
		"total_size":  session.Size,
		"chunk_size":  chunkSize,
		"offset":      session.Size,
		"first":       false,
		"final":       true,
	}, "", CallOptions{Timeout: 90 * time.Second})
	if err == nil {
		finishUploadSession(session)
		auditFileTransfer(c, "upload", clientUUID, session.Path)
	} else if strings.Contains(err.Error(), "unknown upload session") {
		// A previous merge request may have timed out after the agent renamed the part file.
		raw, statErr := Call(c.Request.Context(), clientUUID, "stat", map[string]any{
			"path": session.Path,
		}, "", CallOptions{Timeout: 60 * time.Second})
		if statErr == nil {
			var info struct {
				Size int64 `json:"size"`
			}
			if json.Unmarshal(raw, &info) == nil && info.Size == session.Size {
				finishUploadSession(session)
				auditFileTransfer(c, "upload", clientUUID, session.Path)
				api.RespondSuccess(c, gin.H{"complete": true})
				return
			}
		}
		saveUploadSession(session)
		respondTransferError(c, err)
		return
	} else {
		saveUploadSession(session)
		respondTransferError(c, err)
		return
	}
	api.RespondSuccess(c, gin.H{"complete": true})
}

func cancelUpload(c *gin.Context, clientUUID string) {
	id := strings.TrimSpace(c.Query("upload_id"))
	if id == "" {
		// Accept the JSON form used by older web clients as well as the query
		// parameter form used by the current endpoint.
		var request struct {
			UploadID string `json:"upload_id"`
		}
		if err := c.ShouldBindJSON(&request); err == nil {
			id = strings.TrimSpace(request.UploadID)
		}
	}
	if clientUUID == "" || id == "" {
		api.RespondError(c, http.StatusBadRequest, "uuid and upload_id are required")
		return
	}
	session, _, err := acquireUploadSession(id, clientUUID, "", 0, 0)
	if err != nil {
		respondTransferError(c, err)
		return
	}
	_, _ = Call(c.Request.Context(), clientUUID, "upload_cancel", map[string]any{
		"upload_id": id,
		"path":      session.Path,
	}, "", CallOptions{Timeout: 30 * time.Second})
	session.mu.Lock()
	finishUploadSession(session)
	session.mu.Unlock()
	api.RespondSuccess(c, gin.H{"cancelled": true})
}

func Download(c *gin.Context) {
	clientUUID := c.Param("uuid")
	path := strings.TrimSpace(c.Query("path"))
	if clientUUID == "" || path == "" {
		api.RespondError(c, http.StatusBadRequest, "uuid and path are required")
		return
	}
	downloadFile(c, clientUUID, path)
}

func downloadFile(c *gin.Context, clientUUID, path string) {
	raw, err := Call(c.Request.Context(), clientUUID, "stat", map[string]any{"path": path}, "", CallOptions{Timeout: 60 * time.Second})
	if err != nil {
		respondTransferError(c, err)
		return
	}
	var info struct {
		Name       string    `json:"name"`
		Size       int64     `json:"size"`
		IsDir      bool      `json:"is_dir"`
		ModifiedAt time.Time `json:"modified_at"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		api.RespondError(c, http.StatusBadGateway, "invalid file metadata from agent")
		return
	}
	if info.IsDir {
		api.RespondError(c, http.StatusBadRequest, "cannot download a directory")
		return
	}
	if info.Size < 0 {
		api.RespondError(c, http.StatusBadGateway, "invalid negative file size from agent")
		return
	}
	etag := fmt.Sprintf("\"%x-%x\"", info.Size, info.ModifiedAt.UnixNano())
	start, end := int64(0), info.Size-1
	partial := false
	if rangeHeader := strings.TrimSpace(c.GetHeader("Range")); rangeHeader != "" && ifRangeAllowsRange(c.GetHeader("If-Range"), etag, info.ModifiedAt) {
		var ok bool
		start, end, ok = parseSingleByteRange(rangeHeader, info.Size)
		if !ok {
			c.Header("Content-Range", fmt.Sprintf("bytes */%d", info.Size))
			api.RespondError(c, http.StatusRequestedRangeNotSatisfiable, "invalid byte range")
			return
		}
		partial = true
	}
	contentLength := int64(0)
	if info.Size > 0 {
		contentLength = end - start + 1
	}

	name := info.Name
	if name == "" {
		name = filepath.Base(path)
	}
	disposition := "attachment"
	if c.Query("inline") == "1" || strings.EqualFold(c.Query("inline"), "true") {
		disposition = "inline"
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	setHeaders := func() {
		c.Header("Content-Type", contentType)
		c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
		c.Header("Accept-Ranges", "bytes")
		c.Header("Content-Length", strconv.FormatInt(contentLength, 10))
		c.Header("Cache-Control", "no-store")
		c.Header("ETag", etag)
		if !info.ModifiedAt.IsZero() {
			c.Header("Last-Modified", info.ModifiedAt.UTC().Format(http.TimeFormat))
		}
		if partial {
			c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size))
			c.Status(http.StatusPartialContent)
		} else {
			c.Status(http.StatusOK)
		}
	}
	if c.Request.Method == http.MethodHead || contentLength == 0 {
		setHeaders()
		auditFileTransfer(c, "download", clientUUID, path)
		return
	}

	var session struct {
		DownloadID string `json:"download_id"`
		Size       int64  `json:"size"`
		ChunkSize  int64  `json:"chunk_size"`
		ChunkCount int64  `json:"chunk_count"`
	}
	initRaw, initErr := Call(c.Request.Context(), clientUUID, "download_init", map[string]any{
		"path":       path,
		"offset":     start,
		"size":       contentLength,
		"chunk_size": DefaultTransferChunkSize,
	}, "", CallOptions{Timeout: 60 * time.Second})
	if initErr != nil {
		respondTransferError(c, initErr)
		return
	}
	if err := json.Unmarshal(initRaw, &session); err != nil || session.DownloadID == "" || session.Size != contentLength || session.ChunkSize <= 0 || session.ChunkSize > MaxTransferChunkSize || session.ChunkCount <= 0 {
		respondTransferError(c, errors.New("invalid download session from agent"))
		return
	}

	finished := false
	defer func() {
		operation := "download_cancel"
		if finished {
			operation = "download_finish"
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = Call(cleanupCtx, clientUUID, operation, map[string]any{"download_id": session.DownloadID}, "", CallOptions{Timeout: 30 * time.Second})
	}()

	setHeaders()
	if err := streamDownload(c, clientUUID, session.DownloadID, start, session.Size, session.ChunkSize); err != nil {
		return
	}
	finished = true
	auditFileTransfer(c, "download", clientUUID, path)
}

type downloadChunkResult struct {
	Index    int64
	Offset   int64
	Read     int64
	Data     []byte
	Reserved int64
	Err      error
}

func fetchDownloadChunk(ctx context.Context, clientUUID, downloadID string, index, offset, length, reserved int64) <-chan downloadChunkResult {
	result := make(chan downloadChunkResult, 1)
	go func() {
		chunkRaw, callErr := Call(ctx, clientUUID, "download_chunk", map[string]any{
			"download_id": downloadID,
			"chunk_index": index,
			"offset":      offset,
			"length":      length,
		}, "", CallOptions{Timeout: 90 * time.Second})
		if callErr != nil {
			result <- downloadChunkResult{Reserved: reserved, Err: callErr}
			return
		}
		var chunk struct {
			Data   string `json:"data"`
			Index  int64  `json:"chunk_index"`
			Offset int64  `json:"offset"`
			Read   int64  `json:"read"`
		}
		if err := json.Unmarshal(chunkRaw, &chunk); err != nil {
			result <- downloadChunkResult{Reserved: reserved, Err: errors.New("invalid download chunk from agent")}
			return
		}
		data, decodeErr := base64.StdEncoding.DecodeString(chunk.Data)
		if decodeErr != nil || int64(len(data)) != chunk.Read {
			result <- downloadChunkResult{Reserved: reserved, Err: errors.New("invalid encoded download chunk from agent")}
			return
		}
		result <- downloadChunkResult{Index: chunk.Index, Offset: chunk.Offset, Read: chunk.Read, Data: data, Reserved: reserved}
	}()
	return result
}

type pendingDownloadChunk struct {
	index    int64
	offset   int64
	length   int64
	reserved int64
	result   <-chan downloadChunkResult
}

func streamDownload(c *gin.Context, clientUUID, downloadID string, start, size, chunkSize int64) error {
	end := start + size
	offset := start
	index := int64(0)
	for offset < end {
		chunkSize = fitDownloadChunkSize(chunkSize, end-offset)
		windowSize := downloadPrefetchWindow(chunkSize, end-offset)
		pending := make([]pendingDownloadChunk, 0, windowSize)
		nextOffset := offset
		for len(pending) < windowSize && nextOffset < end {
			length := min(chunkSize, end-nextOffset)
			reserved := max(int64(1), length*2)
			if !reserveDownloadMemory(reserved) {
				if len(pending) > 0 {
					break
				}
				reserved = 0
			}
			pending = append(pending, pendingDownloadChunk{
				index:    index,
				offset:   nextOffset,
				length:   length,
				reserved: reserved,
				result:   fetchDownloadChunk(c.Request.Context(), clientUUID, downloadID, index, nextOffset, length, reserved),
			})
			nextOffset += length
			index++
		}

		retryWindow := false
		for requestIndex, request := range pending {
			chunk := <-request.result
			releaseDownloadMemory(request.reserved)
			if chunk.Err != nil {
				if isDownloadChunkTooLargeError(chunk.Err) && chunkSize > minDownloadChunkSize {
					chunkSize = max(minDownloadChunkSize, chunkSize/2)
					index = request.index
					discardPendingDownloadChunks(pending[requestIndex+1:])
					retryWindow = true
					break
				}
				discardPendingDownloadChunks(pending[requestIndex+1:])
				return chunk.Err
			}
			if chunk.Index != request.index || chunk.Offset != request.offset || chunk.Read != request.length || int64(len(chunk.Data)) != request.length {
				discardPendingDownloadChunks(pending[requestIndex+1:])
				return errors.New("invalid download chunk sequence from agent")
			}
			if _, err := c.Writer.Write(chunk.Data); err != nil {
				chunk.Data = nil
				discardPendingDownloadChunks(pending[requestIndex+1:])
				return err
			}
			chunk.Data = nil
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
			offset += chunk.Read
		}
		if retryWindow {
			// Requests after the failed one may already be in flight. Discard
			// their buffered results and continue from the next unwritten offset.
			continue
		}
		chunkSize = growDownloadChunkSize(chunkSize, end-offset)
	}
	if offset != end {
		return errors.New("download ended before the requested range")
	}
	return nil
}

func downloadPrefetchWindow(chunkSize, remaining int64) int {
	available := availableDownloadMemory()
	if available <= 0 {
		return 1
	}
	perChunk := max(int64(1), chunkSize*2)
	window := 1 + int(available/perChunk)
	if window > maxDownloadPrefetch {
		window = maxDownloadPrefetch
	}
	remainingChunks := (remaining + chunkSize - 1) / chunkSize
	if int64(window) > remainingChunks {
		window = int(remainingChunks)
	}
	if window < 1 {
		return 1
	}
	return window
}

func growDownloadChunkSize(current, remaining int64) int64 {
	if current >= MaxTransferChunkSize || remaining <= current {
		return min(current, remaining)
	}
	candidate := current + current/2
	if candidate > MaxTransferChunkSize {
		candidate = MaxTransferChunkSize
	}
	available := availableDownloadMemory()
	if available <= 0 || candidate*2 > available {
		return current
	}
	return min(candidate, remaining)
}

func fitDownloadChunkSize(current, remaining int64) int64 {
	current = min(current, remaining)
	available := availableDownloadMemory()
	if available <= 0 {
		return min(current, min(minDownloadChunkSize, remaining))
	}
	maxChunk := available / 2
	if maxChunk >= minDownloadChunkSize && current > maxChunk {
		return maxChunk
	}
	return current
}

func availableDownloadMemory() int64 {
	capacity := downloadMemoryCapacity()
	downloadMemoryMu.Lock()
	reserved := downloadMemoryReserved
	downloadMemoryMu.Unlock()
	available := capacity - reserved
	if available < 0 {
		return 0
	}
	return available
}

func downloadMemoryCapacity() int64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	budget := downloadMemoryBudget
	if limit := debug.SetMemoryLimit(-1); limit > 0 && limit < budget {
		budget = limit
	}
	return budget - int64(stats.Alloc) - downloadMemoryReserve
}

func reserveDownloadMemory(bytes int64) bool {
	if bytes <= 0 {
		return true
	}
	capacity := downloadMemoryCapacity()
	downloadMemoryMu.Lock()
	defer downloadMemoryMu.Unlock()
	if capacity-downloadMemoryReserved < bytes {
		return false
	}
	downloadMemoryReserved += bytes
	return true
}

func releaseDownloadMemory(bytes int64) {
	if bytes <= 0 {
		return
	}
	downloadMemoryMu.Lock()
	downloadMemoryReserved -= bytes
	if downloadMemoryReserved < 0 {
		downloadMemoryReserved = 0
	}
	downloadMemoryMu.Unlock()
}

func discardPendingDownloadChunks(pending []pendingDownloadChunk) {
	for _, request := range pending {
		go func(request pendingDownloadChunk) {
			<-request.result
			releaseDownloadMemory(request.reserved)
		}(request)
	}
}

func isDownloadChunkTooLargeError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "413") ||
		strings.Contains(message, "too large") ||
		strings.Contains(message, "request entity") ||
		strings.Contains(message, "payload")
}

func ifRangeAllowsRange(value, etag string, modifiedAt time.Time) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	if value == etag {
		return true
	}
	if timestamp, err := http.ParseTime(value); err == nil && !modifiedAt.IsZero() {
		return !modifiedAt.UTC().Truncate(time.Second).After(timestamp)
	}
	return false
}

func parseSingleByteRange(value string, size int64) (int64, int64, bool) {
	if size <= 0 || !strings.HasPrefix(strings.ToLower(value), "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimSpace(value[len("bytes="):])
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	if strings.TrimSpace(parts[0]) == "" {
		suffix, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if strings.TrimSpace(parts[1]) != "" {
		end, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}

func CreatePreviewToken(c *gin.Context) {
	clientUUID := c.Param("uuid")
	path := strings.TrimSpace(c.Query("path"))
	if clientUUID == "" || path == "" {
		api.RespondError(c, http.StatusBadRequest, "uuid and path are required")
		return
	}

	token := uuid.NewString()
	now := time.Now()
	previewTokenMu.Lock()
	for id, item := range previewTokens {
		if now.After(item.ExpiresAt) {
			delete(previewTokens, id)
		}
	}
	previewTokens[token] = previewToken{
		ClientUUID: clientUUID,
		Path:       path,
		ExpiresAt:  now.Add(previewTokenTTL),
	}
	previewTokenMu.Unlock()

	api.RespondSuccess(c, gin.H{
		"token":      token,
		"expires_in": int(previewTokenTTL.Seconds()),
	})
}

func PreviewDownload(c *gin.Context) {
	clientUUID := c.Param("uuid")
	token := strings.TrimSpace(c.Query("preview_token"))
	if clientUUID == "" || token == "" {
		api.RespondError(c, http.StatusBadRequest, "uuid and preview_token are required")
		return
	}

	previewTokenMu.Lock()
	item, ok := previewTokens[token]
	if ok && (item.ClientUUID != clientUUID || time.Now().After(item.ExpiresAt)) {
		delete(previewTokens, token)
		ok = false
	}
	previewTokenMu.Unlock()

	if !ok {
		api.RespondError(c, http.StatusUnauthorized, "Invalid or expired preview token")
		return
	}
	// The token is bound to the exact path. Keeping the path out of the public
	// URL avoids nested URL encoding issues for non-ASCII filenames.
	downloadFile(c, clientUUID, item.Path)
}

func acquireUploadSession(id, clientUUID, path string, size, offset int64) (*uploadSession, bool, error) {
	if id == "" {
		if offset != 0 {
			return nil, false, errors.New("upload_id is required after the first chunk")
		}
		select {
		case uploadSlots <- struct{}{}:
		default:
			return nil, false, ErrTooManyUploads
		}
		session := &uploadSession{
			ID:        uuid.NewString(),
			UUID:      clientUUID,
			Path:      path,
			Size:      size,
			ChunkSize: DefaultTransferChunkSize,
			ExpiresAt: time.Now().Add(uploadSessionTTL),
			slotHeld:  true,
		}
		uploadMu.Lock()
		uploadSessions[session.ID] = session
		uploadMu.Unlock()
		ensureUploadSessionDirectory()
		saveUploadSession(session)
		return session, true, nil
	}

	uploadMu.Lock()
	session := uploadSessions[id]
	uploadMu.Unlock()
	if session != nil && removeExpiredUploadSession(id, session) {
		session = nil
	}
	if session == nil {
		if restoredSession := restoreUploadSession(id); restoredSession != nil {
			uploadMu.Lock()
			if existing := uploadSessions[id]; existing != nil {
				session = existing
			} else {
				select {
				case uploadSlots <- struct{}{}:
					restoredSession.slotHeld = true
					uploadSessions[id] = restoredSession
					session = restoredSession
				default:
					uploadMu.Unlock()
					return nil, false, ErrTooManyUploads
				}
			}
			uploadMu.Unlock()
		} else {
			return nil, false, ErrUnknownToken
		}
	}
	// Continuation requests identify the session by upload_id and client UUID;
	// path and size are only supplied when creating (or explicitly validating)
	// a session. Treat omitted values as wildcards so chunk/merge can resume.
	if session.UUID != clientUUID ||
		(path != "" && session.Path != path) ||
		(size != 0 && session.Size != size) {
		return nil, false, errors.New("upload session does not match request")
	}
	return session, false, nil
}

func loadUploadSession(id string) (*persistedUploadSession, error) {
	if id == "" {
		return nil, ErrUnknownToken
	}
	data, err := os.ReadFile(uploadSessionPath(id))
	if err != nil {
		return nil, ErrUnknownToken
	}
	var restored persistedUploadSession
	if err := json.Unmarshal(data, &restored); err != nil {
		return nil, ErrUnknownToken
	}
	if time.Now().After(restored.ExpiresAt) {
		removeUploadSessionState(id)
		return nil, ErrUnknownToken
	}
	return &restored, nil
}

func finishUploadSession(session *uploadSession) {
	if session == nil {
		return
	}
	removeUploadSessionState(session.ID)
	uploadMu.Lock()
	if current := uploadSessions[session.ID]; current == session {
		delete(uploadSessions, session.ID)
		releaseUploadSlotLocked(session)
	}
	uploadMu.Unlock()
}

func uploadSessionExpired(session *uploadSession, now time.Time) bool {
	if session == nil {
		return true
	}
	session.mu.Lock()
	expired := now.After(session.ExpiresAt)
	session.mu.Unlock()
	return expired
}

func removeExpiredUploadSession(id string, session *uploadSession) bool {
	if !uploadSessionExpired(session, time.Now()) {
		return false
	}
	uploadMu.Lock()
	if current := uploadSessions[id]; current != session {
		uploadMu.Unlock()
		return false
	}
	delete(uploadSessions, id)
	releaseUploadSlotLocked(session)
	removeUploadSessionState(id)
	uploadMu.Unlock()
	// The agent owns the temporary part file. Best-effort cleanup is performed
	// after releasing the server lock so a slow/offline agent cannot block the
	// upload session manager.
	go cancelAgentUpload(session)
	return true
}

func releaseUploadSlotLocked(session *uploadSession) {
	if session == nil || !session.slotHeld {
		return
	}
	session.slotHeld = false
	select {
	case <-uploadSlots:
	default:
	}
}

func saveUploadSession(session *uploadSession) {
	data, err := json.Marshal(persistedUploadSession{
		ID:         session.ID,
		UUID:       session.UUID,
		Path:       session.Path,
		Size:       session.Size,
		ChunkSize:  session.ChunkSize,
		NextOffset: session.NextOffset,
		ExpiresAt:  session.ExpiresAt,
	})
	if err != nil {
		return
	}
	path := uploadSessionPath(session.ID)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return
	}
	_ = os.Chtimes(path, session.ExpiresAt, session.ExpiresAt)
}

func removeUploadSessionState(id string) {
	if id == "" {
		return
	}
	_ = os.Remove(uploadSessionPath(id))
}

func startUploadCleanup() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			uploadMu.Lock()
			sessions := make(map[string]*uploadSession, len(uploadSessions))
			for id, session := range uploadSessions {
				sessions[id] = session
			}
			uploadMu.Unlock()
			for id, session := range sessions {
				if uploadSessionExpired(session, now) {
					removeExpiredUploadSession(id, session)
				}
			}
		}
	}()
}

func ensureUploadSessionDirectory() {
	_ = os.MkdirAll(filepath.Dir(uploadSessionPath("x")), 0755)
}

func restoreUploadSession(id string) *uploadSession {
	restored, err := loadUploadSession(id)
	if err != nil {
		return nil
	}
	return &uploadSession{
		ID:         restored.ID,
		UUID:       restored.UUID,
		Path:       restored.Path,
		Size:       restored.Size,
		ChunkSize:  restored.ChunkSize,
		NextOffset: restored.NextOffset,
		ExpiresAt:  restored.ExpiresAt,
	}
}

func cancelAgentUpload(session *uploadSession) {
	if session == nil || session.UUID == "" || session.ID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = Call(ctx, session.UUID, "upload_cancel", map[string]any{
		"upload_id": session.ID,
		"path":      session.Path,
	}, "", CallOptions{Timeout: 10 * time.Second})
}

func uploadChunkCount(size, chunkSize int64) int64 {
	if size <= 0 || chunkSize <= 0 {
		return 0
	}
	return (size + chunkSize - 1) / chunkSize
}

func parseInt64Query(c *gin.Context, name string) (int64, error) {
	value := c.Query(name)
	if value == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	return strconv.ParseInt(value, 10, 64)
}

func respondTransferError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, ErrUnsupported):
		status = http.StatusBadRequest
	case errors.Is(err, ErrOffline):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrTimeout):
		status = http.StatusGatewayTimeout
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
	case errors.Is(err, ErrUnknownToken):
		status = http.StatusGone
	case errors.Is(err, ErrTooManyUploads):
		status = http.StatusTooManyRequests
	}
	api.RespondError(c, status, err.Error())
}

func auditFileTransfer(c *gin.Context, action, clientUUID, path string) {
	actor := c.GetString("uuid")
	auditlog.Log(c.ClientIP(), actor, fmt.Sprintf("file %s: %s:%s", action, clientUUID, path), "info")
}
