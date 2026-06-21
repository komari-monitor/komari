package metric

import (
	"fmt"
	"math"
	"time"
)

// RollupTier describes one downsampled resolution: raw points (or the next
// finer tier) are aggregated into buckets Interval wide, and those buckets are
// kept for Retention. A policy lists tiers from finest to coarsest, e.g.
//
//	1m kept 7d  ->  5m kept 30d  ->  1h kept 1y
//
// As data ages it falls off the finer tiers and survives only in coarser ones,
// so storage shrinks with age — the "data gets sparser as it gets older"
// behavior of a downsampling TSDB.
//
// RollupTier 描述一个降采样分辨率：原始点（或下一层更细的层级）会被聚合进
// Interval 宽的桶，而这些桶会保留 Retention。策略会按从细到粗排列层级，例如：
//
//	1m 保留 7d  ->  5m 保留 30d  ->  1h 保留 1y
//
// 随着数据变旧，它会从更细层级中过期，只在更粗层级中保留下来，因此存储量会随
// 数据年龄下降，也就是降采样 TSDB 中“数据越旧越稀疏”的行为。
type RollupTier struct {
	// Interval is the bucket width for this rollup tier.
	//
	// Interval 是该 rollup 层的桶宽。
	Interval time.Duration `json:"interval"`
	// Retention is how long buckets in this tier are kept.
	//
	// Retention 是该层桶的保留时间。
	Retention time.Duration `json:"retention"`
}

// RollupPolicy is the full retention ladder for a store: how long raw points
// live, then a chain of progressively coarser tiers. Compact materializes the
// tiers and enforces every retention window.
//
// RollupPolicy 描述完整保留阶梯：原始点保留多久，以及后续逐级变粗的
// rollup 层；Compact 会物化这些层并执行保留窗口。
type RollupPolicy struct {
	// RawRetention is how long raw points are kept before Compact deletes them
	// (after they have been rolled into the finest tier). Zero means "never
	// delete raw" — rollups are still built, but raw is retained.
	//
	// RawRetention 是 Compact 删除原始点之前保留它们的时间（在它们已进入最细
	// rollup 层之后）。零值表示“永不删除原始点”；rollup 仍会构建，但原始点保留。
	RawRetention time.Duration `json:"raw_retention"`
	// Tiers are ordered finest-first. Each Interval must be a positive integer
	// multiple of the previous tier's Interval (so a coarse bucket is composed
	// of whole finer buckets), and each Retention must be >= the previous
	// tier's Retention (coarse data outlives fine data).
	//
	// Tiers 按从细到粗排序。每个 Interval 必须是前一层 Interval 的正整数倍
	// （这样粗桶由完整细桶组成），每个 Retention 必须 >= 前一层 Retention
	// （粗数据比细数据活得更久）。
	Tiers []RollupTier `json:"tiers"`
	// Compression tunes the per-bucket t-digest (size vs. percentile accuracy).
	// <=1 uses the default (100).
	//
	// Compression 调整每个桶的 t-digest（大小与百分位精度之间的取舍）。
	// <=1 时使用默认值（100）。
	Compression float64 `json:"compression"`
}

// Enabled reports whether the policy actually defines any rollup tiers.
//
// Enabled 表示策略是否实际定义了 rollup 层级。
func (p RollupPolicy) Enabled() bool { return len(p.Tiers) > 0 }

// compression returns the effective t-digest compression for a rollup policy.
//
// compression 返回 rollup 策略实际使用的 t-digest 压缩参数。
func (p RollupPolicy) compression() float64 {
	if p.Compression <= 1 {
		return defaultTDigestCompression
	}
	return p.Compression
}

func (p RollupPolicy) rawCutoff(now time.Time) time.Time {
	if p.RawRetention <= 0 || len(p.Tiers) == 0 {
		return time.Time{}
	}
	cutoff := now.UTC().Add(-p.RawRetention)
	interval := p.Tiers[0].Interval.Nanoseconds()
	return time.Unix(0, floorDivNano(cutoff.UnixNano(), interval)).UTC()
}

// Validate enforces the structural rules that make cascading composition and
// retention well-defined.
//
// Validate 检查策略结构是否满足级联合成和保留语义所需的约束。
func (p RollupPolicy) Validate() error {
	if len(p.Tiers) == 0 {
		return nil // a store with no tiers simply does no rollup work
	}
	if p.RawRetention < 0 {
		return fmt.Errorf("%w: raw retention cannot be negative", ErrInvalidArgument)
	}
	var prev RollupTier
	for i, tr := range p.Tiers {
		if tr.Interval <= 0 {
			return fmt.Errorf("%w: tier %d interval must be positive", ErrInvalidArgument, i)
		}
		if tr.Retention <= 0 {
			return fmt.Errorf("%w: tier %d retention must be positive", ErrInvalidArgument, i)
		}
		if i == 0 {
			// The finest tier rolls up raw points. Require raw retention (when
			// set) to cover at least two of its buckets so the in-progress and
			// most-recent sealed bucket still have their raw backing when Compact
			// runs and deletes old raw.
			if p.RawRetention > 0 && p.RawRetention < 2*tr.Interval {
				return fmt.Errorf("%w: raw retention must be >= 2x the finest tier interval", ErrInvalidArgument)
			}
		} else {
			if tr.Interval <= prev.Interval {
				return fmt.Errorf("%w: tier %d interval must be larger than tier %d", ErrInvalidArgument, i, i-1)
			}
			if tr.Interval%prev.Interval != 0 {
				return fmt.Errorf("%w: tier %d interval must be a multiple of tier %d interval", ErrInvalidArgument, i, i-1)
			}
			if tr.Retention < prev.Retention {
				return fmt.Errorf("%w: tier %d retention must be >= tier %d retention", ErrInvalidArgument, i, i-1)
			}
		}
		prev = tr
	}
	return nil
}

// rollupBucket is the in-memory accumulator for one (metric, entity,
// resolution, bucket) cell. It carries exactly the summaries that can be
// re-aggregated losslessly when composing coarser tiers — count/sum/sumSq for
// avg & population stddev, min/max, first/last by timestamp — plus a t-digest
// so arbitrary percentiles survive downsampling with bounded error.
//
// rollupBucket 是单个（指标、实体、分辨率、桶）单元的内存累加器。它只携带在
// 合成更粗层级时可无损重新聚合的摘要：用于平均值和总体标准差的 count/sum/sumSq，
// min/max，按时间记录的 first/last，外加一个 t-digest，让任意百分位能以
// 有界误差在降采样后保留下来。
type rollupBucket struct {
	// count is the total number of raw points represented.
	//
	// count 是该桶代表的原始点总数。
	count int64
	// sum is the sum of represented values.
	//
	// sum 是该桶代表值的总和。
	sum float64
	// sumSq is the sum of squared values for population stddev.
	//
	// sumSq 是用于总体标准差的平方和。
	sumSq float64
	// min is the minimum represented value.
	//
	// min 是该桶代表的最小值。
	min float64
	// max is the maximum represented value.
	//
	// max 是该桶代表的最大值。
	max float64
	// firstVal is the value with the earliest timestamp.
	//
	// firstVal 是时间戳最早的值。
	firstVal float64
	// firstTS is the earliest timestamp in nanoseconds.
	//
	// firstTS 是最早时间戳的纳秒值。
	firstTS int64
	// lastVal is the value with the latest timestamp.
	//
	// lastVal 是时间戳最晚的值。
	lastVal float64
	// lastTS is the latest timestamp in nanoseconds.
	//
	// lastTS 是最晚时间戳的纳秒值。
	lastTS int64
	// digest estimates percentiles for represented values.
	//
	// digest 用于估算该桶代表值的百分位。
	digest *TDigest
	// tagsHash carries the stable tag-set fingerprint for this rollup cell.
	//
	// tagsHash 携带该 rollup 单元的稳定标签集合指纹。
	tagsHash string
	// tagsJSON carries the canonical tag map written back to the tags column.
	//
	// tagsJSON 携带写回 tags 列的规范标签 map。
	tagsJSON string
}

// newRollupBucket creates an empty rollup accumulator.
//
// newRollupBucket 创建一个空的 rollup 累加器。
func newRollupBucket(compression float64) *rollupBucket {
	return &rollupBucket{
		min:     0,
		max:     0,
		firstTS: 0,
		lastTS:  0,
		digest:  NewTDigest(compression),
	}
}

// addPoint folds a raw observation into the bucket.
//
// addPoint 将一个原始观测值合入当前桶。
func (b *rollupBucket) addPoint(value float64, tsNano int64) {
	if b.count == 0 {
		b.min, b.max = value, value
		b.firstVal, b.firstTS = value, tsNano
		b.lastVal, b.lastTS = value, tsNano
	} else {
		if value < b.min {
			b.min = value
		}
		if value > b.max {
			b.max = value
		}
		if tsNano < b.firstTS {
			b.firstVal, b.firstTS = value, tsNano
		}
		if tsNano > b.lastTS {
			b.lastVal, b.lastTS = value, tsNano
		}
	}
	b.count++
	b.sum += value
	b.sumSq += value * value
	b.digest.Add(value, 1)
}

// mergeStored folds a finer rollup row (already-summarized) into this coarser
// bucket. This is the cascade step: tier i+1 buckets are built by merging the
// tier i rows they span.
//
// mergeStored 将一个已汇总的细层 rollup 行合入当前更粗桶。这是级联步骤：
// tier i+1 的桶通过合并其覆盖的 tier i 行构建。
func (b *rollupBucket) mergeStored(o *rollupBucket) {
	if o.count == 0 {
		return
	}
	if b.count == 0 {
		b.min, b.max = o.min, o.max
		b.firstVal, b.firstTS = o.firstVal, o.firstTS
		b.lastVal, b.lastTS = o.lastVal, o.lastTS
	} else {
		if o.min < b.min {
			b.min = o.min
		}
		if o.max > b.max {
			b.max = o.max
		}
		if o.firstTS < b.firstTS {
			b.firstVal, b.firstTS = o.firstVal, o.firstTS
		}
		if o.lastTS > b.lastTS {
			b.lastVal, b.lastTS = o.lastVal, o.lastTS
		}
	}
	b.count += o.count
	b.sum += o.sum
	b.sumSq += o.sumSq
	if b.digest == nil {
		b.digest = NewTDigest(defaultTDigestCompression)
	}
	b.digest.Merge(o.digest)
}

// value computes the requested aggregation from the bucket summaries. ok=false
// means the aggregation is not derivable from a rollup (only AggRate, which
// needs the ordered raw series).
//
// value 从桶摘要中计算请求的聚合值；ok=false 表示该聚合无法由 rollup 推导
// （目前主要是需要有序原始序列的 AggRate）。
func (b *rollupBucket) value(agg Aggregation) (float64, bool) {
	switch agg {
	case AggAvg:
		if b.count == 0 {
			return 0, true
		}
		return b.sum / float64(b.count), true
	case AggMin:
		return b.min, true
	case AggMax:
		return b.max, true
	case AggSum:
		return b.sum, true
	case AggCount:
		return float64(b.count), true
	case AggFirst:
		return b.firstVal, true
	case AggLast:
		return b.lastVal, true
	case AggStdDev:
		if b.count == 0 {
			return 0, true
		}
		mean := b.sum / float64(b.count)
		variance := b.sumSq/float64(b.count) - mean*mean
		if variance < 0 {
			variance = 0 // floating-point guard
		}
		return math.Sqrt(variance), true
	case AggRate:
		return 0, false // rate needs the ordered raw series; not in a rollup
	default:
		if frac, ok := parsePercentile(agg); ok {
			if b.digest == nil || b.digest.Count() == 0 {
				return 0, true
			}
			return b.digest.Quantile(frac), true
		}
		return 0, false
	}
}
