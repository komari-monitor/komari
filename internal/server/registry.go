package server

import (
	"github.com/komari-monitor/komari/internal/app"
)

func init() {
	app.RegisterModuleFactory(NewHTTPModule().Name(), func() app.Module { return NewHTTPModule() })
}
