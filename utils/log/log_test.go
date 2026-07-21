package logger

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

func TestInfoWritesStructuredFields(t *testing.T) {
	previous := defaultLogger
	var output bytes.Buffer
	defaultLogger = slog.New(NewConsoleHandler(&output, slog.LevelDebug))
	t.Cleanup(func() { defaultLogger = previous })

	Info("metricstore", "store initialized", "driver", "sqlite", "points", 12)

	line := output.String()
	if !regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[INFO/METRICSTORE\]`).MatchString(line) {
		t.Fatalf("unexpected console log format %q", line)
	}
	for _, want := range []string{
		"[INFO/METRICSTORE] store initialized",
		"driver=sqlite",
		"points=12",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected %q in log output %q", want, line)
		}
	}
}

func TestGormLoggerSkipsSuccessfulQueriesByDefault(t *testing.T) {
	previous := defaultLogger
	var output bytes.Buffer
	defaultLogger = slog.New(NewConsoleHandler(&output, slog.LevelDebug))
	t.Cleanup(func() { defaultLogger = previous })

	logger := NewGormLogger()
	if logger.LogLevel != gormlogger.Warn {
		t.Fatalf("default GORM log level = %v, want %v", logger.LogLevel, gormlogger.Warn)
	}
	logger.Trace(t.Context(), time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)
	if output.Len() != 0 {
		t.Fatalf("successful query should not be logged by default: %q", output.String())
	}
}
