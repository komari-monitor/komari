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
	key    hotRollupKey
	bucket *rollupBucket
}

func (s *Store) writePreparedHotRollups(ctx context.Context, prepared []preparedMetricPoint, now time.Time, rebuild map[hotRollupKey]struct{}) error {
	minuteMillis := time.Minute.Milliseconds()
	compression := s.cfg.RollupPolicy.compression()
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
	_, err := s.flushClosedHotRollups(ctx, now)
	return err
}

func (s *Store) rebuildHotRollupsLocked(keys map[hotRollupKey]struct{}, compression float64) {
	s.rawMu.RLock()
	defer s.rawMu.RUnlock()
	minuteMillis := time.Minute.Milliseconds()
	for key := range keys {
		series := s.raw[rawSeriesKey{metricName: key.metricName, entityID: key.entityID, tagsHash: key.tagsHash}]
		if series == nil {
			delete(s.hot, key)
			continue
		}
		bucket := newRollupBucket(compression)
		bucket.tagsHash, bucket.tagsJSON = key.tagsHash, series.tagsJSON
		bucket.labelsHash = key.labelsHash
		for _, sample := range series.samples {
			if bucketStartMillis(sample.timestamp, minuteMillis) != key.bucket || series.labelHashes[sample.labelID] != key.labelsHash {
				continue
			}
			bucket.labelsJSON = series.labelsJSON[sample.labelID]
			bucket.addPoint(sample.value, sample.timestamp)
		}
		if bucket.count == 0 {
			delete(s.hot, key)
			continue
		}
		s.hot[key] = bucket
	}
}

func (s *Store) flushClosedHotRollups(ctx context.Context, now time.Time) (int, error) {
	closed := s.takeClosedHotRollups(bucketStartMillis(now.UTC().UnixMilli(), time.Minute.Milliseconds()))
	if len(closed) == 0 {
		return 0, nil
	}
	if err := s.persistHotRollups(ctx, closed); err != nil {
		s.restoreHotRollups(closed)
		return 0, err
	}
	return len(closed), nil
}

func (s *Store) flushAllHotRollups(ctx context.Context) error {
	s.hotMu.Lock()
	all := make([]hotRollup, 0, len(s.hot))
	for key, bucket := range s.hot {
		all = append(all, hotRollup{key: key, bucket: bucket})
		delete(s.hot, key)
	}
	s.hotMu.Unlock()
	if len(all) == 0 {
		return nil
	}
	if err := s.persistHotRollups(ctx, all); err != nil {
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
			closed = append(closed, hotRollup{key: key, bucket: bucket})
			delete(s.hot, key)
		}
	}
	return closed
}

func (s *Store) restoreHotRollups(closed []hotRollup) {
	s.hotMu.Lock()
	defer s.hotMu.Unlock()
	for _, item := range closed {
		if current := s.hot[item.key]; current != nil {
			current.mergeStored(item.bucket)
		} else {
			s.hot[item.key] = item.bucket
		}
	}
}

func (s *Store) persistHotRollups(ctx context.Context, closed []hotRollup) error {
	byMetric := make(map[string]map[rollupKey]*rollupBucket)
	for _, item := range closed {
		buckets := byMetric[item.key.metricName]
		if buckets == nil {
			buckets = make(map[rollupKey]*rollupBucket)
			byMetric[item.key.metricName] = buckets
		}
		buckets[rollupKey{entityID: item.key.entityID, tagsHash: item.key.tagsHash, labelsHash: item.key.labelsHash, bucket: item.key.bucket}] = item.bucket
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
		if _, err := s.writeTierCascadeTx(ctx, name, byMetric[name], tx); err != nil {
			return err
		}
	}
	return tx.Commit()
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
		copyBucket := *bucket
		if !needDigest {
			copyBucket.digest = nil
		}
		out = append(out, storedRollup{entityID: key.entityID, bucket: key.bucket, bucketData: &copyBucket})
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
	}
	return int64(len(matched)), nil
}
