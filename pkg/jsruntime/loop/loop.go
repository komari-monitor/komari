// Package loop serializes native MoeJS host jobs.
package loop

import (
	"github.com/Calcium-Ion/moejs"
	upstream "github.com/Calcium-Ion/moejs/eventloop"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"github.com/komari-monitor/komari/pkg/jsruntime/require"
	"sync/atomic"
	"time"
)

type EventLoop struct {
	core                                                 *upstream.Loop
	registry                                             *require.Registry
	onError                                              func(error)
	setTimeout, setInterval, clearTimeout, clearInterval moejs.Value
}
type Option func(*EventLoop)

func WithRegistry(r *require.Registry) Option { return func(l *EventLoop) { l.registry = r } }

func WithOnError(f func(error)) Option { return func(l *EventLoop) { l.onError = f } }
func NewEventLoop(opts ...Option) *EventLoop {
	l := &EventLoop{}
	for _, opt := range opts {
		opt(l)
	}
	// Plugins may extend builtins (for example reflect-metadata). Keep those
	// changes private to this runtime instead of using shared frozen intrinsics.
	vm := moejs.NewRuntime(moejs.Options{MutableIntrinsics: true})
	var err error
	l.core, err = upstream.New(vm, upstream.Options{OnError: func(err error) {
		if l.onError != nil {
			l.onError(err)
		}
	}})
	if err != nil {
		panic(err)
	}
	if l.registry == nil {
		l.registry = require.NewRegistry()
	}
	l.registry.Enable(vm)
	l.setTimeout = native.Global(vm, "setTimeout")
	l.setInterval = native.Global(vm, "setInterval")
	l.clearTimeout = native.Global(vm, "clearTimeout")
	l.clearInterval = native.Global(vm, "clearInterval")
	return l
}
func (l *EventLoop) Runtime() *moejs.Runtime { return l.core.Runtime() }
func (l *EventLoop) Start() error            { return l.core.Start() }
func (l *EventLoop) Run(fn func(*moejs.Runtime)) error {
	return l.core.Run(func(vm *moejs.Runtime) error {
		if fn != nil {
			fn(vm)
		}
		return nil
	})
}
func (l *EventLoop) RunOnLoop(fn func(*moejs.Runtime)) bool { return l.core.RunOnLoop(fn) }
func (l *EventLoop) Stop() error                            { return l.core.Stop() }
func (l *EventLoop) StopNoWait()                            { l.core.StopNoWait() }

// Terminate is an external-goroutine close: interrupt, join, drop pending work.
func (l *EventLoop) Terminate()      { l.core.Terminate() }
func (l *EventLoop) Interrupt(v any) { l.core.Interrupt(v) }

type timerState struct {
	cancelled atomic.Bool
	value     moejs.Value
}
type Timer struct{ state timerState }
type Interval struct{ state timerState }

func (l *EventLoop) schedule(s *timerState, fn func(*moejs.Runtime), delay time.Duration, repeating bool) bool {
	return l.RunOnLoop(func(vm *moejs.Runtime) {
		if s.cancelled.Load() {
			return
		}
		cb := native.Bind(vm, "hostTimer", 0, func(_ *moejs.Realm, _ moejs.Value, _ []moejs.Value) (moejs.Value, error) {
			if !s.cancelled.Load() {
				fn(vm)
			}
			return moejs.Undefined(), nil
		})
		setter := l.setTimeout
		if repeating {
			setter = l.setInterval
		}
		v, err := vm.CallFunction(setter, moejs.Undefined(), cb, native.From(vm, float64(delay)/float64(time.Millisecond)))
		if err != nil {
			if l.onError != nil {
				l.onError(err)
			}
			return
		}
		s.value = v
	})
}
func (l *EventLoop) SetTimeout(fn func(*moejs.Runtime), delay time.Duration) *Timer {
	t := &Timer{}
	if !l.schedule(&t.state, fn, delay, false) {
		return nil
	}
	return t
}
func (l *EventLoop) SetInterval(fn func(*moejs.Runtime), delay time.Duration) *Interval {
	t := &Interval{}
	if !l.schedule(&t.state, fn, delay, true) {
		return nil
	}
	return t
}
func (l *EventLoop) cancel(s *timerState, repeating bool) {
	s.cancelled.Store(true)
	l.RunOnLoop(func(vm *moejs.Runtime) {
		if s.value.IsUndefined() {
			return
		}
		clear := l.clearTimeout
		if repeating {
			clear = l.clearInterval
		}
		_, err := vm.CallFunction(clear, moejs.Undefined(), s.value)
		if err != nil && l.onError != nil {
			l.onError(err)
		}
	})
}
func (l *EventLoop) ClearTimeout(t *Timer) {
	if t != nil {
		l.cancel(&t.state, false)
	}
}
func (l *EventLoop) ClearInterval(t *Interval) {
	if t != nil {
		l.cancel(&t.state, true)
	}
}
