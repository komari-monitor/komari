package dbcore

import "github.com/komari-monitor/komari/internal/app"

func init() {
	app.RegisterModuleFactory(NewDBModule().Name(), NewDBModule)
}
