package cmd

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/eventType"
	logutil "github.com/komari-monitor/komari/internal/log"
	"github.com/komari-monitor/komari/public"
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
var AllowCors bool = false

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
	internal.All()
	if conf.Version != conf.Version_Development {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(logutil.GinLogger())
	r.Use(logutil.GinRecovery())

	err, _ := event.Trigger(eventType.ServerInitializeStart, event.M{"engine": r})
	if err != nil {
		slog.Error("Something went wrong during ServerInitializeStart event.", slog.Any("error", err))
		os.Exit(1)
	}

	event.On(eventType.ConfigUpdated, event.ListenerFunc(func(e event.Event) error {
		newConf := e.Get("new").(conf.Config)
		AllowCors = newConf.Site.AllowCors
		public.UpdateIndex(newConf.ToV1Format())
		return nil
	}), event.High)

	r.Use(func(c *gin.Context) {
		if AllowCors {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Length, Content-Type, Authorization, Accept, X-CSRF-Token, X-Requested-With, Set-Cookie")
			c.Header("Access-Control-Expose-Headers", "Content-Length, Authorization, Set-Cookie")
			c.Header("Access-Control-Allow-Credentials", "false")
			c.Header("Access-Control-Max-Age", "43200") // 12 hours
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(204)
				return
			}
		}
		c.Next()
	})

	public.Static(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(handlers...)
	})

	srv := &http.Server{
		Addr:    flags.Listen,
		Handler: r,
	}

	event.Trigger(eventType.ServerInitializeDone, event.M{})
	ScheduledEventTasksInit()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	log.Printf("Starting server on %s ...", flags.Listen)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			OnFatal(err)
			event.Trigger(eventType.ProcessExit, event.M{})
			log.Fatalf("listen: %s\n", err)
		}
	}()

	<-quit
	OnShutdown()
	event.Trigger(eventType.ProcessExit, event.M{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

}

// #region 定时任务
func DoScheduledWork() {
	tasks.ReloadPingSchedule()

	//records.DeleteRecordBefore(time.Now().Add(-time.Hour * 24 * 30))

	records.CompactRecord()

	event.On(eventType.SchedulerEvery30Minutes, event.ListenerFunc(func(e event.Event) error {
		cfg, err := conf.GetWithV1Format()
		if err != nil {
			slog.Warn("Failed to get config in scheduled task:", "error", err)
			return err
		}
		records.DeleteRecordBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
		records.CompactRecord()
		tasks.ClearTaskResultsByTimeBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
		tasks.DeletePingRecordsBefore(time.Now().Add(-time.Hour * time.Duration(cfg.PingRecordPreserveTime)))
		auditlog.RemoveOldLogs()
		return nil
	}))

	event.On(eventType.SchedulerEveryMinute, event.ListenerFunc(func(e event.Event) error {
		cfg, err := conf.GetWithV1Format()
		if err != nil {
			slog.Warn("Failed to get config in scheduled task:", "error", err)
			return err
		}
		if !cfg.RecordEnabled {
			records.DeleteAll()
			tasks.DeleteAllPingRecords()
		}

		return nil
	}))
}

func OnShutdown() {
	auditlog.Log("", "", "server is shutting down", "info")
}

func OnFatal(err error) {
	auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
}

func ScheduledEventTasksInit() {
	go DoScheduledWork()
	go func() {
		every1m := time.NewTicker(1 * time.Minute)
		every5m := time.NewTicker(5 * time.Minute)
		every30m := time.NewTicker(30 * time.Minute)
		every1h := time.NewTicker(1 * time.Hour)
		every1d := time.NewTicker(24 * time.Hour)
		for {
			select {
			case <-every1m.C:
				event.Async(eventType.SchedulerEveryMinute, event.M{"interval": "1m"})
			case <-every5m.C:
				event.Async(eventType.SchedulerEvery5Minutes, event.M{"interval": "5m"})
			case <-every30m.C:
				event.Async(eventType.SchedulerEvery30Minutes, event.M{"interval": "30m"})
			case <-every1h.C:
				event.Async(eventType.SchedulerEveryHour, event.M{"interval": "1h"})
			case <-every1d.C:
				event.Async(eventType.SchedulerEveryDay, event.M{"interval": "1d"})
			}
		}
	}()
}
