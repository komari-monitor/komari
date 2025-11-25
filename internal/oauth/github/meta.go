package github

import (
	"github.com/komari-monitor/komari/internal/oauth/factory"
	"github.com/patrickmn/go-cache"
)

func init() {
	factory.RegisterOidcProvider(func() factory.IOidcProvider {
		return &Github{}
	})
}

type Github struct {
	Addition
	stateCache *cache.Cache // 用于存储state和用户信息的映射
}

type Addition struct {
	ClientId     string `json:"client_id" required:"true"`
	ClientSecret string `json:"client_secret" required:"true"`
}

type GitHubUser struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
	Email string `json:"email"`
}
