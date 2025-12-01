package notifier

import (
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	go CheckExpireScheduledWork()
	event.On(eventType.SchedulerEveryMinute, event.ListenerFunc(func(e event.Event) error {
		CheckTraffic()
		return nil
	}))
}
