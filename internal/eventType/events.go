package eventType

const (
	ClientWebsocketConnected    = "client.websocket.connected"    // 客户端通过 websocket 连接
	ClientWebsocketDisconnected = "client.websocket.disconnected" // 客户端断开 websocket 连接
	ClientMessageReceived       = "client.message.received"       // 收到客户端消息
	ClientCreated               = "client.created"                // 新客户端创建
	ClientUpdated               = "client.updated"                // 客户端信息更新
	ClientDeleted               = "client.deleted"                // 客户端删除
	ClientRenewed               = "client.renewed"                // 客户端续期

	UserUpdateUsername = "user.update.username" // 用户更改用户名
	UserUpdatePassword = "user.update.password" // 用户更改密码
	UserLogin          = "user.login.succeeded" // 用户登录
	UserLogout         = "user.logout"          // 用户登出
	UserOidcBound      = "user.oidc.bound"      // 用户绑定 OIDC
	UserOidcUnbound    = "user.oidc.unbound"    // 用户解绑 OIDC
	UserTwoFaAdded     = "user.2fa.added"       // 用户添加 2FA
	UserTwoFaRemoved   = "user.2fa.removed"     // 用户移除 2FA
	UserSessionRevoked = "user.session.revoked" // 用户会话撤销

	LoginFailed = "user.login.failed" // 用户登录失败

	ConfigUpdated = "config.updated" // 配置更改。当服务器第一次启动载入配置时也会触发此事件，其中 old 全部为go类型零值， new 为载入的配置内容，若在配置载入前读取Conf，返回的将是零值配置。

	ProcessStart = "process.start" // 进程启动，如果在此事件的监听器中返回错误，进程将退出
	ProcessExit  = "process.exit"  // 进程停止

	ServerInitializeStart = "server.routers.start"     // 服务器路由初始化开始，如果在此事件的监听器中返回错误，进程将退出
	ServerInitializeDone  = "server.routers.done"      // 服务器路由初始化完成
	ServerListenGrpcStart = "server.listen.grpc.start" // 服务器开始监听 gRPC
	ServerListenGrpcStop  = "server.listen.grpc.stop"  // 服务器停止监听 gRPC

	TaskCreated = "task.created" // 任务创建
	TaskUpdated = "task.updated" // 任务更新

	HttpRequestReceived = "http.request.received" // 收到 HTTP 请求

	SchedulerDatabase       = "scheduler.database"       // 数据库定时任务
	SchedulerEveryMinute    = "scheduler.everyminute"    // 每分钟定时触发
	SchedulerEvery5Minutes  = "scheduler.every5minutes"  // 每五分钟定时触发
	SchedulerEvery30Minutes = "scheduler.every30minutes" // 每三十分钟定时触发
	SchedulerEveryHour      = "scheduler.everyhour"      // 每小时定时触发
	SchedulerEveryDay       = "scheduler.everyday"       // 每天定时触发，服务器启动的当天不会触发此事件

	NotificationSent   = "notification.sent"   // 通知发送
	NotificationFailed = "notification.failed" // 通知发送失败

	TerminalEstablished = "terminal.established" // 终端连接建立
	TerminalClosed      = "terminal.closed"      // 终端连接关闭

	GeoIpUpdateStart  = "geoip.update.start"  // GeoIP 数据库更新开始
	GeoIpUpdateDone   = "geoip.update.done"   // GeoIP 数据库更新完成
	GeoIpUpdateFailed = "geoip.update.failed" // GeoIP 数据库更新失败
)
