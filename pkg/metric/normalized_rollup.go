package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
	if _, err := tx.ExecContext(ctx,
		s.insertIgnoreSQL(s.tables.series, "(metric_name, entity_id, tags_hash, tags)",
			"("+joinSQL([]string{s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.jsonPlaceholder(4)})+")"),
		metricName, entityID, tagsHash, tagsJSON,
	); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s AND tags_hash = %s",
		s.tables.series, s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3),
	), metricName, entityID, tagsHash).Scan(&id)
	return id, err
}

func (s *Store) internLabelsTx(ctx context.Context, labelsHash, labelsJSON string, tx *sql.Tx) (int64, error) {
	if labelsJSON == "" {
		labelsJSON = "{}"
	}
	if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL(s.tables.labels, "(labels_hash, labels)", "("+s.dialect.placeholder(1)+", "+s.dialect.jsonPlaceholder(2)+")"), labelsHash, labelsJSON); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT id FROM %s WHERE labels_hash = %s", s.tables.labels, s.dialect.placeholder(1)), labelsHash).Scan(&id)
	return id, err
}

func (s *Store) internResolutionTx(ctx context.Context, interval time.Duration, tx *sql.Tx) (int64, error) {
	milli := interval.Milliseconds()
	if _, err := tx.ExecContext(ctx, s.insertIgnoreSQL(s.tables.resolutions, "(resolution_milli)", "("+s.dialect.placeholder(1)+")"), milli); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT id FROM %s WHERE resolution_milli = %s", s.tables.resolutions, s.dialect.placeholder(1)), milli).Scan(&id)
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

func (s *Store) upsertRollupTx(ctx context.Context, metricName string, interval time.Duration, key rollupKey, bucket *rollupBucket, tx *sql.Tx) error {
	seriesID, err := s.internSeriesTx(ctx, metricName, key.entityID, key.tagsHash, bucket.tagsJSON, tx)
	if err != nil {
		return err
	}
	labelID, err := s.internLabelsTx(ctx, key.labelsHash, bucket.labelsJSON, tx)
	if err != nil {
		return err
	}
	resolutionID, err := s.internResolutionTx(ctx, interval, tx)
	if err != nil {
		return err
	}
	columns := "(series_id, resolution_id, label_id, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest, created_at_milli)"
	values := make([]string, 15)
	for i := range values {
		values[i] = s.dialect.placeholder(i + 1)
	}
	var suffix string
	switch s.cfg.Driver {
	case DriverMySQL:
		suffix = " ON DUPLICATE KEY UPDATE count=VALUES(count), sum=VALUES(sum), sum_sq=VALUES(sum_sq), min_val=VALUES(min_val), max_val=VALUES(max_val), first_val=VALUES(first_val), first_ts_milli=VALUES(first_ts_milli), last_val=VALUES(last_val), last_ts_milli=VALUES(last_ts_milli), digest=VALUES(digest), created_at_milli=VALUES(created_at_milli)"
	case DriverPostgreSQL:
		suffix = " ON CONFLICT(series_id, resolution_id, label_id, bucket_milli) DO UPDATE SET count=EXCLUDED.count, sum=EXCLUDED.sum, sum_sq=EXCLUDED.sum_sq, min_val=EXCLUDED.min_val, max_val=EXCLUDED.max_val, first_val=EXCLUDED.first_val, first_ts_milli=EXCLUDED.first_ts_milli, last_val=EXCLUDED.last_val, last_ts_milli=EXCLUDED.last_ts_milli, digest=EXCLUDED.digest, created_at_milli=EXCLUDED.created_at_milli"
	default:
		suffix = " ON CONFLICT(series_id, resolution_id, label_id, bucket_milli) DO UPDATE SET count=excluded.count, sum=excluded.sum, sum_sq=excluded.sum_sq, min_val=excluded.min_val, max_val=excluded.max_val, first_val=excluded.first_val, first_ts_milli=excluded.first_ts_milli, last_val=excluded.last_val, last_ts_milli=excluded.last_ts_milli, digest=excluded.digest, created_at_milli=excluded.created_at_milli"
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s %s VALUES (%s)%s", s.tables.rollups, columns, joinSQL(values), suffix),
		seriesID, resolutionID, labelID, key.bucket, bucket.count, bucket.sum, bucket.sumSq, bucket.min, bucket.max,
		bucket.firstVal, bucket.firstTS, bucket.lastVal, bucket.lastTS, bucket.encodedDigest(), timeMillis(time.Now()),
	)
	return err
}
