package logger

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		// Successful queries are high-volume and provide little diagnostic value
		// in normal debug output. Callers investigating SQL can explicitly use
		// LogMode(gormlogger.Info) for per-query traces.
		LogLevel: gormlogger.Warn,
	}
}

func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newlogger := *l
	newlogger.LogLevel = level
	return &newlogger
}

func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Info {
		InfoContext(ctx, "gorm", fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Warn {
		WarnContext(ctx, "gorm", fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Error {
		ErrorContext(ctx, "gorm", fmt.Sprintf(msg, data...))
	}
}

func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.LogLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()

	fileWithLineNum := utils.FileWithLineNum()

	switch {
	case err != nil && l.LogLevel >= gormlogger.Error && (!errors.Is(err, gorm.ErrRecordNotFound) || !l.IgnoreRecordNotFoundError):
		ErrorContext(ctx, "gorm", "query failed", "elapsed", elapsed, "rows", rows, "sql", sql, "error", err, "source", fileWithLineNum)
	case elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= gormlogger.Warn:
		WarnContext(ctx, "gorm", "slow query", "elapsed", elapsed, "rows", rows, "sql", sql, "source", fileWithLineNum)
	case l.LogLevel >= gormlogger.Info:
		DebugContext(ctx, "gorm", "query completed", "elapsed", elapsed, "rows", rows, "sql", sql, "source", fileWithLineNum)
	}
}
