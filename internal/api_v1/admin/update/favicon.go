package update

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/database/auditlog"
)

func UploadFavicon(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 5<<20) // 5MB
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			resp.RespondError(c, http.StatusRequestEntityTooLarge, "File too large. Maximum size is 5MB")
		} else {
			resp.RespondError(c, http.StatusBadRequest, err.Error())
		}
		return
	}
	if err := os.WriteFile("./data/favicon.ico", data, 0644); err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to save favicon: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "Favicon uploaded", "info")
	resp.RespondSuccess(c, nil)
}

func DeleteFavicon(c *gin.Context) {
	if err := os.Remove("./data/favicon.ico"); err != nil {
		if os.IsNotExist(err) {
			resp.RespondError(c, http.StatusNotFound, "Favicon not found")
		} else {
			resp.RespondError(c, http.StatusInternalServerError, "Failed to delete favicon: "+err.Error())
		}
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "Favicon deleted", "info")
	resp.RespondSuccess(c, nil)
}
