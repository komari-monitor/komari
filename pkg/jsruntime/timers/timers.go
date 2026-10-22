package timers

import (
	"errors"
	moe "github.com/Calcium-Ion/moejs"
	e "github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/internal/bridge"
	"github.com/komari-monitor/komari/pkg/jsruntime/loop"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"time"
)

type Module struct {
	runtime *bridge.Runtime
	next    int64
	handles map[int64]any
}

func New(r *bridge.Runtime) *Module { return &Module{runtime: r, handles: make(map[int64]any)} }
func (m *Module) Inject(vm *moe.Runtime) error {
	for _, name := range []string{"setTimeout", "setInterval", "setImmediate"} {
		name := name
		fn := native.Bind(vm, name, 2, func(_ *moe.Realm, _ moe.Value, args []moe.Value) (moe.Value, error) {
			callback := moe.Arg(args, 0)
			if !e.IsCallable(callback) {
				return moe.Undefined(), native.TypeError(vm, "timer callback must be a function")
			}
			first := 2
			delay := time.Duration(0)
			if name == "setImmediate" {
				first = 1
			} else {
				delay = timerDelay(vm, moe.Arg(args, 1))
			}
			var params []moe.Value
			if len(args) > first {
				params = append(params, args[first:]...)
			}
			m.next++
			id := m.next
			job := func(rt *moe.Runtime) {
				if name != "setInterval" {
					delete(m.handles, id)
				}
				err := m.runtime.RunJob(rt, name, func() error { _, err := rt.CallFunction(callback, moe.Undefined(), params...); return err })
				if errors.Is(err, bridge.ErrExecutionTimeout) {
					m.clear(id)
				}
			}
			if name == "setInterval" {
				m.handles[id] = m.runtime.Loop().SetInterval(job, delay)
			} else {
				m.handles[id] = m.runtime.Loop().SetTimeout(job, delay)
			}
			return moe.Int(id), nil
		})
		if err := vm.SetGlobal(name, fn); err != nil {
			return err
		}
	}
	clear := native.Bind(vm, "clearTimer", 1, func(_ *moe.Realm, _ moe.Value, args []moe.Value) (moe.Value, error) {
		m.clear(native.Int(vm, moe.Arg(args, 0)))
		return moe.Undefined(), nil
	})
	for _, name := range []string{"clearTimeout", "clearInterval", "clearImmediate"} {
		if err := vm.SetGlobal(name, clear); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) clear(id int64) {
	switch timer := m.handles[id].(type) {
	case *loop.Timer:
		m.runtime.Loop().ClearTimeout(timer)
	case *loop.Interval:
		m.runtime.Loop().ClearInterval(timer)
	}
	delete(m.handles, id)
}
func timerDelay(vm *moe.Runtime, v moe.Value) time.Duration {
	n := native.Int(vm, v)
	if n <= 0 {
		return 0
	}
	const max = int64((1<<63 - 1) / int64(time.Millisecond))
	if n > max {
		n = max
	}
	return time.Duration(n) * time.Millisecond
}
