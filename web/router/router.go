package router

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/api/admin"
	"github.com/komari-monitor/komari/web/api/admin/clipboard"
	log_api "github.com/komari-monitor/komari/web/api/admin/log"
	"github.com/komari-monitor/komari/web/api/admin/notification"
	"github.com/komari-monitor/komari/web/api/admin/test"
	"github.com/komari-monitor/komari/web/api/admin/update"
	"github.com/komari-monitor/komari/web/api/client"
	public_api "github.com/komari-monitor/komari/web/api/public"
	"github.com/komari-monitor/komari/web/api/terminal"
	"github.com/komari-monitor/komari/web/public"
	jsonRpc "github.com/komari-monitor/komari/web/rpc/jsonrpc"
)

// Register binds all HTTP, WebSocket, JSON-RPC and static frontend routes.
func Register(r *gin.Engine) {
	r.Any("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})
	// #region 公开路由
	r.POST("/api/login", public_api.Login)
	r.GET("/api/me", public_api.GetMe)
	r.GET("/api/clients", api.GetClients)
	r.GET("/api/nodes", public_api.GetNodesInformation)
	r.GET("/api/public", public_api.GetPublicSettings)
	r.GET("/api/oauth", public_api.OAuth)
	r.GET("/api/oauth_callback", public_api.OAuthCallback)
	r.GET("/api/logout", public_api.Logout)
	r.GET("/api/version", public_api.GetVersion)
	r.GET("/api/recent/:uuid", public_api.GetClientRecentRecords)

	r.GET("/api/records/load", public_api.GetRecordsByUUID)
	r.GET("/api/records/ping", public_api.GetPingRecords)
	r.GET("/api/task/ping", public_api.GetPublicPingTasks)
	r.GET("/api/rpc2", jsonRpc.OnRpcRequest)
	r.POST("/api/rpc2", jsonRpc.OnRpcRequest)
	r.GET("/api/mjpeg_live", public_api.MjpegLiveHandler)
	// #region Agent
	r.POST("/api/clients/register", client.RegisterClient)
	tokenAuthrized := r.Group("/api/clients", api.RequireRole(api.RoleAdmin, api.RoleClient))
	{
		tokenAuthrized.GET("/report", client.WebSocketReport) // websocket
		tokenAuthrized.POST("/uploadBasicInfo", client.UploadBasicInfo)
		tokenAuthrized.POST("/report", client.UploadReport)
		tokenAuthrized.GET("/v2/rpc", client.WebSocketV2RPC)
		tokenAuthrized.POST("/v2/rpc", client.UploadV2RPC)
		tokenAuthrized.GET("/terminal", terminal.EstablishConnection)
		tokenAuthrized.POST("/task/result", client.TaskResult)
		tokenAuthrized.GET("/ping/tasks", client.GetPingTasks)
		tokenAuthrized.POST("/ping/result", client.UploadPingResult)
	}
	// #region 管理员
	adminAuthrized := r.Group("/api/admin", api.RequireRole(api.RoleAdmin))
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
			taskGroup.POST("/exec", api.RequireSensitive2FA(), admin.Exec)
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
			settingsGroup.GET("/xtermjs", admin.GetXtermJSSettings)
			settingsGroup.POST("/xtermjs", admin.SetXtermJSSettings)
			settingsGroup.POST("/oidc", admin.SetOidcProvider)
			settingsGroup.GET("/oidc", admin.GetOidcProvider)
			settingsGroup.POST("/message-sender", admin.SetMessageSenderProvider)
			settingsGroup.GET("/message-sender", admin.GetMessageSenderProvider)
			settingsGroup.GET("/cloudflared", admin.GetCloudflaredStatus)
			settingsGroup.POST("/cloudflared/start", admin.StartCloudflared)
			settingsGroup.POST("/cloudflared/stop", admin.StopCloudflared)
			settingsGroup.POST("/cloudflared/remove-token", admin.RemoveCloudflaredToken)
		}
		// themes
		themeGroup := adminAuthrized.Group("/theme")
		{
			themeGroup.PUT("/upload", admin.UploadTheme)
			themeGroup.GET("/list", admin.ListThemes)
			themeGroup.POST("/delete", admin.DeleteTheme)
			themeGroup.GET("/set", admin.SetTheme)
			themeGroup.POST("/update", admin.UpdateTheme)
			themeGroup.POST("/import", admin.ImportTheme)
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
			clientGroup.GET("/:uuid/terminal", api.RequireSensitive2FA(), terminal.RequestTerminal)
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
			two_factorGroup.POST("/disable", api.RequireSensitive2FA(), admin.Disable2FA)
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
			// traffic report notifications
			trafficReportGroup := notificationGroup.Group("/traffic-report")
			{
				trafficReportGroup.GET("/", notification.ListTrafficReportNotifications)
				trafficReportGroup.POST("/edit", notification.EditTrafficReportNotifications)
				trafficReportGroup.POST("/enable", notification.EnableTrafficReportNotifications)
				trafficReportGroup.POST("/disable", notification.DisableTrafficReportNotifications)
			}
		}

		pingTaskGroup := adminAuthrized.Group("/ping")
		{
			pingTaskGroup.GET("/", admin.GetAllPingTasks)
			pingTaskGroup.POST("/add", admin.AddPingTask)
			pingTaskGroup.POST("/delete", admin.DeletePingTask)
			pingTaskGroup.POST("/edit", admin.EditPingTask)
			pingTaskGroup.POST("/order", admin.OrderPingTask)

		}

	}

	public.Static(r.Group("/"), func(handlers ...gin.HandlerFunc) {
		r.NoRoute(handlers...)
	})
}
