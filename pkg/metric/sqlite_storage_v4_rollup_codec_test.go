package metric

import (
	"math"
	"testing"
)

func TestSQLiteV4RollupCodecRoundTripPreservesBits(t *testing.T) {
	digest := NewTDigest(100)
	for i := 0; i < 200; i++ {
		digest.Add(float64(i)/7, 1)
	}
	digestBlob := digest.Encode()
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
			digest:     digestBlob,
			createdAt:  1_700_000_000_000_000_000 + int64(i*17),
		}
	}
	for start := 0; start < len(records); start += sqliteV4RollupBlockLimit {
		end := min(start+sqliteV4RollupBlockLimit, len(records))
		encoded, err := encodeSQLiteV4RollupBlock(records[start:end])
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload,
			encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, true)
		if err != nil {
			t.Fatal(err)
		}
		if !sqliteV4RollupRecordsEqual(records[start:end], decoded) {
			t.Fatal("SQLite V4 rollup codec changed a float bit pattern, timestamp, count, digest, or creation time")
		}
	}
}

func TestSQLiteV4RollupCodecRejectsCorruption(t *testing.T) {
	digest := NewTDigest(100)
	digest.Add(1, 1)
	record := sqliteV4RollupRecord{
		bucketNano: 1, count: 1,
		sumBits: math.Float64bits(1), sumSqBits: math.Float64bits(1),
		minBits: math.Float64bits(1), maxBits: math.Float64bits(1),
		firstBits: math.Float64bits(1), firstTS: 1,
		lastBits: math.Float64bits(1), lastTS: 1,
		digest: digest.Encode(), createdAt: 2,
	}
	encoded, err := encodeSQLiteV4RollupBlock([]sqliteV4RollupRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	encoded.payload[len(encoded.payload)-1] ^= 0xff
	if _, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload,
		encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, true); err == nil {
		t.Fatal("corrupt SQLite V4 rollup block unexpectedly decoded")
	}
}

func TestSQLiteV4RollupSummaryDecodeDoesNotReadDigestSection(t *testing.T) {
	digest := NewTDigest(100)
	for i := 0; i < 1000; i++ {
		digest.Add(float64(i%137), 1)
	}
	record := sqliteV4RollupRecord{
		bucketNano: 1, count: 1000,
		sumBits: math.Float64bits(1), sumSqBits: math.Float64bits(2),
		minBits: math.Float64bits(0), maxBits: math.Float64bits(136),
		firstBits: math.Float64bits(1), firstTS: 1,
		lastBits: math.Float64bits(2), lastTS: 2,
		digest: digest.Encode(), createdAt: 3,
	}
	encoded, err := encodeSQLiteV4RollupBlock([]sqliteV4RollupRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	encoded.digestPayload[len(encoded.digestPayload)-1] ^= 0xff
	decoded, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload,
		encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, false)
	if err != nil || len(decoded) != 1 || len(decoded[0].digest) != 0 {
		t.Fatalf("summary-only decode touched digest section: records=%d err=%v", len(decoded), err)
	}
	if _, err := decodeSQLiteV4RollupBlock(encoded.codec, encoded.count, encoded.checksum, encoded.payload,
		encoded.digestCodec, encoded.digestChecksum, encoded.digestPayload, true); err == nil {
		t.Fatal("percentile decode unexpectedly accepted a corrupt digest section")
	}
}
