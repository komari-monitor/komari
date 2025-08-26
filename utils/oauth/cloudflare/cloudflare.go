package cloudflare

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/komari-monitor/komari/utils/oauth/factory"
)

func (c *Cloudflare) GetName() string {
	return "cloudflare_access"
}

func (c *Cloudflare) GetConfiguration() factory.Configuration {
	return &c.Addition
}

func (c *Cloudflare) GetAuthorizationURL(redirectURI string) (string, string) {
	// Cloudflare Access 不需要跳转，直接返回回调 URL
	// 前端应该直接调用回调接口
	return redirectURI, ""
}

func (c *Cloudflare) OnCallback(ctx context.Context, state string, query map[string]string, callbackURI string) (factory.OidcCallback, error) {
	// 从请求头中获取 Cloudflare Access JWT
	accessJWT := query["cf_access_jwt"]
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
	
	keySet := oidc.NewRemoteKeySet(ctx, certsURL)
	verifier := oidc.NewVerifier(teamDomain, keySet, config)
	
	// 验证 token
	idToken, err := verifier.Verify(ctx, accessJWT)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to verify Cloudflare Access token: %w", err)
	}
	
	// 提取用户信息
	var claims struct {
		// Email string `json:"email"`
		Sub   string `json:"sub"`
	}
	
	if err := idToken.Claims(&claims); err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to extract claims from token: %w", err)
	}
	
	// 使用 sub 作为用户 ID
	return factory.OidcCallback{UserId: claims.Sub}, nil
}

func (c *Cloudflare) Init() error {
	// 验证配置
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