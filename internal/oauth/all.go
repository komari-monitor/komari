package oauth

import (
	_ "github.com/komari-monitor/komari/internal/oauth/cloudflare"
	_ "github.com/komari-monitor/komari/internal/oauth/factory"
	_ "github.com/komari-monitor/komari/internal/oauth/generic"
	_ "github.com/komari-monitor/komari/internal/oauth/github"
	_ "github.com/komari-monitor/komari/internal/oauth/qq"
)

func All() {
	//empty function to ensure all OIDC providers are registered
}
