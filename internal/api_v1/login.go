package api_v1

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/api_v1/resp"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/eventType"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TwoFa    string `json:"2fa_code"`
}

func Login(c *gin.Context) {
	conf, _ := conf.GetWithV1Format()
	if conf.DisablePasswordLogin {
		resp.RespondError(c, http.StatusForbidden, "Password login is disabled")
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	var data LoginRequest
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	if data.Username == "" || data.Password == "" {
		resp.RespondError(c, http.StatusBadRequest, "Invalid request body: Username and password are required")
		return
	}

	uuid, success := accounts.CheckPassword(data.Username, data.Password)
	if !success {
		resp.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		event.Trigger(eventType.LoginFailed, event.M{
			"username": data.Username,
			"method":   "password",
			"ip":       c.ClientIP(),
			"ua":       c.Request.UserAgent(),
			"header":   c.Request.Header,
			"referrer": c.Request.Referer(),
			"host":     c.Request.Host,
		})
		return
	}
	// 2FA
	user, _ := accounts.GetUserByUUID(uuid)
	if user.TwoFactor != "" { // 开启了2FA
		if data.TwoFa == "" {
			resp.RespondError(c, http.StatusUnauthorized, "2FA code is required")
			return
		}
		if ok, err := accounts.Verify2Fa(uuid, data.TwoFa); err != nil || !ok {
			resp.RespondError(c, http.StatusUnauthorized, "Invalid 2FA code")
			return
		}
	}
	// Create session
	session, err := accounts.CreateSession(uuid, 2592000, c.Request.UserAgent(), c.ClientIP(), "password")
	if err != nil {
		resp.RespondError(c, http.StatusInternalServerError, "Failed to create session: "+err.Error())
		return
	}
	c.SetCookie("session_token", session, 2592000, "/", "", false, true)
	auditlog.Log(c.ClientIP(), uuid, "logged in (password)", "login")
	resp.RespondSuccess(c, gin.H{"set-cookie": gin.H{"session_token": session}})
	event.Trigger(eventType.UserLogin, event.M{
		"username": data.Username,
		"method":   "password",
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"header":   c.Request.Header,
		"referrer": c.Request.Referer(),
		"host":     c.Request.Host,
	})
}
func Logout(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	accounts.DeleteSession(session)
	c.SetCookie("session_token", "", -1, "/", "", false, true)
	auditlog.Log(c.ClientIP(), "", "logged out", "logout")
	event.Trigger(eventType.UserLogout, event.M{
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"header":   c.Request.Header,
		"referrer": c.Request.Referer(),
		"host":     c.Request.Host,
	})
	c.Redirect(302, "/")

}
