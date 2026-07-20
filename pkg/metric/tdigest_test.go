package metric

import (
	"bytes"
	"compress/flate"
	"io"
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

func TestTDigestCompressionIsLosslessAndReadsLegacyFormat(t *testing.T) {
	td := NewTDigest(100)
	for i := 0; i < 600; i++ {
		td.Add(float64(i%20), 1)
	}
	legacy := td.encodeRaw()
	compressed := td.Encode()
	if len(compressed) >= len(legacy) {
		t.Fatalf("compressed digest size = %d, want less than legacy %d", len(compressed), len(legacy))
	}
	if len(compressed) < 3 || compressed[0] != tdigestMagic0 || compressed[1] != tdigestCompressedMagic1 {
		t.Fatalf("unexpected compressed digest header: %v", compressed[:min(3, len(compressed))])
	}
	for name, blob := range map[string][]byte{"legacy": legacy, "compressed": compressed} {
		decoded, err := DecodeTDigest(blob)
		if err != nil {
			t.Fatalf("decode %s digest: %v", name, err)
		}
		if decoded.Count() != td.Count() {
			t.Fatalf("%s digest count = %v, want %v", name, decoded.Count(), td.Count())
		}
		for _, q := range []float64{0.5, 0.7, 0.95, 0.99} {
			if got, want := decoded.Quantile(q), td.Quantile(q); math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("%s digest q=%v = %v, want bit-identical %v", name, q, got, want)
			}
		}
	}
	reader := flate.NewReader(bytes.NewReader(compressed[3:]))
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress digest payload: %v", err)
	}
	_ = reader.Close()
	if !bytes.Equal(decompressed, legacy) {
		t.Fatal("compressed digest payload does not reproduce the legacy bytes")
	}
}
