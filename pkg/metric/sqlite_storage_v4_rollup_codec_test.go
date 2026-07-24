package metric

import (
	"math"
	"testing"
)

func TestSQLiteV4RollupCodecRoundTripPreservesBits(t *testing.T) {
	records := make([]sqliteV4RollupRecord, 700)
	for i := range records {
		bucket := int64(-9_000_000_000 + i*60_000_000_000)
		records[i] = sqliteV4RollupRecord{
			bucketNano: bucket,
			count:      int64(10 + i%7),
			sumBits:    math.Float64bits(float64(i)*1.25 + 100),
			sumSqBits:  math.Float64bits(float64(i*i) + 0.5),
			minBits:    math.Float64bits(float64(i) - 7.5),
			maxBits:    math.Float64bits(float64(i) + 9.5),
			firstBits:  math.Float64bits(float64(i) + 0.125),
			firstTS:    bucket + int64(i%11),
			lastBits:   math.Float64bits(float64(i) + 0.875),
			lastTS:     bucket + 59_999_999_999 - int64(i%13),
			digest:     []byte{byte(i), byte(i >> 8), 0, 1, 2, 3},
			createdAt:  1_700_000_000_000_000_000 + int64(i*17),
		}
	}
	for start := 0; start < len(records); start += sqliteV4RollupBlockLimit {
		end := min(start+sqliteV4RollupBlockLimit, len(records))
		encoded, err := encodeSQLiteV4RollupBlock(records[start:end])
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload)
		if err != nil {
			t.Fatal(err)
		}
		if !sqliteV4RollupRecordsEqual(records[start:end], decoded) {
			t.Fatal("SQLite V4 rollup codec changed a float bit pattern, timestamp, count, digest, or creation time")
		}
	}
}

func TestSQLiteV4RollupCodecRejectsCorruption(t *testing.T) {
	record := sqliteV4RollupRecord{
		bucketNano: 1, count: 1,
		sumBits: math.Float64bits(1), sumSqBits: math.Float64bits(1),
		minBits: math.Float64bits(1), maxBits: math.Float64bits(1),
		firstBits: math.Float64bits(1), firstTS: 1,
		lastBits: math.Float64bits(1), lastTS: 1,
		digest: []byte("digest"), createdAt: 2,
	}
	encoded, err := encodeSQLiteV4RollupBlock([]sqliteV4RollupRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	encoded.payload[len(encoded.payload)-1] ^= 0xff
	if _, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload); err == nil {
		t.Fatal("corrupt SQLite V4 rollup block unexpectedly decoded")
	}
}
