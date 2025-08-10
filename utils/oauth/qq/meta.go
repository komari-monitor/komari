package qq

import (
	"github.com/komari-monitor/komari/utils/oauth/factory"
	"github.com/patrickmn/go-cache"
)

func init() {
	factory.RegisterOidcProvider(func() factory.IOidcProvider {
		return &QQ{}
	})
}

type QQ struct {
	Addition
	stateCache *cache.Cache // 用于存储state和用户信息的映射
}

type Addition struct {
	AggregationURL string `json:"aggregation_url" required:"true" default:"https://login.qjqq.cn"` // 聚合登录地址
	AppId          string `json:"app_id" required:"true"`
	AppKey         string `json:"app_key" required:"true"`
	LoginType      string `json:"login_type" required:"true"` // 登录方式，如qq, google等
}