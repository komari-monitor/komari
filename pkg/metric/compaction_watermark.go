package metric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// compactionWatermark returns the last boundary for which a metric's raw data
// was successfully materialized into rollups and retained/deleted in the same
// transaction. A missing row is expected for stores created before watermark
// tracking was introduced or for metrics that have never been compacted.
func (s *Store) compactionWatermark(ctx context.Context, metricName string) (time.Time, bool, error) {
	var nano int64
	err := s.reader().QueryRowContext(ctx, fmt.Sprintf(
		`SELECT watermark_nano FROM %s WHERE metric_name = %s`,
		s.tables.watermarks, s.dialect.placeholder(1),
	), metricName).Scan(&nano)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if nano <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(0, nano).UTC(), true, nil
}

// persistCompactionWatermarkTx records a successful compaction boundary in the
// same transaction as rollup writes and raw retention deletion. It is
// monotonic so a delayed caller cannot move the read boundary backwards.
func (s *Store) persistCompactionWatermarkTx(ctx context.Context, metricName string, watermark time.Time, tx *sql.Tx) error {
	watermark = watermark.UTC()
	if watermark.IsZero() {
		return s.clearCompactionWatermarkTx(ctx, metricName, tx)
	}

	var previous int64
	err := tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT watermark_nano FROM %s WHERE metric_name = %s`,
		s.tables.watermarks, s.dialect.placeholder(1),
	), metricName).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && previous > watermark.UnixNano() {
		watermark = time.Unix(0, previous).UTC()
	}

	_, err = tx.ExecContext(ctx, s.dialect.upsertCompactionWatermarkSQL(s.tables),
		metricName, watermark.UnixNano(), time.Now().UTC().UnixNano(),
	)
	return err
}

func (s *Store) clearCompactionWatermarkTx(ctx context.Context, metricName string, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE metric_name = %s`,
		s.tables.watermarks, s.dialect.placeholder(1),
	), metricName)
	return err
}
