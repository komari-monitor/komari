package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// DashboardBucketInterval is the fixed rollup interval mirrored into
// dashboard_buckets. It matches the backing tier used for 24-hour dashboard
// charts (5-minute buckets).
const DashboardBucketInterval = 5 * time.Minute

// DashboardRetention covers the admin dashboard window plus one bucket slack.
const DashboardRetention = 25 * time.Hour

const dashboardEntityIDChunkSize = 128

const dashboardBucketColumns = "(metric_name, entity_id, tags_hash, tags, labels_hash, labels, bucket_milli, count, sum, sum_sq, min_val, max_val, first_val, first_ts_milli, last_val, last_ts_milli, digest)"

var dashboardMetricNames = map[string]struct{}{
	"cpu.usage":           {},
	"gpu.usage":           {},
	"gpu.device.usage":    {},
	"gpu.memory.used":     {},
	"gpu.memory.total":    {},
	"gpu.temperature":     {},
	"memory.used":         {},
	"swap.used":           {},
	"load.average":        {},
	"disk.used":           {},
	"net.in.rate":         {},
	"net.out.rate":        {},
	"net.total.up":        {},
	"net.total.down":      {},
	"traffic.up":          {},
	"traffic.down":        {},
	"process.count":       {},
	"connections.tcp":     {},
	"connections.udp":     {},
	"ping.latency_ms":     {},
	"ping.loss":           {},
}

// IsDashboardMetric reports whether name is mirrored into dashboard_buckets.
func IsDashboardMetric(metricName string) bool {
	_, ok := dashboardMetricNames[metricName]
	return ok
}

func DashboardMetricBucketIndexName(tablePrefix string) string {
	return tablePrefix + "dashboard_metric_bucket_idx"
}

func (s *Store) dashboardUpsertSuffix() string {
	switch s.cfg.Driver {
	case DriverMySQL:
		return " ON DUPLICATE KEY UPDATE count=VALUES(count), sum=VALUES(sum), sum_sq=VALUES(sum_sq), min_val=VALUES(min_val), max_val=VALUES(max_val), first_val=VALUES(first_val), first_ts_milli=VALUES(first_ts_milli), last_val=VALUES(last_val), last_ts_milli=VALUES(last_ts_milli), digest=VALUES(digest)"
	case DriverPostgreSQL:
		return " ON CONFLICT(metric_name, entity_id, tags_hash, labels_hash, bucket_milli) DO UPDATE SET count=EXCLUDED.count, sum=EXCLUDED.sum, sum_sq=EXCLUDED.sum_sq, min_val=EXCLUDED.min_val, max_val=EXCLUDED.max_val, first_val=EXCLUDED.first_val, first_ts_milli=EXCLUDED.first_ts_milli, last_val=EXCLUDED.last_val, last_ts_milli=EXCLUDED.last_ts_milli, digest=EXCLUDED.digest, tags=EXCLUDED.tags, labels=EXCLUDED.labels"
	default:
		return " ON CONFLICT(metric_name, entity_id, tags_hash, labels_hash, bucket_milli) DO UPDATE SET count=excluded.count, sum=excluded.sum, sum_sq=excluded.sum_sq, min_val=excluded.min_val, max_val=excluded.max_val, first_val=excluded.first_val, first_ts_milli=excluded.first_ts_milli, last_val=excluded.last_val, last_ts_milli=excluded.last_ts_milli, digest=excluded.digest, tags=excluded.tags, labels=excluded.labels"
	}
}

func (s *Store) upsertDashboardBucketTx(ctx context.Context, metricName string, key rollupKey, bucket *rollupBucket, tx *sql.Tx) error {
	if bucket == nil || bucket.count == 0 {
		return s.deleteDashboardBucketTx(ctx, metricName, key, tx)
	}
	tagsJSON := bucket.tagsJSON
	if tagsJSON == "" {
		tagsJSON = "{}"
	}
	labelsJSON := bucket.labelsJSON
	if labelsJSON == "" {
		labelsJSON = "{}"
	}
	digest := bucket.encodedDigest()
	query := fmt.Sprintf(`INSERT INTO %s %s VALUES (%s) %s`,
		s.tables.dashboard,
		dashboardBucketColumns,
		joinSQL([]string{
			s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.jsonPlaceholder(4),
			s.dialect.placeholder(5), s.dialect.jsonPlaceholder(6), s.dialect.placeholder(7), s.dialect.placeholder(8),
			s.dialect.placeholder(9), s.dialect.placeholder(10), s.dialect.placeholder(11), s.dialect.placeholder(12),
			s.dialect.placeholder(13), s.dialect.placeholder(14), s.dialect.placeholder(15), s.dialect.placeholder(16),
			s.dialect.placeholder(17),
		}),
		s.dashboardUpsertSuffix(),
	)
	_, err := tx.ExecContext(ctx, query,
		metricName, key.entityID, key.tagsHash, tagsJSON, key.labelsHash, labelsJSON, key.bucket,
		bucket.count, bucket.sum, bucket.sumSq, bucket.min, bucket.max,
		bucket.firstVal, bucket.firstTS, bucket.lastVal, bucket.lastTS, digest,
	)
	// Live upserts must not mark the mirror ready: a single recent row would
	// otherwise make SeriesBatch prefer dashboard_buckets while older hours
	// inside the query window are still missing.
	return err
}

func (s *Store) deleteDashboardBucketTx(ctx context.Context, metricName string, key rollupKey, tx *sql.Tx) error {
	query := fmt.Sprintf(`DELETE FROM %s WHERE metric_name = %s AND entity_id = %s AND tags_hash = %s AND labels_hash = %s AND bucket_milli = %s`,
		s.tables.dashboard,
		s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.placeholder(4), s.dialect.placeholder(5),
	)
	_, err := tx.ExecContext(ctx, query, metricName, key.entityID, key.tagsHash, key.labelsHash, key.bucket)
	return err
}

func (s *Store) cleanupDashboardBucketsBeforeTx(ctx context.Context, beforeMilli int64, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE bucket_milli < %s", s.tables.dashboard, s.dialect.placeholder(1)),
		beforeMilli,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) mirrorRollupToDashboardTx(ctx context.Context, metricName string, interval time.Duration, key rollupKey, bucket *rollupBucket, tx *sql.Tx) error {
	if interval != DashboardBucketInterval || !IsDashboardMetric(metricName) {
		return nil
	}
	return s.upsertDashboardBucketTx(ctx, metricName, key, bucket, tx)
}

func (s *Store) dashboardGroupEligible(group *batchSeriesGroup, queryStart time.Time) bool {
	if !s.DashboardBucketsReady() {
		return false
	}
	if group.key.resolution != DashboardBucketInterval {
		return false
	}
	// Use the reconciled coverage watermark, not the retention constant. A
	// mirror that only holds the last N hours must not serve a longer window
	// or the leading portion would silently disappear.
	coveredFrom := s.dashboardCoveredFromMilli.Load()
	if coveredFrom <= 0 || queryStart.UTC().Before(time.UnixMilli(coveredFrom).UTC()) {
		return false
	}
	for metricName := range group.metricNames {
		if !IsDashboardMetric(metricName) {
			return false
		}
	}
	return len(group.metricNames) > 0
}

func (s *Store) markDashboardCoveredFrom(fromMilli int64) {
	if fromMilli <= 0 {
		s.dashboardReady.Store(false)
		s.dashboardCoveredFromMilli.Store(0)
		return
	}
	s.dashboardCoveredFromMilli.Store(fromMilli)
	s.dashboardReady.Store(true)
}

func (s *Store) clearDashboardCoverage() {
	s.dashboardReady.Store(false)
	s.dashboardCoveredFromMilli.Store(0)
}

// advanceDashboardCoverage raises the guaranteed coverage start after older
// mirror rows were intentionally deleted by retention cleanup.
func (s *Store) advanceDashboardCoverage(fromMilli int64) {
	if fromMilli <= 0 || !s.dashboardReady.Load() {
		return
	}
	for {
		cur := s.dashboardCoveredFromMilli.Load()
		if cur >= fromMilli {
			return
		}
		if s.dashboardCoveredFromMilli.CompareAndSwap(cur, fromMilli) {
			return
		}
	}
}

func dashboardRetentionCutoffMilli(now time.Time) int64 {
	return bucketStartMillis(now.UTC().Add(-DashboardRetention).UnixMilli(), DashboardBucketInterval.Milliseconds())
}

func (s *Store) scanDashboardRollupGroup(ctx context.Context, query BatchSeriesQuery, group *batchSeriesGroup, seriesByIdentity map[seriesIdentity]*seriesReadMeta, accumulators map[string]*metricSeriesAccumulator) (int, error) {
	metricNames := make([]string, 0, len(group.metricNames))
	for metricName := range group.metricNames {
		metricNames = append(metricNames, metricName)
	}
	sort.Strings(metricNames)

	entityIDs := append([]string(nil), query.EntityIDs...)
	sort.Strings(entityIDs)

	startMilli := bucketStartMillis(query.Start.UnixMilli(), DashboardBucketInterval.Milliseconds())
	endMilli := query.End.UnixMilli()
	indexName := DashboardMetricBucketIndexName(s.cfg.TablePrefix)

	totalRows := 0
	for start := 0; start < len(entityIDs); start += dashboardEntityIDChunkSize {
		end := start + dashboardEntityIDChunkSize
		if end > len(entityIDs) {
			end = len(entityIDs)
		}
		chunk := entityIDs[start:end]
		if len(chunk) == 0 {
			chunk = nil
		}
		rendered := s.dialect.renderDashboardRead(s.tables, indexName, dashboardReadPlan{
			MetricNames: metricNames,
			EntityIDs:   chunk,
			Tags:        query.Tags,
			StartMilli:  startMilli,
			EndMilli:    endMilli,
			Fields:      group.fields,
		})
		rows, err := s.scanDashboardRows(ctx, rendered, query.Tags, seriesByIdentity, group, accumulators)
		totalRows += rows
		if err != nil {
			return totalRows, err
		}
	}
	if len(entityIDs) == 0 {
		rendered := s.dialect.renderDashboardRead(s.tables, indexName, dashboardReadPlan{
			MetricNames: metricNames,
			Tags:        query.Tags,
			StartMilli:  startMilli,
			EndMilli:    endMilli,
			Fields:      group.fields,
		})
		return s.scanDashboardRows(ctx, rendered, query.Tags, seriesByIdentity, group, accumulators)
	}
	return totalRows, nil
}

func (s *Store) scanDashboardRows(ctx context.Context, rendered renderedSQL, tagFilter map[string]string, seriesByIdentity map[seriesIdentity]*seriesReadMeta, group *batchSeriesGroup, accumulators map[string]*metricSeriesAccumulator) (int, error) {
	rows, err := s.reader().QueryContext(ctx, rendered.Query, rendered.Args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var metricName, entityID, tagsHash, tagsJSON, labelsHash, labelsJSON string
	var bucket, count, firstTS, lastTS int64
	var sum, sumSq, min, max, firstVal, lastVal float64
	var digestBlob []byte
	destinations := []any{&metricName, &entityID, &tagsHash, &tagsJSON, &labelsHash, &labelsJSON, &bucket, &count}
	if group.fields&rollupReadSum != 0 {
		destinations = append(destinations, &sum)
	}
	if group.fields&rollupReadSumSq != 0 {
		destinations = append(destinations, &sumSq)
	}
	if group.fields&rollupReadMin != 0 {
		destinations = append(destinations, &min)
	}
	if group.fields&rollupReadMax != 0 {
		destinations = append(destinations, &max)
	}
	if group.fields&rollupReadFirst != 0 {
		destinations = append(destinations, &firstVal, &firstTS)
	}
	if group.fields&rollupReadLast != 0 {
		destinations = append(destinations, &lastVal, &lastTS)
	}
	if group.fields&rollupReadDigest != 0 {
		destinations = append(destinations, &digestBlob)
	}

	rowCount := 0
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			return rowCount, err
		}
		if _, ok := group.metricNames[metricName]; !ok {
			continue
		}
		meta, matched, err := memorySeriesMeta(seriesByIdentity, metricName, entityID, tagsHash, tagsJSON, tagFilter)
		if err != nil {
			return rowCount, err
		}
		if !matched {
			continue
		}
		accumulator := accumulators[metricName]
		state := accumulator.consume(meta, bucket, count, sum, sumSq, min, max, firstVal, firstTS, lastVal, lastTS, nil)
		if group.fields&rollupReadDigest != 0 && accumulator.needDigest {
			if err := mergeEncodedRollupDigest(state.summary.digest, count, min, max, digestBlob); err != nil {
				return rowCount, err
			}
		}
		rowCount++
	}
	return rowCount, rows.Err()
}

// ReconcileDashboardBuckets aligns dashboard_buckets with 5-minute rollups for
// the retention window and publishes the coverage watermark. Callers must not
// run this on the Open critical path: a full-window upsert can take longer than
// the metric-store connect timeout and would prevent the HTTP server from
// binding. Until it finishes, DashboardBucketsReady stays false and SeriesBatch
// reads rollups instead of the mirror.
func (s *Store) ReconcileDashboardBuckets(ctx context.Context) error {
	return s.ensureDashboardBuckets(ctx)
}

func (s *Store) ensureDashboardBuckets(ctx context.Context) error {
	names := make([]string, 0, len(dashboardMetricNames))
	for name := range dashboardMetricNames {
		names = append(names, name)
	}
	sort.Strings(names)
	cutoff := dashboardRetentionCutoffMilli(time.Now())

	var minBucket, maxBucket sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT MIN(bucket_milli), MAX(bucket_milli) FROM %s", s.tables.dashboard),
	).Scan(&minBucket, &maxBucket); err != nil {
		s.clearDashboardCoverage()
		return err
	}

	// Fresh enough means the newest mirror bucket is within two 5m intervals of
	// now (plus coarse seal grace). Anything older is a stalled mirror that
	// still has an old MIN, so "MIN <= cutoff" alone would skip repair.
	freshBefore := time.Now().UTC().Add(-(2*DashboardBucketInterval + coarseRollupGrace)).UnixMilli()
	needsFullCopy := !minBucket.Valid || minBucket.Int64 > cutoff ||
		!maxBucket.Valid || maxBucket.Int64 < freshBefore

	// Empty, short, or stalled mirrors need a full retention copy. A healthy
	// mirror only backfills metrics that have rollups but zero mirror rows
	// (e.g. renamed ping.latency_ms) so ordinary restarts stay cheap.
	if needsFullCopy {
		if err := s.backfillDashboardMetrics(ctx, names, cutoff); err != nil {
			s.clearDashboardCoverage()
			return err
		}
	} else {
		missing, err := s.dashboardMetricsMissingDespiteRollups(ctx)
		if err != nil {
			s.clearDashboardCoverage()
			return err
		}
		if err := s.backfillDashboardMetrics(ctx, missing, cutoff); err != nil {
			s.clearDashboardCoverage()
			return err
		}
	}
	s.markDashboardCoveredFrom(cutoff)
	return nil
}

func (s *Store) dashboardMetricsMissingDespiteRollups(ctx context.Context) ([]string, error) {
	names := make([]string, 0, len(dashboardMetricNames))
	for name := range dashboardMetricNames {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, 0, len(names)+1)
	for i, name := range names {
		args = append(args, name)
		placeholders[i] = s.dialect.placeholder(len(args))
	}
	args = append(args, DashboardBucketInterval.Milliseconds())
	resolutionPlaceholder := s.dialect.placeholder(len(args))
	query := fmt.Sprintf(`SELECT DISTINCT s.metric_name
		FROM %s s
		JOIN %s r ON r.series_id = s.id
		JOIN %s d ON d.id = r.resolution_id AND d.resolution_milli = %s
		WHERE s.metric_name IN (%s)
			AND NOT EXISTS (
				SELECT 1 FROM %s db WHERE db.metric_name = s.metric_name
			)
		ORDER BY s.metric_name`,
		s.tables.series, s.tables.rollups, s.tables.resolutions, resolutionPlaceholder,
		joinSQL(placeholders), s.tables.dashboard,
	)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	missing := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		missing = append(missing, name)
	}
	return missing, rows.Err()
}

func (s *Store) backfillDashboardMetrics(ctx context.Context, names []string, cutoffMilli int64) error {
	if len(names) == 0 {
		return nil
	}
	placeholders := make([]string, len(names))
	args := make([]any, 0, len(names)+2)
	for i, name := range names {
		args = append(args, name)
		placeholders[i] = s.dialect.placeholder(len(args))
	}
	args = append(args, DashboardBucketInterval.Milliseconds(), cutoffMilli)
	resolutionPlaceholder := s.dialect.placeholder(len(args) - 1)
	cutoffPlaceholder := s.dialect.placeholder(len(args))
	query := fmt.Sprintf(`INSERT INTO %s %s
		SELECT s.metric_name, s.entity_id, s.tags_hash, s.tags, l.labels_hash, l.labels, r.bucket_milli,
			r.count, r.sum, r.sum_sq, r.min_val, r.max_val, r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli, r.digest
		FROM %s r
		JOIN %s s ON s.id = r.series_id
		JOIN %s d ON d.id = r.resolution_id AND d.resolution_milli = %s
		JOIN %s l ON l.id = r.label_id
		WHERE s.metric_name IN (%s) AND r.bucket_milli >= %s %s`,
		s.tables.dashboard, dashboardBucketColumns,
		s.tables.rollups, s.tables.series, s.tables.resolutions, resolutionPlaceholder,
		s.tables.labels, joinSQL(placeholders), cutoffPlaceholder, s.dashboardUpsertSuffix(),
	)
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

func (s *Store) deleteDashboardForMetricTx(ctx context.Context, metricName string, tx *sql.Tx) error {
	if !IsDashboardMetric(metricName) {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE metric_name = %s", s.tables.dashboard, s.dialect.placeholder(1)),
		metricName,
	)
	return err
}

func (s *Store) deleteDashboardForMetricsTx(ctx context.Context, names []string, tx *sql.Tx) error {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if IsDashboardMetric(name) {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	placeholders := make([]string, len(filtered))
	args := make([]any, len(filtered))
	for i, name := range filtered {
		args[i] = name
		placeholders[i] = s.dialect.placeholder(i + 1)
	}
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE metric_name IN (%s)", s.tables.dashboard, joinSQL(placeholders)),
		args...,
	)
	return err
}

func (s *Store) deleteDashboardBeforeTx(ctx context.Context, metricName string, beforeMilli int64, tx *sql.Tx) error {
	if !IsDashboardMetric(metricName) {
		return nil
	}
	beforeMilli = bucketStartMillis(beforeMilli, DashboardBucketInterval.Milliseconds())
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE metric_name = %s AND bucket_milli < %s", s.tables.dashboard, s.dialect.placeholder(1), s.dialect.placeholder(2)),
		metricName, beforeMilli,
	)
	return err
}

func (s *Store) deleteDashboardGroupTx(ctx context.Context, names []string, beforeMilli int64, all bool, tx *sql.Tx) (int64, error) {
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if IsDashboardMetric(name) {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(filtered)+1)
	placeholders := make([]string, len(filtered))
	for i, name := range filtered {
		args = append(args, name)
		placeholders[i] = s.dialect.placeholder(len(args))
	}
	where := "metric_name IN (" + joinSQL(placeholders) + ")"
	if !all {
		beforeMilli = bucketStartMillis(beforeMilli, DashboardBucketInterval.Milliseconds())
		args = append(args, beforeMilli)
		where += " AND bucket_milli < " + s.dialect.placeholder(len(args))
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s", s.tables.dashboard, where), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) deleteDashboardForEntityTx(ctx context.Context, entityID string, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE entity_id = %s", s.tables.dashboard, s.dialect.placeholder(1)),
		entityID,
	)
	return err
}
