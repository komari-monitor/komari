package dbcore

import (
	"context"
	"fmt"
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
	logu "github.com/komari-monitor/komari/internal/log"
	"go.uber.org/fx"
	"gorm.io/driver/sqlite"
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
	var err error

	logConfig := &gorm.Config{
		Logger:                                   logu.NewGormLogger(),
		DisableForeignKeyConstraintWhenMigrating: true,
	}

	switch cfg.Database.DatabaseType {
	case "sqlite", "":
		instance, err = gorm.Open(sqlite.Open(cfg.Database.DatabaseFile), logConfig)
		if err != nil {
			err = fmt.Errorf("failed to connect to SQLite3 database: %w", err)
			return nil, err
		}
		log.Printf("Using SQLite database file: %s", cfg.Database.DatabaseFile)
		instance.Exec("PRAGMA wal = ON;")
		if err := instance.Exec("PRAGMA journal_mode = WAL;").Error; err != nil {
			log.Printf("Failed to enable WAL mode for SQLite: %v", err)
		}
		instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
	default:
		err = fmt.Errorf("unsupported database type: %s", cfg.Database.DatabaseType)
		return nil, err
	}

	if needsMigration(instance) {
		log.Println("Version changed, running database migration...")
		if err := runMigrations(instance); err != nil {
			err = fmt.Errorf("failed to run migrations: %w", err)
			return nil, err
		}
		updateSchemaVersion(instance)
		log.Println("Database migration completed")
	} else {
		log.Println("Version unchanged, skipping migration")
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
