package cloudflare

import (
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/oauth/factory"
	"github.com/komari-monitor/komari/pkg/utils"
)

func (c *Cloudflare) GetName() string {
	return "CloudflareAccess"
}

func (c *Cloudflare) GetConfiguration() factory.Configuration {
	return &c.Addition
}

func (c *Cloudflare) GetAuthorizationURL(redirectURI string) (string, string) {
	// 生成 state
	state := utils.GenerateRandomString(8)
	// 将 state 作为参数附加到回调 URL
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	authURL := fmt.Sprintf("%s%sstate=%s", redirectURI, sep, state)
	return authURL, state
}

func (c *Cloudflare) OnCallback(ctx *gin.Context, state string, query map[string]string, callbackURI string) (factory.OidcCallback, error) {
	// 从请求头中获取 Cloudflare Access JWT
	accessJWT := ctx.GetHeader("Cf-Access-Jwt-Assertion")
	if accessJWT == "" {
		return factory.OidcCallback{}, fmt.Errorf("no Cloudflare Access JWT found")
	}

	// 验证 JWT
	teamDomain := c.Addition.TeamDomain
	// 如果不是完整的URL，则默认添加 https 前缀和 cloudflareaccess.com 后缀
	if !strings.HasPrefix(teamDomain, "https://") {
		teamDomain = "https://" + teamDomain + ".cloudflareaccess.com"
	}

	certsURL := fmt.Sprintf("%s/cdn-cgi/access/certs", teamDomain)

	config := &oidc.Config{
		ClientID: c.Addition.PolicyAUD,
	}

	keySet := oidc.NewRemoteKeySet(ctx.Request.Context(), certsURL)
	verifier := oidc.NewVerifier(teamDomain, keySet, config)

	// 验证 token
	idToken, err := verifier.Verify(ctx.Request.Context(), accessJWT)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to verify Cloudflare Access token: %w", err)
	}

	// 提取用户信息
	var claims struct {
		// Email string `json:"email"`
		Sub string `json:"sub"`
	}

	if err := idToken.Claims(&claims); err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to extract claims from token: %w", err)
	}

	// 使用 sub 作为用户 ID
	return factory.OidcCallback{UserId: claims.Sub}, nil
}

func (c *Cloudflare) Init() error {
	if c.Addition.TeamDomain == "" {
		return fmt.Errorf("team_domain is required")
	}
	if c.Addition.PolicyAUD == "" {
		return fmt.Errorf("policy_aud is required")
	}

	return nil
}

func (c *Cloudflare) Destroy() error {
	// Cloudflare Access 提供商不需要特殊的清理操作
	return nil
}

var _ factory.IOidcProvider = (*Cloudflare)(nil)
