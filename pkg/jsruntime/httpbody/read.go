// Package httpbody provides bounded helpers for reading HTTP request or
// response bodies. It is shared between the jsruntime fetch/http modules and
// the plugin host so that both enforce the same size limit.
package httpbody

import (
	"fmt"
	"io"
)

func ReadAll(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("HTTP body exceeds %d bytes", limit)
	}
	return data, nil
}
