package clipboard

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	clipboardDB "github.com/komari-monitor/komari/internal/database/clipboard"
	"github.com/komari-monitor/komari/internal/database/models"
)

// GetClipboard retrieves a clipboard entry by ID
func GetClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	cb, err := clipboardDB.GetClipboardByID(id)
	if err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to get clipboard: "+err.Error())
		return
	}
	resp.RespondSuccess(c, cb)
}

// ListClipboard lists all clipboard entries
func ListClipboard(c *gin.Context) {
	list, err := clipboardDB.ListClipboard()
	if err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to list clipboard: "+err.Error())
		return
	}
	resp.RespondSuccess(c, list)
}

// CreateClipboard creates a new clipboard entry
func CreateClipboard(c *gin.Context) {
	var req models.Clipboard
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if err := clipboardDB.CreateClipboard(&req); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to create clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "create clipboard:"+strconv.Itoa(req.Id), "info")
	resp.RespondSuccess(c, req)
}

// UpdateClipboard updates an existing clipboard entry
func UpdateClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	var fields map[string]interface{}
	if err := c.ShouldBindJSON(&fields); err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if err := clipboardDB.UpdateClipboardFields(id, fields); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to update clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "update clipboard:"+strconv.Itoa(id), "info")
	resp.RespondSuccess(c, nil)
}

// DeleteClipboard deletes a clipboard entry
func DeleteClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	if err := clipboardDB.DeleteClipboard(id); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to delete clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "delete clipboard:"+strconv.Itoa(id), "warn")
	resp.RespondSuccess(c, nil)
}

// BatchDeleteClipboard deletes multiple clipboard entries
func BatchDeleteClipboard(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		resp.RespondError(c, http.StatusBadRequest, "IDs cannot be empty")
		return
	}
	if err := clipboardDB.DeleteClipboardBatch(req.IDs); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to batch delete clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "batch delete clipboard: "+strconv.Itoa(len(req.IDs))+" items", "warn")
	resp.RespondSuccess(c, nil)
}
