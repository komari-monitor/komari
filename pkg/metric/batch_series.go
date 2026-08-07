package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

type batchSeriesGroupKey struct {
	resolution time.Duration
	needDigest bool
}

type batchSeriesGroup struct {
	key         batchSeriesGroupKey
	metricNames map[string]struct{}
	fields      rollupReadFields
}

type seriesIdentity struct {
	metricName string
	entityID   string
	tagsHash   string
}

type seriesReadMeta struct {
	id         int64
	metricName string
	entityID   string
	tagsHash   string
	tagsJSON   string
	tags       map[string]string
}

type rollupRateSample struct {
	timestamp int64
	value     float64
}

type rollupAggregateState struct {
	summary     *rollupBucket
	rateSamples []rollupRateSample
	tags        map[string]string
}

type metricSeriesAccumulator struct {
	spec        BatchSeriesSpec
	compression float64
	needDigest  bool
	needSummary bool
	needRate    bool
	groups      map[rollupKey]*rollupAggregateState
	rateGroups  map[rollupKey]*rollupAggregateState
}

// SeriesBatch is the single rollup query implementation. It resolves metric
// metadata once, scans each selected backing tier once, and streams rows into
// the final output buckets.
func (s *Store) SeriesBatch(ctx context.Context, query BatchSeriesQuery, now time.Time) (BatchSeriesResult, error) {
	if err := s.ensureOpen(); err != nil {
		return BatchSeriesResult{}, err
	}
	if err := query.Validate(); err != nil {
		return BatchSeriesResult{}, err
	}
	return s.seriesBatchAt(ctx, query.normalized(), now.UTC(), nil)
}

// GetMetrics loads the requested metric definitions with one database query.
func (s *Store) GetMetrics(ctx context.Context, metricNames []string) (map[string]Definition, error) {
	if err := s.ensureOpen(); err != nil {
		return nil, err
	}
	if len(metricNames) == 0 {
		return map[string]Definition{}, nil
	}
	return s.loadBatchDefinitions(ctx, metricNames)
}

func (s *Store) seriesBatchAt(ctx context.Context, query BatchSeriesQuery, now time.Time, forcedResolutions map[string]time.Duration) (BatchSeriesResult, error) {
	metricNames := make([]string, 0, len(query.Specs))
	for _, spec := range query.Specs {
		metricNames = append(metricNames, spec.MetricName)
	}
	definitions, err := s.loadBatchDefinitions(ctx, metricNames)
	if err != nil {
		return BatchSeriesResult{}, err
	}

	result := BatchSeriesResult{
		Definitions: definitions,
		Values:      make(map[string]map[Aggregation][]AggregatePoint, len(query.Specs)),
	}
	accumulators := make(map[string]*metricSeriesAccumulator, len(query.Specs))
	groups := make(map[batchSeriesGroupKey]*batchSeriesGroup)
	for _, spec := range query.Specs {
		values := make(map[Aggregation][]AggregatePoint, len(spec.Aggregations))
		accumulator := &metricSeriesAccumulator{
			spec: spec, compression: s.cfg.RollupPolicy.compression(),
			groups: make(map[rollupKey]*rollupAggregateState), rateGroups: make(map[rollupKey]*rollupAggregateState),
		}
		for _, aggregation := range spec.Aggregations {
			values[aggregation] = []AggregatePoint{}
			if aggregation == AggRate {
				accumulator.needRate = true
			} else {
				accumulator.needSummary = true
			}
			if isPercentile(aggregation) {
				accumulator.needDigest = true
			}
		}
		result.Values[spec.MetricName] = values
		accumulators[spec.MetricName] = accumulator

		policy := s.cfg.RollupPolicy
		if definition, ok := definitions[spec.MetricName]; ok {
			if definition.RetentionDays <= 0 {
				continue
			}
			policy = policy.withMetricRetention(time.Duration(definition.RetentionDays) * 24 * time.Hour)
		}
		if len(policy.Tiers) == 0 {
			continue
		}
		resolution := seriesResolutionForPolicy(query.Start, spec.Interval, now, policy)
		if forced, ok := forcedResolutions[spec.MetricName]; ok {
			resolution = forced
		}
		key := batchSeriesGroupKey{resolution: resolution, needDigest: accumulator.needDigest}
		group := groups[key]
		if group == nil {
			group = &batchSeriesGroup{key: key, metricNames: make(map[string]struct{})}
			groups[key] = group
		}
		group.metricNames[spec.MetricName] = struct{}{}
		group.fields |= rollupFieldsForAggregations(spec.Aggregations)
	}

	seriesByID, seriesByIdentity, err := s.loadSeriesDictionary(ctx, seriesDictionaryPlan{
		MetricNames: metricNames,
		EntityIDs:   query.EntityIDs,
		Tags:        query.Tags,
	})
	if err != nil {
		return BatchSeriesResult{}, err
	}

	orderedGroups := make([]*batchSeriesGroup, 0, len(groups))
	for _, group := range groups {
		orderedGroups = append(orderedGroups, group)
	}
	sort.Slice(orderedGroups, func(i, j int) bool {
		if orderedGroups[i].key.resolution != orderedGroups[j].key.resolution {
			return orderedGroups[i].key.resolution < orderedGroups[j].key.resolution
		}
		return !orderedGroups[i].key.needDigest && orderedGroups[j].key.needDigest
	})

	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()
	for _, group := range orderedGroups {
		if err := s.scanPersistedRollupGroup(ctx, query, group, seriesByID, accumulators); err != nil {
			return BatchSeriesResult{}, err
		}
		if err := s.scanMemoryRollupGroup(query, group, seriesByIdentity, accumulators); err != nil {
			return BatchSeriesResult{}, err
		}
	}

	for metricName, accumulator := range accumulators {
		values, err := accumulator.points(query.Order)
		if err != nil {
			return BatchSeriesResult{}, err
		}
		result.Values[metricName] = values
	}
	return result, nil
}

func (s *Store) loadBatchDefinitions(ctx context.Context, metricNames []string) (map[string]Definition, error) {
	args := make([]any, 0, len(metricNames))
	placeholders := make([]string, 0, len(metricNames))
	for _, metricName := range metricNames {
		args = append(args, metricName)
		placeholders = append(placeholders, s.dialect.placeholder(len(args)))
	}
	rows, err := s.reader().QueryContext(ctx, fmt.Sprintf(
		"SELECT name, type, unit, description, retention_days, metadata, created_at_milli, updated_at_milli FROM %s WHERE name IN (%s) ORDER BY name ASC",
		s.tables.definitions, strings.Join(placeholders, ", "),
	), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	definitions := make(map[string]Definition, len(metricNames))
	for rows.Next() {
		definition, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		definitions[definition.Name] = definition
	}
	return definitions, rows.Err()
}

func (s *Store) loadSeriesDictionary(ctx context.Context, plan seriesDictionaryPlan) (map[int64]*seriesReadMeta, map[seriesIdentity]*seriesReadMeta, error) {
	rendered := s.dialect.renderSeriesDictionary(s.tables, s.cfg.TablePrefix+"series_metric_entity_idx", plan)
	rows, err := s.reader().QueryContext(ctx, rendered.Query, rendered.Args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byID := make(map[int64]*seriesReadMeta)
	byIdentity := make(map[seriesIdentity]*seriesReadMeta)
	for rows.Next() {
		var meta seriesReadMeta
		var rawTags any
		if err := rows.Scan(&meta.id, &meta.metricName, &meta.entityID, &meta.tagsHash, &rawTags); err != nil {
			return nil, nil, err
		}
		meta.tagsJSON, err = rawJSONToString(rawTags)
		if err != nil {
			return nil, nil, err
		}
		meta.tags, err = rollupTagsFromJSON(meta.tagsJSON)
		if err != nil {
			return nil, nil, err
		}
		byID[meta.id] = &meta
		byIdentity[seriesIdentity{metricName: meta.metricName, entityID: meta.entityID, tagsHash: meta.tagsHash}] = &meta
	}
	return byID, byIdentity, rows.Err()
}

func (s *Store) scanPersistedRollupGroup(ctx context.Context, query BatchSeriesQuery, group *batchSeriesGroup, seriesByID map[int64]*seriesReadMeta, accumulators map[string]*metricSeriesAccumulator) error {
	seriesIDs := make([]int64, 0)
	for seriesID, meta := range seriesByID {
		if _, ok := group.metricNames[meta.metricName]; ok {
			seriesIDs = append(seriesIDs, seriesID)
		}
	}
	if len(seriesIDs) == 0 {
		return nil
	}
	sort.Slice(seriesIDs, func(i, j int) bool { return seriesIDs[i] < seriesIDs[j] })
	resolutionID, found, err := s.resolveResolutionID(ctx, group.key.resolution)
	if err != nil || !found {
		return err
	}
	rendered := s.dialect.renderRollupRead(s.tables, s.cfg.TablePrefix+"rollups_resolution_bucket_idx", rollupReadPlan{
		SeriesIDs:    seriesIDs,
		ResolutionID: resolutionID,
		StartMilli:   bucketStartMillis(query.Start.UnixMilli(), group.key.resolution.Milliseconds()),
		EndMilli:     query.End.UnixMilli(),
		Fields:       group.fields,
	})
	rows, err := s.reader().QueryContext(ctx, rendered.Query, rendered.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var seriesID, bucket, count, firstTS, lastTS int64
	var sum, sumSq, min, max, firstVal, lastVal float64
	var digestBlob []byte
	destinations := []any{&seriesID, &bucket, &count}
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
	for rows.Next() {
		err = rows.Scan(destinations...)
		if err != nil {
			return err
		}
		meta := seriesByID[seriesID]
		accumulator := accumulators[meta.metricName]
		state := accumulator.consume(meta, bucket, count, sum, sumSq, min, max, firstVal, firstTS, lastVal, lastTS, nil)
		if accumulator.needDigest {
			if err := mergeEncodedRollupDigest(state.summary.digest, count, min, max, digestBlob); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func rollupFieldsForAggregations(aggregations []Aggregation) rollupReadFields {
	var fields rollupReadFields
	for _, aggregation := range aggregations {
		switch aggregation {
		case AggAvg, AggSum:
			fields |= rollupReadSum
		case AggMin:
			fields |= rollupReadMin
		case AggMax:
			fields |= rollupReadMax
		case AggFirst:
			fields |= rollupReadFirst
		case AggLast, AggRate:
			fields |= rollupReadLast
		case AggStdDev:
			fields |= rollupReadSum | rollupReadSumSq
		default:
			if isPercentile(aggregation) {
				fields |= rollupReadMin | rollupReadMax | rollupReadDigest
			}
		}
	}
	return fields
}

func (s *Store) resolveResolutionID(ctx context.Context, resolution time.Duration) (int64, bool, error) {
	var resolutionID int64
	err := s.reader().QueryRowContext(ctx,
		"SELECT id FROM "+s.tables.resolutions+" WHERE resolution_milli = "+s.dialect.placeholder(1),
		resolution.Milliseconds(),
	).Scan(&resolutionID)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return resolutionID, err == nil, err
}

func (s *Store) scanMemoryRollupGroup(query BatchSeriesQuery, group *batchSeriesGroup, seriesByIdentity map[seriesIdentity]*seriesReadMeta, accumulators map[string]*metricSeriesAccumulator) error {
	if group.key.resolution == time.Minute {
		return s.scanHotRollupGroup(query, group, seriesByIdentity, accumulators)
	}
	return s.scanCoarseRollupGroup(query, group, seriesByIdentity, accumulators)
}

func (s *Store) scanHotRollupGroup(query BatchSeriesQuery, group *batchSeriesGroup, seriesByIdentity map[seriesIdentity]*seriesReadMeta, accumulators map[string]*metricSeriesAccumulator) error {
	start := bucketStartMillis(query.Start.UnixMilli(), time.Minute.Milliseconds())
	end := query.End.UnixMilli()
	entityIDs := stringSet(query.EntityIDs)
	s.hotMu.RLock()
	defer s.hotMu.RUnlock()
	for key, bucket := range s.hot {
		if _, ok := group.metricNames[key.metricName]; !ok || key.bucket < start || key.bucket > end || bucket.count == 0 {
			continue
		}
		if len(entityIDs) > 0 {
			if _, ok := entityIDs[key.entityID]; !ok {
				continue
			}
		}
		meta, matched, err := memorySeriesMeta(seriesByIdentity, key.metricName, key.entityID, key.tagsHash, bucket.tagsJSON, query.Tags)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		accumulators[key.metricName].consume(meta, key.bucket, bucket.count, bucket.sum, bucket.sumSq, bucket.min, bucket.max, bucket.firstVal, bucket.firstTS, bucket.lastVal, bucket.lastTS, bucket.digest)
	}
	return nil
}

func (s *Store) scanCoarseRollupGroup(query BatchSeriesQuery, group *batchSeriesGroup, seriesByIdentity map[seriesIdentity]*seriesReadMeta, accumulators map[string]*metricSeriesAccumulator) error {
	start := bucketStartMillis(query.Start.UnixMilli(), group.key.resolution.Milliseconds())
	end := query.End.UnixMilli()
	entityIDs := stringSet(query.EntityIDs)
	s.coarseMu.RLock()
	defer s.coarseMu.RUnlock()
	for key, parent := range s.coarse {
		if key.interval != group.key.resolution || key.bucket < start || key.bucket > end {
			continue
		}
		if _, ok := group.metricNames[key.metricName]; !ok {
			continue
		}
		if len(entityIDs) > 0 {
			if _, ok := entityIDs[key.entityID]; !ok {
				continue
			}
		}
		childKeys := make([]rollupKey, 0, len(parent.children))
		for childKey := range parent.children {
			childKeys = append(childKeys, childKey)
		}
		if len(childKeys) == 0 {
			continue
		}
		sortRollupKeys(childKeys)
		firstChild := parent.children[childKeys[0]]
		meta, matched, err := memorySeriesMeta(seriesByIdentity, key.metricName, key.entityID, key.tagsHash, firstChild.tagsJSON, query.Tags)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		summary := newRollupBucketWithDigest(s.cfg.RollupPolicy.compression(), group.key.needDigest)
		for _, childKey := range childKeys {
			child := parent.children[childKey]
			mergeRollupValues(summary, child.count, child.sum, child.sumSq, child.min, child.max, child.firstVal, child.firstTS, child.lastVal, child.lastTS, child.digest, group.key.needDigest)
		}
		accumulators[key.metricName].consume(meta, key.bucket, summary.count, summary.sum, summary.sumSq, summary.min, summary.max, summary.firstVal, summary.firstTS, summary.lastVal, summary.lastTS, summary.digest)
	}
	return nil
}

func memorySeriesMeta(seriesByIdentity map[seriesIdentity]*seriesReadMeta, metricName, entityID, tagsHash, tagsJSON string, filter map[string]string) (*seriesReadMeta, bool, error) {
	identity := seriesIdentity{metricName: metricName, entityID: entityID, tagsHash: tagsHash}
	if meta := seriesByIdentity[identity]; meta != nil {
		return meta, true, nil
	}
	tags, err := rollupTagsFromJSON(tagsJSON)
	if err != nil {
		return nil, false, err
	}
	for key, value := range filter {
		if tags[key] != value {
			return nil, false, nil
		}
	}
	meta := &seriesReadMeta{metricName: metricName, entityID: entityID, tagsHash: tagsHash, tagsJSON: tagsJSON, tags: tags}
	seriesByIdentity[identity] = meta
	return meta, true, nil
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func (a *metricSeriesAccumulator) consume(meta *seriesReadMeta, sourceBucket, count int64, sum, sumSq, min, max, firstVal float64, firstTS int64, lastVal float64, lastTS int64, digest *TDigest) *rollupAggregateState {
	key := rollupKey{bucket: bucketStartMillis(sourceBucket, a.spec.Interval.Milliseconds())}
	if a.spec.PreserveSeries {
		key.entityID = meta.entityID
		key.tagsHash = meta.tagsHash
	}
	if a.needSummary {
		state := a.groups[key]
		if state == nil {
			state = &rollupAggregateState{summary: newRollupBucketWithDigest(a.compression, a.needDigest)}
			if a.spec.PreserveSeries {
				state.tags = meta.tags
			}
			a.groups[key] = state
		}
		mergeRollupValues(state.summary, count, sum, sumSq, min, max, firstVal, firstTS, lastVal, lastTS, digest, a.needDigest)
		if !a.needRate {
			return state
		}
	}
	if a.needRate {
		rateKey := rollupKey{
			bucket:   bucketStartMillis(sourceBucket, a.spec.Interval.Milliseconds()),
			entityID: meta.entityID,
			tagsHash: meta.tagsHash,
		}
		state := a.rateGroups[rateKey]
		if state == nil {
			state = &rollupAggregateState{tags: meta.tags}
			a.rateGroups[rateKey] = state
		}
		state.rateSamples = append(state.rateSamples, rollupRateSample{timestamp: lastTS, value: lastVal})
	}
	return a.groups[key]
}

func mergeEncodedRollupDigest(target *TDigest, count int64, min, max float64, blob []byte) error {
	if count == 0 {
		return nil
	}
	if len(blob) == 0 {
		if min != max {
			return fmt.Errorf("metric: missing t-digest for rollup with %d samples", count)
		}
		target.Add(min, float64(count))
		return nil
	}
	digest, err := DecodeTDigest(blob)
	if err != nil {
		return err
	}
	target.Merge(digest)
	return nil
}

func mergeRollupValues(target *rollupBucket, count int64, sum, sumSq, min, max, firstVal float64, firstTS int64, lastVal float64, lastTS int64, digest *TDigest, includeDigest bool) {
	if count == 0 {
		return
	}
	if target.count == 0 {
		target.min, target.max = min, max
		target.firstVal, target.firstTS = firstVal, firstTS
		target.lastVal, target.lastTS = lastVal, lastTS
	} else {
		if min < target.min {
			target.min = min
		}
		if max > target.max {
			target.max = max
		}
		if firstTS < target.firstTS {
			target.firstVal, target.firstTS = firstVal, firstTS
		}
		if lastTS > target.lastTS {
			target.lastVal, target.lastTS = lastVal, lastTS
		}
	}
	target.count += count
	target.sum += sum
	target.sumSq += sumSq
	if includeDigest && digest != nil {
		target.digest.Merge(digest)
	}
}

func (a *metricSeriesAccumulator) points(order Order) (map[Aggregation][]AggregatePoint, error) {
	values := make(map[Aggregation][]AggregatePoint, len(a.spec.Aggregations))
	var summaryKeys []rollupKey
	if a.needSummary {
		summaryKeys = make([]rollupKey, 0, len(a.groups))
		for key := range a.groups {
			summaryKeys = append(summaryKeys, key)
		}
		sortRollupKeys(summaryKeys)
	}
	var rateKeys []rollupKey
	if a.needRate {
		rateKeys = make([]rollupKey, 0, len(a.rateGroups))
		for key, state := range a.rateGroups {
			rateKeys = append(rateKeys, key)
			sort.SliceStable(state.rateSamples, func(i, j int) bool { return state.rateSamples[i].timestamp < state.rateSamples[j].timestamp })
		}
		sortRollupKeys(rateKeys)
	}
	for _, aggregation := range a.spec.Aggregations {
		keys := summaryKeys
		if aggregation == AggRate {
			keys = rateKeys
		}
		start, end, step := 0, len(keys), 1
		if order == OrderDesc {
			start, end, step = len(keys)-1, -1, -1
		}
		points := make([]AggregatePoint, 0, len(keys))
		for keyIndex := start; keyIndex != end; keyIndex += step {
			key := keys[keyIndex]
			state := a.groups[key]
			count, value := 0, 0.0
			if aggregation == AggRate {
				state = a.rateGroups[key]
				value = rollupCounterRate(state.rateSamples)
				count = len(state.rateSamples)
			} else {
				var ok bool
				value, ok = state.summary.value(aggregation)
				if !ok {
					return nil, fmt.Errorf("%w: aggregation %q requires raw samples", ErrInvalidArgument, aggregation)
				}
				count = int(state.summary.count)
			}
			points = append(points, AggregatePoint{
				MetricName: a.spec.MetricName,
				EntityID:   key.entityID,
				Bucket:     fromMillis(key.bucket),
				Value:      value,
				Count:      count,
				Tags:       state.tags,
			})
		}
		values[aggregation] = points
	}
	return values, nil
}

func rollupCounterRate(samples []rollupRateSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	seconds := float64(samples[len(samples)-1].timestamp-samples[0].timestamp) / 1000
	if seconds <= 0 {
		return 0
	}
	increase := 0.0
	for i := 1; i < len(samples); i++ {
		if delta := samples[i].value - samples[i-1].value; delta > 0 {
			increase += delta
		}
	}
	return increase / seconds
}
