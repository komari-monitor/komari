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
		if err := s.dropIndex(ctx, index); err != nil {
			return err
		}
	}
	return nil
}

// indexColumns reads the stored column list for one index. found is false when
// the index does not exist yet, which is not an error.
//
// indexColumns 读取一个索引当前存储的列顺序；found 为 false 表示索引还不存在。
func (s *Store) indexColumns(ctx context.Context, index normalizedIndex) ([]string, bool, error) {
	switch s.cfg.Driver {
	case DriverMySQL:
		rows, err := s.db.QueryContext(ctx, "SELECT COLUMN_NAME FROM information_schema.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ? ORDER BY SEQ_IN_INDEX",
			index.table, index.name)
		if err != nil {
			return nil, false, err
		}
		return collectIndexColumns(rows)
	case DriverPostgreSQL:
		rows, err := s.db.QueryContext(ctx, "SELECT a.attname FROM pg_index i "+
			"JOIN pg_class c ON c.oid = i.indexrelid "+
			"JOIN LATERAL unnest(i.indkey::smallint[]) WITH ORDINALITY AS k(attnum, ord) ON true "+
			"JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum "+
			"WHERE c.relname = ? AND k.attnum > 0 ORDER BY k.ord", index.name)
		if err != nil {
			return nil, false, err
		}
		return collectIndexColumns(rows)
	default:
		var statement sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", index.name).Scan(&statement)
		if err == sql.ErrNoRows || !statement.Valid {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return sqliteIndexColumns(statement.String), true, nil
	}
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

// sqliteIndexColumns extracts the column list from a stored CREATE INDEX
// statement. Non-simple entries keep their first token, which is enough to
// detect a stale definition.
//
// sqliteIndexColumns 从存储的 CREATE INDEX 语句中提取列定义。
func sqliteIndexColumns(statement string) []string {
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
	column = strings.Trim(column, "\"\x60'[]")
	if fields := strings.Fields(column); len(fields) > 0 {
		return fields[0]
	}
	return column
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
