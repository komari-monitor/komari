package plugin

import (
	"bytes"
	"sync"
)

// LogBuffer is a bounded ring buffer holding one plugin's console output and
// lifecycle markers. Old bytes are dropped from the front when the limit is
// reached.
type LogBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func newLogBuffer(limit int) *LogBuffer {
	if limit <= 0 {
		limit = defaultLogBufferSize
	}
	return &LogBuffer{limit: limit}
}

func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.buf.Write(p)
	if b.buf.Len() > b.limit {
		b.buf.Next(b.buf.Len() - b.limit)
	}
	return len(p), nil
}

func (b *LogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *LogBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}
