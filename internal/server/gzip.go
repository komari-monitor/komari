package server

import (
	"compress/gzip"
	"io"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	writer *gzip.Writer
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	return w.writer.Write(data)
}

func (w *gzipResponseWriter) WriteString(s string) (int, error) {
	return w.writer.Write([]byte(s))
}

// gzipJSONResponses compresses JSON/RPC payloads when the client accepts gzip.
// Dashboard queryMetrics responses are multi-megabyte JSON; without compression
// transfer alone dominates page load time on typical admin links.
func gzipJSONResponses() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}
		if c.Request.Method == "HEAD" {
			c.Next()
			return
		}
		// WebSocket upgrades must not be wrapped — Content-Encoding would
		// corrupt the 101 handshake used by /api/rpc2.
		if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			c.Next()
			return
		}

		gz := gzipWriterPool.Get().(*gzip.Writer)
		defer gzipWriterPool.Put(gz)
		gz.Reset(c.Writer)
		defer gz.Close()

		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")
		c.Writer = &gzipResponseWriter{ResponseWriter: c.Writer, writer: gz}
		c.Next()
	}
}
