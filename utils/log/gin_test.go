package logger

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGinLoggerFormatsCompletedRequest(t *testing.T) {
	previous := defaultLogger
	var output bytes.Buffer
	defaultLogger = slog.New(NewConsoleHandler(&output, slog.LevelDebug))
	t.Cleanup(func() { defaultLogger = previous })

	router := gin.New()
	router.Use(GinLogger())
	router.POST("/api/clients/v2/rpc", func(c *gin.Context) { c.Status(http.StatusOK) })

	request := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc?token=secret", nil)
	request.RemoteAddr = "8.148.73.139:12345"
	router.ServeHTTP(httptest.NewRecorder(), request)

	line := output.String()
	if !regexp.MustCompile(`(?:\x1b\[\d+m)?\[INFO/GIN\](?:\x1b\[0m)? 200 POST /api/clients/v2/rpc \| 8\.148\.73\.139 \| [^\r\n]+`).MatchString(line) {
		t.Fatalf("unexpected Gin log format %q", line)
	}
	if bytes.Contains([]byte(line), []byte("token=secret")) {
		t.Fatalf("Gin log must omit query string: %q", line)
	}
}
