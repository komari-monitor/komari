package flags

var (
	// 数据库配置
	DatabaseType string // 数据库类型
	DatabaseFile string // 数据库文件路径
	DatabaseHost string // 数据库主机地址
	DatabasePort string // 数据库端口
	DatabaseUser string // 数据库用户名
	DatabasePass string // 数据库密码
	DatabaseName string // 数据库名称

	// 服务配置
	Listen string

	// 资源限制配置
	MaxTerminalSessions int // 最大终端会话数
	MaxWebSocketConns   int // 最大 WebSocket 连接数
	MaxRecordsCacheSize int // 最大记录缓存大小
)
