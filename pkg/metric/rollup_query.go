package metric

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AggregateRollup reduces one explicitly selected materialized tier into the
// requested output interval.
func (s *Store) AggregateRollup(ctx context.Context, query AggregateQuery, resolution time.Duration) ([]AggregatePoint, error) {
	return s.aggregateRollupAt(ctx, query, resolution, time.Now().UTC())
}

func (s *Store) aggregateRollupAt(ctx context.Context, query AggregateQuery, resolution time.Duration, now time.Time) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if resolution <= 0 {
		return nil, fmt.Errorf("%w: rollup resolution must be positive", ErrInvalidArgument)
	}
	entityIDs := []string(nil)
	if query.EntityID != "" {
		entityIDs = []string{query.EntityID}
	}
	batch := BatchSeriesQuery{
		Specs: []BatchSeriesSpec{{
			MetricName:     query.MetricName,
			Aggregations:   []Aggregation{query.Aggregation},
			Interval:       query.Interval,
			PreserveSeries: query.PreserveSeries,
		}},
		EntityIDs: entityIDs,
		Start:     query.Start,
		End:       query.End,
		Tags:      query.Tags,
		Order:     query.Order,
	}
	if err := batch.Validate(); err != nil {
		return nil, err
	}
	result, err := s.seriesBatchAt(ctx, batch.normalized(), now.UTC(), map[string]time.Duration{query.MetricName: resolution})
	if err != nil {
		return nil, err
	}
	return pageBuckets(result.Values[query.MetricName][query.Aggregation], query.BucketLimit, query.BucketOffset), nil
}

// Series selects one deterministic backing tier and delegates to SeriesBatch.
func (s *Store) Series(ctx context.Context, query AggregateQuery, now time.Time) ([]AggregatePoint, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	entityIDs := []string(nil)
	if query.EntityID != "" {
		entityIDs = []string{query.EntityID}
	}
	result, err := s.SeriesBatch(ctx, BatchSeriesQuery{
		Specs: []BatchSeriesSpec{{
			MetricName:     query.MetricName,
			Aggregations:   []Aggregation{query.Aggregation},
			Interval:       query.Interval,
			PreserveSeries: query.PreserveSeries,
		}},
		EntityIDs: entityIDs,
		Start:     query.Start,
		End:       query.End,
		Tags:      query.Tags,
		Order:     query.Order,
	}, now)
	if err != nil {
		return nil, err
	}
	return pageBuckets(result.Values[query.MetricName][query.Aggregation], query.BucketLimit, query.BucketOffset), nil
}

// SeriesAggregates derives multiple aggregations from the same streamed scan.
func (s *Store) SeriesAggregates(ctx context.Context, query Query, aggregations []Aggregation, interval time.Duration, preserveSeries bool, now time.Time) (map[Aggregation][]AggregatePoint, error) {
	entityIDs := []string(nil)
	if query.EntityID != "" {
		entityIDs = []string{query.EntityID}
	}
	result, err := s.SeriesBatch(ctx, BatchSeriesQuery{
		Specs: []BatchSeriesSpec{{
			MetricName:     query.MetricName,
			Aggregations:   aggregations,
			Interval:       interval,
			PreserveSeries: preserveSeries,
		}},
		EntityIDs: entityIDs,
		Start:     query.Start,
		End:       query.End,
		Tags:      query.Tags,
		Order:     query.Order,
	}, now)
	if err != nil {
		return nil, err
	}
	return result.Values[query.MetricName], nil
}

func seriesResolutionForPolicy(start time.Time, interval time.Duration, now time.Time, policy RollupPolicy) time.Duration {
	preferred := policy.Tiers[0].Interval
	if tier := bestRollupTier(policy, interval, start.UTC(), now.UTC()); tier != nil {
		return tier.Interval
	}
	for i := len(policy.Tiers) - 1; i >= 0; i-- {
		tier := policy.Tiers[i]
		if interval >= tier.Interval && interval%tier.Interval == 0 {
			preferred = tier.Interval
			break
		}
	}
	return preferred
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
	var best *RollupTier
	for i := range policy.Tiers {
		tier := &policy.Tiers[i]
		if interval >= tier.Interval && interval%tier.Interval == 0 && !now.Add(-tier.Retention).After(start) {
			best = tier
		}
	}
	return best
}

// scanRollupRowsBetween is retained for compaction maintenance and internal
// verification. User-facing aggregation goes through SeriesBatch.
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
	return scanStoredRollupsForMaintenance(rows, needDigest, s.cfg.RollupPolicy.compression())
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

func scanStoredRollupsForMaintenance(rows *sql.Rows, needDigest bool, compression float64) ([]storedRollup, error) {
	result := make([]storedRollup, 0)
	for rows.Next() {
		var entityID, tagsHash, tagsJSON, labelsHash, labelsJSON string
		var bucket, count, firstTS, lastTS int64
		var sum, sumSq, min, max, firstVal, lastVal float64
		var digest []byte
		var err error
		if needDigest {
			err = rows.Scan(&entityID, &tagsHash, &tagsJSON, &labelsHash, &labelsJSON, &bucket, &count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS, &digest)
		} else {
			err = rows.Scan(&entityID, &tagsHash, &tagsJSON, &labelsHash, &labelsJSON, &bucket, &count, &sum, &sumSq, &min, &max, &firstVal, &firstTS, &lastVal, &lastTS)
		}
		if err != nil {
			return nil, err
		}
		var decoded *TDigest
		if needDigest {
			decoded, err = digestFromRollup(count, min, max, digest, compression)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, storedRollup{entityID: entityID, bucket: bucket, bucketData: &rollupBucket{
			count: count, sum: sum, sumSq: sumSq, min: min, max: max,
			firstVal: firstVal, firstTS: firstTS, lastVal: lastVal, lastTS: lastTS,
			digest: decoded, tagsHash: tagsHash, tagsJSON: tagsJSON, labelsHash: labelsHash, labelsJSON: labelsJSON,
		}})
	}
	return result, rows.Err()
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
