package admin

import (
	"net/http"

	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"

	"github.com/gin-gonic/gin"
)

func GetSessions(c *gin.Context) {

	ss, err := accounts.GetAllSessions()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to retrieve sessions: "+err.Error())
		return
	}
	current, _ := c.Cookie("session_token")
	c.JSON(http.StatusOK, gin.H{"status": "success", "current": current, "data": ss})
}

func DeleteSession(c *gin.Context) {
	var req struct {
		Session string `json:"session" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request: "+err.Error())
		return
	}
	err := accounts.DeleteSession(req.Session)
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to delete session: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "delete session", "info")
	api.RespondSuccess(c, nil)
}

func DeleteAllSession(c *gin.Context) {

	err := accounts.DeleteAllSessions()
	if err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to delete all sessions: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "delete all sessions", "warn")
	api.RespondSuccess(c, nil)
}
