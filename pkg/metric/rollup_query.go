package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AggregateRollup reduces one materialized tier into the requested output
// interval. The hot minute is folded in separately so recent charts never wait
// for another report before their final real bucket becomes visible.
func (s *Store) AggregateRollup(ctx context.Context, query AggregateQuery, resolution time.Duration) ([]AggregatePoint, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if resolution <= 0 {
		return nil, fmt.Errorf("%w: rollup resolution must be positive", ErrInvalidArgument)
	}
	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()
	q := query.Query.normalized()
	needDigest := isPercentile(query.Aggregation)
	rows, err := s.scanRollupRowsBetween(ctx, q.MetricName, q.EntityID, q.Tags, resolution.Milliseconds(), bucketStartMillis(q.Start.UnixMilli(), resolution.Milliseconds()), q.End.UnixMilli(), needDigest)
	if err != nil {
		return nil, err
	}
	hot, err := s.hotRollupRows(q.MetricName, q.EntityID, q.Tags, q.Start, q.End, needDigest)
	if err != nil {
		return nil, err
	}
	rows = append(rows, hot...)
	if query.Aggregation == AggRate {
		points := representativePoints(q.MetricName, rows)
		return pageBuckets(mustAggregatePoints(points, query), query.BucketLimit, query.BucketOffset), nil
	}
	groups := foldRollupRows(nil, rows, query.Interval, s.cfg.RollupPolicy.compression(), query.PreserveSeries, false, needDigest)
	result, err := rollupGroupsToPoints(groups, query)
	if err != nil {
		return nil, err
	}
	return pageBuckets(result, query.BucketLimit, query.BucketOffset), nil
}

func mustAggregatePoints(points []Point, query AggregateQuery) []AggregatePoint {
	result, err := AggregatePoints(points, query)
	if err != nil {
		return []AggregatePoint{}
	}
	return result
}

func representativePoints(metricName string, rows []storedRollup) []Point {
	points := make([]Point, 0, len(rows))
	for _, row := range rows {
		tags, err := rollupTagsFromJSON(row.bucketData.tagsJSON)
		if err != nil {
			continue
		}
		labels, err := rollupTagsFromJSON(row.bucketData.labelsJSON)
		if err != nil {
			continue
		}
		points = append(points, Point{MetricName: metricName, EntityID: row.entityID, Timestamp: fromMillis(row.bucketData.lastTS), Value: row.bucketData.lastVal, Tags: tags, Labels: labels})
	}
	return points
}

func foldRollupRows(groups map[rollupKey]*rollupBucket, rows []storedRollup, interval time.Duration, compression float64, preserveSeries, preserveLabels, needDigest bool) map[rollupKey]*rollupBucket {
	if groups == nil {
		groups = make(map[rollupKey]*rollupBucket)
	}
	for _, row := range rows {
		key := rollupKey{bucket: bucketStartMillis(row.bucket, interval.Milliseconds())}
		if preserveSeries {
			key.entityID, key.tagsHash = row.entityID, row.bucketData.tagsHash
			if preserveLabels {
				key.labelsHash = row.bucketData.labelsHash
			}
		}
		bucket := groups[key]
		if bucket == nil {
			bucket = newRollupBucketWithDigest(compression, needDigest)
			bucket.tagsHash, bucket.tagsJSON = row.bucketData.tagsHash, row.bucketData.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = row.bucketData.labelsHash, row.bucketData.labelsJSON
			groups[key] = bucket
		}
		bucket.mergeStored(row.bucketData)
	}
	return groups
}

func rollupGroupsToPoints(groups map[rollupKey]*rollupBucket, query AggregateQuery) ([]AggregatePoint, error) {
	keys := make([]rollupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sortRollupKeys(keys)
	result := make([]AggregatePoint, 0, len(keys))
	for _, key := range keys {
		bucket := groups[key]
		value, ok := bucket.value(query.Aggregation)
		if !ok {
			return nil, fmt.Errorf("%w: aggregation %q requires raw samples", ErrInvalidArgument, query.Aggregation)
		}
		tags, err := rollupTagsFromJSON(bucket.tagsJSON)
		if err != nil {
			return nil, err
		}
		if !query.PreserveSeries {
			tags = map[string]string{}
		}
		result = append(result, AggregatePoint{MetricName: query.MetricName, EntityID: key.entityID, Bucket: fromMillis(key.bucket), Value: value, Count: int(bucket.count), Tags: tags})
	}
	return result, nil
}

func (s *Store) scanRollupRowsBetween(ctx context.Context, metricName, entityID string, tags map[string]string, resolutionMilli, lowerBucket, upperBucket int64, needDigest bool) ([]storedRollup, error) {
	return s.scanRollupRowsBetweenWith(ctx, s.reader(), metricName, entityID, tags, time.Duration(resolutionMilli)*time.Millisecond, lowerBucket, upperBucket, needDigest)
}

func (s *Store) scanRollupRowsBetweenWith(ctx context.Context, q querier, metricName, entityID string, tags map[string]string, resolution time.Duration, lowerBucket, upperBucket int64, needDigest bool) ([]storedRollup, error) {
	args := []any{metricName, resolution.Milliseconds(), lowerBucket, upperBucket}
	parts := []string{
		"s.metric_name = " + s.dialect.placeholder(1),
		"d.resolution_milli = " + s.dialect.placeholder(2),
		"r.bucket_milli >= " + s.dialect.placeholder(3),
		"r.bucket_milli <= " + s.dialect.placeholder(4),
	}
	if entityID != "" {
		args = append(args, entityID)
		parts = append(parts, "s.entity_id = "+s.dialect.placeholder(len(args)))
	}
	for _, key := range sortedKeys(tags) {
		args = append(args, tags[key])
		parts = append(parts, s.dialect.jsonExtractEquals("s.tags", key, s.dialect.placeholder(len(args))))
	}
	columns := "s.entity_id, s.tags_hash, s.tags, l.labels_hash, l.labels, r.bucket_milli, r.count, r.sum, r.sum_sq, r.min_val, r.max_val, r.first_val, r.first_ts_milli, r.last_val, r.last_ts_milli"
	if needDigest {
		columns += ", r.digest"
	}
	sqlText := fmt.Sprintf("SELECT %s FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s d ON d.id = r.resolution_id JOIN %s l ON l.id = r.label_id WHERE %s ORDER BY r.bucket_milli ASC", columns, s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels, joinSQLWith(parts, " AND "))
	rows, err := q.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStoredRollups(rows, needDigest, s.cfg.RollupPolicy.compression())
}

func joinSQLWith(parts []string, separator string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, part := range parts[1:] {
		out += separator + part
	}
	return out
}

func scanStoredRollups(rows *sql.Rows, needDigest bool, compression float64) ([]storedRollup, error) {
	result := make([]storedRollup, 0)
	for rows.Next() {
		var entityID, tagsHash, labelsHash string
		var rawTags, rawLabels any
		var bucket, count, firstTS, lastTS int64
		var sum, sumSq, min, max, firstVal, lastVal float64
		var digest []byte
		args := []any{&entityID, &tagsHash, &rawTags, &labelsHash, &rawLabels, &bucket, &count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS}
		if needDigest {
			args = append(args, &digest)
		}
		if err := rows.Scan(args...); err != nil {
			return nil, err
		}
		tagsJSON, err := rawJSONToString(rawTags)
		if err != nil {
			return nil, err
		}
		labelsJSON, err := rawJSONToString(rawLabels)
		if err != nil {
			return nil, err
		}
		var d *TDigest
		if needDigest {
			d, err = digestFromRollup(count, min, max, digest, compression)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, storedRollup{entityID: entityID, bucket: bucket, bucketData: &rollupBucket{count: count, sum: sum, sumSq: sumSq, min: min, max: max, firstVal: firstVal, firstTS: firstTS, lastVal: lastVal, lastTS: lastTS, digest: d, tagsHash: tagsHash, tagsJSON: tagsJSON, labelsHash: labelsHash, labelsJSON: labelsJSON}})
	}
	return result, rows.Err()
}

func rawJSONToString(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "{}", nil
	case string:
		if v == "" {
			return "{}", nil
		}
		return v, nil
	case []byte:
		if len(v) == 0 {
			return "{}", nil
		}
		return string(v), nil
	default:
		return "", fmt.Errorf("unsupported JSON column type %T", value)
	}
}

func rollupTagsFromJSON(raw string) (map[string]string, error) {
	values, err := decodeMapString(raw)
	if err != nil {
		return nil, err
	}
	return cloneStringMap(values), nil
}

func (s *Store) CompatibleSeriesInterval(start, now time.Time, interval time.Duration) time.Duration {
	if interval <= 0 || !s.cfg.RollupPolicy.Enabled() {
		return interval
	}
	policy := s.cfg.RollupPolicy
	if interval < policy.Tiers[0].Interval {
		interval = policy.Tiers[0].Interval
	}
	backing := policy.Tiers[len(policy.Tiers)-1]
	for _, tier := range policy.Tiers {
		if !now.UTC().Add(-tier.Retention).After(start.UTC()) {
			backing = tier
			break
		}
	}
	if interval <= backing.Interval {
		return backing.Interval
	}
	if remainder := interval % backing.Interval; remainder != 0 {
		interval += backing.Interval - remainder
	}
	return interval
}

func bestRollupTier(policy RollupPolicy, interval time.Duration, start, now time.Time) *RollupTier {
	for i := range policy.Tiers {
		tier := &policy.Tiers[i]
		if interval >= tier.Interval && interval%tier.Interval == 0 && !now.Add(-tier.Retention).After(start) {
			return tier
		}
	}
	return nil
}

func (s *Store) Series(ctx context.Context, query AggregateQuery, now time.Time) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	preferred := s.cfg.RollupPolicy.Tiers[0].Interval
	if tier := bestRollupTier(s.cfg.RollupPolicy, query.Interval, query.Start.UTC(), now.UTC()); tier != nil {
		preferred = tier.Interval
	} else {
		for i := len(s.cfg.RollupPolicy.Tiers) - 1; i >= 0; i-- {
			tier := s.cfg.RollupPolicy.Tiers[i]
			if query.Interval >= tier.Interval && query.Interval%tier.Interval == 0 {
				preferred = tier.Interval
				break
			}
		}
	}
	resolution, err := s.coveringSeriesResolution(ctx, query.Query.normalized(), preferred)
	if err != nil {
		return nil, err
	}
	return s.AggregateRollup(ctx, query, resolution)
}

// coveringSeriesResolution falls back to a coarser retained tier only when
// the preferred tier starts after the requested range. This happens directly
// after a legacy migration: historical 5m/hour rollups predate the newly
// written 1m tier. Returning the coarser summary preserves the entire query
// rather than presenting a false gap at that boundary.
func (s *Store) coveringSeriesResolution(ctx context.Context, query Query, preferred time.Duration) (time.Duration, error) {
	policy := s.cfg.RollupPolicy
	selected := preferred
	earliest, latest, found, err := s.rollupTimeBounds(ctx, query, preferred)
	if err != nil {
		return 0, err
	}
	startMilli := query.Start.UTC().UnixMilli()
	endMilli := query.End.UTC().UnixMilli()
	if found && earliest <= startMilli && latest >= startMilli {
		return selected, nil
	}
	selectedFound := found && earliest <= endMilli && latest >= startMilli
	for _, tier := range policy.Tiers {
		if tier.Interval <= preferred {
			continue
		}
		candidateEarliest, candidateLatest, candidateFound, err := s.rollupTimeBounds(ctx, query, tier.Interval)
		if err != nil {
			return 0, err
		}
		if !candidateFound || candidateEarliest > endMilli || candidateLatest < startMilli {
			continue
		}
		if candidateEarliest <= startMilli && candidateLatest >= startMilli {
			return tier.Interval, nil
		}
		if !selectedFound || candidateEarliest < earliest {
			selected, earliest, selectedFound = tier.Interval, candidateEarliest, true
		}
	}
	return selected, nil
}

func (s *Store) rollupTimeBounds(ctx context.Context, query Query, interval time.Duration) (int64, int64, bool, error) {
	args := []any{query.MetricName}
	seriesParts := []string{"s.metric_name = " + s.dialect.placeholder(1)}
	if query.EntityID != "" {
		args = append(args, query.EntityID)
		seriesParts = append(seriesParts, "s.entity_id = "+s.dialect.placeholder(len(args)))
	}
	for _, key := range sortedKeys(query.Tags) {
		args = append(args, query.Tags[key])
		seriesParts = append(seriesParts, s.dialect.jsonExtractEquals("s.tags", key, s.dialect.placeholder(len(args))))
	}
	args = append(args, interval.Milliseconds())
	resolutionPlaceholder := s.dialect.placeholder(len(args))
	args = append(args, query.End.UTC().UnixMilli())
	endPlaceholder := s.dialect.placeholder(len(args))

	var earliest, latest sql.NullInt64
	err := s.reader().QueryRowContext(ctx, fmt.Sprintf(`SELECT MIN(r.first_ts_milli), MAX(r.last_ts_milli) FROM %s r
		WHERE r.series_id IN (SELECT s.id FROM %s s WHERE %s)
		AND r.resolution_id IN (SELECT d.id FROM %s d WHERE d.resolution_milli = %s)
		AND r.first_ts_milli <= %s`, s.tables.rollups, s.tables.series, joinSQLWith(seriesParts, " AND "),
		s.tables.resolutions, resolutionPlaceholder, endPlaceholder),
		args...).Scan(&earliest, &latest)
	if err != nil {
		return 0, 0, false, err
	}
	return earliest.Int64, latest.Int64, earliest.Valid && latest.Valid, nil
}
