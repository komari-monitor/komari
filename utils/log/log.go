// Package logger provides the server's single runtime logging surface.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

var defaultLogger *slog.Logger

// ConsoleHandler keeps console logs concise while retaining structured fields.
// Example: 2026/07/21 10:51:52 [INFO/SERVER] Starting server on 0.0.0.0:25774 ...
type ConsoleHandler struct {
	w      io.Writer
	level  slog.Level
	attrs  []slog.Attr
	groups []string
}

func NewConsoleHandler(w io.Writer, level slog.Level) *ConsoleHandler {
	return &ConsoleHandler{w: w, level: level}
}

func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *ConsoleHandler) Handle(_ context.Context, record slog.Record) error {
	component := "APP"
	attrs := make([]slog.Attr, 0, len(h.attrs)+record.NumAttrs())
	attrs = append(attrs, h.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})

	var fields strings.Builder
	for _, attr := range attrs {
		if attr.Key == "component" {
			component = strings.ToUpper(attr.Value.String())
			continue
		}
		key := attr.Key
		if len(h.groups) > 0 {
			key = strings.Join(h.groups, ".") + "." + key
		}
		fields.WriteByte(' ')
		fields.WriteString(key)
		fields.WriteByte('=')
		fields.WriteString(formatValue(attr.Value))
	}

	_, err := fmt.Fprintf(
		h.w,
		"%s %s[%s/%s]\033[0m %s%s\n",
		record.Time.Local().Format("2006/01/02 15:04:05"),
		levelColor(record.Level),
		levelName(record.Level),
		component,
		record.Message,
		fields.String(),
	)
	return err
}

func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

func levelName(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

func levelColor(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "\033[36m" // 青
	case level < slog.LevelWarn:
		return "\033[32m" // 绿
	case level < slog.LevelError:
		return "\033[33m" // 黄
	default:
		return "\033[31m" // 红
	}
}

func formatValue(value slog.Value) string {
	var rendered string
	if value.Kind() == slog.KindString {
		rendered = value.String()
	} else {
		rendered = fmt.Sprint(value.Any())
	}
	if strings.ContainsAny(rendered, " \t\r\n\"") {
		return fmt.Sprintf("%q", rendered)
	}
	return rendered
}

// Setup installs the process-wide console logger before the application starts.
func Setup(level slog.Level) {
	defaultLogger = slog.New(NewConsoleHandler(os.Stdout, level))
	slog.SetDefault(defaultLogger)
}

func logger() *slog.Logger {
	if defaultLogger != nil {
		return defaultLogger
	}
	return slog.Default()
}

func logArgs(component string, args []any) []any {
	return append([]any{"component", component}, args...)
}

func Debug(component, message string, args ...any) {
	logger().Debug(message, logArgs(component, args)...)
}
func Info(component, message string, args ...any) {
	logger().Info(message, logArgs(component, args)...)
}
func Warn(component, message string, args ...any) {
	logger().Warn(message, logArgs(component, args)...)
}
func Error(component, message string, args ...any) {
	logger().Error(message, logArgs(component, args)...)
}

func Debugf(component, format string, args ...any) { Debug(component, fmt.Sprintf(format, args...)) }
func Infof(component, format string, args ...any)  { Info(component, fmt.Sprintf(format, args...)) }
func Warnf(component, format string, args ...any)  { Warn(component, fmt.Sprintf(format, args...)) }
func Errorf(component, format string, args ...any) { Error(component, fmt.Sprintf(format, args...)) }

func InfoArgs(component string, args ...any)  { Info(component, fmt.Sprint(args...)) }
func WarnArgs(component string, args ...any)  { Warn(component, fmt.Sprint(args...)) }
func ErrorArgs(component string, args ...any) { Error(component, fmt.Sprint(args...)) }

func Fatalf(component, format string, args ...any) {
	Errorf(component, format, args...)
	os.Exit(1)
}

func DebugContext(ctx context.Context, component, message string, args ...any) {
	logger().DebugContext(ctx, message, logArgs(component, args)...)
}

func InfoContext(ctx context.Context, component, message string, args ...any) {
	logger().InfoContext(ctx, message, logArgs(component, args)...)
}

func WarnContext(ctx context.Context, component, message string, args ...any) {
	logger().WarnContext(ctx, message, logArgs(component, args)...)
}

func ErrorContext(ctx context.Context, component, message string, args ...any) {
	logger().ErrorContext(ctx, message, logArgs(component, args)...)
}
