package dbcore

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/common"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database/models"
	logutil "github.com/komari-monitor/komari/utils/log"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// zipDirectoryExcluding 将 srcDir 打包为 dstZip，exclude 是绝对路径集合需要排除
func zipDirectoryExcluding(srcDir, dstZip string, exclude map[string]struct{}) error {
	// 规范化排除路径为绝对路径
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}

	out, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	absSrc, _ := filepath.Abs(srcDir)
	walkErr := filepath.Walk(absSrc, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 排除 backup.zip 本身
		if _, ok := normExclude[path]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// 计算 zip 内相对路径
		rel, err := filepath.Rel(absSrc, path)
		if err != nil {
			return err
		}
		// 根目录跳过
		if rel == "." {
			return nil
		}
		// 替换为正斜杠
		zipName := filepath.ToSlash(rel)

		if info.IsDir() {
			_, err := zw.Create(zipName + "/")
			return err
		}
		// 普通文件
		fh, err := os.Open(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(zipName)
		if err != nil {
			fh.Close()
			return err
		}
		if _, err := io.Copy(w, fh); err != nil {
			fh.Close()
			return err
		}
		fh.Close()
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	return zw.Close()
}

// removeAllInDirExcept 删除 dir 下除 exclude 指定绝对路径外的所有文件和文件夹
func removeAllInDirExcept(dir string, exclude map[string]struct{}) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	normExclude := make(map[string]struct{}, len(exclude))
	for p := range exclude {
		abs, _ := filepath.Abs(p)
		normExclude[abs] = struct{}{}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(absDir, e.Name())
		if _, ok := normExclude[full]; ok {
			continue
		}
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	return nil
}

// unzipToDir 将 zipPath 解压到 dstDir，包含路径遍历保护
func unzipToDir(zipPath, dstDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
	absDst, _ := filepath.Abs(dstDir)

	for _, f := range zr.File {
		// 构造目标路径并做路径遍历保护
		cleanName := filepath.Clean(f.Name)
		targetPath := filepath.Join(absDst, cleanName)
		if !strings.HasPrefix(targetPath, absDst+string(os.PathSeparator)) && targetPath != absDst {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(targetPath)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// mergeClientInfo 将旧版ClientInfo数据迁移到新版Client表
func mergeClientInfo(db *gorm.DB) {
	var clientInfos []common.ClientInfo
	if err := db.Find(&clientInfos).Error; err != nil {
		log.Printf("Failed to read ClientInfo table: %v", err)
		return
	}

	for _, info := range clientInfos {
		var client models.Client
		if err := db.Where("uuid = ?", info.UUID).First(&client).Error; err != nil {
			log.Printf("Could not find Client record with UUID %s: %v", info.UUID, err)
			continue
		}

		// 更新Client记录
		client.Name = info.Name
		client.CpuName = info.CpuName
		client.Virtualization = info.Virtualization
		client.Arch = info.Arch
		client.CpuCores = info.CpuCores
		client.OS = info.OS
		client.GpuName = info.GpuName
		client.IPv4 = info.IPv4
		client.IPv6 = info.IPv6
		client.Region = info.Region
		client.Remark = info.Remark
		client.PublicRemark = info.PublicRemark
		client.MemTotal = info.MemTotal
		client.SwapTotal = info.SwapTotal
		client.DiskTotal = info.DiskTotal
		client.Version = info.Version
		client.Weight = info.Weight
		client.Price = info.Price
		client.BillingCycle = info.BillingCycle
		client.ExpiredAt = models.FromTime(info.ExpiredAt)
		// Save updated Client record
		if err := db.Save(&client).Error; err != nil {
			log.Printf("Failed to update Client record: %v", err)
			continue
		}
	}

	// Backup and rename old table after migration
	if err := db.Migrator().RenameTable("client_infos", "client_infos_backup"); err != nil {
		log.Printf("Failed to backup ClientInfo table: %v", err)
		return
	}
	log.Println("Data migration completed, old table has been backed up as client_infos_backup")
}

func MergeDatabase(db *gorm.DB) {
	if db.Migrator().HasTable("client_infos") {
		log.Println("[>0.0.5] Legacy ClientInfo table detected, starting data migration...")
		mergeClientInfo(db)
	}
	if db.Migrator().HasColumn(&models.Config{}, "allow_cros") {
		log.Println("[>0.0.5a] Renaming column 'allow_cros' to 'allow_cors' in config table...")
		db.Migrator().RenameColumn(&models.Config{}, "allow_cros", "allow_cors")
	}
	if db.Migrator().HasColumn(&models.LoadNotification{}, "client") {
		log.Println("[>0.1.4] Rebuilding LoadNotification table....")
		db.Migrator().DropTable(&models.LoadNotification{})
	}
	if !db.Migrator().HasTable(&models.OidcProvider{}) && db.Migrator().HasTable(&models.Config{}) {
		log.Println("[>1.0.2] Merge OidcProvider table....")
		var config struct {
			OAuthClientID     string `json:"o_auth_client_id" gorm:"type:varchar(255)"`
			OAuthClientSecret string `json:"o_auth_client_secret" gorm:"type:varchar(255)"`
		}
		if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&config).Error; err != nil {
			log.Println("Failed to get config for OIDC provider migration:", err)
		}
		db.AutoMigrate(&models.OidcProvider{})
		j, err := json.Marshal(&map[string]string{
			"client_id":     config.OAuthClientID,
			"client_secret": config.OAuthClientSecret,
		})
		if err != nil {
			log.Println("Failed to marshal OIDC provider config:", err)
			return
		}
		db.Save(&models.OidcProvider{
			Name:     "github",
			Addition: string(j),
		})
		db.AutoMigrate(&models.Config{})
		db.Model(&models.Config{}).Where("id = 1").Update("o_auth_provider", "github")
	}
	if !db.Migrator().HasTable(&models.MessageSenderProvider{}) && db.Migrator().HasTable(&models.Config{}) {
		log.Println("[>1.0.2] Migrate MessageSender configuration....")
		var config struct {
			TelegramBotToken   string `json:"telegram_bot_token" gorm:"type:varchar(255)"`
			TelegramChatID     string `json:"telegram_chat_id" gorm:"type:varchar(255)"`
			TelegramEndpoint   string `json:"telegram_endpoint" gorm:"type:varchar(255)"`
			EmailHost          string `json:"email_host" gorm:"type:varchar(255)"`
			EmailPort          int    `json:"email_port" gorm:"type:int"`
			EmailUsername      string `json:"email_username" gorm:"type:varchar(255)"`
			EmailPassword      string `json:"email_password" gorm:"type:varchar(255)"`
			EmailSender        string `json:"email_sender" gorm:"type:varchar(255)"`
			EmailReceiver      string `json:"email_receiver" gorm:"type:varchar(255)"`
			EmailUseSSL        bool   `json:"email_use_ssl" gorm:"type:boolean"`
			NotificationMethod string `json:"notification_method" gorm:"type:varchar(50)"`
		}
		if err := db.Raw("SELECT * FROM configs LIMIT 1").Scan(&config).Error; err != nil {
			log.Println("Failed to get config for MessageSender migration:", err)
		}

		db.AutoMigrate(&models.MessageSenderProvider{})

		// 迁移Telegram配置
		if config.NotificationMethod == "telegram" && config.TelegramBotToken != "" {
			telegramConfig := map[string]interface{}{
				"bot_token": config.TelegramBotToken,
				"chat_id":   config.TelegramChatID,
				"endpoint":  config.TelegramEndpoint,
			}
			if telegramConfig["endpoint"] == "" {
				telegramConfig["endpoint"] = "https://api.telegram.org/bot"
			}
			telegramConfigJSON, err := json.Marshal(telegramConfig)
			if err != nil {
				log.Println("Failed to marshal Telegram config:", err)
			} else {
				db.Save(&models.MessageSenderProvider{
					Name:     "telegram",
					Addition: string(telegramConfigJSON),
				})
			}
		}

		// 迁移Email配置
		if config.NotificationMethod == "email" && config.EmailHost != "" {
			emailConfig := map[string]interface{}{
				"host":     config.EmailHost,
				"port":     config.EmailPort,
				"username": config.EmailUsername,
				"password": config.EmailPassword,
				"sender":   config.EmailSender,
				"receiver": config.EmailReceiver,
				"use_ssl":  config.EmailUseSSL,
			}
			emailConfigJSON, err := json.Marshal(emailConfig)
			if err != nil {
				log.Println("Failed to marshal Email config:", err)
			} else {
				db.Save(&models.MessageSenderProvider{
					Name:     "email",
					Addition: string(emailConfigJSON),
				})
			}
		}

		// 删除旧的配置字段
		if db.Migrator().HasColumn(&models.Config{}, "telegram_bot_token") {
			db.Migrator().DropColumn(&models.Config{}, "telegram_bot_token")
		}
		if db.Migrator().HasColumn(&models.Config{}, "telegram_chat_id") {
			db.Migrator().DropColumn(&models.Config{}, "telegram_chat_id")
		}
		if db.Migrator().HasColumn(&models.Config{}, "telegram_endpoint") {
			db.Migrator().DropColumn(&models.Config{}, "telegram_endpoint")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_host") {
			db.Migrator().DropColumn(&models.Config{}, "email_host")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_port") {
			db.Migrator().DropColumn(&models.Config{}, "email_port")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_username") {
			db.Migrator().DropColumn(&models.Config{}, "email_username")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_password") {
			db.Migrator().DropColumn(&models.Config{}, "email_password")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_sender") {
			db.Migrator().DropColumn(&models.Config{}, "email_sender")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_receiver") {
			db.Migrator().DropColumn(&models.Config{}, "email_receiver")
		}
		if db.Migrator().HasColumn(&models.Config{}, "email_use_ssl") {
			db.Migrator().DropColumn(&models.Config{}, "email_use_ssl")
		}
	}
}

var (
	instance *gorm.DB
	once     sync.Once
)

func GetDBInstance() *gorm.DB {
	once.Do(func() {

		var err error

		// 在数据库初始化前执行：如果存在 ./data/backup.zip，则进行恢复逻辑
		func() {
			backupZipPath := filepath.Join(".", "data", "backup.zip")
			if _, statErr := os.Stat(backupZipPath); statErr == nil {
				// 4. 把除了 ./data/backup.zip 之外的所有文件压缩到 ./backup/{time}.zip
				if err := os.MkdirAll("./backup", 0755); err != nil {
					log.Printf("[restore] failed to create backup dir: %v", err)
				} else {
					tsName := time.Now().Format("20060102-150405")
					bakPath := filepath.Join("./backup", fmt.Sprintf("%s.zip", tsName))
					if zipErr := zipDirectoryExcluding("./data", bakPath, map[string]struct{}{backupZipPath: {}}); zipErr != nil {
						log.Printf("[restore] failed to zip current data: %v", zipErr)
					} else {
						log.Printf("[restore] current data zipped to %s", bakPath)
					}
				}

				// 5. 删除除了 ./data/backup.zip 之外的所有文件
				if delErr := removeAllInDirExcept("./data", map[string]struct{}{backupZipPath: {}}); delErr != nil {
					log.Printf("[restore] failed to cleanup data dir: %v", delErr)
				}

				// 6. 解压 ./data/backup.zip 到 ./data
				if unzipErr := unzipToDir(backupZipPath, "./data"); unzipErr != nil {
					log.Printf("[restore] failed to unzip backup into data: %v", unzipErr)
				} else {
					log.Printf("[restore] backup.zip extracted to ./data")
				}

				// 7. 删除 ./data/backup.zip
				if rmErr := os.Remove(backupZipPath); rmErr != nil {
					log.Printf("[restore] failed to remove backup.zip: %v", rmErr)
				} else {
					log.Printf("[restore] backup.zip removed")
				}
				// 8. 删除标记
				if rmErr := os.Remove("./data/komari-backup-markup"); rmErr != nil {
					log.Printf("[restore] failed to remove komari-backup-markup: %v", rmErr)
				} else {
					log.Printf("[restore] komari-backup-markup removed")
				}
			}
		}()

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
		// case "mysql":
		// 	// MySQL 连接
		// 	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=True&loc=Local",
		// 		flags.DatabaseUser,
		// 		flags.DatabasePass,
		// 		flags.DatabaseHost,
		// 		flags.DatabasePort,
		// 		flags.DatabaseName)
		// 	instance, err = gorm.Open(mysql.Open(dsn), logConfig)
		// 	if err != nil {
		// 		log.Fatalf("Failed to connect to MySQL database: %v", err)
		// 	}
		// 	log.Printf("Using MySQL database: %s@%s:%s/%s", flags.DatabaseUser, flags.DatabaseHost, flags.DatabasePort, flags.DatabaseName)
		default:
			log.Fatalf("Unsupported database type: %s", flags.DatabaseType)
		}
		config.SetDb(instance)
		MergeDatabase(instance)
		// 自动迁移模型
		err = instance.AutoMigrate(
			&models.User{},
			&models.Client{},
			&models.Record{},
			&models.GPURecord{},
			// &models.Config{},
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
