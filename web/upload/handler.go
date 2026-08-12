package upload

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
)

const maxChunkRequestSize = ChunkSize + 1*1024*1024

type Result struct {
	Message string
	Data    any
}

type Finalizer func(Session) (Result, error)

type Handler struct {
	Store      *Store
	Finalizers map[Purpose]Finalizer
}

func NewHandler(store *Store, finalizers map[Purpose]Finalizer) *Handler {
	return &Handler{Store: store, Finalizers: finalizers}
}

func (h *Handler) Init(c *gin.Context) {
	var request struct {
		Purpose  Purpose `json:"purpose" binding:"required"`
		Size     int64   `json:"size" binding:"required"`
		Filename string  `json:"filename"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if _, ok := h.Finalizers[request.Purpose]; !ok {
		api.RespondError(c, http.StatusBadRequest, "unsupported upload purpose")
		return
	}
	session, err := h.Store.Init(request.Purpose, request.Filename, request.Size)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	api.RespondSuccess(c, gin.H{
		"upload_id":  session.ID,
		"chunk_size": ChunkSize,
	})
}

func (h *Handler) Chunk(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxChunkRequestSize)
	uploadID := c.PostForm("upload_id")
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
	if err := h.Store.SaveChunk(uploadID, index, chunk); err != nil {
		h.respondUploadError(c, err)
		return
	}
	api.RespondSuccess(c, gin.H{"received": true, "chunk_index": index})
}

func (h *Handler) Merge(c *gin.Context) {
	var request struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	session, err := h.Store.Merge(request.UploadID)
	if err != nil {
		_ = h.Store.Cancel(request.UploadID)
		h.respondUploadError(c, err)
		return
	}
	defer h.Store.Cancel(session.ID)

	finalize, ok := h.Finalizers[session.Metadata.Purpose]
	if !ok {
		api.RespondError(c, http.StatusBadRequest, "unsupported upload purpose")
		return
	}
	result, err := finalize(session)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	api.RespondSuccessMessage(c, result.Message, result.Data)
}

func (h *Handler) Cancel(c *gin.Context) {
	var request struct {
		UploadID string `json:"upload_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		api.RespondError(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if err := h.Store.Cancel(request.UploadID); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	api.RespondSuccess(c, gin.H{})
}

func (h *Handler) respondUploadError(c *gin.Context, err error) {
	if errors.Is(err, ErrNotFound) {
		api.RespondError(c, http.StatusNotFound, "upload not found or expired")
		return
	}
	api.RespondError(c, http.StatusBadRequest, err.Error())
}
