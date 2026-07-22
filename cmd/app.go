package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	d_notification "github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/corn"
	"github.com/komari-monitor/komari/pkg/migrations"
	"github.com/komari-monitor/komari/pkg/resourceprobe"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/geoip"
	logger "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/notifier"
	"github.com/komari-monitor/komari/web/api"
	installweb "github.com/komari-monitor/komari/web/install"
	"github.com/komari-monitor/komari/web/oauth"
	frontendpublic "github.com/komari-monitor/komari/web/public"
	recoveryweb "github.com/komari-monitor/komari/web/recovery"
	"github.com/komari-monitor/komari/web/router"
	"github.com/komari-monitor/komari/web/security"
	upgradeweb "github.com/komari-monitor/komari/web/update"
)

// cleanupFunc 是一个关闭阶段执行的清理函数。
type cleanupFunc struct {
	name string
	fn   func(ctx context.Context) error
}

// App 显式建模服务端的启动生命周期。
//
// 过去 RunServer 把目录创建、数据库、metric store、GeoIP、定时任务、通知、OAuth、
// Gin 中间件、路由、HTTP 启动与 shutdown 全部混在一个函数里，
// 启动顺序只能靠通读整段代码推断，异步初始化过早且吞掉错误，关闭也不完整。
//
// App 把这些拆成有序阶段：
//
//	Bootstrap    基础设施：目录、数据库、配置快照
//	InitStores   存储：metric store
//	InitProviders 外部 provider：OAuth（同步，路由依赖）、GeoIP、消息发送
//	StartBackground 后台：定时任务
//	BuildRouter  构建 Gin 引擎与路由
//	Run          启动 HTTP 服务并阻塞直到收到信号
//	Shutdown     反序执行已登记的清理函数
//
// 每个阶段返回错误即可让上层决定是否中止启动；各资源在创建时把对应清理登记到
// cleanup 栈，关闭时按后进先出（LIFO）顺序释放。
type App struct {
	settings                *config.Settings
	engine                  *gin.Engine
	server                  *http.Server
	reload                  *ReloadManager
	dbReady                 bool
	oauthReady              bool
	metricStoreCleanupAdded bool

	cleanups []cleanupFunc
}

const (
	metricStoreReconnectAttempts = 3
	metricStoreReconnectInterval = 5 * time.Second
)

// NewApp 构造一个空的 App。真正的初始化在各阶段方法中完成。
func NewApp() *App {
	return &App{
		reload: NewReloadManager(),
	}
}

// addCleanup 登记一个关闭阶段的清理函数（后进先出执行）。
func (a *App) addCleanup(name string, fn func(ctx context.Context) error) {
	a.cleanups = append(a.cleanups, cleanupFunc{name: name, fn: fn})
}

// Bootstrap 初始化基础设施：数据目录、数据库、配置快照。
func (a *App) Bootstrap() error {
	if err := os.MkdirAll("./data/theme", os.ModePerm); err != nil {
		return fmt.Errorf("failed to create theme directory: %w", err)
	}

	// 注入版本标识，供 dbcore 在检测到版本升级时自动备份 ./data。
	// 必须在 Initialize() 之前设置。
	dbcore.SetVersionID(utils.CurrentVersion + "-" + utils.VersionHash)

	// 显式初始化数据库（返回错误而非在 getter 里 log.Fatal）。
	if err := dbcore.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	a.dbReady = true
	a.addCleanup("database", func(context.Context) error {
		return dbcore.Close()
	})

	gin.SetMode(gin.ReleaseMode)

	lowResourceMode, err := ensureLowResourceModeDefault()
	if err != nil {
		return fmt.Errorf("failed to initialize low resource mode: %w", err)
	}
	if err := dbcore.ConfigureLowResourceMode(lowResourceMode); err != nil {
		return err
	}

	conf, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	a.settings = conf
	return nil
}

func ensureLowResourceModeDefault() (bool, error) {
	values, err := config.GetMany(map[string]any{config.LowResourceModeKey: nil})
	if err != nil {
		return false, err
	}
	if saved, ok := values[config.LowResourceModeKey]; ok {
		if enabled, ok := saved.(bool); ok {
			return enabled, nil
		}
		return false, fmt.Errorf("%s must be a boolean", config.LowResourceModeKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	result := resourceprobe.Detect(ctx, "./data")
	if err := config.Set(config.LowResourceModeKey, result.LowResource); err != nil {
		return false, err
	}
	logger.Infof("server",
		"Low resource mode auto-detection: enabled=%t memory=%dMiB disk_free=%dMiB cpu=%.0fops/s random_write=%.2fMiB/s iops=%.0f reasons=%v",
		result.LowResource,
		result.MemoryBytes/(1024*1024),
		result.DiskFreeBytes/(1024*1024),
		result.CPUOpsPerSecond,
		result.WriteBytesPerSecond/(1024*1024),
		result.WriteIOPS,
		result.Reasons,
	)
	return result.LowResource, nil
}

// ConnectMetricStore performs one connection attempt and registers its
// cleanup only after a store has actually been opened. InitializeStore is
// retryable, so a failed attempt does not poison later attempts.
func (a *App) ConnectMetricStore() error {
	if err := metricstore.InitializeStore(); err != nil {
		return fmt.Errorf("failed to initialize metric store: %s", redactMetricStoreError(err))
	}
	if !a.metricStoreCleanupAdded {
		a.addCleanup("metric-store", func(ctx context.Context) error {
			return metricstore.CloseStoreContext(ctx)
		})
		a.metricStoreCleanupAdded = true
	}
	return nil
}

func redactMetricStoreError(err error) string {
	if err == nil {
		return ""
	}
	dsn := ""
	if cfg, cfgErr := config.GetManyAs[metricstore.MetricStoreConfig](); cfgErr == nil {
		dsn = cfg.DSN
	}
	return metricstore.RedactConnectionError(err.Error(), dsn)
}

// ConnectMetricStoreWithRetry retries a failed monitoring database connection
// every five seconds. The initial attempt is immediate; the interval applies
// between subsequent attempts.
func (a *App) ConnectMetricStoreWithRetry() error {
	attempt := 0
	err := retryMetricStoreConnection(metricStoreReconnectAttempts, metricStoreReconnectInterval, func() error {
		attempt++
		err := a.ConnectMetricStore()
		if err != nil {
			logger.Warn("server", "Metric store connection attempt failed", "attempt", attempt, "max_attempts", metricStoreReconnectAttempts, "error", err)
		}
		return err
	})
	if err == nil && attempt > 1 {
		logger.Infof("server", "Metric store connection recovered on attempt %d/%d", attempt, metricStoreReconnectAttempts)
	}
	return err
}

func retryMetricStoreConnection(attempts int, interval time.Duration, connect func() error) error {
	if attempts < 1 {
		return fmt.Errorf("metric store retry attempts must be positive")
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(interval)
		}
		if err := connect(); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return lastErr
}

// InitStores initializes independent storage components and runs metrics
// migrations. The connection itself is established by ConnectMetricStore so
// startup can enter the restricted recovery flow after repeated failures.
func (a *App) InitStores() error {
	if err := a.ConnectMetricStore(); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize metric store: %v", err))
		return err
	}

	if err := migrations.RunMetricStoreMigrations(migrations.MetricContext{
		DB:    dbcore.GetDBInstance(),
		Store: metricstore.GetStore(),
	}); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Metric store one-shot migrations failed: %v", err))
		return fmt.Errorf("metric store one-shot migrations failed: %w", err)
	}
	// 存储后端切换时把上一个 metrics 目标库的数据搬运到当前目标（失败即启动失败）。
	if err := metricstore.RunStartupMigration(); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Metrics startup migration failed: %v", err))

		return fmt.Errorf("metrics startup migration failed: %w", err)
	}
	metricstore.StartReportBatcher()
	a.addCleanup("metric-report-batcher", func(ctx context.Context) error {
		return metricstore.StopReportBatcher(ctx)
	})
	return nil
}

// InitProviders 初始化外部 provider。
//
// OAuth 必须在 HTTP 服务开始接收请求之前同步完成，否则 oauth.CurrentProvider()
// 存在空指针风险；GeoIP 与消息发送允许后台初始化。
func (a *App) InitProviders() error {
	a.initOAuth()

	// GeoIP：可能涉及下载/加载 mmdb，放后台执行避免拖慢启动。
	go geoip.InitGeoIp()
	a.addCleanup("geoip", func(context.Context) error {
		return geoip.Shutdown()
	})

	// 消息发送 provider。
	messageSender.Initialize()
	a.addCleanup("message-sender", func(context.Context) error {
		return messageSender.Shutdown()
	})

	return nil
}

// initOAuth initializes the provider once. The restricted upgrade server also
// needs OAuth login, so provider setup may happen before the normal provider
// stage without registering duplicate cleanup work.
func (a *App) initOAuth() {
	if a.oauthReady {
		return
	}
	if err := oauth.Initialize(); err != nil {
		// Keep password login available when an OAuth provider is misconfigured.
		logger.Errorf("server", "Failed to initialize OAuth provider: %v", err)
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize OAuth provider: %v", err))
	}
	a.oauthReady = true
	a.addCleanup("oauth", func(context.Context) error {
		return oauth.Shutdown()
	})
}

func (a *App) LegacyUpgradeRequired() (bool, migrations.LegacyMonitoringSummary, error) {
	return migrations.LegacyMonitoringMigrationRequired(dbcore.GetDBInstance())
}

// InstallRequired reports whether the instance still needs the first-run guide.
// A zero-user database is deliberately treated as incomplete so an interrupted
// guide can be resumed after a restart.
func (a *App) InstallRequired() (bool, error) {
	var count int64
	if err := dbcore.GetDBInstance().Model(&models.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

// RunInstallGuide starts the restricted first-run HTTP server.
func (a *App) RunInstallGuide() (bool, error) {
	controller := installweb.NewController(dbcore.GetDBInstance())
	controller.Activate()
	defer controller.Deactivate()

	r := gin.New()
	r.Use(logger.GinLogger())
	r.Use(logger.GinRecovery())
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})
	controller.Register(r)
	frontendpublic.Static(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(func(c *gin.Context) {
			requestPath := c.Request.URL.Path
			if strings.HasPrefix(requestPath, "/api") {
				api.RespondError(c, http.StatusNotFound, "Not found in install mode")
				return
			}
			if c.Request.Method == http.MethodGet && requestPath != installweb.PagePath && filepath.Ext(requestPath) == "" {
				c.Redirect(http.StatusTemporaryRedirect, installweb.PagePath)
				return
			}
			for _, handler := range handlers {
				handler(c)
				if c.IsAborted() {
					return
				}
			}
		})
	})

	a.engine = r
	a.server = &http.Server{Addr: flags.Listen, Handler: r}
	serverErr := make(chan error, 1)
	logger.Infof("server", "First-run installation guide is available on %s", flags.Listen)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case err := <-serverErr:
		return false, fmt.Errorf("listen in install mode: %w", err)
	case <-quit:
		return false, a.Shutdown()
	case <-controller.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.server.Shutdown(ctx); err != nil {
			return false, fmt.Errorf("stop install server: %w", err)
		}
		a.server = nil
		a.engine = nil
		return true, nil
	}
}

// RunMetricStoreRecovery serves the authenticated DSN-only recovery page
// after the monitoring database has failed all automatic reconnect attempts.
// The main SQLite database remains open, so existing administrator sessions
// and password/OAuth login continue to work while the metric store is down.
func (a *App) RunMetricStoreRecovery(initialErr error) (bool, error) {
	a.initOAuth()
	controller := recoveryweb.NewController(initialErr, metricStoreReconnectAttempts)
	controller.Activate()
	defer controller.Deactivate()

	r := gin.New()
	r.Use(logger.GinLogger())
	r.Use(logger.GinRecovery())
	cors := security.NewCorsController(a.settings.CorsOriginCheckEnabled, a.settings.CorsAllowedOrigins)
	r.Use(cors.Middleware())
	r.Use(api.IdentityMiddleware())
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	controller.Register(r)
	frontendpublic.StaticRestricted(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(func(c *gin.Context) {
			requestPath := c.Request.URL.Path
			if strings.HasPrefix(requestPath, "/api") {
				api.RespondError(c, http.StatusNotFound, "Not found in database recovery mode")
				return
			}
			if c.Request.Method == http.MethodGet && requestPath != recoveryweb.PagePath && filepath.Ext(requestPath) == "" {
				c.Redirect(http.StatusTemporaryRedirect, recoveryweb.PagePath)
				return
			}
			for _, handler := range handlers {
				handler(c)
				if c.IsAborted() {
					return
				}
			}
		})
	})

	a.engine = r
	a.server = &http.Server{Addr: flags.Listen, Handler: r}
	serverErr := make(chan error, 1)
	logger.Infof("server", "Metric store recovery is available on %s", flags.Listen)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		return false, fmt.Errorf("listen in database recovery mode: %w", err)
	case <-quit:
		return false, a.Shutdown()
	case <-controller.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.server.Shutdown(ctx); err != nil {
			return false, fmt.Errorf("stop database recovery server: %w", err)
		}
		a.server = nil
		a.engine = nil
		return true, nil
	}
}

// RunLegacyUpgrade starts a restricted HTTP server on the normal listen
// address. It returns only after the migration finishes and the restricted
// listener has released the port, or after shutdown/interruption.
func (a *App) RunLegacyUpgrade(summary migrations.LegacyMonitoringSummary) (bool, error) {
	a.initOAuth()
	controller := upgradeweb.NewController(dbcore.GetDBInstance(), summary)
	controller.Activate()
	defer controller.Deactivate()

	r := gin.New()
	r.Use(logger.GinLogger())
	r.Use(logger.GinRecovery())
	cors := security.NewCorsController(a.settings.CorsOriginCheckEnabled, a.settings.CorsAllowedOrigins)
	r.Use(cors.Middleware())
	r.Use(api.IdentityMiddleware())
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	controller.Register(r)
	frontendpublic.Static(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(func(c *gin.Context) {
			requestPath := c.Request.URL.Path
			if strings.HasPrefix(requestPath, "/api") {
				api.RespondError(c, http.StatusNotFound, "Not found in upgrade mode")
				return
			}
			if c.Request.Method == http.MethodGet && requestPath != upgradeweb.PagePath && filepath.Ext(requestPath) == "" {
				c.Redirect(http.StatusTemporaryRedirect, upgradeweb.PagePath)
				return
			}
			for _, handler := range handlers {
				handler(c)
				if c.IsAborted() {
					return
				}
			}
		})
	})

	a.engine = r
	a.server = &http.Server{Addr: flags.Listen, Handler: r}
	serverErr := make(chan error, 1)
	logger.Infof("server", "Legacy monitoring data requires the 1.2.7 upgrade wizard on %s", flags.Listen)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		return false, fmt.Errorf("listen in upgrade mode: %w", err)
	case <-quit:
		return false, a.Shutdown()
	case <-controller.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.server.Shutdown(ctx); err != nil {
			return false, fmt.Errorf("stop upgrade server: %w", err)
		}
		a.server = nil
		a.engine = nil
		return true, nil
	}
}

// StartBackground 启动后台工作：定时任务。
func (a *App) StartBackground() error {
	registerScheduledWork()
	a.addCleanup("scheduler", func(context.Context) error {
		corn.StopAll()
		return nil
	})
	return nil
}

// registerReloadHandlers 把此前散落各处的 config.Subscribe 统一登记到 reload 管理器。
func (a *App) registerReloadHandlers(cors *security.CorsController) {
	// OAuth provider 切换。
	a.reload.Register("oauth-provider", func(event config.ConfigEvent) {
		if ok, t := config.IsChangedT[string](event, config.OAuthProviderKey); ok {
			if t == "" || t == "none" {
				t = "github"
			}
			oidcProvider, err := database.GetOidcConfigByName(t)
			if err != nil {
				logger.Errorf("server", "Failed to get OIDC provider config: %v", err)
				return
			}
			logger.Infof("server", "Using %s as OIDC provider", oidcProvider.Name)
			if err := oauth.LoadProvider(oidcProvider.Name, oidcProvider.Addition); err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
	})

	// GeoIP provider 切换。
	a.reload.Register("geoip-provider", func(event config.ConfigEvent) {
		if event.IsChanged(config.GeoIpProviderKey) {
			go geoip.InitGeoIp()
		}
	})

	// 消息发送方式切换。
	a.reload.Register("message-sender", func(event config.ConfigEvent) {
		if event.IsChanged(config.NotificationMethodKey) {
			go messageSender.Initialize()
		}
	})

	// CORS 配置热更新。
	a.reload.Register("cors", func(event config.ConfigEvent) {
		cors.Update(event)
	})
}

// BuildRouter 构建 Gin 引擎、中间件与全部路由，并登记热重载处理器。
func (a *App) BuildRouter() error {
	r := gin.New()
	r.Use(logger.GinLogger())
	r.Use(logger.GinRecovery())

	cors := security.NewCorsController(a.settings.CorsOriginCheckEnabled, a.settings.CorsAllowedOrigins)
	r.Use(cors.Middleware())

	r.Use(api.IdentityMiddleware())
	r.Use(api.PrivateSiteMiddleware())

	r.Use(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	// The recovery page only exists on the temporary listener. If a browser
	// keeps its URL while the normal router comes back, leave the stale guide
	// instead of serving it without its recovery API.
	r.GET(recoveryweb.PagePath, func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/")
	})

	router.Register(r)

	// 集中登记并启动热重载订阅。
	a.registerReloadHandlers(cors)
	a.reload.Start()

	a.engine = r
	return nil
}

// Run 启动 HTTP 服务并阻塞直到收到中断信号或服务异常退出。
func (a *App) Run() error {
	a.server = &http.Server{
		Addr:    flags.Listen,
		Handler: a.engine,
	}

	serverErr := make(chan error, 1)
	logger.Infof("server", "Starting server on %s ...", flags.Listen)
	go func() {
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		a.onFatal(err)
		return fmt.Errorf("listen: %w", err)
	case <-quit:
		return a.Shutdown()
	}
}

// Shutdown 优雅关闭：先停止接收新请求，再反序执行已登记的清理函数。
func (a *App) Shutdown() error {
	if a.dbReady {
		auditlog.Log("", "", "server is shutting down", "info")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 先关闭 HTTP 服务，停止接收新请求。
	if a.server != nil {
		if err := a.server.Shutdown(ctx); err != nil {
			logger.Infof("server", "HTTP server forced to shutdown: %v", err)
		}
	}

	// 反序释放资源（后进先出）。
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		c := a.cleanups[i]
		if err := c.fn(ctx); err != nil {
			logger.Errorf("server", "cleanup %q failed: %v", c.name, err)
		}
	}
	return nil
}

// onFatal 处理 HTTP 服务致命错误：尽力释放已登记资源。
func (a *App) onFatal(err error) {
	if a.dbReady {
		auditlog.Log("", "", "server encountered a fatal error: "+err.Error(), "error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		c := a.cleanups[i]
		if cerr := c.fn(ctx); cerr != nil {
			logger.Errorf("server", "cleanup %q failed: %v", c.name, cerr)
		}
	}
}

// registerScheduledWork 注册所有定时任务与首启动同步逻辑。
func registerScheduledWork() {
	if err := tasks.ReloadPingSchedule(); err != nil {
		logger.ErrorArgs("server", "Failed to reload ping schedule:", err)
	}
	if err := d_notification.ReloadLoadNotificationSchedule(); err != nil {
		logger.ErrorArgs("server", "Failed to reload load notification schedule:", err)
	}

	if err := corn.AddFunc("records:cleanup", "@every 30m", cleanupScheduledData); err != nil {
		logger.ErrorArgs("server", "Failed to add cleanup scheduled task:", err)
	}
	if err := corn.AddContextFunc("metrics:compact", "@every 5m", true, compactMetricStore); err != nil {
		logger.ErrorArgs("server", "Failed to add metric compact scheduled task:", err)
	}
	if err := corn.AddFunc("notifier:traffic", "@every 1m", notifier.CheckTraffic); err != nil {
		logger.ErrorArgs("server", "Failed to add traffic notification task:", err)
	}
	if err := corn.AddFunc("notifier:expire", "0 0 9 * * *", notifier.CheckExpireScheduledWork); err != nil {
		logger.ErrorArgs("server", "Failed to add expire notification scheduled task:", err)
	}

	notifier.InitTrafficReportSchedule()
}

const taskResultRetentionDays = 30

func cleanupScheduledData() {
	before := time.Now().UTC().Add(-24 * time.Hour * taskResultRetentionDays)
	if err := tasks.ClearTaskResultsByTimeBefore(before); err != nil {
		logger.Errorf("server", "Failed to clean expired task results: %v", err)
	}

	auditlog.RemoveOldLogs()
	accounts.RemoveExpiredSessions()
}

func compactMetricStore(ctx context.Context) {
	compactCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	written, err := metricstore.Compact(compactCtx, time.Now().UTC())
	if errors.Is(err, metricstore.ErrCompactInProgress) {
		return
	}
	if err != nil {
		logger.Errorf("server", "Failed to compact metric store after writing %d rollup buckets: %v", written, err)
		return
	}
	if written > 0 {
		logger.Infof("server", "Metric store compacted %d rollup buckets", written)
	}
}
