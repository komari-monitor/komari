package metric

import (
	"context"
	"sort"
	"time"
)

// hotRollupKey identifies one active minute bucket. The dictionary dimensions
// remain compact strings; full tag and label JSON lives only on the bucket.
type hotRollupKey struct {
	metricName string
	entityID   string
	tagsHash   string
	labelsHash string
	bucket     int64
}

type hotRollup struct {
	key     hotRollupKey
	bucket  *rollupBucket
	replace bool
}

func (s *Store) writePreparedHotRollups(ctx context.Context, prepared []preparedMetricPoint, now time.Time, rebuild map[hotRollupKey]struct{}) error {
	minuteMillis := time.Minute.Milliseconds()
	compression := s.cfg.RollupPolicy.compression()
	if len(rebuild) > 0 {
		s.rollupViewMu.Lock()
		defer s.rollupViewMu.Unlock()
	}
	s.hotMu.Lock()
	for _, point := range prepared {
		key := hotRollupKey{
			metricName: point.metricName,
			entityID:   point.entityID,
			tagsHash:   point.tagsHash,
			labelsHash: point.labelsHash,
			bucket:     bucketStartMillis(point.timestamp, minuteMillis),
		}
		bucket := s.hot[key]
		if bucket == nil {
			bucket = newRollupBucket(compression)
			bucket.tagsHash, bucket.tagsJSON = point.tagsHash, point.tagsJSON
			bucket.labelsHash, bucket.labelsJSON = point.labelsHash, point.labelsJSON
			s.hot[key] = bucket
		}
		bucket.addPoint(point.value, point.timestamp)
	}
	if len(rebuild) > 0 {
		s.rebuildHotRollupsLocked(rebuild, compression)
	}
	s.hotMu.Unlock()
	var err error
	if len(rebuild) > 0 {
		_, err = s.flushClosedHotRollupsUnderView(ctx, now)
	} else {
		_, err = s.flushClosedHotRollups(ctx, now)
	}
	if err != nil {
		return err
	}
	if len(rebuild) > 0 {
		_, err = s.flushClosedCoarseRollupsUnderView(ctx, now)
	} else {
		_, err = s.flushClosedCoarseRollups(ctx, now)
	}
	return err
}

func (s *Store) rebuildHotRollupsLocked(keys map[hotRollupKey]struct{}, compression float64) {
	s.rawMu.RLock()
	defer s.rawMu.RUnlock()
	minuteMillis := time.Minute.Milliseconds()
	for key := range keys {
		series := s.raw[rawSeriesKey{metricName: key.metricName, entityID: key.entityID, tagsHash: key.tagsHash}]
		bucket := newRollupBucket(compression)
		bucket.tagsHash = key.tagsHash
		bucket.labelsHash = key.labelsHash
		if series != nil {
			bucket.tagsJSON = series.tagsJSON
			if labelID, ok := series.labelIDs[key.labelsHash]; ok {
				bucket.labelsJSON = series.labelsJSON[labelID]
			}
			bucketEnd := key.bucket + minuteMillis
			decoder := newRawSampleDecoder(series.compressed)
			for sample, more := decoder.next(); more; sample, more = decoder.next() {
				if sample.timestamp < key.bucket {
					continue
				}
				if sample.timestamp >= bucketEnd {
					break
				}
				if series.labelHashes[sample.labelID] != key.labelsHash {
					continue
				}
				bucket.labelsJSON = series.labelsJSON[sample.labelID]
				bucket.addPoint(sample.value, sample.timestamp)
			}
			directStart := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= key.bucket })
			directEnd := sort.Search(len(series.samples), func(i int) bool { return series.samples[i].timestamp >= bucketEnd })
			for _, sample := range series.samples[directStart:directEnd] {
				if series.labelHashes[sample.labelID] != key.labelsHash {
					continue
				}
				bucket.labelsJSON = series.labelsJSON[sample.labelID]
				bucket.addPoint(sample.value, sample.timestamp)
			}
		}
		s.hot[key] = bucket
		s.hotReplace[key] = struct{}{}
	}
}

func (s *Store) flushClosedHotRollups(ctx context.Context, now time.Time) (int, error) {
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()
	return s.flushClosedHotRollupsUnderView(ctx, now)
}

func (s *Store) flushClosedHotRollupsUnderView(ctx context.Context, now time.Time) (int, error) {
	flushBefore := bucketStartMillis(now.UTC().UnixMilli(), time.Minute.Milliseconds())
	closed := s.takeClosedHotRollups(flushBefore)
	if len(closed) == 0 {
		return 0, nil
	}
	if err := s.persistHotRollups(ctx, closed, now); err != nil {
		s.restoreHotRollups(closed)
		return 0, err
	}
	return len(closed), nil
}

func (s *Store) flushAllHotRollups(ctx context.Context) error {
	s.rollupViewMu.Lock()
	defer s.rollupViewMu.Unlock()
	s.hotMu.Lock()
	all := make([]hotRollup, 0, len(s.hot))
	for key, bucket := range s.hot {
		_, replace := s.hotReplace[key]
		all = append(all, hotRollup{key: key, bucket: bucket, replace: replace})
		delete(s.hot, key)
		delete(s.hotReplace, key)
	}
	s.hotMu.Unlock()
	if len(all) == 0 {
		return nil
	}
	if err := s.persistHotRollups(ctx, all, time.Now().UTC()); err != nil {
		s.restoreHotRollups(all)
		return err
	}
	return nil
}

func (s *Store) takeClosedHotRollups(currentBucket int64) []hotRollup {
	s.hotMu.Lock()
	defer s.hotMu.Unlock()
	closed := make([]hotRollup, 0)
	for key, bucket := range s.hot {
		if key.bucket < currentBucket {
			_, replace := s.hotReplace[key]
			closed = append(closed, hotRollup{key: key, bucket: bucket, replace: replace})
			delete(s.hot, key)
			delete(s.hotReplace, key)
		}
	}
	return closed
}

func (s *Store) restoreHotRollups(closed []hotRollup) {
	s.hotMu.Lock()
	defer s.hotMu.Unlock()
	for _, item := range closed {
		current, exists := s.hot[item.key]
		_, currentReplaces := s.hotReplace[item.key]
		if !exists {
			s.hot[item.key] = item.bucket
			if item.replace {
				s.hotReplace[item.key] = struct{}{}
			}
			continue
		}
		if currentReplaces {
			continue
		}
		if item.replace {
			item.bucket.mergeStored(current)
			s.hot[item.key] = item.bucket
			s.hotReplace[item.key] = struct{}{}
			continue
		}
		current.mergeStored(item.bucket)
	}
}

func (s *Store) persistHotRollups(ctx context.Context, closed []hotRollup, now time.Time) error {
	type metricBuckets struct {
		merge   map[rollupKey]*rollupBucket
		replace map[rollupKey]*rollupBucket
		policy  RollupPolicy
	}
	byMetric := make(map[string]*metricBuckets)
	for _, item := range closed {
		buckets := byMetric[item.key.metricName]
		if buckets == nil {
			buckets = &metricBuckets{merge: make(map[rollupKey]*rollupBucket), replace: make(map[rollupKey]*rollupBucket)}
			byMetric[item.key.metricName] = buckets
		}
		key := rollupKey{entityID: item.key.entityID, tagsHash: item.key.tagsHash, labelsHash: item.key.labelsHash, bucket: item.key.bucket}
		if item.replace {
			buckets.replace[key] = item.bucket
		} else {
			buckets.merge[key] = item.bucket
		}
	}
	names := make([]string, 0, len(byMetric))
	for name := range byMetric {
		names = append(names, name)
	}
	sort.Strings(names)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		buckets := byMetric[name]
		policy, err := s.metricRollupPolicyTx(ctx, name, tx)
		if err != nil {
			return err
		}
		buckets.policy = policy
		if _, err := s.writeTierCascadeTx(ctx, name, policy, buckets.merge, tx); err != nil {
			return err
		}
		if err := s.replaceMinuteRollupsTx(ctx, name, policy, buckets.replace, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, name := range names {
		buckets := byMetric[name]
		minute := make(map[rollupKey]*rollupBucket, len(buckets.merge)+len(buckets.replace))
		for key, bucket := range buckets.merge {
			minute[key] = bucket
		}
		for key, bucket := range buckets.replace {
			minute[key] = bucket
		}
		s.addMinuteParents(name, buckets.policy, minute, now)
	}
	return nil
}

func (s *Store) hotRollupRows(metricName, entityID string, tags map[string]string, start, end time.Time, needDigest bool) ([]storedRollup, error) {
	startMilli, endMilli := start.UTC().UnixMilli(), end.UTC().UnixMilli()
	s.hotMu.RLock()
	defer s.hotMu.RUnlock()
	out := make([]storedRollup, 0)
	for key, bucket := range s.hot {
		if key.metricName != metricName || key.bucket+time.Minute.Milliseconds() <= startMilli || key.bucket > endMilli || (entityID != "" && key.entityID != entityID) {
			continue
		}
		if bucket.count == 0 {
			continue
		}
		bucketTags, err := rollupTagsFromJSON(bucket.tagsJSON)
		if err != nil {
			return nil, err
		}
		matched := true
		for name, value := range tags {
			if bucketTags[name] != value {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		out = append(out, storedRollup{entityID: key.entityID, bucket: key.bucket, bucketData: bucket.clone(needDigest)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].bucket < out[j].bucket })
	return out, nil
}

// deleteHotRollups removes active minute buckets matched by the same series
// dimensions used by persisted deletes. A non-nil cutoff removes only buckets
// whose minute starts before it.
func (s *Store) deleteHotRollups(metricName, entityID string, tags map[string]string, cutoff *time.Time) (int64, error) {
	s.hotMu.Lock()
	defer s.hotMu.Unlock()

	var cutoffMilli int64
	if cutoff != nil {
		cutoffMilli = cutoff.UTC().UnixMilli()
	}
	matched := make([]hotRollupKey, 0)
	for key, bucket := range s.hot {
		if metricName != "" && key.metricName != metricName {
			continue
		}
		if entityID != "" && key.entityID != entityID {
			continue
		}
		if cutoff != nil && key.bucket >= cutoffMilli {
			continue
		}
		if len(tags) > 0 {
			bucketTags, err := rollupTagsFromJSON(bucket.tagsJSON)
			if err != nil {
				return 0, err
			}
			matches := true
			for name, value := range tags {
				if bucketTags[name] != value {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}
		matched = append(matched, key)
	}
	for _, key := range matched {
		delete(s.hot, key)
		delete(s.hotReplace, key)
	}
	return int64(len(matched)), nil
}
