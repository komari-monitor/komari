package log

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"os"
	"runtime"
	"time"
)

func Green(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[32m"+format+"\033[0m", v...)
}

func Yellow(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[33m"+format+"\033[0m", v...)
}

func Red(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[31m"+format+"\033[0m", v...)
}

func Blue(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[34m"+format+"\033[0m", v...)
}

func Cyan(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[36m"+format+"\033[0m", v...)
}

func Gray(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[90m"+format+"\033[0m", v...)
}

func White(format string, v ...interface{}) string {
	return fmt.Sprintf("\033[37m"+format+"\033[0m", v...)
}

type LogHandler struct {
	w     io.Writer
	level slog.Level
	group string
}

func NewHandler(w io.Writer, level slog.Level) *LogHandler {
	return &LogHandler{w: w, level: level, group: ""}
}

func (h *LogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *LogHandler) Handle(_ context.Context, r slog.Record) error {
	var file string
	var line int
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		file = f.File
		line = f.Line
	}

	timeStr := r.Time.Format("2006/01/02 15:04:05")

	// 检查是否有分组信息（从 attributes 中获取）
	group := h.group
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "_group" {
			group = a.Value.String()
			return false // 停止迭代
		}
		return true
	})

	levelStr := ""
	switch r.Level {
	case slog.LevelDebug:
		if group != "" {
			levelStr = Cyan("[DEBUG/%s]", group)
		} else {
			levelStr = Cyan("[DEBUG]")
		}
	case slog.LevelInfo:
		if group != "" {
			levelStr = Green(fmt.Sprintf("[INFO/%s]", group))
		} else {
			levelStr = Green("[INFO]")
		}
	case slog.LevelWarn:
		if group != "" {
			levelStr = Yellow(fmt.Sprintf("[WARN/%s]", group))
		} else {
			levelStr = Yellow("[WARN]")
		}
	case slog.LevelError:
		if group != "" {
			levelStr = Red(fmt.Sprintf("[ERROR/%s]", group))
		} else {
			levelStr = Red("[ERROR]")
		}
	}

	var msg string
	if file != "" {
		msg = fmt.Sprintf("%s %s %s %s",
			timeStr,
			levelStr,
			r.Message,
			Gray(fmt.Sprintf("(%s:%d)", file, line)))
	} else {
		msg = fmt.Sprintf("%s %s %s",
			timeStr,
			levelStr,
			r.Message)
	}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "_group" { // 跳过内部使用的 _group 属性
			msg += fmt.Sprintf(" %s=%s", Cyan(a.Key), Yellow(fmt.Sprintf("%v", a.Value)))
		}
		return true
	})

	msg += "\n"
	_, err := h.w.Write([]byte(msg))
	return err
}

func (h *LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *LogHandler) WithGroup(name string) slog.Handler {
	return h
}

// SetupGlobalLogger 设置全局标准库 log 使用 slog handler
func SetupGlobalLogger(level slog.Level) {
	handler := NewHandler(os.Stdout, level)
	logger := slog.New(handler)

	// 设置 slog 默认 logger
	slog.SetDefault(logger)

	// 设置标准库 log 使用 slog
	stdlog.SetOutput(os.Stdout)
	stdlog.SetFlags(0) // 清除默认标志
	stdlog.SetPrefix("")

	// 替换标准库 log 的输出为 slog handler
	stdlog.SetOutput(&writerAdapter{handler: handler, level: level})
}

// writerAdapter 将标准库 log 的输出适配到 slog
type writerAdapter struct {
	handler slog.Handler
	level   slog.Level
}

func (w *writerAdapter) Write(p []byte) (n int, err error) {
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}

	var pcs [1]uintptr
	runtime.Callers(4, pcs[:]) // skip [Callers, Write, log.Output, log.Printf/etc]

	r := slog.NewRecord(time.Now(), w.level, msg, pcs[0])
	return len(p), w.handler.Handle(context.Background(), r)
}

// GetWriter 返回一个 io.Writer，可以用于 Gin 等框架
func GetWriter() io.Writer {
	handler := slog.Default().Handler()
	return &writerAdapter{handler: handler, level: slog.LevelInfo}
}
