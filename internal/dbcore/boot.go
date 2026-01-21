package dbcore

import (
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/komari-monitor/komari/internal/conf"
	logu "github.com/komari-monitor/komari/internal/log"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	dbBootOnce sync.Once
	dbBootErr  error
)

// Boot initializes the global DB instance based on the provided config.
//
// It is safe to call multiple times; the first call wins.
func BootWithConfig(cfg *conf.Config) error {
	if cfg == nil {
		return errors.New("dbcore: config is nil")
	}

	dbBootOnce.Do(func() {
		var err error

		logConfig := &gorm.Config{
			Logger:                                   logu.NewGormLogger(),
			DisableForeignKeyConstraintWhenMigrating: true,
		}

		switch cfg.Database.DatabaseType {
		case "sqlite", "":
			instance, err = gorm.Open(sqlite.Open(cfg.Database.DatabaseFile), logConfig)
			if err != nil {
				dbBootErr = fmt.Errorf("failed to connect to SQLite3 database: %w", err)
				return
			}
			log.Printf("Using SQLite database file: %s", cfg.Database.DatabaseFile)
			instance.Exec("PRAGMA wal = ON;")
			if err := instance.Exec("PRAGMA journal_mode = WAL;").Error; err != nil {
				log.Printf("Failed to enable WAL mode for SQLite: %v", err)
			}
			instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		case "mysql":
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=True&loc=Local",
				cfg.Database.DatabaseUser,
				cfg.Database.DatabasePass,
				cfg.Database.DatabaseHost,
				cfg.Database.DatabasePort,
				cfg.Database.DatabaseName)
			instance, err = gorm.Open(mysql.Open(dsn), logConfig)
			if err != nil {
				dbBootErr = fmt.Errorf("failed to connect to MySQL database: %w", err)
				return
			}
			log.Printf("Using MySQL database: %s@%s:%s/%s", cfg.Database.DatabaseUser, cfg.Database.DatabaseHost, cfg.Database.DatabasePort, cfg.Database.DatabaseName)
			instance.Exec("SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci")
		default:
			dbBootErr = fmt.Errorf("unsupported database type: %s", cfg.Database.DatabaseType)
			return
		}

		if needsMigration(instance) {
			log.Println("Version changed, running database migration...")
			if err := runMigrations(instance); err != nil {
				dbBootErr = fmt.Errorf("failed to run migrations: %w", err)
				return
			}
			updateSchemaVersion(instance)
			log.Println("Database migration completed")
		} else {
			log.Println("Version unchanged, skipping migration")
		}
	})

	return dbBootErr
}

// Boot initializes the global DB instance based on current conf.Conf.
//
// It is safe to call multiple times; the first call wins.
func Boot() error {
	return BootWithConfig(conf.Conf)
}
