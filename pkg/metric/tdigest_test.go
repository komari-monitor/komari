package metric

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// exactQuantile computes an exact quantile for a sample slice.
//
// exactQuantile 为样本切片计算精确分位数。
func exactQuantile(xs []float64, q float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return percentileSorted(s, q)
}

// TestTDigestAccuracySmoke checks basic t-digest accuracy.
//
// TestTDigestAccuracySmoke 检查 t-digest 的基本精度。
func TestTDigestAccuracySmoke(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var xs []float64
	td := NewTDigest(100)
	for i := 0; i < 100000; i++ {
		// Exponential-ish: skewed, heavy right tail (latency-like).
		x := math.Abs(rng.NormFloat64())*100 + rng.ExpFloat64()*50
		xs = append(xs, x)
		td.Add(x, 1)
	}
	for _, q := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
		exact := exactQuantile(xs, q)
		est := td.Quantile(q)
		relErr := math.Abs(est-exact) / math.Abs(exact)
		t.Logf("q=%.3f exact=%.3f est=%.3f relErr=%.4f", q, exact, est, relErr)
		if relErr > 0.02 {
			t.Errorf("q=%.3f relErr %.4f exceeds 2%%", q, relErr)
		}
	}
}

// Rollup composition: many finer buckets, each a digest over points drawn from
// the SAME distribution, merged into one coarse digest. Its quantiles must
// track the quantiles of all the raw points combined.
//
// 该测试模拟 rollup 合成：多个细桶的 digest 合并成一个粗桶 digest，合并后
// 的分位数应接近所有原始点合在一起计算出的精确分位数。
func TestTDigestMergeMatchesCombined(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var all []float64
	coarse := NewTDigest(100)
	for b := 0; b < 24; b++ { // 24 fine buckets -> 1 coarse
		fine := NewTDigest(100)
		for i := 0; i < 5000; i++ {
			x := math.Abs(rng.NormFloat64())*80 + 100
			all = append(all, x)
			fine.Add(x, 1)
		}
		coarse.Merge(fine)
	}
	if coarse.Count() != float64(len(all)) {
		t.Fatalf("merged weight %v != %d points", coarse.Count(), len(all))
	}
	for _, q := range []float64{0.5, 0.9, 0.95, 0.99, 0.999} {
		exact := exactQuantile(all, q)
		est := coarse.Quantile(q)
		relErr := math.Abs(est-exact) / math.Abs(exact)
		t.Logf("merged q=%.3f exact=%.3f est=%.3f relErr=%.4f", q, exact, est, relErr)
		if relErr > 0.02 {
			t.Errorf("merged q=%.3f relErr %.4f exceeds 2%%", q, relErr)
		}
	}
}

func TestTDigestMergeGroupingAccuracy(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	all := make([]float64, 0, 24*2000)
	fine := make([]*TDigest, 24)
	for bucket := range fine {
		fine[bucket] = NewTDigest(100)
		for i := 0; i < 2000; i++ {
			value := math.Abs(rng.NormFloat64())*70 + rng.ExpFloat64()*30
			fine[bucket].Add(value, 1)
			all = append(all, value)
		}
	}

	flat := NewTDigest(100)
	grouped := NewTDigest(100)
	for _, digest := range fine {
		flat.Merge(digest)
	}
	for groupStart := 0; groupStart < len(fine); groupStart += 4 {
		group := NewTDigest(100)
		for _, digest := range fine[groupStart : groupStart+4] {
			group.Merge(digest)
		}
		grouped.Merge(group)
	}

	for name, digest := range map[string]*TDigest{"flat": flat, "grouped": grouped} {
		if digest.Count() != float64(len(all)) || digest.min != exactQuantile(all, 0) || digest.max != exactQuantile(all, 1) {
			t.Fatalf("%s exact aggregates changed: count=%v min=%v max=%v", name, digest.Count(), digest.min, digest.max)
		}
		for _, q := range []float64{0.5, 0.95, 0.99, 0.999} {
			exact := exactQuantile(all, q)
			relErr := math.Abs(digest.Quantile(q)-exact) / math.Abs(exact)
			if relErr > 0.02 {
				t.Fatalf("%s q=%v relative error %v exceeds 2%%", name, q, relErr)
			}
		}
	}
}

func TestTDigestMergeDefersProcessingUntilThreshold(t *testing.T) {
	source := NewTDigest(30)
	source.Add(42, 1)
	if source.processed {
		t.Fatal("source should have buffered centroid before merge")
	}

	merged := NewTDigest(30)
	for i := 0; i <= merged.processThreshold(); i++ {
		merged.Merge(source)
	}
	if !merged.processed {
		t.Fatal("merge did not process after exceeding threshold")
	}
	if source.processed {
		t.Fatal("merge processed the source digest")
	}

	merged.Merge(source)
	if merged.processed {
		t.Fatal("merge below threshold should remain buffered")
	}
	if merged.Count() != float64(merged.processThreshold()+2) {
		t.Fatalf("count = %v", merged.Count())
	}
	if merged.processed {
		t.Fatal("Count processed buffered centroids")
	}
	if value := merged.Quantile(0.5); value != 42 || !merged.processed {
		t.Fatalf("Quantile did not finalize digest: value=%v processed=%v", value, merged.processed)
	}

	merged.Merge(source)
	if merged.processed {
		t.Fatal("merge below threshold should remain buffered before Encode")
	}
	encoded := merged.Encode()
	if len(encoded) == 0 || !merged.processed {
		t.Fatal("Encode did not finalize digest")
	}
	decoded, err := DecodeTDigest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Count() != merged.Count() || decoded.min != merged.min || decoded.max != merged.max {
		t.Fatalf("round trip changed exact aggregates: %#v vs %#v", decoded, merged)
	}
}

// TestTDigestEncodeRoundTrip verifies t-digest serialization.
//
// TestTDigestEncodeRoundTrip 验证 t-digest 编码和解码往返。
func TestTDigestEncodeRoundTrip(t *testing.T) {
	td := NewTDigest(50)
	for i := 0; i < 2000; i++ {
		td.Add(float64(i%137)+0.5, 1)
	}
	blob := td.Encode()
	back, err := DecodeTDigest(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Count() != td.Count() {
		t.Fatalf("count mismatch: %v vs %v", back.Count(), td.Count())
	}
	for _, q := range []float64{0.1, 0.5, 0.9, 0.99} {
		if math.Abs(back.Quantile(q)-td.Quantile(q)) > 1e-9 {
			t.Fatalf("q=%v mismatch after round-trip: %v vs %v", q, back.Quantile(q), td.Quantile(q))
		}
	}
	// Empty blob -> empty digest, no error.
	if _, err := DecodeTDigest(nil); err != nil {
		t.Fatalf("decode nil: %v", err)
	}
}

func TestStoredTDigestCompressionRoundTrip(t *testing.T) {
	td := NewTDigest(30)
	for i := 0; i < 4000; i++ {
		td.Add(float64(i%97)+float64(i%11)/10, 1)
	}
	legacy := td.Encode()
	stored := encodeStoredTDigest(td)
	if len(stored) == 0 || stored[0] != 'T' || (stored[1] != storedDigestTypeZstd && stored[1] != storedDigestTypeRaw) {
		t.Fatalf("unexpected stored digest envelope: %q", stored[:minInt(len(stored), 3)])
	}
	back, err := DecodeTDigest(stored)
	if err != nil {
		t.Fatalf("decode stored digest: %v", err)
	}
	for _, q := range []float64{0.05, 0.5, 0.95, 0.99} {
		if math.Abs(back.Quantile(q)-td.Quantile(q)) > 1e-9 {
			t.Fatalf("stored q=%v mismatch: %v vs %v", q, back.Quantile(q), td.Quantile(q))
		}
	}
	legacyBack, err := DecodeTDigest(legacy)
	if err != nil || math.Abs(legacyBack.Quantile(0.95)-td.Quantile(0.95)) > 1e-9 {
		t.Fatalf("legacy digest compatibility failed: %v", err)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
