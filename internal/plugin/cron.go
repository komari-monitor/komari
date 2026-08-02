package plugin

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/internal/scheduler"
)

// cronJobSeq gives every registered cron job a process-unique id. Job names
// stay unique across plugin reloads because unload cancels every job of the
// plugin before a new load registers replacements.
var cronJobSeq atomic.Uint64

// registerCron registers one cron job for a plugin. The handler is a
// JavaScript function that runs on the plugin's own event loop each time the
// schedule fires. It is called from the plugin's own event loop during script
// evaluation and takes the manager lock itself; the critical section is short
// and never waits on the event loop, so no lock cycle is possible. The job is
// removed automatically when the plugin is unloaded or fails to load.
func (m *Manager) registerCron(short, spec string, fn goja.Callable) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[short]
	if !ok {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	inst.mu.RLock()
	host := inst.host
	alive := inst.runtime != nil
	inst.mu.RUnlock()
	if !alive || host == nil {
		return fmt.Errorf("plugin %q runtime is not ready", short)
	}

	name := fmt.Sprintf("plugin:%s:%d", short, cronJobSeq.Add(1))
	if err := scheduler.AddContextFunc(name, spec, false, func(ctx context.Context) {
		m.runCronJob(short, name, fn)
	}); err != nil {
		return err
	}
	inst.mu.Lock()
	inst.cronJobs = append(inst.cronJobs, name)
	inst.mu.Unlock()
	return nil
}

// runCronJob bridges one scheduled fire into the plugin event loop. It runs
// on a scheduler goroutine and drops the fire when the plugin was unloaded
// (the job is cancelled with the instance, but a fire may already be queued).
// Errors thrown by the handler are reported to the plugin log by RunJob.
func (m *Manager) runCronJob(short, name string, fn goja.Callable) {
	inst := m.instanceFor(short)
	if inst == nil {
		return
	}
	inst.mu.RLock()
	host := inst.host
	alive := inst.runtime != nil
	inst.mu.RUnlock()
	if !alive || host == nil {
		return
	}
	host.RunOnLoop(func(vm *goja.Runtime) {
		_ = host.RunJob(vm, "plugin cron "+name, func() error {
			_, err := fn(goja.Undefined())
			return err
		})
	})
}
