package metric

import "math"

// Pool blocks instead of individual buckets: large queries acquire once per
// block, and scalar summaries share the allocation with their owner.
const (
	queryStateBlockSize    = 64
	queryStateCachedBlocks = 64
	queryStateMaxCentroids = 256
)

type queryStateSlot struct {
	state  rollupAggregateState
	bucket queryRollupSummary
	digest *TDigest
}

type queryStateBlock struct {
	slots [queryStateBlockSize]queryStateSlot
	used  int
	next  *queryStateBlock
}

// A bounded cache avoids retaining the high-water mark of a fleet query.
// At most 4096 slots are cached; in-flight query storage is not limited.
var queryStateBlocks = make(chan *queryStateBlock, queryStateCachedBlocks)

// queryStateArena belongs to one SeriesBatch invocation, never to Store. No
// returned AggregatePoint refers to its mutable storage (tags are only borrowed).
// Both cached block count and per-slot buffers are bounded. Overflow blocks
// become garbage; only in-flight query storage grows with result cardinality.
type queryStateArena struct {
	head *queryStateBlock
}

func (a *queryStateArena) acquire(compression float64, summary, digest bool) *rollupAggregateState {
	block := a.head
	if block == nil || block.used == len(block.slots) {
		select {
		case block = <-queryStateBlocks:
		default:
			block = new(queryStateBlock)
		}
		block.next = a.head
		a.head = block
	}
	slot := &block.slots[block.used]
	block.used++
	if summary {
		slot.state.summary = &slot.bucket
		if digest {
			if compression <= 1 {
				compression = defaultTDigestCompression
			}
			if slot.digest == nil {
				slot.digest = new(TDigest)
			}
			slot.digest.compression = compression
			slot.digest.min = math.Inf(1)
			slot.digest.max = math.Inf(-1)
			slot.digest.processed = true
			slot.bucket.digest = slot.digest
		}
	}
	return &slot.state
}

func (a *queryStateArena) release() {
	for block := a.head; block != nil; {
		next := block.next
		for i := 0; i < block.used; i++ {
			slot := &block.slots[i]
			if slot.digest != nil {
				centroids := slot.digest.centroids
				if cap(centroids) > queryStateMaxCentroids {
					centroids = nil
				}
				*slot.digest = TDigest{centroids: centroids[:0]}
			}
			// Drop borrowed tags and all bucket metadata before publishing to the pool.
			slot.state = rollupAggregateState{}
			slot.bucket = queryRollupSummary{}
		}
		block.used, block.next = 0, nil
		select {
		case queryStateBlocks <- block:
		default: // Full: do not retain an oversized/concurrent query high-water mark.
		}
		block = next
	}
	a.head = nil
}
