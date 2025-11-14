package clipboard

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	clipboardDB "github.com/komari-monitor/komari/internal/database/clipboard"
	"github.com/komari-monitor/komari/internal/database/models"
)

// GetClipboard retrieves a clipboard entry by ID
func GetClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	cb, err := clipboardDB.GetClipboardByID(id)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to get clipboard: "+err.Error())
		return
	}
	api.RespondSuccess(c, cb)
}

// ListClipboard lists all clipboard entries
func ListClipboard(c *gin.Context) {
	list, err := clipboardDB.ListClipboard()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to list clipboard: "+err.Error())
		return
	}
	api.RespondSuccess(c, list)
}

// CreateClipboard creates a new clipboard entry
func CreateClipboard(c *gin.Context) {
	var req models.Clipboard
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if err := clipboardDB.CreateClipboard(&req); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to create clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "create clipboard:"+strconv.Itoa(req.Id), "info")
	api.RespondSuccess(c, req)
}

// UpdateClipboard updates an existing clipboard entry
func UpdateClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	var fields map[string]interface{}
	if err := c.ShouldBindJSON(&fields); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if err := clipboardDB.UpdateClipboardFields(id, fields); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to update clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "update clipboard:"+strconv.Itoa(id), "info")
	api.RespondSuccess(c, nil)
}

// DeleteClipboard deletes a clipboard entry
func DeleteClipboard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid ID")
		return
	}
	if err := clipboardDB.DeleteClipboard(id); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to delete clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "delete clipboard:"+strconv.Itoa(id), "warn")
	api.RespondSuccess(c, nil)
}

// BatchDeleteClipboard deletes multiple clipboard entries
func BatchDeleteClipboard(c *gin.Context) {
	var req struct {
		IDs []int `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		api.RespondError(c, http.StatusBadRequest, "IDs cannot be empty")
		return
	}
	if err := clipboardDB.DeleteClipboardBatch(req.IDs); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to batch delete clipboard: "+err.Error())
		return
	}
	userUUID, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), userUUID.(string), "batch delete clipboard: "+strconv.Itoa(len(req.IDs))+" items", "warn")
	api.RespondSuccess(c, nil)
}
