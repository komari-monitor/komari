package flags

import "strings"

const (
	DatabaseTypeSQLite   = "sqlite"
	DatabaseTypePostgres = "postgres"
)

var (
	// 数据库配置
	DatabaseType string // 数据库类型：sqlite, postgres
	DatabaseFile string // SQLite数据库文件路径
	DatabaseDSN  string // PostgreSQL 连接 DSN

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

func IsPostgres() bool {
	return NormalizeDatabaseType(DatabaseType) == DatabaseTypePostgres
}

func SupportedDatabaseTypes() string {
	return DatabaseTypeSQLite + ", " + DatabaseTypePostgres
}
