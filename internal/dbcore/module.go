package dbcore

import (
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/app"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
	"gorm.io/gorm"
)

type dbModule struct{}

var _ app.Module = (*dbModule)(nil)

// NewDBModule provides the "db" module for the lifecycle app.
//
// It initializes the legacy global DB instance and returns it for DI.
func NewDBModule() app.Module { return &dbModule{} }

func (m *dbModule) Name() string      { return "db" }
func (m *dbModule) Depends() []string { return []string{"config"} }

func (m *dbModule) Provide(r app.Registry) error {
	return r.Provide(func(cfg *conf.Config) (*gorm.DB, error) {
		if err := BootWithConfig(cfg); err != nil {
			return nil, err
		}
		return GetDBInstance(), nil
	})
}

func (m *dbModule) Hooks() app.Hooks {
	return app.Hooks{
		Init: func(_ *gorm.DB) error { return nil },
		Start: func() {
			// Move legacy event registrations out of init() to avoid implicit side-effects.
			event.On(eventType.SchedulerEvery5Minutes, event.ListenerFunc(func(e event.Event) error {
				db := GetDBInstance()
				if db == nil {
					return nil
				}
				if conf.Conf.Database.DatabaseType == "sqlite" || conf.Conf.Database.DatabaseType == "" {
					db.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
				}
				return nil
			}))

			event.On(eventType.SchedulerEveryDay, event.ListenerFunc(func(e event.Event) error {
				db := GetDBInstance()
				if db == nil {
					return nil
				}
				if conf.Conf.Database.DatabaseType == "sqlite" || conf.Conf.Database.DatabaseType == "" {
					db.Exec("VACUUM;")
				}
				return nil
			}))
		},
		Stop: func() error {
			db := GetDBInstance()
			if db == nil {
				return nil
			}
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.Close()
		},
	}
}

func init() {
	// Ensure "config" can be auto-materialized even if callers only import dbcore.
	app.RegisterModuleFactory("config", conf.NewConfigModule)
	app.RegisterModuleFactory("db", NewDBModule)
}
