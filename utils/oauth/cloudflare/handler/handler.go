package handler

import (
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/oauth"
)

// CloudflareConfig Cloudflare 配置结构
type CloudflareConfig struct {
	TeamDomain string `json:"team_domain"`
	PolicyAUD  string `json:"policy_aud"`
}

// HandleOAuth 处理 Cloudflare OAuth 请求（直接认证，不跳转）
func HandleOAuth(c *gin.Context) {
	HandleOAuthCallback(c)
}

// HandleOAuthCallback 处理 Cloudflare OAuth 回调
func HandleOAuthCallback(c *gin.Context) {
	// 从请求头获取 JWT
	accessJWT := c.GetHeader("Cf-Access-Jwt-Assertion")
	if accessJWT == "" {
		c.JSON(401, gin.H{"status": "error", "message": "No Cloudflare Access JWT found"})
		return
	}

	// 构造查询参数
	queries := map[string]string{
		"cf_access_jwt": accessJWT,
	}

	// 调用 OAuth 提供商验证
	oidcUser, err := oauth.CurrentProvider().OnCallback(c.Request.Context(), "", queries, utils.GetCallbackURL(c))
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": "Failed to verify Cloudflare Access token: " + err.Error()})
		return
	}

	// 生成 SSO ID
	ssoID := fmt.Sprintf("cloudflare_%s", oidcUser.UserId)

	// 检查是否为绑定外部账号流程
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
		err = accounts.BindingExternalAccount(user.UUID, ssoID)
		if err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		auditlog.Log(c.ClientIP(), user.UUID, "bound external account (Cloudflare Access)"+fmt.Sprintf(",sso_id: %s", ssoID), "login")
		c.Redirect(302, "/manage")
		return
	}

	// 尝试获取用户（登录流程）
	user, err := accounts.GetUserBySSO(ssoID)
	if err != nil {
		c.JSON(401, gin.H{
			"status":  "error",
			"message": "please log in and bind your external account first.",
		})
		return
	}

	// 创建会话
	session, err := accounts.CreateSession(user.UUID, 2592000, c.Request.UserAgent(), c.ClientIP(), "cloudflare_access")
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// 设置 cookie 并返回
	c.SetCookie("session_token", session, 2592000, "/", "", false, true)
	auditlog.Log(c.ClientIP(), user.UUID, "logged in (Cloudflare Access)", "login")
	c.Redirect(302, "/admin")
}

// HandleSetOidcProvider 处理 Cloudflare OIDC 提供商配置
func HandleSetOidcProvider(oidcConfig *models.OidcProvider) error {
	// 解析 Cloudflare 配置进行验证
	var cloudflareConfig CloudflareConfig
	if err := json.Unmarshal([]byte(oidcConfig.Addition), &cloudflareConfig); err != nil {
		return fmt.Errorf("failed to parse Cloudflare config: %w", err)
	}

	if cloudflareConfig.TeamDomain == "" {
		return fmt.Errorf("team_domain is required for Cloudflare provider")
	}

	if cloudflareConfig.PolicyAUD == "" {
		return fmt.Errorf("policy_aud is required for Cloudflare provider")
	}
	return nil
}

// IsCloudflareProvider 检查当前是否为 Cloudflare Access 提供商
func IsCloudflareProvider() bool {
	return oauth.CurrentProvider().GetName() == "cloudflare_access"
}