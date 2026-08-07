package metric

import (
	"fmt"
	"strings"
)

// dialect abstracts SQL differences between supported databases.
//
// dialect 抽象不同数据库后端的 SQL 差异。
type dialect interface {
	placeholder(n int) string
	// jsonPlaceholder returns the bind placeholder for a JSON column value.
	// PostgreSQL needs an explicit ::jsonb cast because the driver sends Go
	// strings with a text OID, which the server refuses to assign to a jsonb
	// column. SQLite and MySQL store JSON as text/native and need no cast.
	//
	// jsonPlaceholder 返回 JSON 列值的绑定占位符。PostgreSQL 需要显式的
	// ::jsonb 转换，因为驱动会以 text OID 发送 Go 字符串，服务器拒绝把它赋给
	// jsonb 列。SQLite 和 MySQL 将 JSON 存为文本或原生类型，不需要转换。
	jsonPlaceholder(n int) string
	jsonType() string
	autoIncrementPrimaryKey() string
	insertDefinitionSQL(t tables) string
	// jsonExtractEquals renders a boolean predicate comparing the text value at
	// key inside a JSON column to a bind placeholder. The key is interpolated
	// into the SQL (callers must restrict it to a safe charset); the compared
	// value is always bound.
	//
	// jsonExtractEquals 渲染一个布尔谓词，用于比较 JSON 列内 key 对应的文本值和
	// 绑定占位符。key 会插入 SQL 中（调用方必须限制到安全字符集）；被比较的值
	// 始终通过绑定参数传入。
	jsonExtractEquals(column, key, placeholder string) string
	// blobType is the column type for the rollup t-digest sketch.
	//
	// blobType 是 rollup t-digest sketch 的列类型。
	blobType() string
	renderSeriesDictionary(tables tables, indexName string, plan seriesDictionaryPlan) renderedSQL
	renderRollupRead(tables tables, indexName string, plan rollupReadPlan) renderedSQL
}

// tables stores the physical table names used by a Store.
//
// tables 保存 Store 使用的实际表名。
type tables struct {
	// definitions is the metric definitions table.
	//
	// definitions 是指标定义表。
	definitions string
	// points and watermarks name legacy tables read only by the explicit
	// structure rebuild. The normalized schema never creates either table.
	points string
	// series interns the name/entity/tag/tag-hash tuple.
	series string
	// resolutions interns rollup intervals in milliseconds.
	resolutions string
	// labels interns labels independently from series tags.
	labels string
	// rollups is the downsampled rollups table.
	//
	// rollups 是降采样 rollup 表。
	rollups    string
	watermarks string
	state      string
}

// newDialect returns the SQL dialect implementation for a backend.
//
// newDialect 根据数据库后端创建对应 SQL 方言实现。
func newDialect(driver Driver) dialect {
	switch driver {
	case DriverPostgreSQL:
		return postgresDialect{}
	case DriverMySQL:
		return mysqlDialect{}
	default:
		return sqliteDialect{}
	}
}

// sqliteDialect implements SQL rendering for SQLite.
//
// sqliteDialect 实现 SQLite 方言。
type sqliteDialect struct{}

// placeholder returns a bind placeholder for this SQL dialect.
//
// placeholder 返回 SQLite 的绑定参数占位符。
func (sqliteDialect) placeholder(int) string { return "?" }

// jsonPlaceholder returns a bind placeholder for JSON values.
//
// jsonPlaceholder 返回 SQLite JSON 值的绑定参数占位符。
func (sqliteDialect) jsonPlaceholder(int) string { return "?" }

// jsonType returns the SQL column type used for JSON values.
//
// jsonType 返回 SQLite 使用的 JSON 存储类型。
func (sqliteDialect) jsonType() string { return "TEXT" }

// autoIncrementPrimaryKey returns the SQL definition for an auto-incrementing primary key.
//
// autoIncrementPrimaryKey 返回 SQLite 自增主键定义。
func (sqliteDialect) autoIncrementPrimaryKey() string { return "INTEGER PRIMARY KEY AUTOINCREMENT" }

// insertDefinitionSQL builds SQL for inserting or updating a metric definition.
//
// insertDefinitionSQL 构造 SQLite 指标定义 upsert SQL。
func (sqliteDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type = excluded.type,
			unit = excluded.unit,
			description = excluded.description,
			retention_days = excluded.retention_days,
			metadata = excluded.metadata,
			updated_at_milli = excluded.updated_at_milli`, t.definitions)
}

// mysqlDialect implements SQL rendering for MySQL.
//
// mysqlDialect 实现 MySQL 方言。
type mysqlDialect struct{}

// placeholder returns a bind placeholder for this SQL dialect.
//
// placeholder 返回 MySQL 的绑定参数占位符。
func (mysqlDialect) placeholder(int) string { return "?" }

// jsonPlaceholder returns a bind placeholder for JSON values.
//
// jsonPlaceholder 返回 MySQL JSON 值的绑定参数占位符。
func (mysqlDialect) jsonPlaceholder(int) string { return "?" }

// jsonType returns the SQL column type used for JSON values.
//
// jsonType 返回 MySQL 使用的 JSON 存储类型。
func (mysqlDialect) jsonType() string { return "JSON" }

// autoIncrementPrimaryKey returns the SQL definition for an auto-incrementing primary key.
//
// autoIncrementPrimaryKey 返回 MySQL 自增主键定义。
func (mysqlDialect) autoIncrementPrimaryKey() string { return "BIGINT AUTO_INCREMENT PRIMARY KEY" }

// insertDefinitionSQL builds SQL for inserting or updating a metric definition.
//
// insertDefinitionSQL 构造 MySQL 指标定义 upsert SQL。
func (mysqlDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			type = VALUES(type),
			unit = VALUES(unit),
			description = VALUES(description),
			retention_days = VALUES(retention_days),
			metadata = VALUES(metadata),
			updated_at_milli = VALUES(updated_at_milli)`, t.definitions)
}

// postgresDialect implements SQL rendering for PostgreSQL.
//
// postgresDialect 实现 PostgreSQL 方言。
type postgresDialect struct{}

// placeholder returns a bind placeholder for this SQL dialect.
//
// placeholder 返回 PostgreSQL 的编号绑定参数占位符。
func (postgresDialect) placeholder(n int) string { return fmt.Sprintf("$%d", n) }

// jsonPlaceholder returns a bind placeholder for JSON values.
//
// jsonPlaceholder 返回 PostgreSQL JSONB 值的绑定参数占位符。
func (postgresDialect) jsonPlaceholder(n int) string { return fmt.Sprintf("$%d::jsonb", n) }

// jsonType returns the SQL column type used for JSON values.
//
// jsonType 返回 PostgreSQL 使用的 JSONB 存储类型。
func (postgresDialect) jsonType() string { return "JSONB" }

// autoIncrementPrimaryKey returns the SQL definition for an auto-incrementing primary key.
//
// autoIncrementPrimaryKey 返回 PostgreSQL 自增主键定义。
func (postgresDialect) autoIncrementPrimaryKey() string { return "BIGSERIAL PRIMARY KEY" }

// insertDefinitionSQL builds SQL for inserting or updating a metric definition.
//
// insertDefinitionSQL 构造 PostgreSQL 指标定义 upsert SQL。
func (postgresDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
		ON CONFLICT(name) DO UPDATE SET
			type = EXCLUDED.type,
			unit = EXCLUDED.unit,
			description = EXCLUDED.description,
			retention_days = EXCLUDED.retention_days,
			metadata = EXCLUDED.metadata,
			updated_at_milli = EXCLUDED.updated_at_milli`, t.definitions)
}

// tableName combines a table prefix and logical table name.
//
// tableName 用表名前缀和逻辑表名生成实际表名。
func tableName(prefix, name string) string {
	return prefix + name
}

// insertDefinitionOnlySQL builds a plain INSERT (no upsert clause) for a metric
// definition. CreateMetric uses it so a duplicate name surfaces as a unique
// constraint violation instead of silently overwriting the existing row.
//
// insertDefinitionOnlySQL 构造普通 INSERT（不带 upsert），供
// CreateMetric 保持“只创建”语义；重复名称会暴露为唯一约束错误。
func insertDefinitionOnlySQL(d dialect, t tables) string {
	cols := "(name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli)"
	ph := []string{
		d.placeholder(1),
		d.placeholder(2),
		d.placeholder(3),
		d.placeholder(4),
		d.placeholder(5),
		d.jsonPlaceholder(6),
		d.placeholder(7),
		d.placeholder(8),
	}
	return fmt.Sprintf("INSERT INTO %s %s VALUES (%s)", t.definitions, cols, strings.Join(ph, ", "))
}

// sqlSingleQuote escapes a string for safe inclusion inside a single-quoted SQL
// string literal by doubling embedded single quotes.
//
// sqlSingleQuote 通过把单引号加倍，安全地转义可放入 SQL 单引号字符串
// 字面量的内容。
func sqlSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// sqlJSONPathQuoted builds a quoted JSON path member access ($."key") for the
// given object key and returns it ready to embed inside a single-quoted SQL
// string literal. Using the quoted form ($."key") rather than the bare form
// ($.key) is required for keys that contain dots, hyphens, spaces or other
// characters the JSON path grammar would otherwise interpret (e.g. a dot is a
// member separator, so the bare path "$.region.zone" would look up nested
// member "zone" of object "region" instead of the flat key "region.zone").
//
// Escaping is applied in two layers, innermost first:
//  1. JSON path string escaping inside the double quotes: backslash and double
//     quote are backslash-escaped.
//  2. SQL string-literal escaping of the whole path: single quotes doubled.
//
// sqlJSONPathQuoted 为给定对象键构造带引号的 JSON path 成员访问（$."key"），
// 并返回可嵌入 SQL 单引号字符串字面量的结果。必须使用带引号形式（$."key"），
// 而不是裸形式（$.key），因为包含点号、连字符、空格或其他 JSON path 语法字符的
// key 会被误解；例如点号是成员分隔符，所以裸路径 "$.region.zone" 会查找对象
// "region" 的嵌套成员 "zone"，而不是平铺 key "region.zone"。
//
// 转义按从内到外两层应用：
//  1. 双引号内的 JSON path 字符串转义：反斜杠和双引号使用反斜杠转义。
//  2. 整个 path 的 SQL 字符串字面量转义：单引号加倍。
func sqlJSONPathQuoted(key string) string {
	escaped := strings.ReplaceAll(key, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return sqlSingleQuote("$.\"" + escaped + "\"")
}

// jsonExtractEquals renders a JSON equality predicate.
//
// jsonExtractEquals 构造 SQLite JSON 字段等值过滤表达式。
func (sqliteDialect) jsonExtractEquals(column, key, placeholder string) string {
	return fmt.Sprintf("json_extract(%s, '%s') = %s", column, sqlJSONPathQuoted(key), placeholder)
}

// jsonExtractEquals renders a JSON equality predicate.
//
// jsonExtractEquals 构造 MySQL JSON 字段等值过滤表达式。
func (mysqlDialect) jsonExtractEquals(column, key, placeholder string) string {
	return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '%s')) = %s", column, sqlJSONPathQuoted(key), placeholder)
}

// jsonExtractEquals renders a JSON equality predicate.
//
// jsonExtractEquals 构造 PostgreSQL JSONB 字段等值过滤表达式。
func (postgresDialect) jsonExtractEquals(column, key, placeholder string) string {
	// PostgreSQL's ->> takes the key as a plain text literal (not a JSON path),
	// so a dot or hyphen in the key is harmless; only single quotes need SQL
	// escaping.
	return fmt.Sprintf("%s->>'%s' = %s", column, sqlSingleQuote(key), placeholder)
}
