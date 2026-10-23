package metric

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// normalizedIndex 的形状修复。
//
// CREATE INDEX IF NOT EXISTS 在索引名已存在时是空操作，所以只改
// normalizedIndexesFor 里的列定义只会影响新建的数据库：已有库会留着旧的窄
// 索引，同时静默丢掉覆盖读计划（series 过滤回落到逐候选行回表）。Migrate
// 因此需要把库里存的索引定义和声明的定义比对一次，不一致就重建。
//
// 这里只比对按规范名管理的索引；手工添加的索引不会被删除。

// rebuildStaleIndexes drops normalized indexes whose stored column list no
// longer matches the declared one, so createNormalizedIndexes rebuilds them.
//
// rebuildStaleIndexes 删除列定义已经过时的规范化索引，交给后续创建步骤重建。
func (s *Store) rebuildStaleIndexes(ctx context.Context) error {
	for _, index := range s.normalizedIndexes() {
		existing, found, err := s.indexColumns(ctx, index)
		if err != nil {
			return err
		}
		if !found || sameIndexColumns(existing, index.columns) {
			continue
		}
		if err := s.rebuildIndex(ctx, index); err != nil {
			return err
		}
	}
	return nil
}

// rebuildIndex replaces one stale definition.
//
// MySQL refuses to drop an index that a foreign key still relies on: the rollups
// table references resolutions and the only index leading with resolution_id is
// the one being replaced. The replacement is therefore created under a temporary
// name first, which satisfies the constraint immediately, and only then does the
// old index go away and the new one take the canonical name.
//
// rebuildIndex 替换一个过时的索引定义。MySQL 不允许删除仍被外键依赖的索引，所以
// 先以临时名建好新索引（立刻满足外键约束），再删旧的，最后把新索引改成正式名。
func (s *Store) rebuildIndex(ctx context.Context, index normalizedIndex) error {
	if s.cfg.Driver != DriverMySQL {
		return s.dropIndex(ctx, index)
	}
	staging := index.name + "_rebuild"
	// Clear a staging index left behind by an interrupted run.
	if err := s.dropIndex(ctx, normalizedIndex{name: staging, table: index.table}); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("CREATE INDEX %s ON %s (%s)", staging, index.table, index.columns)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", index.table, index.name)); err != nil {
		// The staged index stays behind and the next start retries cleanly.
		return err
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME INDEX %s TO %s", index.table, staging, index.name))
	return err
}

// indexColumnsQuery returns the introspection statement for one backend. It is
// separate from execution so the placeholder style of every dialect can be
// asserted without a server: SQLite and MySQL bind "?" while PostgreSQL binds
// "$1", and sending "?" to PostgreSQL fails to parse.
//
// indexColumnsQuery 返回对应后端的索引列探测语句。与执行分离，便于在没有服务端
// 的情况下断言占位符风格：SQLite 与 MySQL 用 "?"，PostgreSQL 用 "$1"。
func (s *Store) indexColumnsQuery(index normalizedIndex) (string, []any) {
	switch s.cfg.Driver {
	case DriverMySQL:
		return "SELECT COLUMN_NAME FROM information_schema.STATISTICS " +
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ? " +
			"ORDER BY SEQ_IN_INDEX", []any{index.table, index.name}
	case DriverPostgreSQL:
		// pg_get_indexdef returns the stored DDL, which parses with the same
		// helper as SQLite and avoids depending on pg_index.indkey ordering and
		// int2vector casting, both of which vary across server versions.
		//
		// pg_get_indexdef 直接返回索引 DDL，可与 SQLite 共用解析器，避免依赖
		// pg_index.indkey 的排序与 int2vector 转换（这两者在不同版本上行为不同）。
		return "SELECT pg_get_indexdef(c.oid) FROM pg_class c " +
			"JOIN pg_namespace n ON n.oid = c.relnamespace " +
			"WHERE c.relname = " + s.dialect.placeholder(1) + " AND c.relkind = 'i'",
			[]any{index.name}
	default:
		return "SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", []any{index.name}
	}
}

// indexColumns reads the stored column list for one index. found is false when
// the index does not exist yet, which is not an error.
//
// indexColumns 读取一个索引当前存储的列顺序；found 为 false 表示索引还不存在。
func (s *Store) indexColumns(ctx context.Context, index normalizedIndex) ([]string, bool, error) {
	query, args := s.indexColumnsQuery(index)
	if s.cfg.Driver == DriverMySQL {
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, false, err
		}
		return collectIndexColumns(rows)
	}
	var statement sql.NullString
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&statement)
	if err == sql.ErrNoRows || !statement.Valid {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return indexColumnsFromDDL(statement.String), true, nil
}

func collectIndexColumns(rows *sql.Rows) ([]string, bool, error) {
	defer rows.Close()
	columns := make([]string, 0, 4)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, false, err
		}
		columns = append(columns, normalizeIndexColumn(column))
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(columns) == 0 {
		return nil, false, nil
	}
	return columns, true, nil
}

// indexColumnsFromDDL extracts the column list from a CREATE INDEX statement.
// SQLite and PostgreSQL both expose the stored DDL, so both take this path.
// Entries keep their first token, enough to detect a stale definition.
//
// indexColumnsFromDDL 从 CREATE INDEX 语句中提取列定义；SQLite 与 PostgreSQL
// 都直接暴露存储的 DDL，因此共用这条路径。
func indexColumnsFromDDL(statement string) []string {
	open := strings.Index(statement, "(")
	closing := strings.LastIndex(statement, ")")
	if open < 0 || closing <= open {
		return nil
	}
	parts := strings.Split(statement[open+1:closing], ",")
	columns := make([]string, 0, len(parts))
	for _, part := range parts {
		columns = append(columns, normalizeIndexColumn(part))
	}
	return columns
}

func normalizeIndexColumn(column string) string {
	column = strings.ToLower(strings.TrimSpace(column))
	// Drop ASC/DESC, COLLATE, and operator-class suffixes before unquoting, so a
	// quoted name followed by a direction still reduces to the bare column.
	if fields := strings.Fields(column); len(fields) > 0 {
		column = fields[0]
	}
	return strings.Trim(column, "\"\x60'[]")
}

// sameIndexColumns compares a stored column list with a declared one.
//
// sameIndexColumns 比较存储的列顺序和声明的列顺序是否一致。
func sameIndexColumns(existing []string, declared string) bool {
	want := strings.Split(declared, ",")
	if len(existing) != len(want) {
		return false
	}
	for i, column := range want {
		if existing[i] != normalizeIndexColumn(column) {
			return false
		}
	}
	return true
}

// dropIndex removes one normalized index when it exists.
//
// dropIndex 删除一个已存在的规范化索引。
func (s *Store) dropIndex(ctx context.Context, index normalizedIndex) error {
	if s.cfg.Driver == DriverMySQL {
		exists, err := s.mysqlIndexExists(ctx, index.table, index.name)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		_, err = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s", index.table, index.name))
		return err
	}
	_, err := s.db.ExecContext(ctx, "DROP INDEX IF EXISTS "+index.name)
	return err
}
