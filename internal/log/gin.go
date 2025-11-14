package log

import (
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// GinLogger 返回一个 gin.HandlerFunc，用于记录 HTTP 请求
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// 处理请求
		c.Next()

		// 计算延迟
		latency := time.Since(start)

		// 获取状态码
		statusCode := c.Writer.Status()

		statusCodeColored := func(code int) string {
			if code >= 500 {
				return Red("%d", code)
			} else if code >= 400 {
				return Yellow("%d", code)
			} else if code >= 300 {
				return Cyan("%d", code)
			} else {
				return Green("%d", code)
			}
		}(statusCode)

		msg := fmt.Sprintf("%s %s %s", statusCodeColored, c.Request.Method, path)
		if query != "" {
			msg += "?" + query
		}
		msg += fmt.Sprintf(" | %s | %s", c.ClientIP(), latency)

		if len(c.Errors) > 0 {
			msg += " | " + c.Errors.String()
		}

		handler := slog.Default().Handler()
		var level slog.Level
		if statusCode >= 500 {
			level = slog.LevelError
		} else {
			level = slog.LevelInfo
		}

		r := slog.NewRecord(time.Now(), level, msg, 0)
		r.AddAttrs(slog.String("_group", "GIN")) // 添加分组标识
		handler.Handle(c.Request.Context(), r)
	}
}

// GinRecovery 返回一个 gin.HandlerFunc，用于恢复 panic
func GinRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				lan := FileWithLineNum()
				msg := fmt.Sprintf("panic recovered: %v | %s %s (%s)", err, c.Request.Method, c.Request.URL.Path, lan)
				handler := slog.Default().Handler()
				r := slog.NewRecord(time.Now(), slog.LevelError, msg, 0)
				r.AddAttrs(slog.String("_group", "GIN"))
				handler.Handle(c.Request.Context(), r)
				c.AbortWithStatus(500)
			}
		}()
		c.Next()
	}
}

func FileWithLineNum() string {
	pcs := [20]uintptr{}
	len := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:len])
	for i := 0; i < len; i++ {
		frame, _ := frames.Next()

		if !strings.HasPrefix(frame.Function, "runtime") &&
			!strings.HasPrefix(frame.Function, "cmd") &&
			!strings.HasPrefix(frame.Function, "event") &&
			!strings.HasPrefix(frame.Function, "gorm") &&
			!strings.HasPrefix(frame.Function, "log") &&
			!strings.HasSuffix(frame.File, ".gen.go") ||
			strings.HasSuffix(frame.File, "_test.go") {
			return string(strconv.AppendInt(append([]byte(frame.File), ':'), int64(frame.Line), 10))
		}

	}

	return ""
}
