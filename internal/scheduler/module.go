package scheduler

import (
	"context"
	"time"

	"log/slog"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/app"
	"github.com/komari-monitor/komari/internal/conf"
	"github.com/komari-monitor/komari/internal/database/auditlog"
	"github.com/komari-monitor/komari/internal/database/records"
	"github.com/komari-monitor/komari/internal/database/tasks"
	"github.com/komari-monitor/komari/internal/dbcore"
	"github.com/komari-monitor/komari/internal/eventType"
)

type Module struct {
	stops []StopFunc
}

var _ app.Module = (*Module)(nil)

func NewModule() *Module { return &Module{} }

func (m *Module) Name() string { return "scheduler" }
func (m *Module) Depends() []string {
	// These scheduled tasks depend on DB being available.
	return []string{dbcore.NewDBModule().Name()}
}

func (m *Module) Provide(r app.Registry) error { return nil }

func (m *Module) Hooks() app.Hooks {
	return app.Hooks{
		Start: func(ctx context.Context) {
			_ = ctx
			m.start()
		},
		Stop: func(ctx context.Context) {
			_ = ctx
			m.stop()
		},
	}
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
