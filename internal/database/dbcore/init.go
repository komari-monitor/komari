package dbcore

import (
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		_ = GetDBInstance()
		return nil
	}), event.Max+1)
}
