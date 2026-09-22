package jsruntime

import (
	_ "embed"
	"fmt"

	nodeurl "github.com/dop251/goja_nodejs/url"
	"github.com/komari-monitor/komari/pkg/jsruntime/xhr"
)

// JavaScript compatibility: injects bounded timer APIs, console, URL APIs,
// Fetch API, XMLHttpRequest, and queueMicrotask.
func (r *Runtime) injectGlobals() error {
	if err := r.consoleMod.Inject(r.vm); err != nil {
		return fmt.Errorf("inject console: %w", err)
	}
	nodeurl.Enable(r.vm)
	if err := r.timersMod.Inject(r.vm); err != nil {
		return fmt.Errorf("inject timers: %w", err)
	}
	if r.nodeJS {
		if err := r.injectNodeGlobals(); err != nil {
			return err
		}
	}
	if err := r.fetchMod.Inject(r.vm); err != nil {
		return fmt.Errorf("inject Fetch API: %w", err)
	}
	if err := xhr.Inject(r.vm); err != nil {
		return fmt.Errorf("inject XMLHttpRequest: %w", err)
	}
	if _, err := r.vm.RunString(queueMicrotaskSource); err != nil {
		return fmt.Errorf("inject queueMicrotask: %w", err)
	}
	return nil
}

//go:embed globals.js
var queueMicrotaskSource string
