package admin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/utils/cloudflared"
	"github.com/komari-monitor/komari/web/api"
)

const cloudflaredStopConfirmText = "STOP CLOUDFLARED"

func GetCloudflaredStatus(c *gin.Context) {
	api.RespondSuccess(c, cloudflared.Status())
}

func StartCloudflared(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}

	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	token := strings.TrimSpace(req.Token)
	if token != "" {
		if err := cloudflared.SaveToken(token); err != nil {
			api.RespondError(c, http.StatusInternalServerError, "Failed to save Cloudflare Tunnel token: "+err.Error())
			return
		}
	}
	if err := cloudflared.Start(token); err != nil {
		api.RespondError(c, http.StatusBadRequest, err.Error())
		return
	}
	if uuid, ok := c.Get("uuid"); ok {
		auditlog.Log(c.ClientIP(), uuid.(string), "started cloudflared tunnel", "warn")
	}
	api.RespondSuccess(c, cloudflared.Status())
}

func StopCloudflared(c *gin.Context) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		ConfirmText     string `json:"confirm_text"`
	}

	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}

	disablePasswordLogin, _ := config.GetAs[bool](config.DisablePasswordLoginKey, false)
	if !disablePasswordLogin {
		rawUUID, exists := c.Get("uuid")
		if !exists {
			api.RespondError(c, http.StatusUnauthorized, "Unauthorized.")
			return
		}

		user, err := accounts.GetUserByUUID(rawUUID.(string))
		if err != nil {
			api.RespondError(c, http.StatusUnauthorized, "Failed to verify current user")
			return
		}
		if strings.TrimSpace(req.CurrentPassword) == "" {
			api.RespondError(c, http.StatusBadRequest, "Current password is required")
			return
		}
		if _, ok := accounts.CheckPassword(user.Username, req.CurrentPassword); !ok {
			api.RespondError(c, http.StatusUnauthorized, "Current password is incorrect")
			return
		}
	} else if strings.TrimSpace(req.ConfirmText) != cloudflaredStopConfirmText {
		api.RespondError(c, http.StatusBadRequest, "Type STOP CLOUDFLARED to confirm stopping cloudflared")
		return
	}

	if err := cloudflared.Stop(); err != nil {
		api.RespondError(c, http.StatusInternalServerError, "Failed to stop cloudflared: "+err.Error())
		return
	}
	if uuid, ok := c.Get("uuid"); ok {
		auditlog.Log(c.ClientIP(), uuid.(string), "stopped cloudflared tunnel", "warn")
	}
	api.RespondSuccess(c, cloudflared.Status())
}

func RemoveCloudflaredToken(c *gin.Context) {
	if err := cloudflared.RemoveToken(); err != nil {
		api.RespondError(c, http.StatusBadRequest, "Failed to remove Cloudflare Tunnel token: "+err.Error())
		return
	}
	if uuid, ok := c.Get("uuid"); ok {
		auditlog.Log(c.ClientIP(), uuid.(string), "removed cloudflared tunnel token", "warn")
	}
	api.RespondSuccess(c, cloudflared.Status())
}
