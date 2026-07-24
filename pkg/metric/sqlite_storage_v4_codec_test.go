package metric

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestSQLiteV4BlockCodecPreservesEveryBit(t *testing.T) {
	rng := rand.New(rand.NewSource(47))
	base := time.Date(2026, 7, 24, 9, 0, 0, 123, time.UTC).UnixNano()
	special := []uint64{
		math.Float64bits(0), math.Float64bits(math.Copysign(0, -1)),
		math.Float64bits(math.SmallestNonzeroFloat64), math.Float64bits(math.MaxFloat64),
		0x7ff8000000001234, 0xfff0000000000000,
	}
	points := make([]sqliteV4BlockPoint, 4096)
	for i := range points {
		valueBits := math.Float64bits(1000 + math.Sin(float64(i)/10))
		if i < len(special) {
			valueBits = special[i]
		} else if i%127 == 0 {
			valueBits = rng.Uint64()
		}
		points[i] = sqliteV4BlockPoint{
			timestamp: base + int64(i)*3*time.Second.Nanoseconds() + int64(i%11),
			valueBits: valueBits,
			labels:    []string{`{}`, `{"source":"agent"}`, `{"iface":"eth0"}`}[i%3],
			createdAt: base + int64(i/20)*time.Millisecond.Nanoseconds(),
		}
	}
	encoded, err := encodeSQLiteV4Block(points)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSQLiteV4Block(encoded.codec, encoded.count, encoded.checksum, encoded.payload)
	if err != nil {
		t.Fatal(err)
	}
	if !sqliteV4PointsEqual(points, decoded) {
		t.Fatal("SQLite V4 block round trip changed a timestamp, float bit pattern, label, or creation time")
	}
}

func TestSQLiteV4BlockCodecRejectsCorruption(t *testing.T) {
	points := []sqliteV4BlockPoint{{timestamp: 1, valueBits: math.Float64bits(1.25), labels: `{}`, createdAt: 2}}
	encoded, err := encodeSQLiteV4Block(points)
	if err != nil {
		t.Fatal(err)
	}
	encoded.payload[len(encoded.payload)-1] ^= 0x80
	if _, err := decodeSQLiteV4Block(encoded.codec, encoded.count, encoded.checksum, encoded.payload); err == nil {
		t.Fatal("corrupt SQLite V4 block unexpectedly decoded")
	}
}

func TestSQLiteV4BlockCodecCompressesRepresentativeSeries(t *testing.T) {
	base := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC).UnixNano()
	points := make([]sqliteV4BlockPoint, sqliteV4BlockPointLimit)
	for i := range points {
		points[i] = sqliteV4BlockPoint{
			timestamp: base + int64(i)*3*time.Second.Nanoseconds(),
			valueBits: math.Float64bits(float64(10_000_000 + i*1024)),
			labels:    `{}`,
			createdAt: base,
		}
	}
	encoded, err := encodeSQLiteV4Block(points)
	if err != nil {
		t.Fatal(err)
	}
	const v3ApproximateBytesPerPoint = 32
	if len(encoded.payload) >= len(points)*v3ApproximateBytesPerPoint/2 {
		t.Fatalf("representative V4 payload is not at least 50%% smaller: payload=%d points=%d", len(encoded.payload), len(points))
	}
}
