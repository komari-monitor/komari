// Package jsruntime host services for modules injected by the application.
//
// Host modules (registered through Options.ConfigureHost) run on the runtime's
// event-loop goroutine. They use Host to schedule JavaScript turns from other
// goroutines, to run bounded jobs with error reporting, and to read the
// execution timeout.
package jsruntime

import (
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
)

// Host is the host-facing service boundary of a Runtime. It deliberately
// stays independent of any particular host application so the jsruntime
// package does not depend on gin, RPC registries, or other Komari surfaces.
type Host struct {
	runtime *bridge.Runtime
}

// RunOnLoop queues job on the runtime's event loop. It returns false when
// the loop is no longer running, for example after Runtime.Close.
func (h *Host) RunOnLoop(job func(*goja.Runtime)) bool {
	return h.runtime.RunOnLoop(job)
}

// RunJob runs one asynchronous host callback as a bounded JavaScript turn,
// reporting errors through the runtime's error reporter (the console by
// default). Exit code 0 is treated as a normal termination.
func (h *Host) RunJob(vm *goja.Runtime, name string, job func() error) error {
	return h.runtime.RunJob(vm, name, job)
}

// Timeout returns the per-turn execution timeout configured for the runtime.
func (h *Host) Timeout() time.Duration {
	return h.runtime.Timeout()
}
