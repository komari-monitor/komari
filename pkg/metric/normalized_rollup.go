package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const normalizedRollupColumns = "(series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)"

type normalizedRollupRow struct {
	seriesID       int64
	resolutionID   int64
	labelID        int64
	bucketMilli    int64
	count          int64
	sum            float64
	sumSq          float64
	min            float64
	max            float64
	firstVal       float64
	firstTSMilli   int64
	lastVal        float64
	lastTSMilli    int64
	digest         []byte
	createdAtMilli int64
}

// rollupDictionaryCache is scoped to one write transaction. It avoids repeated
// dictionary lookups across buckets and tiers without retaining every series
// id for the lifetime of the Store.
type rollupDictionaryCache struct {
	series      map[string]int64
	labels      map[string]int64
	resolutions map[int64]int64
}

func newRollupDictionaryCache() *rollupDictionaryCache {
	return &rollupDictionaryCache{
		series:      make(map[string]int64),
		labels:      make(map[string]int64),
		resolutions: make(map[int64]int64),
	}
}

func (c *rollupDictionaryCache) seriesID(ctx context.Context, s *Store, tx *sql.Tx, metricName string, key rollupKey, tagsJSON string) (int64, error) {
	cacheKey := metricName + "\x00" + key.entityID + "\x00" + key.tagsHash
	if id, ok := c.series[cacheKey]; ok {
		return id, nil
	}
	id, err := s.internSeriesTx(ctx, metricName, key.entityID, key.tagsHash, tagsJSON, tx)
	if err == nil {
		c.series[cacheKey] = id
	}
	return id, err
}

func (c *rollupDictionaryCache) labelID(ctx context.Context, s *Store, tx *sql.Tx, hash, labelsJSON string) (int64, error) {
	if id, ok := c.labels[hash]; ok {
		return id, nil
	}
	id, err := s.internLabelsTx(ctx, hash, labelsJSON, tx)
	if err == nil {
		c.labels[hash] = id
	}
	return id, err
}

func (c *rollupDictionaryCache) resolutionID(ctx context.Context, s *Store, tx *sql.Tx, interval time.Duration) (int64, error) {
	milli := interval.Milliseconds()
	if id, ok := c.resolutions[milli]; ok {
		return id, nil
	}
	id, err := s.internResolutionTx(ctx, interval, tx)
	if err == nil {
		c.resolutions[milli] = id
	}
	return id, err
}

func (s *Store) insertIgnoreSQL(table, columns, values string) string {
	switch s.cfg.Driver {
	case DriverMySQL:
		return fmt.Sprintf("INSERT IGNORE INTO %s %s VALUES %s", table, columns, values)
	case DriverPostgreSQL:
		return fmt.Sprintf("INSERT INTO %s %s VALUES %s ON CONFLICT DO NOTHING", table, columns, values)
	default:
		return fmt.Sprintf("INSERT OR IGNORE INTO %s %s VALUES %s", table, columns, values)
	}
}

// internSeriesTx stores the immutable name/entity/tag tuple once. Labels are
// intentionally not part of this dictionary: a label set is independently
// interned and referenced by each rollup row.
func (s *Store) internSeriesTx(ctx context.Context, metricName, entityID, tagsHash, tagsJSON string, tx *sql.Tx) (int64, error) {
	if tagsJSON == "" {
		tagsJSON = "{}"
	}
	var id int64
	lookup := fmt.Sprintf(
		"SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s AND tags_hash = %s",
		s.tables.series, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
	)
	if err := tx.QueryRowContext(ctx, lookup, metricName, entityID, tagsHash).Scan(&id); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		s.insertIgnoreSQL(s.tables.series, "(metric_name, entity_id, tags_hash, tags)",
			"("+joinSQL([]string{s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.jsonPlaceholder(4)})+")"),
		metricName, entityID, tagsHash, tagsJSON,
	); err != nil {
		return 0, err
	}
	err := tx.QueryRowContext(ctx, lookup, metricName, entityID, tagsHash).Scan(&id)
	return id, err
}

func (s *Store) internLabelsTx(ctx context.Context, labelsHash, labelsJSON string, tx *sql.Tx) (int64, error) {
	if labelsJSON == "" {
		labelsJSON = "{}"
	}
	var id int64
	lookup := fmt.Sprintf("SELECT id FROM %s WHERE labels_hash = %s", s.tables.labels, s.dialect.placeholder(1))
	if err := tx.QueryRowContext(ctx, lookup, labelsHash).Scan(&id); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL(s.tables.labels, "(labels_hash, labels)", "("+s.dialect.placeholder(1)+", "+s.dialect.jsonPlaceholder(2)+")"), labelsHash, labelsJSON); err != nil {
		return 0, err
	}
	err := tx.QueryRowContext(ctx, lookup, labelsHash).Scan(&id)
	return id, err
}

func (s *Store) internResolutionTx(ctx context.Context, interval time.Duration, tx *sql.Tx) (int64, error) {
	milli := interval.Milliseconds()
	var id int64
	lookup := fmt.Sprintf("SELECT id FROM %s WHERE resolution_milli = %s", s.tables.resolutions, s.dialect.placeholder(1))
	if err := tx.QueryRowContext(ctx, lookup, milli).Scan(&id); err == nil {
		return id, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL(s.tables.resolutions, "(resolution_milli)", "("+s.dialect.placeholder(1)+")"), milli); err != nil {
		return 0, err
	}
	err := tx.QueryRowContext(ctx, lookup, milli).Scan(&id)
	return id, err
}

func joinSQL(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, part := range parts[1:] {
		out += ", " + part
	}
	return out
}

func (s *Store) normalizedRollupUpsertSuffix() string {
	switch s.cfg.Driver {
	case DriverMySQL:
		return " ON DUPLICATE KEY UPDATE count=VALUES(count), sum=VALUES(sum), sum_sq=VALUES(sum_sq), min_val=VALUES(min_val), max_val=VALUES(max_val), first_val=VALUES(first_val), first_ts_milli=VALUES(first_ts_milli), last_val=VALUES(last_val), last_ts_milli=VALUES(last_ts_milli), digest=VALUES(digest), created_at_milli=VALUES(created_at_milli)"
	case DriverPostgreSQL:
		return " ON CONFLICT(series_id, resolution_id, label_id, bucket_milli) DO UPDATE SET count=EXCLUDED.count, sum=EXCLUDED.sum, sum_sq=EXCLUDED.sum_sq, min_val=EXCLUDED.min_val, max_val=EXCLUDED.max_val, first_val=EXCLUDED.first_val, first_ts_milli=EXCLUDED.first_ts_milli, last_val=EXCLUDED.last_val, last_ts_milli=EXCLUDED.last_ts_milli, digest=EXCLUDED.digest, created_at_milli=EXCLUDED.created_at_milli"
	default:
		return " ON CONFLICT(series_id, resolution_id, label_id, bucket_milli) DO UPDATE SET count=excluded.count, sum=excluded.sum, sum_sq=excluded.sum_sq, min_val=excluded.min_val, max_val=excluded.max_val, first_val=excluded.first_val, first_ts_milli=excluded.first_ts_milli, last_val=excluded.last_val, last_ts_milli=excluded.last_ts_milli, digest=excluded.digest, created_at_milli=excluded.created_at_milli"
	}
}

func (s *Store) upsertNormalizedRollupRowsTx(ctx context.Context, rows []normalizedRollupRow, tx *sql.Tx) error {
	if len(rows) == 0 {
		return nil
	}
	valueGroups := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*15)
	placeholder := 1
	for _, row := range rows {
		values := make([]string, 15)
		for i := range values {
			values[i] = s.dialect.placeholder(placeholder)
			placeholder++
		}
		valueGroups = append(valueGroups, "("+joinSQL(values)+")")
		args = append(args,
			row.seriesID, row.resolutionID, row.labelID, row.bucketMilli,
			row.count, row.sum, row.sumSq, row.min, row.max,
			row.firstVal, row.firstTSMilli, row.lastVal, row.lastTSMilli,
			row.digest, row.createdAtMilli,
		)
	}
	_, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s %s VALUES %s%s",
		s.tables.rollups, normalizedRollupColumns, joinSQL(valueGroups), s.normalizedRollupUpsertSuffix()), args...)
	return err
}

func (s *Store) upsertRollupWithDictionaryTx(ctx context.Context, metricName string, interval time.Duration, key rollupKey, bucket *rollupBucket, cache *rollupDictionaryCache, tx *sql.Tx) error {
	seriesID, err := cache.seriesID(ctx, s, tx, metricName, key, bucket.tagsJSON)
	if err != nil {
		return err
	}
	labelID, err := cache.labelID(ctx, s, tx, key.labelsHash, bucket.labelsJSON)
	if err != nil {
		return err
	}
	resolutionID, err := cache.resolutionID(ctx, s, tx, interval)
	if err != nil {
		return err
	}
	return s.upsertNormalizedRollupRowsTx(ctx, []normalizedRollupRow{{
		seriesID: seriesID, resolutionID: resolutionID, labelID: labelID,
		bucketMilli: key.bucket, count: bucket.count, sum: bucket.sum, sumSq: bucket.sumSq,
		min: bucket.min, max: bucket.max, firstVal: bucket.firstVal, firstTSMilli: bucket.firstTS,
		lastVal: bucket.lastVal, lastTSMilli: bucket.lastTS, digest: bucket.encodedDigest(),
		createdAtMilli: timeMillis(time.Now()),
	}}, tx)
}
