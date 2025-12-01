package dbcore

import (
	"fmt"
	"log"
	"sync"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/database/models"
	"github.com/komari-monitor/komari/internal/eventType"
	logutil "github.com/komari-monitor/komari/internal/log"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var (
	instance *gorm.DB
	once     sync.Once
)

func GetDBInstance() *gorm.DB {
	once.Do(func() {
		var err error

		logConfig := &gorm.Config{
			Logger: logutil.NewGormLogger(),
		}

		// 根据数据库类型选择不同的连接方式
		switch flags.DatabaseType {
		case "sqlite", "":
			// SQLite 连接
			instance, err = gorm.Open(sqlite.Open(flags.DatabaseFile), logConfig)
			if err != nil {
				log.Fatalf("Failed to connect to SQLite3 database: %v", err)
			}
			log.Printf("Using SQLite database file: %s", flags.DatabaseFile)
			instance.Exec("PRAGMA wal = ON;")
			if err := instance.Exec("PRAGMA journal_mode = WAL;").Error; err != nil {
				log.Printf("Failed to enable WAL mode for SQLite: %v", err)
			}
			instance.Exec("VACUUM;")
			instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		case "mysql":
			// MySQL 连接
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=True&loc=Local",
				flags.DatabaseUser,
				flags.DatabasePass,
				flags.DatabaseHost,
				flags.DatabasePort,
				flags.DatabaseName)
			instance, err = gorm.Open(mysql.Open(dsn), logConfig)
			if err != nil {
				log.Fatalf("Failed to connect to MySQL database: %v", err)
			}
			log.Printf("Using MySQL database: %s@%s:%s/%s", flags.DatabaseUser, flags.DatabaseHost, flags.DatabasePort, flags.DatabaseName)
		default:
			log.Fatalf("Unsupported database type: %s", flags.DatabaseType)
		}
		// 自动迁移模型
		err = instance.AutoMigrate(
			&models.User{},
			&models.Client{},
			&models.Record{},
			&models.GPURecord{},
			//&conf.V1Struct{},
			&models.Log{},
			&models.Clipboard{},
			&models.LoadNotification{},
			&models.OfflineNotification{},
			&models.PingRecord{},
			&models.PingTask{},
			&models.OidcProvider{},
			&models.MessageSenderProvider{},
			&models.ThemeConfiguration{},
		)
		if err != nil {
			log.Fatalf("Failed to create tables: %v", err)
		}
		err = instance.Table("records_long_term").AutoMigrate(
			&models.Record{},
		)
		if err != nil {
			log.Printf("Failed to create records_long_term table, it may already exist: %v", err)
		}
		err = instance.Table("gpu_records_long_term").AutoMigrate(
			&models.GPURecord{},
		)
		if err != nil {
			log.Printf("Failed to create gpu_records_long_term table, it may already exist: %v", err)
		}
		err = instance.AutoMigrate(
			&models.Session{},
		)
		if err != nil {
			log.Printf("Failed to create Session table, it may already exist: %v", err)
		}
		err = instance.AutoMigrate(
			&models.Task{},
			&models.TaskResult{},
		)
		if err != nil {
			log.Printf("Failed to create Task and TaskResult table, it may already exist: %v", err)
		}

	})
	return instance
}

func init() {
	event.On(eventType.SchedulerEvery5Minutes, event.ListenerFunc(func(e event.Event) error {
		instance.Exec("PRAGMA wal_checkpoint(TRUNCATE);")
		return nil
	}))
}
