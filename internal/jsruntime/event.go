package jsruntime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"github.com/gookit/event"
)

type runtimeEventState struct {
	stopped *uint32
	loopGID int64
	loop    *eventloop.EventLoop
	vm      *goja.Runtime

	mu        sync.Mutex
	listeners []runtimeListener
}

type runtimeListener struct {
	name     string
	listener event.Listener
}

func (s *runtimeEventState) add(name string, l event.Listener) {
	s.mu.Lock()
	s.listeners = append(s.listeners, runtimeListener{name: name, listener: l})
	s.mu.Unlock()
}

func (s *runtimeEventState) remove(name string, l event.Listener) {
	event.Std().RemoveListener(name, l)

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.listeners) - 1; i >= 0; i-- {
		if s.listeners[i].name == name && s.listeners[i].listener == l {
			s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
			return
		}
	}
}

func (s *runtimeEventState) cleanup() {
	s.mu.Lock()
	listeners := s.listeners
	s.listeners = nil
	s.mu.Unlock()

	for _, item := range listeners {
		event.Std().RemoveListener(item.name, item.listener)
	}
}

type jsEventListener struct {
	state    *runtimeEventState
	cb       goja.Value
	cbFn     goja.Callable
	cbCached bool
}

func (l *jsEventListener) Handle(e event.Event) error {
	if l == nil || l.state == nil {
		return nil
	}
	if l.state.stopped != nil && atomic.LoadUint32(l.state.stopped) == 1 {
		return nil
	}

	invoke := func(vm *goja.Runtime) error {
		if vm == nil {
			return errors.New("vm is nil")
		}

		if !l.cbCached {
			fn, ok := goja.AssertFunction(l.cb)
			if !ok {
				return errors.New("event listener is not a function")
			}
			l.cbFn = fn
			l.cbCached = true
		}

		jsEvt := vm.NewObject()
		_ = jsEvt.Set("name", e.Name())
		_ = jsEvt.Set("data", e.Data())
		_ = jsEvt.Set("get", func(call goja.FunctionCall) goja.Value {
			key := strings.TrimSpace(call.Argument(0).String())
			if key == "" {
				return goja.Undefined()
			}
			return vm.ToValue(e.Get(key))
		})

		ret, err := l.cbFn(goja.Undefined(), jsEvt)
		if err != nil {
			return err
		}
		if isThenable(vm, ret) {
			return fmt.Errorf("event listener returned a Promise/thenable; async listeners are not supported")
		}

		drainMicrotasks(vm)
		return nil
	}

	// If Trigger happens on the same eventloop goroutine, call directly to avoid deadlocks.
	if curGID() == l.state.loopGID {
		return invoke(l.state.vm)
	}

	done := make(chan error, 1)
	l.state.loop.RunOnLoop(func(vm *goja.Runtime) {
		done <- invoke(vm)
	})
	return <-done
}

func exportEventParams(vm *goja.Runtime, val goja.Value) (event.M, error) {
	if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
		return nil, nil
	}

	// Prefer plain object -> map
	var m map[string]any
	if err := vm.ExportTo(val, &m); err == nil {
		return m, nil
	}

	// Fallback: wrap as value
	return event.M{"value": val.Export()}, nil
}

func injectEvent(vm *goja.Runtime, loop *eventloop.EventLoop, stoppedFlag *uint32) (*runtimeEventState, error) {
	if vm == nil {
		return nil, errors.New("vm is nil")
	}
	if loop == nil {
		return nil, errors.New("event loop is nil")
	}

	state := &runtimeEventState{
		stopped: stoppedFlag,
		loopGID: curGID(),
		loop:    loop,
		vm:      vm,
	}

	onFn := func(call goja.FunctionCall) goja.Value {
		name := strings.TrimSpace(call.Argument(0).String())
		if name == "" {
			panic(vm.NewTypeError("event.On: name is required"))
		}

		cb := call.Argument(1)
		fn, ok := goja.AssertFunction(cb)
		if !ok {
			panic(vm.NewTypeError("event.On: callback must be a function"))
		}

		prio := event.Normal
		if pVal := call.Argument(2); pVal != nil && !goja.IsUndefined(pVal) && !goja.IsNull(pVal) {
			if p, ok := pVal.Export().(int64); ok {
				prio = int(p)
			} else if p, ok := pVal.Export().(int32); ok {
				prio = int(p)
			} else if p, ok := pVal.Export().(int); ok {
				prio = p
			}
		}

		listener := &jsEventListener{state: state, cb: cb, cbFn: fn}
		event.On(name, listener, prio)
		state.add(name, listener)

		// Return an unsubscribe function.
		off := vm.ToValue(func(call goja.FunctionCall) goja.Value {
			state.remove(name, listener)
			return goja.Undefined()
		})
		return off
	}

	triggerFn := func(call goja.FunctionCall) goja.Value {
		name := strings.TrimSpace(call.Argument(0).String())
		if name == "" {
			panic(vm.NewTypeError("Event.Trigger: name is required"))
		}

		params, err := exportEventParams(vm, call.Argument(1))
		if err != nil {
			panic(vm.NewGoError(err))
		}

		if err, _ := event.Trigger(name, params); err != nil {
			panic(vm.NewGoError(err))
		}
		return goja.Undefined()
	}

	eventObj := vm.NewObject()
	_ = eventObj.Set("On", onFn)
	_ = eventObj.Set("on", onFn)
	_ = eventObj.Set("Trigger", triggerFn)
	_ = eventObj.Set("trigger", triggerFn)

	eventClassObj := vm.NewObject()
	_ = eventClassObj.Set("Trigger", triggerFn)
	_ = eventClassObj.Set("trigger", triggerFn)

	vm.Set("event", eventObj)
	vm.Set("Event", eventClassObj)

	return state, nil
}
