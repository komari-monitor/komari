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
	sqliteV4BlockCodec       = 1
	sqliteV4BlockPointLimit  = 512
	sqliteV4BlockMagic       = "KMV4"
	sqliteV4PayloadRaw       = byte(0)
	sqliteV4PayloadDeflate   = byte(1)
	sqliteV4MaxDecodedPoints = 1 << 20
)

type sqliteV4BlockPoint struct {
	timestamp int64
	valueBits uint64
	labels    string
	createdAt int64
}

type sqliteV4EncodedBlock struct {
	startNano int64
	endNano   int64
	count     int
	codec     int
	checksum  uint32
	payload   []byte
}

func encodeSQLiteV4Block(points []sqliteV4BlockPoint) (sqliteV4EncodedBlock, error) {
	if len(points) == 0 {
		return sqliteV4EncodedBlock{}, fmt.Errorf("metric: cannot encode an empty SQLite V4 point block")
	}
	if len(points) > sqliteV4MaxDecodedPoints {
		return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 point block is too large: %d", len(points))
	}
	points = append([]sqliteV4BlockPoint(nil), points...)
	sort.SliceStable(points, func(i, j int) bool { return points[i].timestamp < points[j].timestamp })
	for i := 1; i < len(points); i++ {
		if points[i].timestamp <= points[i-1].timestamp {
			return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 block timestamps must be strictly increasing")
		}
	}

	var raw bytes.Buffer
	raw.WriteString(sqliteV4BlockMagic)
	appendUvarintTo(&raw, uint64(len(points)))
	appendVarintTo(&raw, points[0].timestamp)
	if len(points) > 1 {
		delta, ok := checkedSubInt64(points[1].timestamp, points[0].timestamp)
		if !ok {
			return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 timestamp delta overflow")
		}
		appendVarintTo(&raw, delta)
		previousDelta := delta
		for i := 2; i < len(points); i++ {
			delta, ok = checkedSubInt64(points[i].timestamp, points[i-1].timestamp)
			if !ok {
				return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 timestamp delta overflow")
			}
			deltaOfDelta, ok := checkedSubInt64(delta, previousDelta)
			if !ok {
				return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 timestamp delta-of-delta overflow")
			}
			appendVarintTo(&raw, deltaOfDelta)
			previousDelta = delta
		}
	}

	valueWriter := newSQLiteV4BitWriter()
	valueWriter.writeBits(points[0].valueBits, 64)
	previousBits := points[0].valueBits
	previousLeading, previousTrailing := 0, 0
	windowValid := false
	for i := 1; i < len(points); i++ {
		xor := previousBits ^ points[i].valueBits
		if xor == 0 {
			valueWriter.writeBit(false)
			previousBits = points[i].valueBits
			continue
		}
		valueWriter.writeBit(true)
		leading := bits.LeadingZeros64(xor)
		trailing := bits.TrailingZeros64(xor)
		if windowValid && leading >= previousLeading && trailing >= previousTrailing {
			valueWriter.writeBit(false)
			significant := 64 - previousLeading - previousTrailing
			valueWriter.writeBits(xor>>previousTrailing, significant)
		} else {
			valueWriter.writeBit(true)
			significant := 64 - leading - trailing
			valueWriter.writeBits(uint64(leading), 6)
			encodedSignificant := significant
			if significant == 64 {
				encodedSignificant = 0
			}
			valueWriter.writeBits(uint64(encodedSignificant), 6)
			valueWriter.writeBits(xor>>trailing, significant)
			previousLeading, previousTrailing = leading, trailing
			windowValid = true
		}
		previousBits = points[i].valueBits
	}
	valueBytes, valueBits := valueWriter.bytes()
	appendUvarintTo(&raw, uint64(valueBits))
	raw.Write(valueBytes)

	labelIndex := make(map[string]uint64)
	labels := make([]string, 0, 1)
	for _, point := range points {
		if _, ok := labelIndex[point.labels]; ok {
			continue
		}
		labelIndex[point.labels] = uint64(len(labels))
		labels = append(labels, point.labels)
	}
	appendUvarintTo(&raw, uint64(len(labels)))
	for _, label := range labels {
		appendUvarintTo(&raw, uint64(len(label)))
		raw.WriteString(label)
	}
	for _, point := range points {
		appendUvarintTo(&raw, labelIndex[point.labels])
	}

	appendVarintTo(&raw, points[0].createdAt)
	for i := 1; i < len(points); i++ {
		delta, ok := checkedSubInt64(points[i].createdAt, points[i-1].createdAt)
		if !ok {
			return sqliteV4EncodedBlock{}, fmt.Errorf("metric: SQLite V4 creation-time delta overflow")
		}
		appendVarintTo(&raw, delta)
	}

	payload := append([]byte{sqliteV4PayloadRaw}, raw.Bytes()...)
	var compressed bytes.Buffer
	compressed.WriteByte(sqliteV4PayloadDeflate)
	writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
	if err != nil {
		return sqliteV4EncodedBlock{}, err
	}
	if _, err := writer.Write(raw.Bytes()); err != nil {
		_ = writer.Close()
		return sqliteV4EncodedBlock{}, err
	}
	if err := writer.Close(); err != nil {
		return sqliteV4EncodedBlock{}, err
	}
	if compressed.Len() < len(payload) {
		payload = compressed.Bytes()
	}

	return sqliteV4EncodedBlock{
		startNano: points[0].timestamp,
		endNano:   points[len(points)-1].timestamp,
		count:     len(points),
		codec:     sqliteV4BlockCodec,
		checksum:  crc32.ChecksumIEEE(payload),
		payload:   append([]byte(nil), payload...),
	}, nil
}

func decodeSQLiteV4Block(codec, expectedCount int, expectedChecksum uint32, payload []byte) ([]sqliteV4BlockPoint, error) {
	if codec != sqliteV4BlockCodec {
		return nil, fmt.Errorf("metric: unsupported SQLite V4 point block codec %d", codec)
	}
	if len(payload) < 2 {
		return nil, fmt.Errorf("metric: truncated SQLite V4 point block")
	}
	if crc32.ChecksumIEEE(payload) != expectedChecksum {
		return nil, fmt.Errorf("metric: SQLite V4 point block checksum mismatch")
	}
	var raw []byte
	switch payload[0] {
	case sqliteV4PayloadRaw:
		raw = payload[1:]
	case sqliteV4PayloadDeflate:
		reader := flate.NewReader(bytes.NewReader(payload[1:]))
		decompressed, err := io.ReadAll(io.LimitReader(reader, 128<<20))
		closeErr := reader.Close()
		if err != nil {
			return nil, fmt.Errorf("metric: decompress SQLite V4 point block: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("metric: close SQLite V4 point block decoder: %w", closeErr)
		}
		raw = decompressed
	default:
		return nil, fmt.Errorf("metric: unsupported SQLite V4 payload compression %d", payload[0])
	}
	reader := bytes.NewReader(raw)
	magic := make([]byte, len(sqliteV4BlockMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != sqliteV4BlockMagic {
		return nil, fmt.Errorf("metric: invalid SQLite V4 point block header")
	}
	count64, err := binary.ReadUvarint(reader)
	if err != nil || count64 == 0 || count64 > sqliteV4MaxDecodedPoints {
		return nil, fmt.Errorf("metric: invalid SQLite V4 point count")
	}
	count := int(count64)
	if expectedCount >= 0 && count != expectedCount {
		return nil, fmt.Errorf("metric: SQLite V4 point count mismatch: header=%d row=%d", count, expectedCount)
	}
	points := make([]sqliteV4BlockPoint, count)
	firstTimestamp, err := binary.ReadVarint(reader)
	if err != nil {
		return nil, fmt.Errorf("metric: decode SQLite V4 first timestamp: %w", err)
	}
	points[0].timestamp = firstTimestamp
	if count > 1 {
		previousDelta, err := binary.ReadVarint(reader)
		if err != nil || previousDelta <= 0 {
			return nil, fmt.Errorf("metric: invalid SQLite V4 timestamp delta")
		}
		points[1].timestamp, err = checkedAddInt64(points[0].timestamp, previousDelta)
		if err != nil {
			return nil, err
		}
		for i := 2; i < count; i++ {
			deltaOfDelta, err := binary.ReadVarint(reader)
			if err != nil {
				return nil, fmt.Errorf("metric: decode SQLite V4 timestamp delta-of-delta: %w", err)
			}
			delta, ok := checkedAddInt64Value(previousDelta, deltaOfDelta)
			if !ok || delta <= 0 {
				return nil, fmt.Errorf("metric: invalid SQLite V4 timestamp delta")
			}
			points[i].timestamp, err = checkedAddInt64(points[i-1].timestamp, delta)
			if err != nil {
				return nil, err
			}
			previousDelta = delta
		}
	}

	valueBitCount, err := binary.ReadUvarint(reader)
	if err != nil || valueBitCount < 64 || valueBitCount > uint64(reader.Len())*8 {
		return nil, fmt.Errorf("metric: invalid SQLite V4 value bit stream")
	}
	valueByteCount := int((valueBitCount + 7) / 8)
	valueBytes := make([]byte, valueByteCount)
	if _, err := io.ReadFull(reader, valueBytes); err != nil {
		return nil, fmt.Errorf("metric: read SQLite V4 value bit stream: %w", err)
	}
	bitReader := newSQLiteV4BitReader(valueBytes, int(valueBitCount))
	firstValue, err := bitReader.readBits(64)
	if err != nil {
		return nil, err
	}
	points[0].valueBits = firstValue
	previousBits := firstValue
	previousLeading, previousTrailing := 0, 0
	windowValid := false
	for i := 1; i < count; i++ {
		changed, err := bitReader.readBit()
		if err != nil {
			return nil, err
		}
		if !changed {
			points[i].valueBits = previousBits
			continue
		}
		newWindow, err := bitReader.readBit()
		if err != nil {
			return nil, err
		}
		leading, trailing := previousLeading, previousTrailing
		if newWindow {
			leadingBits, err := bitReader.readBits(6)
			if err != nil {
				return nil, err
			}
			significantBits, err := bitReader.readBits(6)
			if err != nil {
				return nil, err
			}
			significant := int(significantBits)
			if significant == 0 {
				significant = 64
			}
			leading = int(leadingBits)
			trailing = 64 - leading - significant
			if trailing < 0 {
				return nil, fmt.Errorf("metric: invalid SQLite V4 Gorilla window")
			}
			previousLeading, previousTrailing = leading, trailing
			windowValid = true
		} else if !windowValid {
			return nil, fmt.Errorf("metric: SQLite V4 Gorilla stream reused a missing window")
		}
		significant := 64 - leading - trailing
		xorBits, err := bitReader.readBits(significant)
		if err != nil {
			return nil, err
		}
		previousBits ^= xorBits << trailing
		points[i].valueBits = previousBits
	}

	labelCount64, err := binary.ReadUvarint(reader)
	if err != nil || labelCount64 == 0 || labelCount64 > uint64(count) {
		return nil, fmt.Errorf("metric: invalid SQLite V4 label dictionary")
	}
	labels := make([]string, int(labelCount64))
	for i := range labels {
		length, err := binary.ReadUvarint(reader)
		if err != nil || length > uint64(reader.Len()) {
			return nil, fmt.Errorf("metric: invalid SQLite V4 label length")
		}
		label := make([]byte, int(length))
		if _, err := io.ReadFull(reader, label); err != nil {
			return nil, err
		}
		labels[i] = string(label)
	}
	for i := range points {
		index, err := binary.ReadUvarint(reader)
		if err != nil || index >= uint64(len(labels)) {
			return nil, fmt.Errorf("metric: invalid SQLite V4 label index")
		}
		points[i].labels = labels[index]
	}

	firstCreatedAt, err := binary.ReadVarint(reader)
	if err != nil {
		return nil, fmt.Errorf("metric: decode SQLite V4 creation time: %w", err)
	}
	points[0].createdAt = firstCreatedAt
	for i := 1; i < len(points); i++ {
		delta, err := binary.ReadVarint(reader)
		if err != nil {
			return nil, fmt.Errorf("metric: decode SQLite V4 creation-time delta: %w", err)
		}
		points[i].createdAt, err = checkedAddInt64(points[i-1].createdAt, delta)
		if err != nil {
			return nil, err
		}
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("metric: SQLite V4 point block contains trailing data")
	}
	return points, nil
}

func appendUvarintTo(dst *bytes.Buffer, value uint64) {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(encoded[:], value)
	dst.Write(encoded[:n])
}

func appendVarintTo(dst *bytes.Buffer, value int64) {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutVarint(encoded[:], value)
	dst.Write(encoded[:n])
}

func checkedSubInt64(a, b int64) (int64, bool) {
	result := a - b
	if (b > 0 && result > a) || (b < 0 && result < a) {
		return 0, false
	}
	return result, true
}

func checkedAddInt64Value(a, b int64) (int64, bool) {
	result := a + b
	if (b > 0 && result < a) || (b < 0 && result > a) {
		return 0, false
	}
	return result, true
}

func checkedAddInt64(a, b int64) (int64, error) {
	result, ok := checkedAddInt64Value(a, b)
	if !ok {
		return 0, fmt.Errorf("metric: SQLite V4 integer delta overflow")
	}
	return result, nil
}

type sqliteV4BitWriter struct {
	data []byte
	bits int
}

func newSQLiteV4BitWriter() *sqliteV4BitWriter { return &sqliteV4BitWriter{} }

func (w *sqliteV4BitWriter) writeBit(value bool) {
	if w.bits%8 == 0 {
		w.data = append(w.data, 0)
	}
	if value {
		w.data[len(w.data)-1] |= 1 << (7 - uint(w.bits%8))
	}
	w.bits++
}

func (w *sqliteV4BitWriter) writeBits(value uint64, count int) {
	for i := count - 1; i >= 0; i-- {
		w.writeBit(value&(uint64(1)<<uint(i)) != 0)
	}
}

func (w *sqliteV4BitWriter) bytes() ([]byte, int) {
	return append([]byte(nil), w.data...), w.bits
}

type sqliteV4BitReader struct {
	data  []byte
	bits  int
	index int
}

func newSQLiteV4BitReader(data []byte, bitCount int) *sqliteV4BitReader {
	return &sqliteV4BitReader{data: data, bits: bitCount}
}

func (r *sqliteV4BitReader) readBit() (bool, error) {
	if r.index >= r.bits {
		return false, io.ErrUnexpectedEOF
	}
	value := r.data[r.index/8]&(1<<(7-uint(r.index%8))) != 0
	r.index++
	return value, nil
}

func (r *sqliteV4BitReader) readBits(count int) (uint64, error) {
	if count < 0 || count > 64 || r.index+count > r.bits {
		return 0, io.ErrUnexpectedEOF
	}
	var value uint64
	for i := 0; i < count; i++ {
		bit, err := r.readBit()
		if err != nil {
			return 0, err
		}
		value <<= 1
		if bit {
			value |= 1
		}
	}
	return value, nil
}

func sqliteV4PointsEqual(a, b []sqliteV4BlockPoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].timestamp != b[i].timestamp || a[i].valueBits != b[i].valueBits ||
			a[i].labels != b[i].labels || a[i].createdAt != b[i].createdAt {
			return false
		}
	}
	return true
}
