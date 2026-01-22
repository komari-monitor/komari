package scheduler

import (
	"context"
	"time"

	"log/slog"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/eventType"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type Module struct {
	stops []StopFunc
}

func FxModule() fx.Option {
	return fx.Options(
		fx.Provide(func() *Module { return &Module{} }),
		fx.Invoke(registerSchedulerLifecycle),
	)
}

func registerSchedulerLifecycle(lc fx.Lifecycle, m *Module, _ *gorm.DB) {
	// _ *gorm.DB ensures DB is initialized before scheduler starts.
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			_ = ctx
			m.start()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			_ = ctx
			m.stop()
			return nil
		},
	})
}

func (m *Module) start() {
	doScheduledWork()

	m.stops = append(m.stops,
		Every(1*time.Minute, func() { event.Async(eventType.SchedulerEveryMinute, event.M{"interval": "1m"}) }),
		Every(5*time.Minute, func() { event.Async(eventType.SchedulerEvery5Minutes, event.M{"interval": "5m"}) }),
		Every(30*time.Minute, func() { event.Async(eventType.SchedulerEvery30Minutes, event.M{"interval": "30m"}) }),
		Every(1*time.Hour, func() { event.Async(eventType.SchedulerEveryHour, event.M{"interval": "1h"}) }),
		Every(24*time.Hour, func() { event.Async(eventType.SchedulerEveryDay, event.M{"interval": "1d"}) }),
	)
}

func (m *Module) stop() {
	for i := len(m.stops) - 1; i >= 0; i-- {
		if m.stops[i] != nil {
			m.stops[i]()
		}
	}
	m.stops = nil
}

// doScheduledWork matches the legacy behavior: run initial maintenance and register event listeners.
func doScheduledWork() {
	tasks.ReloadPingSchedule()
	records.CompactRecord()

	event.On(eventType.SchedulerEvery30Minutes, event.ListenerFunc(func(e event.Event) error {
		cfg, err := conf.GetWithV1Format()
		if err != nil {
			slog.Warn("Failed to get config in scheduled task:", "error", err)
			return err
		}
		records.DeleteRecordBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
		records.CompactRecord()
		tasks.ClearTaskResultsByTimeBefore(time.Now().Add(-time.Hour * time.Duration(cfg.RecordPreserveTime)))
		tasks.DeletePingRecordsBefore(time.Now().Add(-time.Hour * time.Duration(cfg.PingRecordPreserveTime)))
		auditlog.RemoveOldLogs()
		return nil
	}))

	event.On(eventType.SchedulerEveryMinute, event.ListenerFunc(func(e event.Event) error {
		cfg, err := conf.GetWithV1Format()
		if err != nil {
			slog.Warn("Failed to get config in scheduled task:", "error", err)
			return err
		}
		if !cfg.RecordEnabled {
			records.DeleteAll()
			tasks.DeleteAllPingRecords()
		}
		return nil
	}))
}
