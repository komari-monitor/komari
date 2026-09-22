package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/jsruntime"
	"github.com/komari-monitor/komari/utils/messageSender"
)

// registerNotificationChannel registers one channel owned by a plugin. The
// channel is tied to the current runtime, so unloading the plugin removes it.
func (m *Manager) registerNotificationChannel(
	short string,
	id string,
	configuration models.Configuration,
	handler goja.Callable,
	host *jsruntime.Host,
) error {
	inst := m.instanceFor(short)
	if inst == nil {
		return fmt.Errorf("plugin %q is not loaded", short)
	}
	if host == nil {
		return fmt.Errorf("plugin %q notification runtime is unavailable", short)
	}
	notificationChannel := &pluginNotificationChannel{
		short:   short,
		host:    host,
		handler: handler,
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.runtime == nil {
		return fmt.Errorf("plugin %q notification runtime is closed", short)
	}
	if err := messageSender.RegisterNotificationChannel(id, configuration, notificationChannel); err != nil {
		return err
	}
	if inst.notificationChannels == nil {
		inst.notificationChannels = make(map[string]struct{})
	}
	inst.notificationChannels[id] = struct{}{}
	return nil
}

type pluginNotificationChannel struct {
	short   string
	host    *jsruntime.Host
	handler goja.Callable
}

func (c *pluginNotificationChannel) Send(ctx context.Context, notification messageSender.Notification, config map[string]any) error {
	if c.host == nil || c.handler == nil {
		return fmt.Errorf("plugin %q notification channel is closed", c.short)
	}
	result := make(chan error, 1)
	var once sync.Once
	finish := func(err error) {
		once.Do(func() { result <- err })
	}

	queued := c.host.RunOnLoop(func(vm *goja.Runtime) {
		var value goja.Value
		runErr := c.host.RunJob(vm, "plugin notification "+c.short, func() error {
			raw, err := json.Marshal(notification)
			if err != nil {
				return err
			}
			var notificationValue map[string]any
			if err := json.Unmarshal(raw, &notificationValue); err != nil {
				return err
			}
			var callErr error
			value, callErr = c.handler(
				goja.Undefined(),
				vm.ToValue(notificationValue),
				vm.ToValue(config),
			)
			return callErr
		})
		if runErr != nil {
			finish(runErr)
			return
		}
		_, ok := value.Export().(*goja.Promise)
		if !ok {
			finish(nil)
			return
		}
		then, ok := goja.AssertFunction(value.ToObject(vm).Get("then"))
		if !ok {
			finish(fmt.Errorf("plugin %q notification channel returned a Promise without then", c.short))
			return
		}
		onFulfilled := vm.ToValue(func(goja.FunctionCall) goja.Value {
			finish(nil)
			return goja.Undefined()
		})
		onRejected := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			finish(fmt.Errorf("plugin %q notification channel failed: %v", c.short, call.Argument(0)))
			return goja.Undefined()
		})
		if _, err := then(value, onFulfilled, onRejected); err != nil {
			finish(fmt.Errorf("plugin %q notification channel Promise failed: %w", c.short, err))
		}
	})
	if !queued {
		return fmt.Errorf("plugin %q notification runtime is closed", c.short)
	}

	timeout := c.host.Timeout()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return context.DeadlineExceeded
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("plugin %q notification channel timed out", c.short)
	}
}

func (c *pluginNotificationChannel) Unload() error {
	return nil
}

func (m *Manager) unregisterNotificationChannels(inst *Instance) error {
	if inst == nil {
		return nil
	}
	inst.mu.Lock()
	ids := make([]string, 0, len(inst.notificationChannels))
	for id := range inst.notificationChannels {
		ids = append(ids, id)
	}
	inst.notificationChannels = nil
	inst.mu.Unlock()
	var firstErr error
	for _, id := range ids {
		if err := messageSender.UnregisterNotificationChannel(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
