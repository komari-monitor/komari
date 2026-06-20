package metric

import (
	"encoding/json"
	"fmt"
)

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
