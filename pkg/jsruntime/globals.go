package jsruntime

import (
	"fmt"

	"github.com/komari-monitor/komari/pkg/jsruntime/xhr"
)

// JavaScript compatibility: injects bounded timer APIs, console, Fetch API,
// XMLHttpRequest, and queueMicrotask.
func (r *Runtime) injectGlobals() error {
	if err := r.consoleMod.Inject(r.vm); err != nil {
		return fmt.Errorf("inject console: %w", err)
	}
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
	if _, err := r.vm.RunString(`
		globalThis.queueMicrotask = function queueMicrotask(callback) {
			if (typeof callback !== "function") {
				throw new TypeError("queueMicrotask callback must be a function");
			}
			Promise.resolve().then(callback);
		};
	`); err != nil {
		return fmt.Errorf("inject queueMicrotask: %w", err)
	}
	return nil
}
