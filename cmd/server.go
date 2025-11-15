package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database"
	"github.com/komari-monitor/komari/internal/database/accounts"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/dbcore"
	"github.com/komari-monitor/komari/internal/database/models"
	d_notification "github.com/komari-monitor/komari/internal/database/notification"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/geoip"
	logutil "github.com/komari-monitor/komari/internal/log"
	"github.com/komari-monitor/komari/internal/messageSender"
	"github.com/komari-monitor/komari/internal/notifier"
	"github.com/komari-monitor/komari/internal/oauth"
	"github.com/komari-monitor/komari/internal/patch"
	"github.com/komari-monitor/komari/internal/restore"
	"github.com/komari-monitor/komari/pkg/cloudflared"
	"github.com/komari-monitor/komari/server"
	"github.com/spf13/cobra"
)

var ServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the server",
	Long:  `Start the server`,
	Run: func(cmd *cobra.Command, args []string) {
		RunServer()
	},
}

func init() {
	// 从环境变量获取监听地址
	listenAddr := GetEnv("KOMARI_LISTEN", "0.0.0.0:25774")
	ServerCmd.PersistentFlags().StringVarP(&flags.Listen, "listen", "l", listenAddr, "监听地址 [env: KOMARI_LISTEN]")
	RootCmd.AddCommand(ServerCmd)
}

func RunServer() {
	// #region 初始化
	// 创建目录
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		log.Fatalf("Failed to create theme directory: %v", err)
	}
	// 进行备份恢复
	if restore.NeedBackupRestore() {
		restore.RestoreBackup()
	}
	conf.Load()
	InitDatabase()
	patch.ApplyPatch()

	if conf.Version != conf.Version_Development {
		gin.SetMode(gin.ReleaseMode)
	}

	config, err := conf.GetWithV1Format()
	if err != nil {
		log.Fatal(err)
	}

	r := gin.New()
	r.Use(logutil.GinLogger())
	r.Use(logutil.GinRecovery())

	event.Trigger(eventType.ServerInitializeStart, event.M{"config": config, "engine": r})

	go geoip.InitGeoIp()
	go DoScheduledWork()
	go messageSender.Initialize()
	go oauth.Initialize()

	server.StartNezhaGRPCServer(config.NezhaCompatListen)

	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		newConf := e.Get("new").(conf.Config)
		oldConf := e.Get("old").(conf.Config)
		if newConf.Login.OAuthProvider != oldConf.Login.OAuthProvider {
			oidcProvider, err := database.GetOidcConfigByName(newConf.Login.OAuthProvider)
			if err != nil {
				log.Printf("Failed to get OIDC provider config: %v", err)
			} else {
				log.Printf("Using %s as OIDC provider", oidcProvider.Name)
			}
			err = oauth.LoadProvider(oidcProvider.Name, oidcProvider.Addition)
			if err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
		if newConf.Notification.NotificationMethod != oldConf.Notification.NotificationMethod {
			messageSender.Initialize()
		}
		return nil
	}), event.Max)

	// 初始化 cloudflared
	if strings.ToLower(GetEnv("KOMARI_ENABLE_CLOUDFLARED", "false")) == "true" {
		err := cloudflared.RunCloudflared() // 阻塞，确保cloudflared跑起来
		if err != nil {
			log.Fatalf("Failed to run cloudflared: %v", err)
		}
	}

	server.Init(r)

	srv := &http.Server{
		Addr:    flags.Listen,
		Handler: r,
	}

	event.Trigger(eventType.ServerInitializeDone, event.M{"config": config})

	log.Printf("Starting server on %s ...", flags.Listen)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		OnFatal(err)
		log.Fatalf("listen: %s\n", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	OnShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

}

func InitDatabase() {
	// // 打印数据库类型和连接信息
	// if flags.DatabaseType == "mysql" {
	// 	log.Printf("使用 MySQL 数据库连接: %s@%s:%s/%s",
	// 		flags.DatabaseUser, flags.DatabaseHost, flags.DatabasePort, flags.DatabaseName)
	// 	log.Printf("环境变量配置: [KOMARI_DB_TYPE=%s] [KOMARI_DB_HOST=%s] [KOMARI_DB_PORT=%s] [KOMARI_DB_USER=%s] [KOMARI_DB_NAME=%s]",
	// 		os.Getenv("KOMARI_DB_TYPE"), os.Getenv("KOMARI_DB_HOST"), os.Getenv("KOMARI_DB_PORT"),
	// 		os.Getenv("KOMARI_DB_USER"), os.Getenv("KOMARI_DB_NAME"))
	// } else {
	// 	log.Printf("使用 SQLite 数据库文件: %s", flags.DatabaseFile)
	// 	log.Printf("环境变量配置: [KOMARI_DB_TYPE=%s] [KOMARI_DB_FILE=%s]",
	// 		os.Getenv("KOMARI_DB_TYPE"), os.Getenv("KOMARI_DB_FILE"))
	// }
	var count int64 = 0
	if dbcore.GetDBInstance().Model(&models.User{}).Count(&count); count == 0 {
		user, passwd, err := accounts.CreateDefaultAdminAccount()
		if err != nil {
			panic(err)
		}
		log.Println("Default admin account created. Username:", user, ", Password:", passwd)
	}
}

// #region 定时任务
func DoScheduledWork() {
	tasks.ReloadPingSchedule()
	d_notification.ReloadLoadNotificationSchedule()
	ticker := time.NewTicker(time.Minute * 30)
	minute := time.NewTicker(60 * time.Second)
	//records.DeleteRecordBefore(time.Now().Add(-time.Hour * 24 * 30))
	records.CompactRecord()
	cfg, _ := conf.GetWithV1Format()
	go notifier.CheckExpireScheduledWork()
	for {
		select {
		case <-ticker.C:
			records.DeleteRecordBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
			records.CompactRecord()
			tasks.ClearTaskResultsByTimeBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
			tasks.DeletePingRecordsBefore(time.Now().Add(-time.Hour * time.Duration(cfg.PingRecordPreserveTime)))
			auditlog.RemoveOldLogs()
		case <-minute.C:
			api.SaveClientReportToDB()
			if !cfg.RecordEnabled {
				records.DeleteAll()
				tasks.DeleteAllPingRecords()
			}
			// 每分钟检查一次流量提醒
			go notifier.CheckTraffic()
		}
	}

}

func OnShutdown() {
	auditlog.Log("", "", "server is shutting down", "info")
	cloudflared.Kill()
}

func OnFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
	cloudflared.Kill()
}
