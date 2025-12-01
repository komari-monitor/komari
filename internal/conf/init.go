package conf

import (
	"encoding/json"
	"os"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"
)

func init() {
	Conf = &Config{}
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		b, err := os.ReadFile(flags.ConfigFile)
		if err != nil {
			return err
		}
		cst := &Config{}
		if err := json.Unmarshal(b, cst); err != nil {
			return err
		}
		Conf = cst
		return nil
	}), event.Max+2)

	event.On(eventType.ServerInitializeDone, event.ListenerFunc(func(e event.Event) error {
		event.Trigger(eventType.ConfigUpdated, event.M{
			"old": Config{},
			"new": *Conf,
		})
		return nil
	}), event.Low)
}
