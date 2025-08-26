package oauth

import (
	_ "github.com/komari-monitor/komari/utils/oauth/cloudflare"
	_ "github.com/komari-monitor/komari/utils/oauth/factory"
	_ "github.com/komari-monitor/komari/utils/oauth/generic"
	_ "github.com/komari-monitor/komari/utils/oauth/github"
	_ "github.com/komari-monitor/komari/utils/oauth/qq"
)

func All() {
	//empty function to ensure all OIDC providers are registered
}