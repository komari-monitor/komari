package api_v1

import (
	"fmt"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/oauth"
	"github.com/komari-monitor/komari/pkg/utils"
)

// /api/oauth
func OAuth(c *gin.Context) {
	cfg, _ := conf.GetWithV1Format()
	if !cfg.OAuthEnabled {
		c.JSON(403, gin.H{"status": "error", "error": "OAuth is not enabled"})
		return
	}

	authURL, state := oauth.CurrentProvider().GetAuthorizationURL(utils.GetCallbackURL(c))

	c.SetCookie("oauth_state", state, 3600, "/", "", false, true)

	c.Redirect(302, authURL)
}

// /api/oauth_callback
func OAuthCallback(c *gin.Context) {

	// 验证state防止CSRF攻击
	state, _ := c.Cookie("oauth_state")
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	// 获取当前OAuth提供商名称
	providerName := oauth.CurrentProvider().GetName()

	providersSkipStateCheck := []string{"qq"}
	if slices.Contains(providersSkipStateCheck, providerName) {
		// 对于QQ登录，由于是通过QQ聚合登录平台中转，state可能会不匹配
		// 但我们仍然需要验证state的存在性（不能是空的）
		if state == "" {
			c.JSON(400, gin.H{"status": "error", "error": "Invalid state"})
			return
		}
	} else {
		// 对于其他提供商，严格验证state匹配
		if state == "" || state != c.Query("state") {
			c.JSON(400, gin.H{"status": "error", "error": "Invalid state"})
			return
		}
	}

	queries := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			queries[key] = values[0]
		}
	}
	oidcUser, err := oauth.CurrentProvider().OnCallback(c, state, queries, utils.GetCallbackURL(c))
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "error": "Failed to get user info: " + err.Error()})
		return
	}

	// ID作为SSO ID
	sso_id := fmt.Sprintf("%s_%s", oauth.CurrentProvider().GetName(), oidcUser.UserId)

	// 如果cookie中有binding_external_account，说明是绑定外部账号
	// 否则是登录
	uuid, _ := c.Cookie("binding_external_account")
	c.SetCookie("binding_external_account", "", -1, "/", "", false, true)
	if uuid != "" {
		// 绑定外部账号
		session, _ := c.Cookie("session_token")
		user, err := accounts.GetUserBySession(session)
		if err != nil || user.UUID != uuid {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		err = accounts.BindingExternalAccount(user.UUID, sso_id)
		if err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		auditlog.Log(c.ClientIP(), user.UUID, "bound external account (OAuth)"+fmt.Sprintf(",sso_id: %s", sso_id), "login")
		event.Trigger(eventType.UserOidcBound, event.M{
			"user":   user.UUID,
			"sso_id": sso_id,
		})
		c.Redirect(302, "/admin")

		return
	}

	// 尝试获取用户
	user, err := accounts.GetUserBySSO(sso_id)
	if err != nil {
		c.JSON(401, gin.H{
			"status":  "error",
			"message": "please log in and bind your external account first.",
		})
		event.Trigger(eventType.UserLogin, event.M{
			"username": user.Username,
			"method":   "oauth",
			"ip":       c.ClientIP(),
			"ua":       c.Request.UserAgent(),
			"header":   c.Request.Header,
			"referrer": c.Request.Referer(),
			"host":     c.Request.Host,
			"error":    "no linked account",
			"sso_id":   sso_id,
		})
		return
	}

	// 创建会话
	session, err := accounts.CreateSession(user.UUID, 2592000, c.Request.UserAgent(), c.ClientIP(), "oauth")
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": err.Error()})
		event.Trigger(eventType.LoginFailed, event.M{
			"username": user.Username,
			"method":   "oauth",
			"ip":       c.ClientIP(),
			"ua":       c.Request.UserAgent(),
			"header":   c.Request.Header,
			"referrer": c.Request.Referer(),
			"host":     c.Request.Host,
			"error":    err.Error(),
			"sso_id":   sso_id,
		})
		return
	}

	// 设置cookie并返回
	c.SetCookie("session_token", session, 2592000, "/", "", false, true)
	auditlog.Log(c.ClientIP(), user.UUID, "logged in (OAuth)", "login")
	event.Trigger(eventType.UserLogin, event.M{
		"username": user.Username,
		"method":   "oauth",
		"ip":       c.ClientIP(),
		"ua":       c.Request.UserAgent(),
		"header":   c.Request.Header,
		"referrer": c.Request.Referer(),
		"host":     c.Request.Host,
	})
	c.Redirect(302, "/admin")

}
