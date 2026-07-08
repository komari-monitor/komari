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
	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/metricstore"
	"github.com/komari-monitor/komari/database/models"
	d_notification "github.com/komari-monitor/komari/database/notification"
	"github.com/komari-monitor/komari/database/records"
	"github.com/komari-monitor/komari/database/tasks"
	"github.com/komari-monitor/komari/pkg/config"
	"github.com/komari-monitor/komari/pkg/corn"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/utils/geoip"
	logutil "github.com/komari-monitor/komari/utils/log"
	"github.com/komari-monitor/komari/utils/messageSender"
	"github.com/komari-monitor/komari/utils/notifier"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/nezha"
	"github.com/komari-monitor/komari/web/oauth"
	report_cache "github.com/komari-monitor/komari/web/report"
	"github.com/komari-monitor/komari/web/router"
	"github.com/komari-monitor/komari/web/security"
)

// cleanupFunc 是一个关闭阶段执行的清理函数。
type cleanupFunc struct {
	name string
	fn   func(ctx context.Context) error
}

// App 显式建模服务端的启动生命周期。
//
// 过去 RunServer 把目录创建、数据库、metric store、GeoIP、定时任务、通知、OAuth、
// Nezha、Gin 中间件、路由、HTTP 启动与 shutdown 全部混在一个函数里，
// 启动顺序只能靠通读整段代码推断，异步初始化过早且吞掉错误，关闭也不完整。
//
// App 把这些拆成有序阶段：
//
//	Bootstrap    基础设施：目录、数据库、默认管理员、配置快照
//	InitStores   存储：metric store
//	InitProviders 外部 provider：OAuth（同步，路由依赖）、GeoIP、消息发送、Nezha 兼容
//	StartBackground 后台：定时任务
//	BuildRouter  构建 Gin 引擎与路由
//	Run          启动 HTTP 服务并阻塞直到收到信号
//	Shutdown     反序执行已登记的清理函数
//
// 每个阶段返回错误即可让上层决定是否中止启动；各资源在创建时把对应清理登记到
// cleanup 栈，关闭时按后进先出（LIFO）顺序释放。
type App struct {
	settings *config.Settings
	engine   *gin.Engine
	server   *http.Server
	reload   *ReloadManager
	dbReady  bool

	cleanups []cleanupFunc
}

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

// Bootstrap 初始化基础设施：数据目录、数据库、默认管理员账号、配置快照。
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

	if utils.VersionHash != "unknown" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 首次启动创建默认管理员账号。
	if err := ensureDefaultAdmin(); err != nil {
		return fmt.Errorf("failed to ensure default admin account: %w", err)
	}

	conf, err := config.GetManyAs[config.Settings]()
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}
	a.settings = conf
	return nil
}

// InitStores 初始化独立存储组件（metric store）并执行一次性历史数据迁移。
//
// metric store 现在始终启用（旧的 metric_store_enabled 开关已废弃）：
// 未显式配置时使用 SQLite（./data/metrics.db），否则使用配置的 MySQL/PostgreSQL。
// 初始化失败即启动失败，不再静默 fallback 到旧 records 表。
//
// 初始化成功后执行启动迁移：当 metrics 存储后端发生变化（例如从默认 SQLite
// 切换到 MySQL/PostgreSQL）时，把上一个 metrics 目标库的数据搬运到当前目标。
// 旧 komari.db 的监控表已彻底废弃，不再参与。迁移失败同样让启动失败，
// 并打印明确错误。
func (a *App) InitStores() error {
	if err := metricstore.InitializeStore(); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize metric store: %v", err))
		return fmt.Errorf("failed to initialize metric store: %w", err)
	}
	a.addCleanup("metric-store", func(context.Context) error {
		return metricstore.CloseStore()
	})

	// 存储后端切换时把上一个 metrics 目标库的数据搬运到当前目标（失败即启动失败）。
	if err := metricstore.RunStartupMigration(); err != nil {
		auditlog.EventLog("error", fmt.Sprintf("Metrics startup migration failed: %v", err))

		return fmt.Errorf("metrics startup migration failed: %w", err)
	}
	return nil
}

// InitProviders 初始化外部 provider。
//
// OAuth 必须在 HTTP 服务开始接收请求之前同步完成，否则 oauth.CurrentProvider()
// 存在空指针风险；GeoIP 与消息发送允许后台初始化；Nezha 兼容按配置决定是否启动。
func (a *App) InitProviders() error {
	// OAuth：同步初始化并处理错误，保证路由可用前 provider 已就绪。
	if err := oauth.Initialize(); err != nil {
		// 记录但不致命：Initialize 内部在失败时已回退到 github provider。
		log.Printf("Failed to initialize OAuth provider: %v", err)
		auditlog.EventLog("error", fmt.Sprintf("Failed to initialize OAuth provider: %v", err))
	}
	a.addCleanup("oauth", func(context.Context) error {
		return oauth.Shutdown()
	})

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

	// Nezha 兼容 gRPC 服务：按配置启动，且无论初始是否启动都登记清理，
	// 以覆盖运行期通过热重载开启的情况。
	if a.settings.NezhaCompatEnabled {
		listen := a.settings.NezhaCompatListen
		go func() {
			if err := nezha.StartNezhaCompat(listen); err != nil {
				log.Printf("Nezha compat server error: %v", err)
				auditlog.EventLog("error", fmt.Sprintf("Nezha compat server error: %v", err))
			}
		}()
	}
	a.addCleanup("nezha-compat", func(context.Context) error {
		// 未运行时 StopNezhaCompat 返回错误，属正常情况，忽略即可。
		_ = nezha.StopNezhaCompat()
		return nil
	})

	return nil
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
				log.Printf("Failed to get OIDC provider config: %v", err)
				return
			}
			log.Printf("Using %s as OIDC provider", oidcProvider.Name)
			if err := oauth.LoadProvider(oidcProvider.Name, oidcProvider.Addition); err != nil {
				auditlog.EventLog("error", fmt.Sprintf("Failed to load OIDC provider: %v", err))
			}
		}
	})

	// Nezha 兼容服务开关。
	a.reload.Register("nezha-compat", func(event config.ConfigEvent) {
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
	r.Use(logutil.GinLogger())
	r.Use(logutil.GinRecovery())

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
	log.Printf("Starting server on %s ...", flags.Listen)
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
			log.Printf("HTTP server forced to shutdown: %v", err)
		}
	}

	// 反序释放资源（后进先出）。
	for i := len(a.cleanups) - 1; i >= 0; i-- {
		c := a.cleanups[i]
		if err := c.fn(ctx); err != nil {
			log.Printf("cleanup %q failed: %v", c.name, err)
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
			log.Printf("cleanup %q failed: %v", c.name, cerr)
		}
	}
}

// ensureDefaultAdmin 首次启动（无任何用户）时创建默认管理员账号。
func ensureDefaultAdmin() error {
	var count int64
	dbcore.GetDBInstance().Model(&models.User{}).Count(&count)
	if count != 0 {
		return nil
	}
	user, passwd, err := accounts.CreateDefaultAdminAccount()
	if err != nil {
		return err
	}
	log.Println("Default admin account created. Username:", user, ", Password:", passwd)
	return nil
}

// registerScheduledWork 注册所有定时任务与首启动同步逻辑。
func registerScheduledWork() {
	if err := tasks.ReloadPingSchedule(); err != nil {
		log.Println("Failed to reload ping schedule:", err)
	}
	if err := d_notification.ReloadLoadNotificationSchedule(); err != nil {
		log.Println("Failed to reload load notification schedule:", err)
	}

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
