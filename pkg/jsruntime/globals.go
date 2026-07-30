package jsruntime

import "fmt"

// JavaScript compatibility: injects bounded timer APIs, console, Fetch API,
// XMLHttpRequest, and queueMicrotask.
func (r *Runtime) injectGlobals() error {
	r.injectConsole()
	r.injectTimers()
	if r.nodeJS {
		if err := r.injectNodeGlobals(); err != nil {
			return err
		}
	}
	if err := r.injectFetch(); err != nil {
		return err
	}
	if err := r.injectXMLHttpRequest(); err != nil {
		return err
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
