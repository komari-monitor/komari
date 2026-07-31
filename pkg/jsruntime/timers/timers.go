package timers

import (
	"errors"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
)

type Module struct {
	runtime *bridge.Runtime
}

func New(runtime *bridge.Runtime) *Module {
	return &Module{runtime: runtime}
}

func (m *Module) Inject(vm *goja.Runtime) error {
	if err := vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		callback, args := timerCallback(vm, call, 2)
		timer := m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
			m.runtime.RunJob(vm, "setTimeout", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
		}, timerDelay(call.Argument(1)))
		return vm.ToValue(timer)
	}); err != nil {
		return err
	}
	if err := vm.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		callback, args := timerCallback(vm, call, 2)
		var interval *eventloop.Interval
		interval = m.runtime.Loop().SetInterval(func(vm *goja.Runtime) {
			err := m.runtime.RunJob(vm, "setInterval", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
			if errors.Is(err, bridge.ErrExecutionTimeout) {
				m.runtime.Loop().ClearInterval(interval)
			}
		}, timerDelay(call.Argument(1)))
		return vm.ToValue(interval)
	}); err != nil {
		return err
	}
	if err := vm.Set("setImmediate", func(call goja.FunctionCall) goja.Value {
		callback, args := timerCallback(vm, call, 1)
		timer := m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) {
			m.runtime.RunJob(vm, "setImmediate", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
		}, 0)
		return vm.ToValue(timer)
	}); err != nil {
		return err
	}
	clear := func(call goja.FunctionCall) goja.Value { return m.clearTimer(call) }
	if err := vm.Set("clearTimeout", clear); err != nil {
		return err
	}
	if err := vm.Set("clearInterval", clear); err != nil {
		return err
	}
	return vm.Set("clearImmediate", clear)
}

func timerCallback(vm *goja.Runtime, call goja.FunctionCall, firstArgument int) (goja.Callable, []goja.Value) {
	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(vm.NewTypeError("timer callback must be a function"))
	}
	if len(call.Arguments) <= firstArgument {
		return callback, nil
	}
	return callback, append([]goja.Value(nil), call.Arguments[firstArgument:]...)
}

func (m *Module) clearTimer(call goja.FunctionCall) goja.Value {
	switch timer := call.Argument(0).Export().(type) {
	case *eventloop.Timer:
		m.runtime.Loop().ClearTimeout(timer)
	case *eventloop.Interval:
		m.runtime.Loop().ClearInterval(timer)
	}
	return goja.Undefined()
}

func timerDelay(value goja.Value) time.Duration {
	milliseconds := value.ToInteger()
	if milliseconds <= 0 {
		return 0
	}
	const maxMilliseconds = int64((1<<63 - 1) / int64(time.Millisecond))
	if milliseconds > maxMilliseconds {
		milliseconds = maxMilliseconds
	}
	return time.Duration(milliseconds) * time.Millisecond
}
