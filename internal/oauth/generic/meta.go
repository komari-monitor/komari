package generic

import (
	"github.com/komari-monitor/komari/internal/oauth/factory"
	"github.com/patrickmn/go-cache"
)

func init() {
	factory.RegisterOidcProvider(func() factory.IOidcProvider {
		return &Generic{}
	})
}

type Generic struct {
	Addition
	stateCache *cache.Cache // 用于存储state和用户信息的映射
}

type Addition struct {
	ClientId     string `json:"client_id" required:"true"`
	ClientSecret string `json:"client_secret" required:"true"`
	AuthURL      string `json:"auth_url" required:"true"`
	TokenURL     string `json:"token_url" required:"true"`
	UserInfoURL  string `json:"user_info_url" required:"true"`
	Scope        string `json:"scope"`
	UserIDField  string `json:"user_id_field" required:"true"`
}
