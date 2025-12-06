package plugin

import (
	"log/slog"

	"github.com/gookit/event"
	"github.com/komari-monitor/komari/internal/eventType"
	"github.com/komari-monitor/komari/internal/jsruntime"
)

var (
	runtime *jsruntime.JsRuntime
)

func init() {
	event.On(eventType.ProcessStart, event.ListenerFunc(func(e event.Event) error {
		slog.Warn("Plugin module is not implemented yet.")
		runtime = jsruntime.NewJsRuntime().WithNodejs().WithMemoryKv("pluginKv")
		return nil
	}))
}
