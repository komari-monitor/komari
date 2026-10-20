package qq

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/web/oauth/factory"
	"github.com/patrickmn/go-cache"
)

func (q *QQ) GetName() string {
	return "qq"
}

func (q *QQ) GetConfiguration() factory.Configuration {
	return &q.Addition
}

func (q *QQ) GetAuthorizationURL(redirectURI string) (string, string) {
	state := utils.GenerateRandomString(16)

	requestURL := fmt.Sprintf(
		"%s/connect.php?act=login&appid=%s&appkey=%s&type=%s&redirect_uri=%s",
		q.Addition.AggregationURL,
		url.QueryEscape(q.Addition.AppId),
		url.QueryEscape(q.Addition.AppKey),
		url.QueryEscape(q.Addition.LoginType),
		url.QueryEscape(redirectURI),
	)

	resp, err := http.Get(requestURL)
	if err != nil {
		return "", state
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", state
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		URL  string `json:"url"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", state
	}

	if result.Code != 0 {
		return "", state
	}

	q.stateCache.Set(state, true, cache.DefaultExpiration)
	return result.URL, state
}

// OnCallback 处理 QQ OAuth 回调
func (q *QQ) OnCallback(ctx *gin.Context, state string, query map[string]string, callbackURI string) (factory.OidcCallback, error) {
	code := query["code"]
	loginType := query["type"]
	if loginType == "" {
		loginType = q.Addition.LoginType
	}

	if q.stateCache == nil {
		return factory.OidcCallback{}, fmt.Errorf("state cache not initialized")
	}
	if _, ok := q.stateCache.Get(state); !ok {
		return factory.OidcCallback{}, fmt.Errorf("invalid state")
	}

	if code == "" {
		return factory.OidcCallback{}, fmt.Errorf("no authorization code provided")
	}

	callbackURL := fmt.Sprintf(
		"%s/connect.php?act=callback&appid=%s&appkey=%s&type=%s&code=%s",
		q.Addition.AggregationURL,
		url.QueryEscape(q.Addition.AppId),
		url.QueryEscape(q.Addition.AppKey),
		url.QueryEscape(loginType),
		url.QueryEscape(code),
	)

	resp, err := http.Get(callbackURL)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to get user info: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return factory.OidcCallback{}, fmt.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to read response: %v", err)
	}

	var result struct {
		Code        int    `json:"code"`
		Msg         string `json:"msg"`
		Type        string `json:"type"`
		SocialUid   string `json:"social_uid"`
		AccessToken string `json:"access_token"`
		FaceImg     string `json:"faceimg"`
		Nickname    string `json:"nickname"`
		Gender      string `json:"gender"`
		Location    string `json:"location"`
		IP          string `json:"ip"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return factory.OidcCallback{}, fmt.Errorf("failed to parse callback response: %v, response body: %s", err, string(body))
	}

	if result.Code != 0 {
		return factory.OidcCallback{}, fmt.Errorf("QQ login callback failed with code %d: %s", result.Code, result.Msg)
	}

	if result.SocialUid == "" {
		return factory.OidcCallback{}, fmt.Errorf("empty social_uid returned, full response: %s", string(body))
	}

	return factory.OidcCallback{UserId: result.SocialUid}, nil
}

func (q *QQ) Init() error {
	q.stateCache = cache.New(time.Minute*5, time.Minute*10)
	return nil
}

func (q *QQ) Destroy() error {
	q.stateCache.Flush()
	return nil
}

var _ factory.IOidcProvider = (*QQ)(nil)
