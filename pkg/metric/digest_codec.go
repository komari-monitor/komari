package metric

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Persisted digest blobs have a small envelope so the legacy TD encoding can
// remain readable forever while newly written rows use compression when it
// actually saves space. TU is an uncompressed new-format payload and TZ is a
// zstd payload; both contain the original TD bytes after the envelope.
const (
	storedDigestMagic0     = 'T'
	storedDigestVersion    = 1
	storedDigestTypeZstd   = 'Z'
	storedDigestTypeRaw    = 'U'
	storedDigestHeaderSize = 3
)

var (
	digestEncoderOnce sync.Once
	digestEncoder     *zstd.Encoder
	digestEncoderErr  error
	digestDecoderOnce sync.Once
	digestDecoder     *zstd.Decoder
	digestDecoderErr  error
)

func getDigestEncoder() (*zstd.Encoder, error) {
	digestEncoderOnce.Do(func() {
		digestEncoder, digestEncoderErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(1)))
	})
	return digestEncoder, digestEncoderErr
}

func getDigestDecoder() (*zstd.Decoder, error) {
	digestDecoderOnce.Do(func() {
		digestDecoder, digestDecoderErr = zstd.NewReader(nil)
	})
	return digestDecoder, digestDecoderErr
}

// encodeStoredTDigest returns the current on-disk representation. Compression
// is deliberately per rollup: each row can be queried independently and a
// poorly compressible digest is stored raw instead of growing larger.
func encodeStoredTDigest(t *TDigest) []byte {
	if t == nil {
		return nil
	}
	raw := t.Encode()
	encoder, err := getDigestEncoder()
	if err != nil {
		return append([]byte(nil), raw...)
	}
	compressed := encoder.EncodeAll(raw, nil)
	if len(compressed)+storedDigestHeaderSize < len(raw) {
		out := make([]byte, storedDigestHeaderSize+len(compressed))
		out[0] = storedDigestMagic0
		out[1] = storedDigestTypeZstd
		out[2] = storedDigestVersion
		copy(out[storedDigestHeaderSize:], compressed)
		return out
	}
	out := make([]byte, storedDigestHeaderSize+len(raw))
	out[0] = storedDigestMagic0
	out[1] = storedDigestTypeRaw
	out[2] = storedDigestVersion
	copy(out[storedDigestHeaderSize:], raw)
	return out
}

// decodeStoredTDigest unwraps both new formats and the legacy raw TD blob.
func decodeStoredTDigest(blob []byte) ([]byte, error) {
	if len(blob) < storedDigestHeaderSize || blob[0] != storedDigestMagic0 {
		return blob, nil
	}
	switch blob[1] {
	case tdigestMagic1:
		return blob, nil
	case storedDigestTypeRaw:
		if blob[2] != storedDigestVersion {
			return nil, fmt.Errorf("metric: unsupported stored t-digest version %d", blob[2])
		}
		return blob[storedDigestHeaderSize:], nil
	case storedDigestTypeZstd:
		if blob[2] != storedDigestVersion {
			return nil, fmt.Errorf("metric: unsupported stored t-digest version %d", blob[2])
		}
		decoder, err := getDigestDecoder()
		if err != nil {
			return nil, err
		}
		raw, err := decoder.DecodeAll(blob[storedDigestHeaderSize:], nil)
		if err != nil {
			return nil, fmt.Errorf("metric: decode compressed t-digest: %w", err)
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("metric: invalid stored t-digest type %q", blob[1])
	}
}

// isLegacyRawTDigest reports whether a blob is the pre-compression format.
func isLegacyRawTDigest(blob []byte) bool {
	return len(blob) >= 2 && blob[0] == tdigestMagic0 && blob[1] == tdigestMagic1
}
