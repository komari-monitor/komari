package flags

import "strings"

const (
	DatabaseTypeSQLite = "sqlite"
)

var (
	// 数据库配置
	DatabaseType string // 数据库类型：sqlite
	DatabaseFile string // SQLite数据库文件路径
	DatabaseHost string // 保留的兼容参数，当前未使用
	DatabasePort string // 保留的兼容参数，当前未使用
	DatabaseUser string // 保留的兼容参数，当前未使用
	DatabasePass string // 保留的兼容参数，当前未使用
	DatabaseName string // 保留的兼容参数，当前未使用

	Listen string
)

func NormalizeDatabaseType(databaseType string) string {
	databaseType = strings.ToLower(strings.TrimSpace(databaseType))
	if databaseType == "" {
		return DatabaseTypeSQLite
	}
	return databaseType
}

func ApplyDatabaseTypeNormalization() string {
	DatabaseType = NormalizeDatabaseType(DatabaseType)
	return DatabaseType
}

func IsSQLite() bool {
	return NormalizeDatabaseType(DatabaseType) == DatabaseTypeSQLite
}

func SupportedDatabaseTypes() string {
	return DatabaseTypeSQLite
}
