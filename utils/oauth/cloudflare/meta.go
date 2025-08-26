package cloudflare

import (
	"github.com/komari-monitor/komari/utils/oauth/factory"
)

func init() {
	factory.RegisterOidcProvider(func() factory.IOidcProvider {
		return &Cloudflare{}
	})
}

type Cloudflare struct {
	Addition
}

type Addition struct {
	TeamDomain string `json:"team_domain" required:"true"`
	PolicyAUD  string `json:"policy_aud" required:"true"`
}