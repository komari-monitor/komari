package metric

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// directRawRetention keeps the newest samples in a directly searchable
	// slice. The rest of rawMemoryRetention is losslessly byte-encoded.
	directRawRetention = time.Minute
	rawMemoryRetention = 10 * time.Minute
)

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

// compressedRawSamples stores older raw samples without aggregating them.
// Timestamps and label ids use varints; values remain IEEE-754 bits so the
// raw API can return the original samples exactly.
type compressedRawSamples struct {
	data      []byte
	count     int
	lastStamp int64
}

type rawSampleDecoder struct {
	data     []byte
	offset   int
	index    int
	count    int
	previous int64
}

func newRawSampleDecoder(samples compressedRawSamples) rawSampleDecoder {
	return rawSampleDecoder{data: samples.data, count: samples.count}
}

func (d *rawSampleDecoder) next() (rawSample, bool) {
	if d.index == d.count {
		return rawSample{}, false
	}
	var timestamp int64
	if d.index == 0 {
		value, size := binary.Varint(d.data[d.offset:])
		d.offset += size
		timestamp = value
	} else {
		delta, size := binary.Uvarint(d.data[d.offset:])
		d.offset += size
		timestamp = d.previous + int64(delta)
	}
	value := math.Float64frombits(binary.LittleEndian.Uint64(d.data[d.offset:]))
	d.offset += 8
	labelID, size := binary.Uvarint(d.data[d.offset:])
	d.offset += size
	d.index++
	d.previous = timestamp
	return rawSample{timestamp: timestamp, value: value, labelID: uint32(labelID)}, true
}

func newCompressedRawSamples(capacity int) compressedRawSamples {
	return compressedRawSamples{data: make([]byte, 0, capacity)}
}

func (c *compressedRawSamples) appendSample(sample rawSample) {
	if c.count == 0 {
		c.data = appendVarint(c.data, sample.timestamp)
	} else {
		c.data = appendUvarint(c.data, uint64(sample.timestamp-c.lastStamp))
	}
	c.data = appendRawSample(c.data, sample)
	c.count++
	c.lastStamp = sample.timestamp
}

func (c compressedRawSamples) firstStamp() int64 {
	value, _ := binary.Varint(c.data)
	return value
}

// rawSeries stores exact samples compactly for one logical series. Tags are
// shared by the series and labels are interned into small integer ids.
type rawSeries struct {
	tagsJSON    string
	labelIDs    map[string]uint32
	labelHashes []string
	labelsJSON  []string
	samples     []rawSample
	compressed  compressedRawSamples
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

// writeRawPoints keeps the newest minute in a directly addressable form. The
// surrounding nine minutes remain individual raw samples in a compact byte
// representation; only their minute rollups are eventually persisted.
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
	now = now.UTC()
	directCutoff := now.Add(-directRawRetention).UnixMilli()
	rawCutoff := rawMemoryCutoff(now)
	rebuild := make(map[hotRollupKey]struct{})
	dirtyLabels := make(map[*rawSeries]struct{})
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.compressRawBeforeLocked("", directCutoff)
	s.pruneRawBeforeLocked("", rawCutoff)
	for _, point := range points {
		if point.timestamp < rawCutoff {
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
		sample := rawSample{timestamp: point.timestamp, value: point.value, labelID: labelID}
		if point.timestamp < directCutoff {
			old, replaced := series.replaceCompressed(sample)
			if replaced {
				markRawRebuild(rebuild, point, series.labelHashes[old.labelID])
				if old.labelID != sample.labelID {
					dirtyLabels[series] = struct{}{}
				}
			}
			markRawRebuild(rebuild, point, point.labelsHash)
			continue
		}
		index := sort.Search(len(series.samples), func(i int) bool {
			return series.samples[i].timestamp >= point.timestamp
		})
		if index < len(series.samples) && series.samples[index].timestamp == point.timestamp {
			old := series.samples[index]
			markRawRebuild(rebuild, point, series.labelHashes[old.labelID])
			markRawRebuild(rebuild, point, point.labelsHash)
			series.samples[index] = sample
			if old.labelID != sample.labelID {
				dirtyLabels[series] = struct{}{}
			}
			continue
		}
		series.samples = append(series.samples, rawSample{})
		copy(series.samples[index+1:], series.samples[index:])
		series.samples[index] = sample
	}
	for series := range dirtyLabels {
		compactRawLabels(series)
	}
	return rebuild
}

func markRawRebuild(rebuild map[hotRollupKey]struct{}, point preparedMetricPoint, labelsHash string) {
	rebuild[hotRollupKey{metricName: point.metricName, entityID: point.entityID, tagsHash: point.tagsHash, labelsHash: labelsHash, bucket: bucketStartMillis(point.timestamp, time.Minute.Milliseconds())}] = struct{}{}
}

type rawBatchPoints struct {
	points     []Point
	tagsHashes []string
	order      Order
}

func (p *rawBatchPoints) Len() int { return len(p.points) }

func (p *rawBatchPoints) Less(i, j int) bool {
	if p.order == OrderDesc {
		return rawPointLess(p.points[j], p.tagsHashes[j], p.points[i], p.tagsHashes[i])
	}
	return rawPointLess(p.points[i], p.tagsHashes[i], p.points[j], p.tagsHashes[j])
}

func (p *rawBatchPoints) Swap(i, j int) {
	p.points[i], p.points[j] = p.points[j], p.points[i]
	p.tagsHashes[i], p.tagsHashes[j] = p.tagsHashes[j], p.tagsHashes[i]
}

func (p *rawBatchPoints) grow(additional int) {
	if additional <= cap(p.points)-len(p.points) {
		return
	}
	pointCount := len(p.points) + additional
	points := make([]Point, len(p.points), pointCount)
	copy(points, p.points)
	p.points = points
	tagsHashes := make([]string, len(p.tagsHashes), pointCount)
	copy(tagsHashes, p.tagsHashes)
	p.tagsHashes = tagsHashes
}

func (s *Store) queryRawPoints(ctx context.Context, query Query) ([]Point, error) {
	entityIDs := []string(nil)
	if query.EntityID != "" {
		entityIDs = []string{query.EntityID}
	}
	batch, err := s.queryRawPointsBatch(ctx, BatchQuery{
		MetricNames: []string{query.MetricName},
		EntityIDs:   entityIDs,
		Start:       query.Start,
		End:         query.End,
		Tags:        query.Tags,
		Order:       query.Order,
	})
	if err != nil {
		return nil, err
	}
	points := batch[query.MetricName]
	startIndex := query.Offset
	if startIndex > len(points) {
		startIndex = len(points)
	}
	endIndex := len(points)
	if query.Limit > 0 && startIndex+query.Limit < endIndex {
		endIndex = startIndex + query.Limit
	}
	return points[startIndex:endIndex], nil
}

func (s *Store) queryRawPointsBatch(ctx context.Context, query BatchQuery) (map[string][]Point, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, end := query.Start.UnixMilli(), query.End.UnixMilli()
	visibleCutoff := time.Now().UTC().Add(-rawMemoryRetention).UnixMilli()
	if start < visibleCutoff {
		start = visibleCutoff
	}
	metricNames := make(map[string]struct{}, len(query.MetricNames))
	matched := make(map[string]*rawBatchPoints, len(query.MetricNames))
	for _, metricName := range query.MetricNames {
		metricNames[metricName] = struct{}{}
		matched[metricName] = &rawBatchPoints{order: query.Order}
	}
	entityIDs := make(map[string]struct{}, len(query.EntityIDs))
	for _, entityID := range query.EntityIDs {
		entityIDs[entityID] = struct{}{}
	}

	s.rawMu.RLock()
	defer s.rawMu.RUnlock()
	for key, series := range s.raw {
		if _, ok := metricNames[key.metricName]; !ok {
			continue
		}
		if len(entityIDs) > 0 {
			if _, ok := entityIDs[key.entityID]; !ok {
				continue
			}
		}
		tags, ok, err := matchRawTags(series.tagsJSON, query.Tags)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		labels := make(map[uint32]map[string]string)
		directStart := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= start })
		directEnd := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp > end })
		additional := directEnd - directStart
		if series.compressed.count > 0 && start <= series.compressed.firstStamp() && end >= series.compressed.lastStamp {
			additional += series.compressed.count
		}
		metricPoints := matched[key.metricName]
		metricPoints.grow(additional)
		if series.compressed.count > 0 && start <= series.compressed.lastStamp && end >= series.compressed.firstStamp() {
			decoder := newRawSampleDecoder(series.compressed)
			for sample, more := decoder.next(); more; sample, more = decoder.next() {
				if sample.timestamp < start {
					continue
				}
				if sample.timestamp > end {
					break
				}
				labelMap, err := rawSampleLabels(series, labels, sample.labelID)
				if err != nil {
					return nil, err
				}
				appendRawQueryPoint(metricPoints, key, tags, labelMap, sample)
			}
		}
		for _, sample := range series.samples[directStart:directEnd] {
			labelMap, err := rawSampleLabels(series, labels, sample.labelID)
			if err != nil {
				return nil, err
			}
			appendRawQueryPoint(metricPoints, key, tags, labelMap, sample)
		}
	}
	result := make(map[string][]Point, len(matched))
	for metricName, metricPoints := range matched {
		sort.Sort(metricPoints)
		result[metricName] = metricPoints.points
	}
	return result, nil
}

func rawSampleLabels(series *rawSeries, labels map[uint32]map[string]string, labelID uint32) (map[string]string, error) {
	if labelMap := labels[labelID]; labelMap != nil {
		return labelMap, nil
	}
	labelMap, err := decodeMapString(series.labelsJSON[labelID])
	if err != nil {
		return nil, err
	}
	labels[labelID] = labelMap
	return labelMap, nil
}

func appendRawQueryPoint(points *rawBatchPoints, key rawSeriesKey, tags, labels map[string]string, sample rawSample) {
	points.points = append(points.points, Point{
		MetricName: key.metricName, EntityID: key.entityID,
		Timestamp: fromMillis(sample.timestamp), Value: sample.value, Tags: tags, Labels: labels,
	})
	points.tagsHashes = append(points.tagsHashes, key.tagsHash)
}

func rawPointLess(left Point, leftTagsHash string, right Point, rightTagsHash string) bool {
	if !left.Timestamp.Equal(right.Timestamp) {
		return left.Timestamp.Before(right.Timestamp)
	}
	if left.EntityID != right.EntityID {
		return left.EntityID < right.EntityID
	}
	return leftTagsHash < rightTagsHash
}

func (s *Store) rawEntityIDs(ctx context.Context, query Query) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	start, end := query.Start.UnixMilli(), query.End.UnixMilli()
	s.rawMu.RLock()
	defer s.rawMu.RUnlock()
	visibleCutoff := time.Now().UTC().Add(-rawMemoryRetention).UnixMilli()
	if start < visibleCutoff {
		start = visibleCutoff
	}
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
		found := false
		if series.compressed.count > 0 && start <= series.compressed.lastStamp && end >= series.compressed.firstStamp() {
			decoder := newRawSampleDecoder(series.compressed)
			for sample, more := decoder.next(); more; sample, more = decoder.next() {
				if sample.timestamp < start {
					continue
				}
				if sample.timestamp > end {
					break
				}
				found = true
				break
			}
		}
		if !found {
			index := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= start })
			found = index < len(series.samples) && series.samples[index].timestamp <= end
		}
		if found {
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

// rawMemoryCutoff keeps the complete oldest minute internally so a late
// replacement can rebuild that minute without dropping its earlier samples.
// Query visibility is still clamped to the exact ten-minute window.
func rawMemoryCutoff(now time.Time) int64 {
	return bucketStartMillis(now.UTC().Add(-rawMemoryRetention).UnixMilli(), time.Minute.Milliseconds())
}

func (s *Store) trimRawWindow(now time.Time) int64 {
	now = now.UTC()
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.compressRawBeforeLocked("", now.Add(-directRawRetention).UnixMilli())
	return s.pruneRawBeforeLocked("", rawMemoryCutoff(now))
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
		deleted += int64(len(series.samples) + series.compressed.count)
		delete(s.raw, key)
	}
	return deleted, nil
}

func (s *Store) deleteRawBefore(metricName string, beforeMilli int64) int64 {
	s.rawMu.Lock()
	defer s.rawMu.Unlock()
	s.compressRawBeforeLocked(metricName, beforeMilli)
	return s.pruneRawBeforeLocked(metricName, beforeMilli)
}

func (s *Store) pruneRawBeforeLocked(metricName string, beforeMilli int64) int64 {
	var deleted int64
	for key, series := range s.raw {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		compressedDeleted := pruneCompressedRawSamples(&series.compressed, beforeMilli)
		deleted += int64(compressedDeleted)
		directIndex := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= beforeMilli })
		if directIndex > 0 {
			deleted += int64(directIndex)
			copy(series.samples, series.samples[directIndex:])
			series.samples = series.samples[:len(series.samples)-directIndex]
		}
		if series.compressed.count == 0 && len(series.samples) == 0 {
			delete(s.raw, key)
			continue
		}
		if compressedDeleted > 0 || directIndex > 0 {
			compactRawLabels(series)
		}
	}
	return deleted
}

func pruneCompressedRawSamples(samples *compressedRawSamples, beforeMilli int64) int {
	if samples.count == 0 || samples.firstStamp() >= beforeMilli {
		return 0
	}
	if samples.lastStamp < beforeMilli {
		deleted := samples.count
		*samples = compressedRawSamples{}
		return deleted
	}
	decoder := newRawSampleDecoder(*samples)
	kept := newCompressedRawSamples(len(samples.data))
	deleted := 0
	for sample, more := decoder.next(); more; sample, more = decoder.next() {
		if sample.timestamp < beforeMilli {
			deleted++
			continue
		}
		kept.appendSample(sample)
	}
	*samples = kept
	return deleted
}

func (s *Store) compressRawBeforeLocked(metricName string, beforeMilli int64) {
	for key, series := range s.raw {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		index := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= beforeMilli })
		if index == 0 {
			continue
		}
		series.compressed.append(series.samples[:index])
		copy(series.samples, series.samples[index:])
		series.samples = series.samples[:len(series.samples)-index]
		if len(series.samples) == 0 {
			series.samples = nil
		}
	}
}

func (s *rawSeries) replaceCompressed(sample rawSample) (rawSample, bool) {
	var old rawSample
	replaced := false
	inserted := false
	decoder := newRawSampleDecoder(s.compressed)
	encoded := newCompressedRawSamples(len(s.compressed.data) + binary.MaxVarintLen64 + 9)
	for current, more := decoder.next(); more; current, more = decoder.next() {
		if !inserted && sample.timestamp < current.timestamp {
			encoded.appendSample(sample)
			inserted = true
		}
		if !inserted && sample.timestamp == current.timestamp {
			old = current
			replaced = true
			encoded.appendSample(sample)
			inserted = true
			continue
		}
		encoded.appendSample(current)
	}
	if !inserted {
		encoded.appendSample(sample)
	}
	s.compressed = encoded
	return old, replaced
}

func (c *compressedRawSamples) append(samples []rawSample) {
	if len(samples) == 0 {
		return
	}
	if c.count > 0 && samples[0].timestamp <= c.lastStamp {
		decoder := newRawSampleDecoder(*c)
		merged := newCompressedRawSamples(len(c.data) + len(samples)*10)
		current, more := decoder.next()
		for sampleIndex := 0; more || sampleIndex < len(samples); {
			if !more {
				merged.appendSample(samples[sampleIndex])
				sampleIndex++
				continue
			}
			if sampleIndex == len(samples) {
				merged.appendSample(current)
				current, more = decoder.next()
				continue
			}
			next := samples[sampleIndex]
			switch {
			case next.timestamp < current.timestamp:
				merged.appendSample(next)
				sampleIndex++
			case next.timestamp == current.timestamp:
				merged.appendSample(next)
				sampleIndex++
				current, more = decoder.next()
			default:
				merged.appendSample(current)
				current, more = decoder.next()
			}
		}
		*c = merged
		return
	}
	for _, sample := range samples {
		c.appendSample(sample)
	}
}

func appendRawSample(dst []byte, sample rawSample) []byte {
	var value [8]byte
	binary.LittleEndian.PutUint64(value[:], math.Float64bits(sample.value))
	dst = append(dst, value[:]...)
	return appendUvarint(dst, uint64(sample.labelID))
}

func appendVarint(dst []byte, value int64) []byte {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutVarint(encoded[:], value)
	return append(dst, encoded[:n]...)
}

func appendUvarint(dst []byte, value uint64) []byte {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(encoded[:], value)
	return append(dst, encoded[:n]...)
}

func compactRawLabels(series *rawSeries) {
	if len(series.labelHashes) <= 1 {
		return
	}
	used := make([]bool, len(series.labelHashes))
	decoder := newRawSampleDecoder(series.compressed)
	for sample, more := decoder.next(); more; sample, more = decoder.next() {
		used[sample.labelID] = true
	}
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
	decoder = newRawSampleDecoder(series.compressed)
	compressed := newCompressedRawSamples(len(series.compressed.data))
	for sample, more := decoder.next(); more; sample, more = decoder.next() {
		sample.labelID = remap[sample.labelID]
		compressed.appendSample(sample)
	}
	series.compressed = compressed
	for i := range series.samples {
		series.samples[i].labelID = remap[series.samples[i].labelID]
	}
	series.labelHashes = labelHashes
	series.labelsJSON = labelsJSON
	series.labelIDs = labelIDs
}

func (s *Store) latestRollupBefore(ctx context.Context, metricName, entityID string, before time.Time) (Point, bool, error) {
	s.rollupViewMu.RLock()
	defer s.rollupViewMu.RUnlock()
	end := before.Add(-time.Nanosecond)
	endMilli := end.UnixMilli()
	var latest Point
	found := false
	for _, tier := range s.cfg.RollupPolicy.Tiers {
		var value float64
		var timestamp int64
		var rawTags, rawLabels any
		err := s.reader().QueryRowContext(ctx, fmt.Sprintf(`SELECT r.last_val, r.last_ts_milli, s.tags, l.labels
			FROM %s r JOIN %s s ON s.id = r.series_id JOIN %s l ON l.id = r.label_id
			WHERE r.series_id IN (SELECT id FROM %s WHERE metric_name = %s AND entity_id = %s)
			AND r.resolution_id IN (SELECT id FROM %s WHERE resolution_milli = %s)
			AND r.last_ts_milli <= %s
			ORDER BY r.last_ts_milli DESC LIMIT 1`,
			s.tables.rollups, s.tables.series, s.tables.labels, s.tables.series,
			s.dialect.placeholder(1), s.dialect.placeholder(2), s.tables.resolutions,
			s.dialect.placeholder(3), s.dialect.placeholder(4)),
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
