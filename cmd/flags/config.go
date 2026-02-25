package flags

var (
	// 数据库配置
	DatabaseFile string // 数据库文件路径

	// 服务配置
	Listen string

	// 资源限制配置
	MaxTerminalSessions int // 最大终端会话数
	MaxWebSocketConns   int // 最大 WebSocket 连接数
	MaxRecordsCacheSize int // 最大记录缓存大小
)