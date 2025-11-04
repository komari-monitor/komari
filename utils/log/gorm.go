package log

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	komari_utils "github.com/komari-monitor/komari/utils"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/utils"
)

// GormLogger 实现 gorm.io/gorm/logger.Interface
type GormLogger struct {
	SlowThreshold             time.Duration
	IgnoreRecordNotFoundError bool
	LogLevel                  gormlogger.LogLevel
}

// NewGormLogger 创建 GORM logger
func NewGormLogger() *GormLogger {
	return &GormLogger{
		SlowThreshold:             200 * time.Millisecond,
		IgnoreRecordNotFoundError: true,
		LogLevel: func(hash string) gormlogger.LogLevel {
			if hash == "unknown" {
				return gormlogger.Info
			}
			return gormlogger.Silent
		}(komari_utils.VersionHash),
	}
}

func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newlogger := *l
	newlogger.LogLevel = level
	return &newlogger
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Info {
		slog.InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Warn {
		slog.WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Error {
		slog.ErrorContext(ctx, fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.LogLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	fileWithLineNum := utils.FileWithLineNum()

	handler := slog.Default().Handler()

	switch {
	case err != nil && l.LogLevel >= gormlogger.Error && (!errors.Is(err, gorm.ErrRecordNotFound) || !l.IgnoreRecordNotFoundError):
		msg := fmt.Sprintf("[%.3fms] [rows:%d] %s | ERROR: %v %s",
			float64(elapsed.Nanoseconds())/1e6, rows, sql, err, Gray("(%s)", fileWithLineNum))
		r := slog.NewRecord(time.Now(), slog.LevelError, msg, 0)
		r.AddAttrs(slog.String("_group", "GORM"))
		handler.Handle(ctx, r)
	case elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= gormlogger.Warn:
		msg := fmt.Sprintf("[%.3fms] [rows:%d] %s | SLOW QUERY %s",
			float64(elapsed.Nanoseconds())/1e6, rows, sql, Gray("(%s)", fileWithLineNum))
		r := slog.NewRecord(time.Now(), slog.LevelWarn, msg, 0)
		r.AddAttrs(slog.String("_group", "GORM"))
		handler.Handle(ctx, r)
	case l.LogLevel >= gormlogger.Info:
		msg := fmt.Sprintf("[%.3fms] [rows:%d] %s %s",
			float64(elapsed.Nanoseconds())/1e6, rows, sql, Gray("(%s)", fileWithLineNum))
		r := slog.NewRecord(time.Now(), slog.LevelDebug, msg, 0)
		r.AddAttrs(slog.String("_group", "GORM"))
		handler.Handle(ctx, r)
	}
}
