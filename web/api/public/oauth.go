package public

import (
	"fmt"
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/web/oauth"
)

// /api/oauth
func OAuth(c *gin.Context) {
	OAuthEnabled, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	if !OAuthEnabled {
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

	// QQ 经聚合平台中转，state 可能不匹配，只校验非空；其他提供商严格校验匹配
	skipStateMatch := slices.Contains([]string{"qq"}, providerName)
	if state == "" || (!skipStateMatch && state != c.Query("state")) {
		c.JSON(400, gin.H{"status": "error", "error": "Invalid state"})
		return
	}

	queries := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		queries[key] = values[0]
	}
	oidcUser, err := oauth.CurrentProvider().OnCallback(c, state, queries, utils.GetCallbackURL(c))
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "error": "Failed to get user info: " + err.Error()})
		return
	}

	// ID作为SSO ID
	sso_id := fmt.Sprintf("%s_%s", providerName, oidcUser.UserId)

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
		if err := accounts.BindingExternalAccount(user.UUID, sso_id); err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		auditlog.Log(c.ClientIP(), user.UUID, "bound external account (OAuth)"+fmt.Sprintf(",sso_id: %s", sso_id), "login")
		c.Redirect(302, "/admin/dashboard")
		return
	}

	// 尝试获取用户
	user, err := accounts.GetUserBySSO(sso_id)
	if err != nil {
		c.JSON(401, gin.H{
			"status":  "error",
			"message": "please log in and bind your external account first.",
		})
		return
	}

	// 创建会话
	session, err := accounts.CreateSession(user.UUID, sessionCookieMaxAge, c.Request.UserAgent(), c.ClientIP(), "oauth")
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// 设置cookie并返回
	setSessionCookie(c, session, sessionCookieMaxAge)
	auditlog.Log(c.ClientIP(), user.UUID, "logged in (OAuth)", "login")
	c.Redirect(302, "/admin/dashboard")
}
