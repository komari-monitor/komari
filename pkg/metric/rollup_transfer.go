package metric

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
)

const defaultRollupTransferBatchSize = 500

// PersistedRollup is the database-independent representation used to move a
// materialized rollup between Stores. It preserves every aggregate needed by
// queries, including the encoded t-digest used for percentile calculations.
type PersistedRollup struct {
	MetricName string
	EntityID   string
	Tags       map[string]string
	Labels     map[string]string
	Resolution time.Duration
	Bucket     time.Time
	Count      int64
	Sum        float64
	SumSq      float64
	Min        float64
	Max        float64
	FirstValue float64
	FirstTime  time.Time
	LastValue  float64
	LastTime   time.Time
	Digest     []byte
	CreatedAt  time.Time
}

// ExportRollups streams all persisted rollups for one metric in deterministic
// batches. The callback must consume each batch before returning.
func (s *Store) ExportRollups(ctx context.Context, metricName string, batchSize int, consume func([]PersistedRollup) error) (int64, error) {
	if err := s.ensureOpen(); err != nil {
		return 0, err
	}
	if strings.TrimSpace(metricName) == "" {
		return 0, fmt.Errorf("%w: metric name is required", ErrInvalidArgument)
	}
	if consume == nil {
		return 0, fmt.Errorf("%w: rollup consumer is required", ErrInvalidArgument)
	}
	if batchSize <= 0 {
		batchSize = defaultRollupTransferBatchSize
	}

	rows, err := s.reader().QueryContext(ctx, fmt.Sprintf(`SELECT
		s.entity_id, s.tags, l.labels, d.resolution_milli, r.bucket_milli,
		r.count, r.sum, r.sum_sq, r.min_val, r.max_val,
		r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli,
		r.digest, r.created_at_milli
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id
		JOIN %s l ON l.id = r.label_id
		WHERE s.metric_name = %s
		ORDER BY r.series_id, r.resolution_id, r.label_id, r.bucket_milli`,
		s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels,
		s.dialect.placeholder(1)), metricName)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	batch := make([]PersistedRollup, 0, batchSize)
	var total int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := consume(batch); err != nil {
			return err
		}
		total += int64(len(batch))
		batch = batch[:0]
		return nil
	}
	for rows.Next() {
		var (
			row                                           PersistedRollup
			rawTags, rawLabels                            any
			resolutionMilli, bucketMilli                  int64
			firstTimeMilli, lastTimeMilli, createdAtMilli int64
		)
		if err := rows.Scan(
			&row.EntityID, &rawTags, &rawLabels, &resolutionMilli, &bucketMilli,
			&row.Count, &row.Sum, &row.SumSq, &row.Min, &row.Max,
			&row.FirstValue, &firstTimeMilli, &row.LastValue, &lastTimeMilli,
			&row.Digest, &createdAtMilli,
		); err != nil {
			return total, err
		}
		row.MetricName = metricName
		row.Resolution = time.Duration(resolutionMilli) * time.Millisecond
		row.Bucket = fromMillis(bucketMilli)
		row.FirstTime = fromMillis(firstTimeMilli)
		row.LastTime = fromMillis(lastTimeMilli)
		row.CreatedAt = fromMillis(createdAtMilli)
		row.Tags, err = decodeMap(rawTags)
		if err != nil {
			return total, err
		}
		row.Labels, err = decodeMap(rawLabels)
		if err != nil {
			return total, err
		}
		row.Digest = append([]byte(nil), row.Digest...)
		batch = append(batch, row)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return total, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	if err := flush(); err != nil {
		return total, err
	}
	return total, nil
}

// ImportRollups idempotently writes rollups exported by ExportRollups. It does
// not synthesize raw samples or rebuild tiers, so source counts and percentile
// sketches retain their original semantics.
func (s *Store) ImportRollups(ctx context.Context, rollups []PersistedRollup) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if len(rollups) == 0 {
		return nil
	}
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cache := newRollupDictionaryCache()
	rows := make([]normalizedRollupRow, 0, len(rollups))
	for i, rollup := range rollups {
		if err := validatePersistedRollup(rollup); err != nil {
			return fmt.Errorf("rollup %d: %w", i, err)
		}
		tagsHash, tagsJSON, err := tagsFingerprint(rollup.Tags)
		if err != nil {
			return err
		}
		labelsHash, labelsJSON, err := tagsFingerprint(rollup.Labels)
		if err != nil {
			return err
		}
		key := rollupKey{entityID: rollup.EntityID, tagsHash: tagsHash, labelsHash: labelsHash, bucket: rollup.Bucket.UTC().UnixMilli()}
		seriesID, err := cache.seriesID(ctx, s, tx, rollup.MetricName, key, tagsJSON)
		if err != nil {
			return err
		}
		labelID, err := cache.labelID(ctx, s, tx, labelsHash, labelsJSON)
		if err != nil {
			return err
		}
		resolutionID, err := cache.resolutionID(ctx, s, tx, rollup.Resolution)
		if err != nil {
			return err
		}
		digest := append([]byte(nil), rollup.Digest...)
		if rollup.Min == rollup.Max {
			digest = nil
		} else {
			d, err := DecodeTDigest(digest)
			if err != nil {
				return fmt.Errorf("rollup %d: decode digest: %w", i, err)
			}
			digest = encodeStoredTDigest(d)
		}
		rows = append(rows, normalizedRollupRow{
			seriesID: seriesID, resolutionID: resolutionID, labelID: labelID,
			bucketMilli: key.bucket, count: rollup.Count, sum: rollup.Sum, sumSq: rollup.SumSq,
			min: rollup.Min, max: rollup.Max, firstVal: rollup.FirstValue, firstTSMilli: rollup.FirstTime.UTC().UnixMilli(),
			lastVal: rollup.LastValue, lastTSMilli: rollup.LastTime.UTC().UnixMilli(), digest: digest,
			createdAtMilli: rollup.CreatedAt.UTC().UnixMilli(),
		})
	}
	if err := s.upsertNormalizedRollupRowsTx(ctx, rows, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func validatePersistedRollup(rollup PersistedRollup) error {
	if strings.TrimSpace(rollup.MetricName) == "" || strings.TrimSpace(rollup.EntityID) == "" {
		return fmt.Errorf("%w: metric name and entity id are required", ErrInvalidArgument)
	}
	if rollup.Resolution < time.Millisecond || rollup.Resolution%time.Millisecond != 0 {
		return fmt.Errorf("%w: rollup interval %s must use whole milliseconds", ErrInvalidArgument, rollup.Resolution)
	}
	if rollup.Count <= 0 || !finiteRollupValues(rollup) || rollup.Min > rollup.Max {
		return fmt.Errorf("%w: invalid rollup aggregate", ErrInvalidArgument)
	}
	if rollup.Bucket.IsZero() || rollup.FirstTime.IsZero() || rollup.LastTime.IsZero() || rollup.CreatedAt.IsZero() {
		return fmt.Errorf("%w: rollup timestamps are required", ErrInvalidArgument)
	}
	if rollup.FirstTime.After(rollup.LastTime) {
		return fmt.Errorf("%w: first timestamp must not follow last timestamp", ErrInvalidArgument)
	}
	if rollup.Min != rollup.Max {
		if len(rollup.Digest) == 0 {
			return fmt.Errorf("%w: non-constant rollup digest is required", ErrInvalidArgument)
		}
		if _, err := DecodeTDigest(rollup.Digest); err != nil {
			return fmt.Errorf("decode rollup digest: %w", err)
		}
	}
	return nil
}

func finiteRollupValues(rollup PersistedRollup) bool {
	for _, value := range [...]float64{
		rollup.Sum, rollup.SumSq, rollup.Min, rollup.Max, rollup.FirstValue, rollup.LastValue,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
