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
