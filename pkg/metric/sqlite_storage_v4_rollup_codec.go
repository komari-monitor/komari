package metric

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math/bits"
	"sort"
)

const (
	sqliteV4LegacyRollupBlockCodec = 1
	sqliteV4RollupBlockCodec       = 2
	sqliteV4RollupBlockLimit       = 512
	sqliteV4RollupBlockMagic       = "KMR4"
	sqliteV4MaxDecodedRollupRows   = 1 << 20
	sqliteV4RollupFloatFieldCount  = 6
)

type sqliteV4RollupRecord struct {
	bucketNano int64
	count      int64
	sumBits    uint64
	sumSqBits  uint64
	minBits    uint64
	maxBits    uint64
	firstBits  uint64
	firstTS    int64
	lastBits   uint64
	lastTS     int64
	digest     []byte
	createdAt  int64
}

type sqliteV4EncodedRollupBlock struct {
	startNano      int64
	endNano        int64
	count          int
	codec          int
	checksum       uint32
	payload        []byte
	digestCodec    int
	digestChecksum uint32
	digestPayload  []byte
}

func encodeSQLiteV4LegacyRollupBlock(records []sqliteV4RollupRecord) (sqliteV4EncodedRollupBlock, error) {
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

	var raw bytes.Buffer
	raw.WriteString(sqliteV4RollupBlockMagic)
	appendUvarintTo(&raw, uint64(len(records)))
	if err := encodeSQLiteV4RollupBuckets(&raw, records); err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	for _, record := range records {
		if record.count < 0 {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: negative SQLite V4 rollup count")
		}
		appendUvarintTo(&raw, uint64(record.count))
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
		appendUvarintTo(&raw, uint64(bitCount))
		raw.Write(encoded)
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
		appendVarintTo(&raw, firstOffset)
		appendVarintTo(&raw, lastOffset)
	}
	for _, record := range records {
		appendUvarintTo(&raw, uint64(len(record.digest)))
		raw.Write(record.digest)
	}
	appendVarintTo(&raw, records[0].createdAt)
	for i := 1; i < len(records); i++ {
		delta, ok := checkedSubInt64(records[i].createdAt, records[i-1].createdAt)
		if !ok {
			return sqliteV4EncodedRollupBlock{}, fmt.Errorf("metric: SQLite V4 rollup creation-time delta overflow")
		}
		appendVarintTo(&raw, delta)
	}

	payload := append([]byte{sqliteV4PayloadRaw}, raw.Bytes()...)
	var compressed bytes.Buffer
	compressed.WriteByte(sqliteV4PayloadDeflate)
	writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
	if err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	if _, err := writer.Write(raw.Bytes()); err != nil {
		_ = writer.Close()
		return sqliteV4EncodedRollupBlock{}, err
	}
	if err := writer.Close(); err != nil {
		return sqliteV4EncodedRollupBlock{}, err
	}
	if compressed.Len() < len(payload) {
		payload = compressed.Bytes()
	}
	return sqliteV4EncodedRollupBlock{
		startNano: records[0].bucketNano,
		endNano:   records[len(records)-1].bucketNano,
		count:     len(records),
		codec:     sqliteV4LegacyRollupBlockCodec,
		checksum:  crc32.ChecksumIEEE(payload),
		payload:   append([]byte(nil), payload...),
	}, nil
}

func decodeSQLiteV4LegacyRollupBlock(codec, expectedCount int, expectedChecksum uint32, payload []byte) ([]sqliteV4RollupRecord, error) {
	if codec != sqliteV4LegacyRollupBlockCodec {
		return nil, fmt.Errorf("metric: unsupported SQLite V4 rollup block codec %d", codec)
	}
	if len(payload) < 2 {
		return nil, fmt.Errorf("metric: truncated SQLite V4 rollup block")
	}
	if crc32.ChecksumIEEE(payload) != expectedChecksum {
		return nil, fmt.Errorf("metric: SQLite V4 rollup block checksum mismatch")
	}
	raw, err := inflateSQLiteV4Payload(payload)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(raw)
	magic := make([]byte, len(sqliteV4RollupBlockMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != sqliteV4RollupBlockMagic {
		return nil, fmt.Errorf("metric: invalid SQLite V4 rollup block header")
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
		if err != nil || value > uint64(^uint64(0)>>1) {
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
	for i := range records {
		length, err := binary.ReadUvarint(reader)
		if err != nil || length > uint64(reader.Len()) {
			return nil, fmt.Errorf("metric: invalid SQLite V4 rollup digest length")
		}
		records[i].digest = make([]byte, int(length))
		if _, err := io.ReadFull(reader, records[i].digest); err != nil {
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
		return nil, fmt.Errorf("metric: SQLite V4 rollup block contains trailing data")
	}
	return records, nil
}

func inflateSQLiteV4Payload(payload []byte) ([]byte, error) {
	switch payload[0] {
	case sqliteV4PayloadRaw:
		return payload[1:], nil
	case sqliteV4PayloadDeflate:
		reader := flate.NewReader(bytes.NewReader(payload[1:]))
		decoded, err := io.ReadAll(io.LimitReader(reader, 128<<20))
		closeErr := reader.Close()
		if err != nil {
			return nil, fmt.Errorf("metric: decompress SQLite V4 rollup block: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("metric: close SQLite V4 rollup block decoder: %w", closeErr)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("metric: unsupported SQLite V4 rollup payload compression %d", payload[0])
	}
}

func encodeSQLiteV4RollupBuckets(dst *bytes.Buffer, records []sqliteV4RollupRecord) error {
	appendVarintTo(dst, records[0].bucketNano)
	if len(records) == 1 {
		return nil
	}
	delta, ok := checkedSubInt64(records[1].bucketNano, records[0].bucketNano)
	if !ok || delta <= 0 {
		return fmt.Errorf("metric: invalid SQLite V4 rollup bucket delta")
	}
	appendVarintTo(dst, delta)
	previousDelta := delta
	for i := 2; i < len(records); i++ {
		delta, ok = checkedSubInt64(records[i].bucketNano, records[i-1].bucketNano)
		if !ok || delta <= 0 {
			return fmt.Errorf("metric: invalid SQLite V4 rollup bucket delta")
		}
		deltaOfDelta, ok := checkedSubInt64(delta, previousDelta)
		if !ok {
			return fmt.Errorf("metric: SQLite V4 rollup bucket delta overflow")
		}
		appendVarintTo(dst, deltaOfDelta)
		previousDelta = delta
	}
	return nil
}

func decodeSQLiteV4RollupBuckets(reader *bytes.Reader, records []sqliteV4RollupRecord) error {
	first, err := binary.ReadVarint(reader)
	if err != nil {
		return fmt.Errorf("metric: decode SQLite V4 first rollup bucket: %w", err)
	}
	records[0].bucketNano = first
	if len(records) == 1 {
		return nil
	}
	previousDelta, err := binary.ReadVarint(reader)
	if err != nil || previousDelta <= 0 {
		return fmt.Errorf("metric: invalid SQLite V4 rollup bucket delta")
	}
	records[1].bucketNano, err = checkedAddInt64(first, previousDelta)
	if err != nil {
		return err
	}
	for i := 2; i < len(records); i++ {
		deltaOfDelta, err := binary.ReadVarint(reader)
		if err != nil {
			return fmt.Errorf("metric: decode SQLite V4 rollup bucket delta: %w", err)
		}
		delta, ok := checkedAddInt64Value(previousDelta, deltaOfDelta)
		if !ok || delta <= 0 {
			return fmt.Errorf("metric: invalid SQLite V4 rollup bucket delta")
		}
		records[i].bucketNano, err = checkedAddInt64(records[i-1].bucketNano, delta)
		if err != nil {
			return err
		}
		previousDelta = delta
	}
	return nil
}

func encodeSQLiteV4FloatBits(values []uint64) ([]byte, int) {
	writer := newSQLiteV4BitWriter()
	writer.writeBits(values[0], 64)
	previous := values[0]
	for _, value := range values[1:] {
		xor := previous ^ value
		if xor == 0 {
			writer.writeBit(false)
			previous = value
			continue
		}
		writer.writeBit(true)
		leading := bits.LeadingZeros64(xor)
		trailing := bits.TrailingZeros64(xor)
		significant := 64 - leading - trailing
		writer.writeBits(uint64(leading), 6)
		if significant == 64 {
			writer.writeBits(0, 6)
		} else {
			writer.writeBits(uint64(significant), 6)
		}
		writer.writeBits(xor>>trailing, significant)
		previous = value
	}
	return writer.bytes()
}

func decodeSQLiteV4FloatBits(encoded []byte, bitCount, count int) ([]uint64, error) {
	reader := newSQLiteV4BitReader(encoded, bitCount)
	first, err := reader.readBits(64)
	if err != nil {
		return nil, err
	}
	values := make([]uint64, count)
	values[0] = first
	previous := first
	for i := 1; i < count; i++ {
		changed, err := reader.readBit()
		if err != nil {
			return nil, err
		}
		if !changed {
			values[i] = previous
			continue
		}
		leading, err := reader.readBits(6)
		if err != nil {
			return nil, err
		}
		significantBits, err := reader.readBits(6)
		if err != nil {
			return nil, err
		}
		significant := int(significantBits)
		if significant == 0 {
			significant = 64
		}
		trailing := 64 - int(leading) - significant
		if trailing < 0 {
			return nil, fmt.Errorf("metric: invalid SQLite V4 rollup float window")
		}
		xor, err := reader.readBits(significant)
		if err != nil {
			return nil, err
		}
		previous ^= xor << trailing
		values[i] = previous
	}
	return values, nil
}

func sqliteV4RollupRecordsEqual(left, right []sqliteV4RollupRecord) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].bucketNano != right[i].bucketNano || left[i].count != right[i].count ||
			left[i].sumBits != right[i].sumBits || left[i].sumSqBits != right[i].sumSqBits ||
			left[i].minBits != right[i].minBits || left[i].maxBits != right[i].maxBits ||
			left[i].firstBits != right[i].firstBits || left[i].firstTS != right[i].firstTS ||
			left[i].lastBits != right[i].lastBits || left[i].lastTS != right[i].lastTS ||
			left[i].createdAt != right[i].createdAt || !sqliteV4TDigestsEqual(left[i].digest, right[i].digest) {
			return false
		}
	}
	return true
}
