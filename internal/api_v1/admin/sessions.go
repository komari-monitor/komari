package admin

import (
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/auditlog"

	"github.com/gin-gonic/gin"
)

func GetSessions(c *gin.Context) {

	ss, err := accounts.GetAllSessions()
	if err != nil {
		api.RespondError(c, 500, "Failed to retrieve sessions: "+err.Error())
		return
	}
	current, _ := c.Cookie("session_token")
	c.JSON(200, gin.H{"status": "success", "current": current, "data": ss})
}

func DeleteSession(c *gin.Context) {
	var req struct {
		Session string `json:"session" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespondError(c, 400, "Invalid request: "+err.Error())
		return
	}
	err := accounts.DeleteSession(req.Session)
	if err != nil {
		api.RespondError(c, 500, "Failed to delete session: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "delete session", "info")
	api.RespondSuccess(c, nil)
}

func DeleteAllSession(c *gin.Context) {

	err := accounts.DeleteAllSessions()
	if err != nil {
		api.RespondError(c, 500, "Failed to delete all sessions: "+err.Error())
		return
	}
	uuid, _ := c.Get("uuid")
	auditlog.Log(c.ClientIP(), uuid.(string), "delete all sessions", "warn")
	api.RespondSuccess(c, nil)
}
