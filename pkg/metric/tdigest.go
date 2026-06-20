package metric

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// TDigest is a mergeable sketch for estimating arbitrary quantiles of a stream
// of float64 values. It is the piece that makes downsampling lossless *enough*
// for percentiles: count/sum/min/max can be re-aggregated exactly when rolling
// finer buckets into coarser ones, but a percentile cannot be recovered from
// those scalars. Storing a small t-digest per rollup bucket lets a coarse
// bucket's pXX be computed by merging the digests of the finer buckets it
// covers, with bounded error and bounded size.
//
// This is the "merging" variant of Dunning & Ertl's t-digest. Centroids near
// the median are allowed to absorb more weight (lower resolution where the CDF
// is flat) while centroids in the tails stay small (high resolution where
// accuracy matters), giving relative error that is small at the extremes — the
// regime that matters for p95/p99/p99.9 latency work.
//
// TDigest 是用于估算 float64 数据流任意分位数的可合并 sketch。它让降采样对
// 百分位来说“足够无损”：count/sum/min/max 可以在把细桶合入粗桶时精确重聚合，
// 但仅靠这些标量无法恢复百分位。每个 rollup 桶保存一个小型 t-digest 后，
// 粗桶的 pXX 就可以通过合并其覆盖的细桶 digest 来计算，并保持有界误差和有界大小。
//
// 这是 Dunning 与 Ertl 的 t-digest 的“merging”变体。靠近中位数的质心允许吸收
// 更多权重（CDF 平坦处使用较低分辨率），尾部质心则保持较小（精度重要处使用
// 较高分辨率），从而在极端位置取得较小相对误差，这正是 p95/p99/p99.9 延迟分析
// 最关心的区间。
type TDigest struct {
	// compression controls the size/accuracy tradeoff.
	//
	// compression 控制大小与精度之间的取舍。
	compression float64
	// centroids stores the digest's weighted clusters.
	//
	// centroids 保存 digest 的加权簇。
	centroids []centroid
	// count is the total observed weight.
	//
	// count 是已观测的总权重。
	count float64
	// min is the minimum observed value.
	//
	// min 是已观测的最小值。
	min float64
	// max is the maximum observed value.
	//
	// max 是已观测的最大值。
	max float64
	// processed reports whether centroids is sorted+merged. Add() appends
	// unprocessed singletons and flips this false; process() restores it.
	//
	// processed 表示 centroids 是否已排序并合并。Add() 会追加未处理的单点并把它
	// 置为 false；process() 会恢复它。
	processed bool
}

// centroid stores one t-digest centroid.
//
// centroid 保存一个 t-digest 质心。
type centroid struct {
	// mean is the centroid's weighted mean.
	//
	// mean 是质心的加权均值。
	mean float64
	// weight is the total weight represented by the centroid.
	//
	// weight 是该质心代表的总权重。
	weight float64
}

const (
	// defaultTDigestCompression is used when a caller supplies no useful value.
	//
	// defaultTDigestCompression 在调用方未提供有效值时使用。
	defaultTDigestCompression = 100.0
	// tdigestMagic0 is the first magic byte in the binary format.
	//
	// tdigestMagic0 是二进制格式的第一个 magic 字节。
	tdigestMagic0 = 'T'
	// tdigestMagic1 is the second magic byte in the binary format.
	//
	// tdigestMagic1 是二进制格式的第二个 magic 字节。
	tdigestMagic1 = 'D'
	// tdigestVersion is the current binary encoding version.
	//
	// tdigestVersion 是当前二进制编码版本。
	tdigestVersion = 1
)

// NewTDigest returns an empty digest. compression trades size for accuracy;
// higher keeps more centroids. Values <= 1 fall back to the default (100),
// which keeps each digest to a few KB while holding tail error to well under
// 1% for typical distributions.
//
// NewTDigest 返回一个空 digest。compression 在大小和精度之间取舍；值越高会保留
// 更多质心。小于等于 1 的值会回退到默认值（100），这通常能把每个 digest 控制在
// 几 KB，同时让典型分布的尾部误差远低于 1%。
func NewTDigest(compression float64) *TDigest {
	if compression <= 1 {
		compression = defaultTDigestCompression
	}
	return &TDigest{
		compression: compression,
		min:         math.Inf(1),
		max:         math.Inf(-1),
		processed:   true,
	}
}

// Add folds a single observation with weight w (w must be > 0) into the digest.
//
// Add 将一个观测值及其权重合入摘要；权重必须大于 0。
func (t *TDigest) Add(x, w float64) {
	if w <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
		return
	}
	t.centroids = append(t.centroids, centroid{mean: x, weight: w})
	t.count += w
	if x < t.min {
		t.min = x
	}
	if x > t.max {
		t.max = x
	}
	t.processed = false
	// Bound the unprocessed buffer so a long stream cannot grow memory without
	// limit; process() collapses it back to ~compression centroids.
	if len(t.centroids) > int(8*t.compression)+16 {
		t.process()
	}
}

// Merge folds every centroid of other into t. This is the operation rollup
// composition relies on: a coarse bucket merges the digests of the finer
// buckets it spans.
//
// Merge 将 other 的每个质心合入 t。rollup 合成依赖这个操作：粗桶会合并它所覆盖的
// 细桶 digest。
func (t *TDigest) Merge(other *TDigest) {
	if other == nil || other.count == 0 {
		return
	}
	other.process()
	for _, c := range other.centroids {
		t.centroids = append(t.centroids, c)
	}
	t.count += other.count
	if other.min < t.min {
		t.min = other.min
	}
	if other.max > t.max {
		t.max = other.max
	}
	t.processed = false
	t.process()
}

// Count returns the total weight observed.
//
// Count 返回已观测样本的总权重。
func (t *TDigest) Count() float64 { return t.count }

// process sorts the buffered centroids by mean and merges adjacent ones while
// the merged weight stays under the quantile-dependent size limit
// 4*N*q*(1-q)/compression. That limit is generous near q=0.5 and tightens to
// near zero in the tails, which is exactly the t-digest accuracy profile.
//
// process 将缓冲质心按均值排序，并在合并后权重仍低于分位数相关大小限制
// 4*N*q*(1-q)/compression 时合并相邻质心。这个限制在 q=0.5 附近较宽松，
// 到尾部会收紧到接近零，这正是 t-digest 的精度分布特征。
func (t *TDigest) process() {
	if t.processed {
		return
	}
	if len(t.centroids) == 0 {
		t.processed = true
		return
	}
	sort.Slice(t.centroids, func(i, j int) bool {
		return t.centroids[i].mean < t.centroids[j].mean
	})
	total := t.count
	merged := t.centroids[:0:0] // fresh backing array; don't alias input mid-merge
	cur := t.centroids[0]
	weightBefore := 0.0
	for i := 1; i < len(t.centroids); i++ {
		next := t.centroids[i]
		proposed := cur.weight + next.weight
		// Quantile at the center of the proposed combined centroid.
		q := (weightBefore + proposed/2) / total
		limit := 4 * total * q * (1 - q) / t.compression
		if proposed <= limit || limit < 1 && proposed <= 1 {
			// Weighted-mean update keeps the centroid's mean exact.
			cur.mean += next.weight * (next.mean - cur.mean) / proposed
			cur.weight = proposed
		} else {
			merged = append(merged, cur)
			weightBefore += cur.weight
			cur = next
		}
	}
	merged = append(merged, cur)
	t.centroids = merged
	t.processed = true
}

// Quantile estimates the value at q in [0,1] using linear interpolation between
// centroid centers, with the extreme tails anchored to the observed min/max.
//
// Quantile 用质心中心之间的线性插值估算 [0,1] 分位点，极端尾部锚定到
// 已观测的最小值和最大值。
func (t *TDigest) Quantile(q float64) float64 {
	t.process()
	n := len(t.centroids)
	if n == 0 {
		return math.NaN()
	}
	if q <= 0 {
		return t.min
	}
	if q >= 1 {
		return t.max
	}
	if n == 1 {
		return t.centroids[0].mean
	}
	index := q * t.count

	// Head: between the observed min and the first centroid's center.
	c0 := t.centroids[0]
	if index < c0.weight/2 {
		z := index / (c0.weight / 2)
		return t.min + (c0.mean-t.min)*z
	}
	weightSoFar := c0.weight / 2
	for i := 0; i < n-1; i++ {
		c := t.centroids[i]
		next := t.centroids[i+1]
		dw := (c.weight + next.weight) / 2
		if index < weightSoFar+dw {
			z := (index - weightSoFar) / dw
			return c.mean*(1-z) + next.mean*z
		}
		weightSoFar += dw
	}
	// Tail: between the last centroid's center and the observed max.
	cl := t.centroids[n-1]
	z := (index - weightSoFar) / (cl.weight / 2)
	if z > 1 {
		z = 1
	}
	return cl.mean + (t.max-cl.mean)*z
}

// Encode serializes the (processed) digest to a compact little-endian blob:
// magic[2] version[1] compression[8] min[8] max[8] count[8] nCentroids[4]
// then nCentroids * (mean[8] weight[8]).
//
// Encode 将处理后的 digest 序列化为紧凑的小端二进制 blob：
// magic[2] version[1] compression[8] min[8] max[8] count[8] nCentroids[4]，
// 后接 nCentroids * (mean[8] weight[8])。
func (t *TDigest) Encode() []byte {
	t.process()
	n := len(t.centroids)
	buf := make([]byte, 0, 3+8*4+4+n*16)
	buf = append(buf, tdigestMagic0, tdigestMagic1, tdigestVersion)
	var tmp [8]byte
	putF := func(f float64) {
		binary.LittleEndian.PutUint64(tmp[:], math.Float64bits(f))
		buf = append(buf, tmp[:]...)
	}
	putF(t.compression)
	putF(t.min)
	putF(t.max)
	putF(t.count)
	var u32 [4]byte
	binary.LittleEndian.PutUint32(u32[:], uint32(n))
	buf = append(buf, u32[:]...)
	for _, c := range t.centroids {
		putF(c.mean)
		putF(c.weight)
	}
	return buf
}

// DecodeTDigest reconstructs a digest produced by Encode. A nil/empty blob
// yields an empty digest so callers can treat "no sketch stored" uniformly.
//
// DecodeTDigest 还原 Encode 生成的摘要；nil 或空 blob 会返回空摘要，
// 方便调用方统一处理“没有保存 sketch”的情况。
func DecodeTDigest(b []byte) (*TDigest, error) {
	if len(b) == 0 {
		return NewTDigest(defaultTDigestCompression), nil
	}
	if len(b) < 3+8*4+4 || b[0] != tdigestMagic0 || b[1] != tdigestMagic1 {
		return nil, errors.New("metric: invalid t-digest blob")
	}
	if b[2] != tdigestVersion {
		return nil, errors.New("metric: unsupported t-digest version")
	}
	off := 3
	getF := func() float64 {
		v := math.Float64frombits(binary.LittleEndian.Uint64(b[off : off+8]))
		off += 8
		return v
	}
	t := &TDigest{processed: true}
	t.compression = getF()
	t.min = getF()
	t.max = getF()
	t.count = getF()
	n := int(binary.LittleEndian.Uint32(b[off : off+4]))
	off += 4
	if n < 0 || off+n*16 > len(b) {
		return nil, errors.New("metric: truncated t-digest blob")
	}
	t.centroids = make([]centroid, n)
	for i := 0; i < n; i++ {
		t.centroids[i].mean = getF()
		t.centroids[i].weight = getF()
	}
	if t.compression <= 1 {
		t.compression = defaultTDigestCompression
	}
	return t, nil
}
