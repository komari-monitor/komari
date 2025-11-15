package internal

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/api_rpc"
	api "github.com/komari-monitor/komari/internal/api_v1"
	"github.com/komari-monitor/komari/internal/api_v1/admin"
	"github.com/komari-monitor/komari/internal/api_v1/admin/clipboard"
	log_api "github.com/komari-monitor/komari/internal/api_v1/admin/log"
	"github.com/komari-monitor/komari/internal/api_v1/admin/notification"
	"github.com/komari-monitor/komari/internal/api_v1/admin/test"
	"github.com/komari-monitor/komari/internal/api_v1/admin/update"
	"github.com/komari-monitor/komari/internal/api_v1/client"
	"github.com/komari-monitor/komari/internal/api_v1/record"
	"github.com/komari-monitor/komari/internal/api_v1/task"
	"github.com/komari-monitor/komari/internal/database/models"
)

func LoadApiV1Routes(r *gin.Engine, conf models.Config) {
	r.Use(func(c *gin.Context) {
		if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	})

	r.Use(api.PrivateSiteMiddleware())

	r.Any("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})
	// #region 公开路由
	r.POST("/api/login", api.Login)
	r.GET("/api/me", api.GetMe)
	r.GET("/api/clients", api.GetClients)
	r.GET("/api/nodes", api.GetNodesInformation)
	r.GET("/api/public", api.GetPublicSettings)
	r.GET("/api/oauth", api.OAuth)
	r.GET("/api/oauth_callback", api.OAuthCallback)
	r.GET("/api/logout", api.Logout)
	r.GET("/api/version", api.GetVersion)
	r.GET("/api/recent/:uuid", api.GetClientRecentRecords)

	r.GET("/api/records/load", record.GetRecordsByUUID)
	r.GET("/api/records/ping", record.GetPingRecords)
	r.GET("/api/task/ping", task.GetPublicPingTasks)
	r.GET("/api/rpc2", api_rpc.OnRpcRequest)
	r.POST("/api/rpc2", api_rpc.OnRpcRequest)

	// #region Agent
	r.POST("/api/clients/register", client.RegisterClient)
	tokenAuthrized := r.Group("/api/clients", api.TokenAuthMiddleware())
	{
		tokenAuthrized.GET("/report", client.WebSocketReport) // websocket
		tokenAuthrized.POST("/uploadBasicInfo", client.UploadBasicInfo)
		tokenAuthrized.POST("/report", client.UploadReport)
		tokenAuthrized.GET("/terminal", client.EstablishConnection)
		tokenAuthrized.POST("/task/result", client.TaskResult)
	}
	// #region 管理员
	adminAuthrized := r.Group("/api/admin", api.AdminAuthMiddleware())
	{
		adminAuthrized.GET("/download/backup", admin.DownloadBackup)
		adminAuthrized.POST("/upload/backup", admin.UploadBackup)
		// test
		testGroup := adminAuthrized.Group("/test")
		{
			testGroup.GET("/geoip", test.TestGeoIp)
			testGroup.POST("/sendMessage", test.TestSendMessage)
		}
		// update
		updateGroup := adminAuthrized.Group("/update")
		{
			updateGroup.POST("/mmdb", update.UpdateMmdbGeoIP)
			updateGroup.POST("/user", update.UpdateUser)
			updateGroup.PUT("/favicon", update.UploadFavicon)
			updateGroup.POST("/favicon", update.DeleteFavicon)
		}
		// tasks
		taskGroup := adminAuthrized.Group("/task")
		{
			taskGroup.GET("/all", admin.GetTasks)
			taskGroup.POST("/exec", admin.Exec)
			taskGroup.GET("/:task_id", admin.GetTaskById)
			taskGroup.GET("/:task_id/result", admin.GetTaskResultsByTaskId)
			taskGroup.GET("/:task_id/result/:uuid", admin.GetSpecificTaskResult)
			taskGroup.GET("/client/:uuid", admin.GetTasksByClientId)
		}
		// settings
		settingsGroup := adminAuthrized.Group("/settings")
		{
			settingsGroup.GET("/", admin.GetSettings)
			settingsGroup.POST("/", admin.EditSettings)
			settingsGroup.POST("/oidc", admin.SetOidcProvider)
			settingsGroup.GET("/oidc", admin.GetOidcProvider)
			settingsGroup.POST("/message-sender", admin.SetMessageSenderProvider)
			settingsGroup.GET("/message-sender", admin.GetMessageSenderProvider)
		}
		// themes
		themeGroup := adminAuthrized.Group("/theme")
		{
			themeGroup.PUT("/upload", admin.UploadTheme)
			themeGroup.GET("/list", admin.ListThemes)
			themeGroup.POST("/delete", admin.DeleteTheme)
			themeGroup.GET("/set", admin.SetTheme)
			themeGroup.POST("/update", admin.UpdateTheme)
			themeGroup.POST("/settings", admin.UpdateThemeSettings)
		}
		// clients
		clientGroup := adminAuthrized.Group("/client")
		{
			clientGroup.POST("/add", admin.AddClient)
			clientGroup.GET("/list", admin.ListClients)
			clientGroup.GET("/:uuid", admin.GetClient)
			clientGroup.POST("/:uuid/edit", admin.EditClient)
			clientGroup.POST("/:uuid/remove", admin.RemoveClient)
			clientGroup.GET("/:uuid/token", admin.GetClientToken)
			clientGroup.POST("/order", admin.OrderWeight)
			// client terminal
			clientGroup.GET("/:uuid/terminal", api.RequestTerminal)
		}

		// records
		recordGroup := adminAuthrized.Group("/record")
		{
			recordGroup.POST("/clear", admin.ClearRecord)
			recordGroup.POST("/clear/all", admin.ClearAllRecords)
		}
		// oauth2
		oauth2Group := adminAuthrized.Group("/oauth2")
		{
			oauth2Group.GET("/bind", admin.BindingExternalAccount)
			oauth2Group.POST("/unbind", admin.UnbindExternalAccount)
		}
		sessionGroup := adminAuthrized.Group("/session")
		{
			sessionGroup.GET("/get", admin.GetSessions)
			sessionGroup.POST("/remove", admin.DeleteSession)
			sessionGroup.POST("/remove/all", admin.DeleteAllSession)
		}
		two_factorGroup := adminAuthrized.Group("/2fa")
		{
			two_factorGroup.GET("/generate", admin.Generate2FA)
			two_factorGroup.POST("/enable", admin.Enable2FA)
			two_factorGroup.POST("/disable", admin.Disable2FA)
		}
		adminAuthrized.GET("/logs", log_api.GetLogs)

		// clipboard
		clipboardGroup := adminAuthrized.Group("/clipboard")
		{
			clipboardGroup.GET("/:id", clipboard.GetClipboard)
			clipboardGroup.GET("", clipboard.ListClipboard)
			clipboardGroup.POST("", clipboard.CreateClipboard)
			clipboardGroup.POST("/:id", clipboard.UpdateClipboard)
			clipboardGroup.POST("/remove", clipboard.BatchDeleteClipboard)
			clipboardGroup.POST("/:id/remove", clipboard.DeleteClipboard)
		}

		notificationGroup := adminAuthrized.Group("/notification")
		{
			// offline notifications
			notificationGroup.GET("/offline", notification.ListOfflineNotifications)
			notificationGroup.POST("/offline/edit", notification.EditOfflineNotification)
			notificationGroup.POST("/offline/enable", notification.EnableOfflineNotification)
			notificationGroup.POST("/offline/disable", notification.DisableOfflineNotification)
			loadAlertGroup := notificationGroup.Group("/load")
			{
				loadAlertGroup.GET("/", notification.GetAllLoadNotifications)
				loadAlertGroup.POST("/add", notification.AddLoadNotification)
				loadAlertGroup.POST("/delete", notification.DeleteLoadNotification)
				loadAlertGroup.POST("/edit", notification.EditLoadNotification)
			}
		}

		pingTaskGroup := adminAuthrized.Group("/ping")
		{
			pingTaskGroup.GET("/", admin.GetAllPingTasks)
			pingTaskGroup.POST("/add", admin.AddPingTask)
			pingTaskGroup.POST("/delete", admin.DeletePingTask)
			pingTaskGroup.POST("/edit", admin.EditPingTask)

		}

	}
}
