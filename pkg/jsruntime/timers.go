package jsruntime

import (
	"errors"
	"fmt"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

func (r *Runtime) injectTimers() {
	_ = r.vm.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		callback, args := r.timerCallback(call, 2)
		timer := r.loop.SetTimeout(func(vm *goja.Runtime) {
			r.runAsyncJob(vm, "setTimeout", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
		}, timerDelay(call.Argument(1)))
		return r.vm.ToValue(timer)
	})
	_ = r.vm.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		callback, args := r.timerCallback(call, 2)
		var interval *eventloop.Interval
		interval = r.loop.SetInterval(func(vm *goja.Runtime) {
			err := r.runAsyncJob(vm, "setInterval", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
			if errors.Is(err, errExecutionTimeout) {
				r.loop.ClearInterval(interval)
			}
		}, timerDelay(call.Argument(1)))
		return r.vm.ToValue(interval)
	})
	_ = r.vm.Set("setImmediate", func(call goja.FunctionCall) goja.Value {
		callback, args := r.timerCallback(call, 1)
		timer := r.loop.SetTimeout(func(vm *goja.Runtime) {
			r.runAsyncJob(vm, "setImmediate", func() error {
				_, err := callback(goja.Undefined(), args...)
				return err
			})
		}, 0)
		return r.vm.ToValue(timer)
	})
	_ = r.vm.Set("clearTimeout", r.clearTimer)
	_ = r.vm.Set("clearInterval", r.clearTimer)
	_ = r.vm.Set("clearImmediate", r.clearTimer)
}

func (r *Runtime) timerCallback(call goja.FunctionCall, firstArgument int) (goja.Callable, []goja.Value) {
	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		panic(r.vm.NewTypeError("timer callback must be a function"))
	}
	if len(call.Arguments) <= firstArgument {
		return callback, nil
	}
	return callback, append([]goja.Value(nil), call.Arguments[firstArgument:]...)
}

func (r *Runtime) clearTimer(call goja.FunctionCall) goja.Value {
	switch timer := call.Argument(0).Export().(type) {
	case *eventloop.Timer:
		r.loop.ClearTimeout(timer)
	case *eventloop.Interval:
		r.loop.ClearInterval(timer)
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

func (r *Runtime) runAsyncJob(vm *goja.Runtime, name string, job func() error) error {
	err := runWithDeadline(vm, time.Now().Add(r.timeout), job)
	if err != nil {
		r.writeConsole(consoleError, []goja.Value{
			vm.ToValue(fmt.Sprintf("%s callback failed: %v", name, err)),
		}, false)
	}
	return err
}
