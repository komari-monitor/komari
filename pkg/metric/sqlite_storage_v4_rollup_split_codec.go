package metric

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"sort"
)

const (
	sqliteV4RollupSummaryMagic = "KMS4"
	sqliteV4RollupDigestMagic  = "KMD4"
	sqliteV4RollupDigestCodec  = 1
)

func encodeSQLiteV4RollupBlock(records []sqliteV4RollupRecord) (sqliteV4EncodedRollupBlock, error) {
	if len(records) == 0 {
		return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: cannot encode an empty SQLite V4 rollup block")
	}
	if len(records) > sqliteV4MaxDecodedRollupRows {
		return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup block is too large: %d", len(records))
	}
	records = append([]sqliteV4RollupRecord(nil), records...)
	sort.SliceStable(records, func(i, j int) bool { return records[i].bucketNano < records[j].bucketNano })
	for i := 1; i < len(records); i++ {
		if records[i].bucketNano <= records[i-1].bucketNano {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup buckets must be strictly increasing")
		}
	}

	var summary bytes.Buffer
	summary.WriteString(sqliteV4RollupSummaryMagic)
	appendUvarintTo(&summary, uint64(len(records)))
	if err := encodeSQLiteV4RollupBuckets(&summary, records); err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	for _, record := range records {
		if record.count < 0 {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: negative SQLite V4 rollup count")
		}
		appendUvarintTo(&summary, uint64(record.count))
	}
	floatFields := [sqliteV4RollupFloatFieldCount]func(sqliteV4RollupRecord) uint64{
		func(record sqliteV4RollupRecord) uint64 { return record.sumBits },
		func(record sqliteV4RollupRecord) uint64 { return record.sumSqBits },
		func(record sqliteV4RollupRecord) uint64 { return record.minBits },
		func(record sqliteV4RollupRecord) uint64 { return record.maxBits },
		func(record sqliteV4RollupRecord) uint64 { return record.firstBits },
		func(record sqliteV4RollupRecord) uint64 { return record.lastBits },
	}
	for _, field := range floatFields {
		values := make([]uint64, len(records))
		for i, record := range records {
			values[i] = field(record)
		}
		encoded, bitCount := encodeSQLiteV4FloatBits(values)
		appendUvarintTo(&summary, uint64(bitCount))
		summary.Write(encoded)
	}
	for _, record := range records {
		firstOffset, ok := checkedSubInt64(record.firstTS, record.bucketNano)
		if !ok {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup first timestamp offset overflow")
		}
		lastOffset, ok := checkedSubInt64(record.lastTS, record.bucketNano)
		if !ok {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup last timestamp offset overflow")
		}
		appendVarintTo(&summary, firstOffset)
		appendVarintTo(&summary, lastOffset)
	}
	appendVarintTo(&summary, records[0].createdAt)
	for i := 1; i < len(records); i++ {
		delta, ok := checkedSubInt64(records[i].createdAt, records[i-1].createdAt)
		if !ok {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup creation-time delta overflow")
		}
		appendVarintTo(&summary, delta)
	}

	var digests bytes.Buffer
	digests.WriteString(sqliteV4RollupDigestMagic)
	appendUvarintTo(&digests, uint64(len(records)))
	for _, record := range records {
		rawDigest, err := sqliteV4RawTDigest(record.digest)
		if err != nil {
			return sqliteV4EncodedRollupBlock{}, err
		}
		appendUvarintTo(&digests, uint64(len(rawDigest)))
		digests.Write(rawDigest)
	}

	payload, err := compressSQLiteV4RollupSection(summary.Bytes(), flate.BestSpeed)
	if err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	digestPayload, err := compressSQLiteV4RollupSection(digests.Bytes(), flate.BestCompression)
	if err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	return sqliteV4EncodedRollupBlock{
		startNano:      records[0].bucketNano,
		endNano:        records[len(records)-1].bucketNano,
		count:          len(records),
		codec:          sqliteV4RollupBlockCodec,
		checksum:       crc32.ChecksumIEEE(payload),
		payload:        payload,
		digestCodec:    sqliteV4RollupDigestCodec,
		digestChecksum: crc32.ChecksumIEEE(digestPayload),
		digestPayload:  digestPayload,
	}, nil
}

func decodeSQLiteV4RollupBlock(codec, expectedCount int, expectedChecksum uint32, payload []byte, digestCodec int, expectedDigestChecksum uint32, digestPayload []byte, needDigest bool) ([]sqliteV4RollupRecord, error) {
	if codec == sqliteV4LegacyRollupBlockCodec {
		return decodeSQLiteV4LegacyRollupBlock(codec, expectedCount, expectedChecksum, payload)
	}
	if codec != sqliteV4RollupBlockCodec {
		return nil, fmt.Errorf("metric: unsupported SQLite V4 rollup block codec %d", codec)
	}
	if len(payload) < 2 || crc32.ChecksumIEEE(payload) != expectedChecksum {
		return nil, fmt.Errorf("metric: SQLite V4 rollup summary checksum mismatch")
	}
	raw, err := inflateSQLiteV4Payload(payload)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(raw)
	magic := make([]byte, len(sqliteV4RollupSummaryMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != sqliteV4RollupSummaryMagic {
		return nil, fmt.Errorf("metric: invalid SQLite V4 rollup summary header")
	}
	count64, err := binary.ReadUvarint(reader)
	if err != nil || count64 == 0 || count64 > sqliteV4MaxDecodedRollupRows {
		return nil, fmt.Errorf("metric: invalid SQLite V4 rollup row count")
	}
	count := int(count64)
	if expectedCount >= 0 && count != expectedCount {
		return nil, fmt.Errorf("metric: SQLite V4 rollup count mismatch: header=%d row=%d", count, expectedCount)
	}
	records := make([]sqliteV4RollupRecord, count)
	if err := decodeSQLiteV4RollupBuckets(reader, records); err != nil {
		return nil, err
	}
	for i := range records {
		value, err := binary.ReadUvarint(reader)
		if err != nil || value > uint64(math.MaxInt64) {
			return nil, fmt.Errorf("metric: invalid SQLite V4 rollup count")
		}
		records[i].count = int64(value)
	}
	floatTargets := [sqliteV4RollupFloatFieldCount]func(*sqliteV4RollupRecord, uint64){
		func(record *sqliteV4RollupRecord, value uint64) { record.sumBits = value },
		func(record *sqliteV4RollupRecord, value uint64) { record.sumSqBits = value },
		func(record *sqliteV4RollupRecord, value uint64) { record.minBits = value },
		func(record *sqliteV4RollupRecord, value uint64) { record.maxBits = value },
		func(record *sqliteV4RollupRecord, value uint64) { record.firstBits = value },
		func(record *sqliteV4RollupRecord, value uint64) { record.lastBits = value },
	}
	for _, assign := range floatTargets {
		bitCount, err := binary.ReadUvarint(reader)
		if err != nil || bitCount < 64 || bitCount > uint64(reader.Len())*8 {
			return nil, fmt.Errorf("metric: invalid SQLite V4 rollup float stream")
		}
		byteCount := int((bitCount + 7) / 8)
		encoded := make([]byte, byteCount)
		if _, err := io.ReadFull(reader, encoded); err != nil {
			return nil, err
		}
		values, err := decodeSQLiteV4FloatBits(encoded, int(bitCount), count)
		if err != nil {
			return nil, err
		}
		for i, value := range values {
			assign(&records[i], value)
		}
	}
	for i := range records {
		firstOffset, err := binary.ReadVarint(reader)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 rollup first timestamp: %w", err)
		}
		lastOffset, err := binary.ReadVarint(reader)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 rollup last timestamp: %w", err)
		}
		records[i].firstTS, err = checkedAddInt64(records[i].bucketNano, firstOffset)
		if err != nil {
			return nil, err
		}
		records[i].lastTS, err = checkedAddInt64(records[i].bucketNano, lastOffset)
		if err != nil {
			return nil, err
		}
	}
	records[0].createdAt, err = binary.ReadVarint(reader)
	if err != nil {
		return nil, fmt.Errorf("metric: decode SQLite V4 rollup creation time: %w", err)
	}
	for i := 1; i < len(records); i++ {
		delta, err := binary.ReadVarint(reader)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 rollup creation-time delta: %w", err)
		}
		records[i].createdAt, err = checkedAddInt64(records[i-1].createdAt, delta)
		if err != nil {
			return nil, err
		}
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("metric: SQLite V4 rollup summary contains trailing data")
	}
	if needDigest {
		if err := decodeSQLiteV4RollupDigestSection(records, digestCodec, expectedDigestChecksum, digestPayload); err != nil {
			return nil, err
		}
	}
	return records, nil
}

func decodeSQLiteV4RollupDigestSection(records []sqliteV4RollupRecord, codec int, expectedChecksum uint32, payload []byte) error {
	if codec != sqliteV4RollupDigestCodec {
		return fmt.Errorf("metric: unsupported SQLite V4 rollup digest codec %d", codec)
	}
	if len(payload) < 2 || crc32.ChecksumIEEE(payload) != expectedChecksum {
		return fmt.Errorf("metric: SQLite V4 rollup digest checksum mismatch")
	}
	raw, err := inflateSQLiteV4Payload(payload)
	if err != nil {
		return err
	}
	reader := bytes.NewReader(raw)
	magic := make([]byte, len(sqliteV4RollupDigestMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != sqliteV4RollupDigestMagic {
		return fmt.Errorf("metric: invalid SQLite V4 rollup digest header")
	}
	count, err := binary.ReadUvarint(reader)
	if err != nil || count != uint64(len(records)) {
		return fmt.Errorf("metric: SQLite V4 rollup digest count mismatch")
	}
	for i := range records {
		length, err := binary.ReadUvarint(reader)
		if err != nil || length > uint64(reader.Len()) {
			return fmt.Errorf("metric: invalid SQLite V4 rollup digest length")
		}
		records[i].digest = make([]byte, int(length))
		if _, err := io.ReadFull(reader, records[i].digest); err != nil {
			return err
		}
	}
	if reader.Len() != 0 {
		return fmt.Errorf("metric: SQLite V4 rollup digest contains trailing data")
	}
	return nil
}

func sqliteV4RawTDigest(encoded []byte) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	digest, err := DecodeTDigest(encoded)
	if err != nil {
		return nil, fmt.Errorf("metric: decode SQLite V4 t-digest: %w", err)
	}
	return digest.encodeRaw(), nil
}

func compressSQLiteV4RollupSection(raw []byte, level int) ([]byte, error) {
	payload := append([]byte{sqliteV4PayloadRaw}, raw...)
	var compressed bytes.Buffer
	compressed.WriteByte(sqliteV4PayloadDeflate)
	writer, err := flate.NewWriter(&compressed, level)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if compressed.Len() < len(payload) {
		return append([]byte(nil), compressed.Bytes()...), nil
	}
	return payload, nil
}

func sqliteV4TDigestsEqual(left, right []byte) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	a, err := DecodeTDigest(left)
	if err != nil {
		return false
	}
	b, err := DecodeTDigest(right)
	if err != nil {
		return false
	}
	if math.Float64bits(a.compression) != math.Float64bits(b.compression) ||
		math.Float64bits(a.count) != math.Float64bits(b.count) ||
		math.Float64bits(a.min) != math.Float64bits(b.min) ||
		math.Float64bits(a.max) != math.Float64bits(b.max) || len(a.centroids) != len(b.centroids) {
		return false
	}
	for i := range a.centroids {
		if math.Float64bits(a.centroids[i].mean) != math.Float64bits(b.centroids[i].mean) ||
			math.Float64bits(a.centroids[i].weight) != math.Float64bits(b.centroids[i].weight) {
			return false
		}
	}
	return true
}
