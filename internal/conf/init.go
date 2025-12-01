package conf

import (
	"encoding/json"
	"os"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/internal/eventType"
)

func Init() {

	b, err := os.ReadFile(flags.ConfigFile)
	if err != nil {
		panic(err)
	}
	cst := &Config{}
	if err := json.Unmarshal(b, cst); err != nil {
		panic(err)
	}
	Conf = cst
	event.Trigger(eventType.ConfigUpdated, event.M{
		"old": Config{},
		"new": *Conf,
	})
}

func init() {
	Conf = &Config{}
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		Init()
		return nil
	}))
}
