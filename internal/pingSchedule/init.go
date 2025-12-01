package pingSchedule

import (
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
		return ReloadPingSchedule()
	}))
}
