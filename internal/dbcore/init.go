package dbcore

import (
	"fmt"
	"log"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/eventType"
	logu "github.com/komari-monitor/komari/internal/log"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		var err error

		logConfig := &gorm.Config{
			Logger:                                   logu.NewGormLogger(),
			DisableForeignKeyConstraintWhenMigrating: true, // 禁用外键约束，避免迁移时的问题
		}

		// 根据数据库类型选择不同的连接方式
		switch conf.Conf.Database.DatabaseType {
		case "sqlite", "":
			// SQLite 连接
			instance, err = gorm.Open(sqlite.Open(conf.Conf.Database.DatabaseFile), logConfig)
			if err != nil {
				log.Fatalf("Failed to connect to SQLite3 database: %v", err)
			}
			log.Printf("Using SQLite database file: %s", conf.Conf.Database.DatabaseFile)
			instance.Exec("PRAGMA wal = ON;")
			if err := instance.Exec("PRAGMA journal_mode = WAL;").Error; err != nil {
				log.Printf("Failed to enable WAL mode for SQLite: %v", err)
			}
			instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		case "mysql":
			// MySQL 连接
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=True&loc=Local",
				conf.Conf.Database.DatabaseUser,
				conf.Conf.Database.DatabasePass,
				conf.Conf.Database.DatabaseHost,
				conf.Conf.Database.DatabasePort,
				conf.Conf.Database.DatabaseName)
			instance, err = gorm.Open(mysql.Open(dsn), logConfig)
			if err != nil {
				return fmt.Errorf("failed to connect to MySQL database: %w", err)
			}
			log.Printf("Using MySQL database: %s@%s:%s/%s", conf.Conf.Database.DatabaseUser, conf.Conf.Database.DatabaseHost, conf.Conf.Database.DatabasePort, conf.Conf.Database.DatabaseName)
			// 设置数据库默认字符集为 utf8mb4
			instance.Exec("SET NAMES utf8mb4 COLLATE utf8mb4_unicode_ci")
		default:
			return fmt.Errorf("unsupported database type: %s", conf.Conf.Database.DatabaseType)
		}

		// 检查版本号决定是否需要迁移
		if needsMigration(instance) {
			log.Println("Version changed, running database migration...")
			if err := runMigrations(instance); err != nil {
				log.Fatalf("Failed to run migrations: %v", err)
			}
			updateSchemaVersion(instance)
			log.Println("Database migration completed")
		} else {
			log.Println("Version unchanged, skipping migration")
		}

		return nil
	}), event.Max+1)

	event.On(eventType.SchedulerEvery5Minutes, event.ListenerFunc(func(e event.Event) error {
		if conf.Conf.Database.DatabaseType == "sqlite" {
			instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		}
		return nil
	}))

	event.On(eventType.SchedulerEveryDay, event.ListenerFunc(func(e event.Event) error {
		if conf.Conf.Database.DatabaseType == "sqlite" {
			instance.Exec("VACUUM;")
		}
		return nil
	}))

}
