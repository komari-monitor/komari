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
	"github.com/komari-monitor/komari/pkg/corn"
	"github.com/komari-monitor/komari/web/api"

	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	d_notification "github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/cloudflared"
	"github.com/komari-monitor/komari/utils/geoip"
	logutil "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/notifier"
	"github.com/komari-monitor/komari/web/nezha"
	"github.com/komari-monitor/komari/web/oauth"
	report_cache "github.com/komari-monitor/komari/web/report"
	"github.com/komari-monitor/komari/web/router"
	"github.com/komari-monitor/komari/web/security"
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
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		log.Fatalf("Failed to create theme directory: %v", err)
	}
	InitDatabase()
	if utils.VersionHash != "unknown" {
		gin.SetMode(gin.ReleaseMode)
	}
	conf, err := config.GetManyAs[config.Settings]()
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

	config.Subscribe(func(event config.ConfigEvent) {
		if event.IsChanged(config.GeoIpProviderKey) {
			go geoip.InitGeoIp()
		}

		if event.IsChanged(config.NotificationMethodKey) {
			go messageSender.Initialize()
		}

	})
	r.Use(security.CorsMiddleware(conf.CorsOriginCheckEnabled, conf.CorsAllowedOrigins))

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
	if err := tasks.ReloadPingSchedule(); err != nil {
		log.Println("Failed to reload ping schedule:", err)
	}
	if err := d_notification.ReloadLoadNotificationSchedule(); err != nil {
		log.Println("Failed to reload load notification schedule:", err)
	}
	records.CompactRecord()

	if err := corn.AddFunc("records:cleanup", "@every 30m", cleanupScheduledData); err != nil {
		log.Println("Failed to add cleanup scheduled task:", err)
	}
	if err := corn.AddFunc("records:minute", "@every 1m", minuteScheduledWork); err != nil {
		log.Println("Failed to add minute scheduled task:", err)
	}
	if err := corn.AddFunc("notifier:expire", "0 0 9 * * *", notifier.CheckExpireScheduledWork); err != nil {
		log.Println("Failed to add expire notification scheduled task:", err)
	}
	notifier.InitTrafficReportSchedule()
}

func cleanupScheduledData() {
	cfg, _ := config.GetManyAs[config.Settings]()
	records.DeleteRecordBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
	records.CompactRecord()
	tasks.ClearTaskResultsByTimeBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
	tasks.DeletePingRecordsBefore(time.Now().Add(-time.Hour * time.Duration(cfg.PingRecordPreserveTime)))
	auditlog.RemoveOldLogs()
	accounts.RemoveExpiredSessions()
}

func minuteScheduledWork() {
	cfg, _ := config.GetManyAs[config.Settings]()
	report_cache.SaveClientReportToDB()
	if !cfg.RecordEnabled {
		records.DeleteAll()
		tasks.DeleteAllPingRecords()
	}
	// 每分钟检查一次流量提醒
	notifier.CheckTraffic()
}

func OnShutdown() {
	auditlog.Log("", "", "server is shutting down", "info")
	corn.StopAll()
	cloudflared.Shutdown()
}

func OnFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
	cloudflared.Shutdown()
}
