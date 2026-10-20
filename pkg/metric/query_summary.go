package metric

// Query summaries do not need persisted tag/label JSON and hashes. Keeping that
// metadata in every output bucket wastes space even when blocks are reused.
type queryRollupSummary struct {
	count                int64
	sum, sumSq, min, max float64
	firstVal             float64
	firstTS              int64
	lastVal              float64
	lastTS               int64
	digest               *TDigest
}

func (b *queryRollupSummary) merge(count int64, sum, sumSq, min, max, firstVal float64, firstTS int64, lastVal float64, lastTS int64, digest *TDigest, includeDigest bool) {
	if count == 0 {
		return
	}
	if b.count == 0 {
		b.min, b.max = min, max
		b.firstVal, b.firstTS = firstVal, firstTS
		b.lastVal, b.lastTS = lastVal, lastTS
	} else {
		if min < b.min {
			b.min = min
		}
		if max > b.max {
			b.max = max
		}
		if firstTS < b.firstTS {
			b.firstVal, b.firstTS = firstVal, firstTS
		}
		if lastTS > b.lastTS {
			b.lastVal, b.lastTS = lastVal, lastTS
		}
	}
	b.count += count
	b.sum += sum
	b.sumSq += sumSq
	if includeDigest && digest != nil {
		b.digest.Merge(digest)
	}
}

func (b *queryRollupSummary) value(agg Aggregation) (float64, bool) {
	// Reuse the established scalar/percentile semantics without copying metadata
	// into the long-lived query state. This temporary stays on the stack.
	bucket := rollupBucket{count: b.count, sum: b.sum, sumSq: b.sumSq, min: b.min, max: b.max,
		firstVal: b.firstVal, firstTS: b.firstTS, lastVal: b.lastVal, lastTS: b.lastTS, digest: b.digest}
	return bucket.value(agg)
}
