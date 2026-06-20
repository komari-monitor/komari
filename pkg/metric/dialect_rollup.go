package metric

import (
	"fmt"
	"strings"
)

// rollupColumns is the fixed column list for the rollups table, shared by the
// upsert builder and the row scanner so they cannot drift apart. tags_hash is a
// stable fingerprint of the (canonical) tag set; tags carries the tag map itself
// so reads can return it and tag filters can be pushed down. Including the tag
// dimension in the key is what keeps each distinct tag combination (e.g. a GPU
// device_index) as its own rollup series instead of being merged together.
//
// rollupColumns 是 rollups 表固定列清单，由 upsert 构造器和行扫描器共用，
// 避免二者漂移。tags_hash 是（规范化）标签集合的稳定指纹；tags 携带标签 map
// 本身，因此读取时可以返回它，也能下推标签过滤。把标签维度纳入 key，正是让
// 每个不同标签组合（例如 GPU 的 device_index）保留为自己的 rollup 序列、
// 而不是被合并在一起的原因。
const rollupColumns = "metric_name, entity_id, tags_hash, tags, resolution_nano, bucket_nano, " +
	"count, sum, sum_sq, min_val, max_val, first_val, first_ts, last_val, last_ts, digest, created_at"

// rollupColumnCount is the number of columns in rollupColumns.
//
// rollupColumnCount 是 rollupColumns 中的列数量。
const rollupColumnCount = 17

// rollupTagsArgIndex is the 1-based position of the `tags` JSON column in
// rollupColumns. PostgreSQL needs a ::jsonb cast on that bind placeholder.
//
// rollupTagsArgIndex 是 tags JSON 列在 rollupColumns 中的 1 基位置；
// PostgreSQL 需要在对应占位符上追加 ::jsonb 转换。
const rollupTagsArgIndex = 4

// blobType returns the column type used to store the t-digest sketch.
//
// blobType 返回用于保存 t-digest 摘要的二进制列类型。
func (sqliteDialect) blobType() string { return "BLOB" }

// blobType returns the binary column type used for t-digest blobs.
//
// blobType 返回 MySQL 用于保存 t-digest 摘要的二进制列类型。
func (mysqlDialect) blobType() string { return "LONGBLOB" }

// blobType returns the binary column type used for t-digest blobs.
//
// blobType 返回 PostgreSQL 用于保存 t-digest 摘要的二进制列类型。
func (postgresDialect) blobType() string { return "BYTEA" }

// upsertRollupSQL builds a single-row upsert for a rollup cell, keyed by
// (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano). Compact
// recomputes a bucket wholesale from its inputs each run, so the conflict action
// replaces every summary column rather than trying to merge in SQL.
//
// upsertRollupSQL 构造单个 rollup 单元的 upsert SQL，其 key 为
// (metric_name, entity_id, tags_hash, resolution_nano, bucket_nano)。Compact
// 每次都会从输入整体重新计算一个桶，因此冲突动作会替换每个摘要列，
// 而不是尝试在 SQL 中合并。
func (d sqliteDialect) upsertRollupSQL(t tables) string {
	return buildUpsertRollupSQL(t.rollups, d, sqliteRollupConflict)
}

// upsertRollupSQL builds backend-specific SQL for upserting a rollup cell.
//
// upsertRollupSQL 构造 MySQL rollup 单元 upsert SQL。
func (d mysqlDialect) upsertRollupSQL(t tables) string {
	return buildUpsertRollupSQL(t.rollups, d, mysqlRollupConflict)
}

// upsertRollupSQL builds backend-specific SQL for upserting a rollup cell.
//
// upsertRollupSQL 构造 PostgreSQL rollup 单元 upsert SQL。
func (d postgresDialect) upsertRollupSQL(t tables) string {
	return buildUpsertRollupSQL(t.rollups, d, postgresRollupConflict)
}

// sqliteRollupConflict is the SQLite conflict clause for replacing rollup cells.
//
// sqliteRollupConflict 是 SQLite 用于替换 rollup 单元的冲突处理子句。
const sqliteRollupConflict = `ON CONFLICT(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano) DO UPDATE SET
	tags = excluded.tags,
	count = excluded.count, sum = excluded.sum, sum_sq = excluded.sum_sq,
	min_val = excluded.min_val, max_val = excluded.max_val,
	first_val = excluded.first_val, first_ts = excluded.first_ts,
	last_val = excluded.last_val, last_ts = excluded.last_ts,
	digest = excluded.digest, created_at = excluded.created_at`

// postgresRollupConflict is the PostgreSQL conflict clause for replacing rollup cells.
//
// postgresRollupConflict 是 PostgreSQL 用于替换 rollup 单元的冲突处理子句。
const postgresRollupConflict = `ON CONFLICT(metric_name, entity_id, tags_hash, resolution_nano, bucket_nano) DO UPDATE SET
	tags = EXCLUDED.tags,
	count = EXCLUDED.count, sum = EXCLUDED.sum, sum_sq = EXCLUDED.sum_sq,
	min_val = EXCLUDED.min_val, max_val = EXCLUDED.max_val,
	first_val = EXCLUDED.first_val, first_ts = EXCLUDED.first_ts,
	last_val = EXCLUDED.last_val, last_ts = EXCLUDED.last_ts,
	digest = EXCLUDED.digest, created_at = EXCLUDED.created_at`

// mysqlRollupConflict is the MySQL conflict clause for replacing rollup cells.
//
// mysqlRollupConflict 是 MySQL 用于替换 rollup 单元的冲突处理子句。
const mysqlRollupConflict = `ON DUPLICATE KEY UPDATE
	tags = VALUES(tags),
	count = VALUES(count), sum = VALUES(sum), sum_sq = VALUES(sum_sq),
	min_val = VALUES(min_val), max_val = VALUES(max_val),
	first_val = VALUES(first_val), first_ts = VALUES(first_ts),
	last_val = VALUES(last_val), last_ts = VALUES(last_ts),
	digest = VALUES(digest), created_at = VALUES(created_at)`

// buildUpsertRollupSQL builds a single-row rollup upsert statement.
//
// buildUpsertRollupSQL 构造单行 rollup upsert SQL。
func buildUpsertRollupSQL(table string, d dialect, conflict string) string {
	ph := make([]string, rollupColumnCount)
	for i := 0; i < rollupColumnCount; i++ {
		if i+1 == rollupTagsArgIndex {
			// The tags column is JSON; PostgreSQL requires an explicit ::jsonb cast.
			ph[i] = d.jsonPlaceholder(i + 1)
		} else {
			ph[i] = d.placeholder(i + 1)
		}
	}
	return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) %s",
		table, rollupColumns, strings.Join(ph, ", "), conflict)
}
