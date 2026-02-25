package flags

var (
	// 数据库配置
	DatabaseType     string // 数据库类型：sqlite, postgres
	DatabaseFile     string // SQLite数据库文件路径
	DatabaseHost     string // PostgreSQL数据库主机地址
	DatabasePort     string // PostgreSQL数据库端口
	DatabaseUser     string // PostgreSQL数据库用户名
	DatabasePass     string // PostgreSQL数据库密码
	DatabaseName     string // PostgreSQL数据库名称
	DatabaseSSLMode  string // PostgreSQL SSL模式 (disable, require, verify-ca, verify-full)
	DatabaseTimezone string // PostgreSQL 时区设置

	Listen string
)
