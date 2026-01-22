package dbcore

import (
	"context"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

// FxModule provides the database instance and lifecycle hooks.
//
// It initializes the legacy global DB instance and returns it for DI.
func FxModule() fx.Option {
	return fx.Options(
		fx.Provide(provideDB),
		fx.Invoke(registerDBHooks),
	)
}

func provideDB(cfg *conf.Config) (*gorm.DB, error) {
	if err := BootWithConfig(cfg); err != nil {
		return nil, err
	}
	return GetDBInstance(), nil
}

func registerDBHooks(lc fx.Lifecycle, _db *gorm.DB) {
	// _db forces construction of DB before installing hooks.
	lc.Append(fx.Hook{
		OnStart: func(_ctx context.Context) error {
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
			return nil
		},
		OnStop: func(ctx context.Context) error {
			db := GetDBInstance()
			if db == nil {
				return nil
			}
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			_ = ctx
			return sqlDB.Close()
		},
	})
}
