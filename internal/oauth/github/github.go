package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/oauth/factory"
	"github.com/komari-monitor/komari/pkg/utils"
	"github.com/patrickmn/go-cache"
)

func init() {

}

func (g *Github) GetName() string {
	return "github"
}
func (g *Github) GetConfiguration() factory.Configuration {
	return &g.Addition
}

func (g *Github) GetAuthorizationURL(_ string) (string, string) {
	state := utils.GenerateRandomString(16)

	// 构建GitHub OAuth授权URL
	authURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&state=%s&scope=user:email",
		url.QueryEscape(g.Addition.ClientId),
		url.QueryEscape(state),
	)
	g.stateCache.Set(state, true, cache.NoExpiration)
	return authURL, state
}
func (g *Github) OnCallback(ctx *gin.Context, state string, query map[string]string, _ string) (factory.OidcCallback, error) {
	code := query["code"]

	// 验证state防止CSRF攻击
	// state, _ := c.Cookie("oauth_state")
	if g.stateCache == nil {
		return factory.OidcCallback{}, fmt.Errorf("state cache not initialized")
	}
	if _, ok := g.stateCache.Get(state); !ok {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}
	if state == "" {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}

	// 获取code
	//code := c.Query("code")
	if code == "" {
		return factory.OidcCallback{}, fmt.Errorf("no code provided")
	}

	// 获取访问令牌
	tokenURL := "https://github.com/login/oauth/access_token"
	data := url.Values{
		"client_id":     {g.Addition.ClientId},
		"client_secret": {g.Addition.ClientSecret},
		"code":          {code},
	}

	req, _ := http.NewRequest("POST", tokenURL, nil)
	req.URL.RawQuery = data.Encode()
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to get access token: %s", utils.DataMasking(err.Error(), []string{g.Addition.ClientSecret, g.Addition.ClientId}))
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Scope       string `json:"scope"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to parse access token response: %s", utils.DataMasking(err.Error(), []string{g.Addition.ClientSecret, g.Addition.ClientId}))
	}

	// 获取用户信息
	userReq, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userReq.Header.Set("Accept", "application/json")

	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to get user info: %v", err)
	}
	defer userResp.Body.Close()

	var githubUser GitHubUser
	if err := json.NewDecoder(userResp.Body).Decode(&githubUser); err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to parse user info response: %v", err)
	}

	return factory.OidcCallback{UserId: fmt.Sprintf("%d", githubUser.ID)}, nil
}
func (g *Github) Init() error {
	g.stateCache = cache.New(time.Minute*5, time.Minute*10)
	return nil
}
func (g *Github) Destroy() error {
	g.stateCache.Flush()
	return nil
}

var _ factory.IOidcProvider = (*Github)(nil)
