package metric

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

const exactRawRetention = time.Minute

type rawSeriesKey struct {
	metricName string
	entityID   string
	tagsHash   string
}

type rawSample struct {
	timestamp int64
	value     float64
	labelID   uint32
}

// rawSeries stores exact samples compactly for one logical series. Tags are
// shared by the series and labels are interned into small integer ids.
type rawSeries struct {
	tagsJSON    string
	labelIDs    map[string]uint32
	labelHashes []string
	labelsJSON  []string
	samples     []rawSample
}

// preparedMetricPoint holds the compact dictionary keys shared by the raw
// writer and the in-memory minute accumulator.
type preparedMetricPoint struct {
	metricName string
	entityID   string
	tagsHash   string
	tagsJSON   string
	labelsHash string
	labelsJSON string
	timestamp  int64
	value      float64
}

func prepareMetricPoints(points []Point) ([]preparedMetricPoint, error) {
	prepared := make([]preparedMetricPoint, 0, len(points))
	for i, point := range points {
		if err := point.Validate(); err != nil {
			return nil, fmt.Errorf("point %d (metric %q, entity %q): %w", i, point.MetricName, point.EntityID, err)
		}
		point = point.normalized()
		tagsHash, tagsJSON, err := tagsFingerprint(point.Tags)
		if err != nil {
			return nil, err
		}
		labelsHash, labelsJSON, err := tagsFingerprint(point.Labels)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedMetricPoint{
			metricName: point.MetricName,
			entityID:   point.EntityID,
			tagsHash:   tagsHash,
			tagsJSON:   tagsJSON,
			labelsHash: labelsHash,
			labelsJSON: labelsJSON,
			timestamp:  point.Timestamp.UnixMilli(),
			value:      point.Value,
		})
	}
	return prepared, nil
}

// writeRawPoints keeps the exact one-minute window in memory. Rollups are
// persisted independently by writePreparedHotRollups.
func (s *Store) writeRawPoints(ctx context.Context, points []preparedMetricPoint) (map[hotRollupKey]struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(points) == 0 {
		return nil, nil
	}
	return s.writeRawPointsAt(points, time.Now().UTC()), nil
}

func (s *Store) writeRawPointsAt(points []preparedMetricPoint, now time.Time) map[hotRollupKey]struct{} {
	cutoff := now.UTC().Add(-exactRawRetention).UnixMilli()
	rebuild := make(map[hotRollupKey]struct{})
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.pruneRawBeforeLocked("", cutoff)
	for _, point := range points {
		if point.timestamp < cutoff {
			continue
		}
		key := rawSeriesKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash}
		series := s.raw[key]
		if series == nil {
			series = &rawSeries{tagsJSON: point.tagsJSON, labelIDs: make(map[string]uint32)}
			s.raw[key] = series
		}
		labelID, ok := series.labelIDs[point.labelsHash]
		if !ok {
			labelID = uint32(len(series.labelsJSON))
			series.labelIDs[point.labelsHash] = labelID
			series.labelHashes = append(series.labelHashes, point.labelsHash)
			series.labelsJSON = append(series.labelsJSON, point.labelsJSON)
		}
		index := sort.Search(len(series.samples), func(i int) bool {
			return series.samples[i].timestamp >= point.timestamp
		})
		sample := rawSample{timestamp: point.timestamp, value: point.value, labelID: labelID}
		if index < len(series.samples) && series.samples[index].timestamp == point.timestamp {
			old := series.samples[index]
			rebuild[hotRollupKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash, labelsHash: series.labelHashes[old.labelID], bucket: bucketStartMillis(point.timestamp, time.Minute.Milliseconds())}] = struct{}{}
			rebuild[hotRollupKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash, labelsHash: point.labelsHash, bucket: bucketStartMillis(point.timestamp, time.Minute.Milliseconds())}] = struct{}{}
			series.samples[index] = sample
			continue
		}
		series.samples = append(series.samples, rawSample{})
		copy(series.samples[index+1:], series.samples[index:])
		series.samples[index] = sample
	}
	return rebuild
}

func (s *Store) queryRawPoints(ctx context.Context, query Query) ([]Point, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, end := query.Start.UnixMilli(), query.End.UnixMilli()
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.pruneRawBeforeLocked("", time.Now().UTC().Add(-exactRawRetention).UnixMilli())

	type resultPoint struct {
		point    Point
		entityID string
		tagsHash string
	}
	matched := make([]resultPoint, 0)
	for key, series := range s.raw {
		if key.metricName != query.MetricName || (query.EntityID != "" && key.entityID != query.EntityID) {
			continue
		}
		tags, ok, err := matchRawTags(series.tagsJSON, query.Tags)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		labels := make(map[uint32]map[string]string)
		first := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= start })
		for _, sample := range series.samples[first:] {
			if sample.timestamp > end {
				break
			}
			labelMap := labels[sample.labelID]
			if labelMap == nil {
				labelMap, err = decodeMapString(series.labelsJSON[sample.labelID])
				if err != nil {
					return nil, err
				}
				labels[sample.labelID] = labelMap
			}
			matched = append(matched, resultPoint{
				point:    Point{MetricName: key.metricName, EntityID: key.entityID, Timestamp: fromMillis(sample.timestamp), Value: sample.value, Tags: cloneStringMap(tags), Labels: cloneStringMap(labelMap)},
				entityID: key.entityID,
				tagsHash: key.tagsHash,
			})
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		left, right := matched[i], matched[j]
		less := left.point.Timestamp.Before(right.point.Timestamp)
		if left.point.Timestamp.Equal(right.point.Timestamp) {
			less = left.entityID < right.entityID || (left.entityID == right.entityID && left.tagsHash < right.tagsHash)
		}
		if query.Order == OrderDesc {
			return !less && !(left.point.Timestamp.Equal(right.point.Timestamp) && left.entityID == right.entityID && left.tagsHash == right.tagsHash)
		}
		return less
	})
	startIndex := query.Offset
	if startIndex > len(matched) {
		startIndex = len(matched)
	}
	endIndex := len(matched)
	if query.Limit > 0 && startIndex+query.Limit < endIndex {
		endIndex = startIndex + query.Limit
	}
	result := make([]Point, 0, endIndex-startIndex)
	for _, item := range matched[startIndex:endIndex] {
		result = append(result, item.point)
	}
	return result, nil
}

func (s *Store) rawEntityIDs(ctx context.Context, query Query) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, end := query.Start.UnixMilli(), query.End.UnixMilli()
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.pruneRawBeforeLocked("", time.Now().UTC().Add(-exactRawRetention).UnixMilli())
	seen := make(map[string]struct{})
	for key, series := range s.raw {
		if key.metricName != query.MetricName || (query.EntityID != "" && key.entityID != query.EntityID) {
			continue
		}
		_, ok, err := matchRawTags(series.tagsJSON, query.Tags)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		index := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= start })
		if index < len(series.samples) && series.samples[index].timestamp <= end {
			seen[key.entityID] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for entityID := range seen {
		result = append(result, entityID)
	}
	sort.Strings(result)
	return result, nil
}

func matchRawTags(raw string, filter map[string]string) (map[string]string, bool, error) {
	tags, err := decodeMapString(raw)
	if err != nil {
		return nil, false, err
	}
	for key, value := range filter {
		if tags[key] != value {
			return tags, false, nil
		}
	}
	return tags, true, nil
}

func (s *Store) deleteRawPoints(metricName, entityID string, tags map[string]string) (int64, error) {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	var deleted int64
	for key, series := range s.raw {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		if entityID != "" && key.entityID != entityID {
			continue
		}
		_, ok, err := matchRawTags(series.tagsJSON, tags)
		if err != nil {
			return deleted, err
		}
		if !ok {
			continue
		}
		deleted += int64(len(series.samples))
		delete(s.raw, key)
	}
	return deleted, nil
}

func (s *Store) deleteRawBefore(metricName string, beforeMilli int64) int64 {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	return s.pruneRawBeforeLocked(metricName, beforeMilli)
}

func (s *Store) pruneRawBeforeLocked(metricName string, beforeMilli int64) int64 {
	var deleted int64
	for key, series := range s.raw {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		index := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= beforeMilli })
		if index == 0 {
			continue
		}
		deleted += int64(index)
		if index == len(series.samples) {
			delete(s.raw, key)
			continue
		}
		copy(series.samples, series.samples[index:])
		series.samples = series.samples[:len(series.samples)-index]
		if cap(series.samples) > len(series.samples)*2+16 {
			series.samples = append([]rawSample(nil), series.samples...)
		}
		compactRawLabels(series)
	}
	return deleted
}

func compactRawLabels(series *rawSeries) {
	if len(series.labelHashes) <= 1 {
		return
	}
	used := make([]bool, len(series.labelHashes))
	for _, sample := range series.samples {
		used[sample.labelID] = true
	}
	usedCount := 0
	for _, keep := range used {
		if keep {
			usedCount++
		}
	}
	if usedCount == len(used) {
		return
	}
	remap := make([]uint32, len(used))
	labelHashes := make([]string, 0, usedCount)
	labelsJSON := make([]string, 0, usedCount)
	labelIDs := make(map[string]uint32, usedCount)
	for oldID, keep := range used {
		if !keep {
			continue
		}
		newID := uint32(len(labelHashes))
		remap[oldID] = newID
		hash := series.labelHashes[oldID]
		labelHashes = append(labelHashes, hash)
		labelsJSON = append(labelsJSON, series.labelsJSON[oldID])
		labelIDs[hash] = newID
	}
	for i := range series.samples {
		series.samples[i].labelID = remap[series.samples[i].labelID]
	}
	series.labelHashes = labelHashes
	series.labelsJSON = labelsJSON
	series.labelIDs = labelIDs
}

func (s *Store) latestRollupBefore(ctx context.Context, metricName, entityID string, before time.Time) (Point, bool, error) {
	end := before.Add(-time.Nanosecond)
	endMilli := end.UnixMilli()
	var latest Point
	found := false
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		var value float64
		var timestamp int64
		var rawTags, rawLabels any
		err := s.reader().QueryRowContext(ctx, fmt.Sprintf(`SELECT r.last_val, r.last_ts_milli, s.tags, l.labels
			FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s d ON d.id = r.resolution_id JOIN %s l ON l.id = r.label_id
			WHERE s.metric_name = %s AND s.entity_id = %s AND d.resolution_milli = %s AND r.last_ts_milli <= %s
			ORDER BY r.last_ts_milli DESC LIMIT 1`,
			s.tables.rollups, s.tables.series, s.tables.resolutions, s.tables.labels,
			s.dialect.placeholder(1), s.dialect.placeholder(2), s.dialect.placeholder(3), s.dialect.placeholder(4)),
			metricName, entityID, tier.Interval.Milliseconds(), endMilli,
		).Scan(&value, &timestamp, &rawTags, &rawLabels)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return Point{}, false, err
		}
		if found && timestamp <= latest.Timestamp.UnixMilli() {
			continue
		}
		tags, err := decodeMap(rawTags)
		if err != nil {
			return Point{}, false, err
		}
		labels, err := decodeMap(rawLabels)
		if err != nil {
			return Point{}, false, err
		}
		latest = Point{MetricName: metricName, EntityID: entityID, Timestamp: fromMillis(timestamp), Value: value, Tags: tags, Labels: labels}
		found = true
	}

	hot, err := s.hotRollupRows(metricName, entityID, nil, time.Unix(0, 0), end, false)
	if err != nil {
		return Point{}, false, err
	}
	for _, row := range hot {
		if row.bucketData.lastTS > endMilli || (found && row.bucketData.lastTS <= latest.Timestamp.UnixMilli()) {
			continue
		}
		tags, err := rollupTagsFromJSON(row.bucketData.tagsJSON)
		if err != nil {
			return Point{}, false, err
		}
		labels, err := rollupTagsFromJSON(row.bucketData.labelsJSON)
		if err != nil {
			return Point{}, false, err
		}
		latest = Point{MetricName: metricName, EntityID: entityID, Timestamp: fromMillis(row.bucketData.lastTS), Value: row.bucketData.lastVal, Tags: tags, Labels: labels}
		found = true
	}
	return latest, found, nil
}
