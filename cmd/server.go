package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/web/api"

	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	d_notification "github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/cloudflared"
	"github.com/komari-monitor/komari/utils/geoip"
	logutil "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/notifier"
	"github.com/komari-monitor/komari/web/nezha"
	"github.com/komari-monitor/komari/web/oauth"
	"github.com/komari-monitor/komari/web/router"
	"github.com/spf13/cobra"
)

var (
	DynamicCorsEnabled bool = false
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
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		log.Fatalf("Failed to create theme directory: %v", err)
	}
	InitDatabase()
	if utils.VersionHash != "unknown" {
		gin.SetMode(gin.ReleaseMode)
	}
	conf, err := config.GetManyAs[config.Legacy]()
	if err != nil {
		log.Fatal(err)
	}
	go geoip.InitGeoIp()
	go DoScheduledWork()
	go messageSender.Initialize()
	// oidcInit
	go oauth.Initialize()

	if conf.NezhaCompatEnabled {
		go func() {
			if err := nezha.StartNezhaCompat(conf.NezhaCompatListen); err != nil {
				log.Printf("Nezha compat server error: %v", err)
				auditlog.EventLog("error", fmt.Sprintf("Nezha compat server error: %v", err))
			}
		}()
	}

	config.Subscribe(func(event config.ConfigEvent) {
		if ok, t := config.IsChangedT[string](event, config.OAuthProviderKey); ok {
			if t == "" || t == "none" {
				t = "github"
			}
			oidcProvider, err := database.GetOidcConfigByName(t)
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

		if ok, t := config.IsChangedT[bool](event, config.NezhaCompatEnabledKey); ok {
			if t {
				l, _ := config.GetAs[string](config.NezhaCompatListenKey)
				if err := nezha.StartNezhaCompat(l); err != nil {
					log.Printf("start Nezha compat server error: %v", err)
					auditlog.EventLog("error", fmt.Sprintf("start Nezha compat server error: %v", err))
				}
			} else {
				if err := nezha.StopNezhaCompat(); err != nil {
					log.Printf("stop Nezha compat server error: %v", err)
					auditlog.EventLog("error", fmt.Sprintf("stop Nezha compat server error: %v", err))
				}
			}
		}

	})
	// 初始化 cloudflared
	if err := cloudflared.AutoStart(GetEnv("KOMARI_CLOUDFLARED_TOKEN", "")); err != nil {
		log.Printf("failed to auto start cloudflared: %v", err)
	}

	r := gin.New()
	r.Use(logutil.GinLogger())
	r.Use(logutil.GinRecovery())

	// 动态 CORS 中间件

	DynamicCorsEnabled = conf.AllowCors
	config.Subscribe(func(event config.ConfigEvent) {
		if ok, t := config.IsChangedT[bool](event, config.AllowCorsKey); ok {
			DynamicCorsEnabled = t
		}
		if event.IsChanged(config.GeoIpProviderKey) {
			go geoip.InitGeoIp()
		}

		if event.IsChanged(config.NotificationMethodKey) {
			go messageSender.Initialize()
		}

	})
	r.Use(func(c *gin.Context) {
		if DynamicCorsEnabled {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, X-CSRF-Token, X-Requested-With, Set-Cookie")
			c.Header("Access-Control-Expose-Headers", "Content-Length, Authorization, Set-Cookie")
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Max-Age", "43200") // 12 hours
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(204)
				return
			}
		}
		c.Next()
	})

	r.Use(api.IdentityMiddleware())
	r.Use(api.PrivateSiteMiddleware())

	r.Use(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	router.Register(r)

	srv := &http.Server{
		Addr:    flags.Listen,
		Handler: r,
	}
	log.Printf("Starting server on %s ...", flags.Listen)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			OnFatal(err)
			log.Fatalf("listen: %s\n", err)
		}
	}()
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
	if err := tasks.MigrateAllClientsExpansion(); err != nil {
		log.Println("Failed to migrate ping task all_clients expansion:", err)
	}
	tasks.ReloadPingSchedule()
	d_notification.ReloadLoadNotificationSchedule()
	ticker := time.NewTicker(time.Minute * 30)
	minute := time.NewTicker(60 * time.Second)
	//records.DeleteRecordBefore(time.Now().Add(-time.Hour * 24 * 30))
	records.CompactRecord()
	go notifier.CheckExpireScheduledWork()
	for {
		cfg, _ := config.GetManyAs[config.Legacy]()
		select {
		case <-ticker.C:
			records.DeleteRecordBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
			records.CompactRecord()
			tasks.ClearTaskResultsByTimeBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
			tasks.DeletePingRecordsBefore(time.Now().Add(-time.Hour * time.Duration(cfg.PingRecordPreserveTime)))
			auditlog.RemoveOldLogs()
			accounts.RemoveExpiredSessions()
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
	cloudflared.Shutdown()
}

func OnFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
	cloudflared.Shutdown()
}
