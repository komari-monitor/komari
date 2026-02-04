package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TwoFa    string `json:"2fa_code"`
}

func Login(c *gin.Context) {
	DisablePasswordLogin, _ := config.GetAs[bool](config.DisablePasswordLoginKey, false)
	if DisablePasswordLogin {
		RespondError(c, http.StatusForbidden, "Password login is disabled")
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	var data LoginRequest
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	if data.Username == "" || data.Password == "" {
		RespondError(c, http.StatusBadRequest, "Invalid request body: Username and password are required")
		return
	}

	uuid, success := accounts.CheckPassword(data.Username, data.Password)
	if !success {
		RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	// 2FA
	user, _ := accounts.GetUserByUUID(uuid)
	if user.TwoFactor != "" { // 开启了2FA
		if data.TwoFa == "" {
			RespondError(c, http.StatusUnauthorized, "2FA code is required")
			return
		}
		if ok, err := accounts.Verify2Fa(uuid, data.TwoFa); err != nil || !ok {
			RespondError(c, http.StatusUnauthorized, "Invalid 2FA code")
			return
		}
	}
	// Create session
	session, err := accounts.CreateSession(uuid, 2592000, c.Request.UserAgent(), c.ClientIP(), "password")
	if err != nil {
		RespondError(c, http.StatusInternalServerError, "Failed to create session: "+err.Error())
		return
	}
	c.SetCookie("session_token", session, 2592000, "/", "", false, true)
	auditlog.Log(c.ClientIP(), uuid, "logged in (password)", "login")
	RespondSuccess(c, gin.H{"set-cookie": gin.H{"session_token": session}})
}
func Logout(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	accounts.DeleteSession(session)
	c.SetCookie("session_token", "", -1, "/", "", false, true)
	auditlog.Log(c.ClientIP(), "", "logged out", "logout")
	c.Redirect(302, "/")
}
