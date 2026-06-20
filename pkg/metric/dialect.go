package metric

import (
	"fmt"
	"strings"
)

type dialect interface {
	placeholder(n int) string
	// jsonPlaceholder returns the bind placeholder for a JSON column value.
	// PostgreSQL needs an explicit ::jsonb cast because the driver sends Go
	// strings with a text OID, which the server refuses to assign to a jsonb
	// column. SQLite and MySQL store JSON as text/native and need no cast.
	jsonPlaceholder(n int) string
	jsonType() string
	autoIncrementPrimaryKey() string
	nowExpr() string
	insertDefinitionSQL(t tables) string
	upsertPointSQL(t tables, rowCount int) string
	// jsonExtractEquals renders a boolean predicate comparing the text value at
	// key inside a JSON column to a bind placeholder. The key is interpolated
	// into the SQL (callers must restrict it to a safe charset); the compared
	// value is always bound.
	jsonExtractEquals(column, key, placeholder string) string
}

type tables struct {
	definitions string
	points      string
}

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

type sqliteDialect struct{}

func (sqliteDialect) placeholder(int) string          { return "?" }
func (sqliteDialect) jsonPlaceholder(int) string      { return "?" }
func (sqliteDialect) jsonType() string                { return "TEXT" }
func (sqliteDialect) autoIncrementPrimaryKey() string { return "INTEGER PRIMARY KEY AUTOINCREMENT" }
func (sqliteDialect) nowExpr() string                 { return "CURRENT_TIMESTAMP" }
func (sqliteDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			type = excluded.type,
			unit = excluded.unit,
			description = excluded.description,
			retention_days = excluded.retention_days,
			metadata = excluded.metadata,
			updated_at = excluded.updated_at`, t.definitions)
}
func (sqliteDialect) upsertPointSQL(t tables, rowCount int) string {
	return buildInsertPointsSQL(t.points, sqliteDialect{}, rowCount, `ON CONFLICT(metric_name, entity_id, ts_nano) DO UPDATE SET
		value = excluded.value,
		tags = excluded.tags,
		labels = excluded.labels,
		created_at = excluded.created_at`)
}

type mysqlDialect struct{}

func (mysqlDialect) placeholder(int) string          { return "?" }
func (mysqlDialect) jsonPlaceholder(int) string      { return "?" }
func (mysqlDialect) jsonType() string                { return "JSON" }
func (mysqlDialect) autoIncrementPrimaryKey() string { return "BIGINT AUTO_INCREMENT PRIMARY KEY" }
func (mysqlDialect) nowExpr() string                 { return "CURRENT_TIMESTAMP" }
func (mysqlDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			type = VALUES(type),
			unit = VALUES(unit),
			description = VALUES(description),
			retention_days = VALUES(retention_days),
			metadata = VALUES(metadata),
			updated_at = VALUES(updated_at)`, t.definitions)
}
func (mysqlDialect) upsertPointSQL(t tables, rowCount int) string {
	return buildInsertPointsSQL(t.points, mysqlDialect{}, rowCount, `ON DUPLICATE KEY UPDATE
		value = VALUES(value),
		tags = VALUES(tags),
		labels = VALUES(labels),
		created_at = VALUES(created_at)`)
}

type postgresDialect struct{}

func (postgresDialect) placeholder(n int) string        { return fmt.Sprintf("$%d", n) }
func (postgresDialect) jsonPlaceholder(n int) string    { return fmt.Sprintf("$%d::jsonb", n) }
func (postgresDialect) jsonType() string                { return "JSONB" }
func (postgresDialect) autoIncrementPrimaryKey() string { return "BIGSERIAL PRIMARY KEY" }
func (postgresDialect) nowExpr() string                 { return "CURRENT_TIMESTAMP" }
func (postgresDialect) insertDefinitionSQL(t tables) string {
	return fmt.Sprintf(`INSERT INTO %s
		(name, type, unit, description, retention_days, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
		ON CONFLICT(name) DO UPDATE SET
			type = EXCLUDED.type,
			unit = EXCLUDED.unit,
			description = EXCLUDED.description,
			retention_days = EXCLUDED.retention_days,
			metadata = EXCLUDED.metadata,
			updated_at = EXCLUDED.updated_at`, t.definitions)
}
func (postgresDialect) upsertPointSQL(t tables, rowCount int) string {
	return buildInsertPointsSQL(t.points, postgresDialect{}, rowCount, `ON CONFLICT(metric_name, entity_id, ts_nano) DO UPDATE SET
		value = EXCLUDED.value,
		tags = EXCLUDED.tags,
		labels = EXCLUDED.labels,
		created_at = EXCLUDED.created_at`)
}

func buildInsertPointsSQL(table string, d dialect, rowCount int, suffix string) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(table)
	b.WriteString(" (metric_name, entity_id, ts_nano, value, tags, labels, created_at) VALUES ")
	arg := 1
	rows := make([]string, rowCount)
	for i := 0; i < rowCount; i++ {
		parts := make([]string, 7)
		for j := range parts {
			// Columns are (metric_name, entity_id, ts_nano, value, tags, labels,
			// created_at); positions 4 and 5 (0-indexed) are JSON values.
			if j == 4 || j == 5 {
				parts[j] = d.jsonPlaceholder(arg)
			} else {
				parts[j] = d.placeholder(arg)
			}
			arg++
		}
		rows[i] = "(" + strings.Join(parts, ", ") + ")"
	}
	b.WriteString(strings.Join(rows, ", "))
	if suffix != "" {
		b.WriteByte(' ')
		b.WriteString(suffix)
	}
	return b.String()
}

func tableName(prefix, name string) string {
	return prefix + name
}

// insertDefinitionOnlySQL builds a plain INSERT (no upsert clause) for a metric
// definition. CreateMetric uses it so a duplicate name surfaces as a unique
// constraint violation instead of silently overwriting the existing row.
func insertDefinitionOnlySQL(d dialect, t tables) string {
	cols := "(name, type, unit, description, retention_days, metadata, created_at, updated_at)"
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
func sqlJSONPathQuoted(key string) string {
	escaped := strings.ReplaceAll(key, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return sqlSingleQuote("$.\"" + escaped + "\"")
}

func (sqliteDialect) jsonExtractEquals(column, key, placeholder string) string {
	return fmt.Sprintf("json_extract(%s, '%s') = %s", column, sqlJSONPathQuoted(key), placeholder)
}

func (mysqlDialect) jsonExtractEquals(column, key, placeholder string) string {
	return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '%s')) = %s", column, sqlJSONPathQuoted(key), placeholder)
}

func (postgresDialect) jsonExtractEquals(column, key, placeholder string) string {
	// PostgreSQL's ->> takes the key as a plain text literal (not a JSON path),
	// so a dot or hyphen in the key is harmless; only single quotes need SQL
	// escaping.
	return fmt.Sprintf("%s->>'%s' = %s", column, sqlSingleQuote(key), placeholder)
}

// sqlAggValueExpr returns the SQL value expression that computes agg over the
// "value" column for the given driver, and whether that aggregation can be
// pushed down to this backend. Aggregations that need the ordered raw series
// (first/last/rate) are never pushed down here. Percentiles and population
// standard deviation are pushed down only where the backend has a portable,
// semantics-matching function:
//
//   - STDDEV_POP: MySQL and PostgreSQL (matches the in-memory population stddev,
//     which divides by N). SQLite has no built-in, so it falls back to memory.
//   - percentile_cont(p) WITHIN GROUP (ORDER BY value): PostgreSQL only. This
//     matches the in-memory linear-interpolation percentile. MySQL lacks a
//     portable continuous-percentile function and SQLite has none, so both fall
//     back to memory.
//
// The simple reductions (avg/min/max/sum/count) are portable everywhere.
func sqlAggValueExpr(driver Driver, agg Aggregation) (string, bool) {
	switch agg {
	case AggAvg:
		return "AVG(value)", true
	case AggMin:
		return "MIN(value)", true
	case AggMax:
		return "MAX(value)", true
	case AggSum:
		return "SUM(value)", true
	case AggCount:
		return "COUNT(*)", true
	case AggStdDev:
		switch driver {
		case DriverMySQL, DriverPostgreSQL:
			return "STDDEV_POP(value)", true
		default:
			return "", false
		}
	case AggP50, AggP95, AggP99:
		if driver == DriverPostgreSQL {
			return fmt.Sprintf("percentile_cont(%s) WITHIN GROUP (ORDER BY value)", percentileFraction(agg)), true
		}
		return "", false
	default:
		return "", false
	}
}

func percentileFraction(agg Aggregation) string {
	switch agg {
	case AggP50:
		return "0.5"
	case AggP95:
		return "0.95"
	case AggP99:
		return "0.99"
	default:
		return "0.5"
	}
}
