package metric

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// encodeMap encodes a string map as JSON.
//
// encodeMap 将字符串 map 编码为 JSON 字符串，nil 会按空对象处理。
func encodeMap(m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// decodeMap decodes a scanned JSON value into a string map.
//
// decodeMap 将数据库扫描出的 JSON 值还原为字符串 map。
func decodeMap(v any) (map[string]string, error) {
	switch x := v.(type) {
	case nil:
		return map[string]string{}, nil
	case string:
		return decodeMapString(x)
	case []byte:
		return decodeMapString(string(x))
	default:
		return nil, fmt.Errorf("unsupported json value type %T", v)
	}
}

// decodeMapString decodes a JSON object string into a string map.
//
// decodeMapString 将 JSON 字符串解码为字符串 map。
func decodeMapString(s string) (map[string]string, error) {
	if s == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

// canonicalTags returns a deterministic JSON encoding of a tag map. encodeMap
// already relies on encoding/json, which sorts map keys, so equal tag maps
// always produce identical bytes — the property tagsFingerprint depends on.
//
// canonicalTags 返回标签 map 的确定性 JSON 编码；相同标签集合总会得到
// 相同字节序列，tagsFingerprint 依赖这个性质。
func canonicalTags(m map[string]string) (string, error) {
	return encodeMap(m)
}

// tagsFingerprint returns a stable hex fingerprint of a tag set, used as the
// rollups table's tags_hash key column. Equal tag maps (regardless of Go map
// iteration order) hash identically, so each distinct tag combination becomes
// its own rollup series; an empty/nil tag map hashes to the fingerprint of "{}".
//
// tagsFingerprint 为标签集合生成稳定的十六进制指纹，用作 rollups 表的
// tags_hash key 列。相同标签 map（无论 Go map 迭代顺序如何）都会得到相同 hash，
// 因此每种不同标签组合都会成为自己的 rollup 序列；空或 nil 标签 map 会得到 "{}"
// 的指纹。
func tagsFingerprint(m map[string]string) (hash string, canonical string, err error) {
	canonical, err = canonicalTags(m)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), canonical, nil
}
