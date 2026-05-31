package oauth

import (
	_ "github.com/komari-monitor/komari/web/oauth/cloudflare"
	_ "github.com/komari-monitor/komari/web/oauth/factory"
	_ "github.com/komari-monitor/komari/web/oauth/generic"
	_ "github.com/komari-monitor/komari/web/oauth/github"
	_ "github.com/komari-monitor/komari/web/oauth/qq"
)

func All() {
	//empty function to ensure all OIDC providers are registered
}
