package notifier

import (
	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	event.On(eventType.SchedulerEveryMinute, event.ListenerFunc(func(e event.Event) error {
		CheckTraffic()
		return nil
	}))
	event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
		go CheckExpireScheduledWork()
		ReloadLoadNotification()
		return nil
	}))
}
