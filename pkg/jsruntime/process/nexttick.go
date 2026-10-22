package process

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)

func (m *Module) initNextTickScheduler(vm *moejs.Runtime) error {
	factoryValue, err := native.Run(vm, nextTickSchedulerSource)
	if err != nil {
		return fmt.Errorf("create process.nextTick scheduler: %w", err)
	}
	factory, _ := checkedFunction(factoryValue)
	schedulerValue, err := native.Call(vm, factory, moejs.Undefined(), native.From(vm, native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		m.drainNextTicks(vm)
		return moejs.Undefined(), nil
	})))
	if err != nil {
		return fmt.Errorf("initialize process.nextTick scheduler: %w", err)
	}
	scheduler := native.Object(vm, schedulerValue)
	m.nextTickTurn, _ = checkedFunction(native.Get(vm, scheduler, "run"))
	m.nextTickSchedule, _ = checkedFunction(native.Get(vm, scheduler, "schedule"))
	if native.IsNil(m.nextTickTurn) || native.IsNil(m.nextTickSchedule) {
		return errors.New("process.nextTick scheduler is incomplete")
	}
	return nil
}
func (m *Module) enqueueNextTick(vm *moejs.Runtime, tick nodeNextTick) {
	m.nextTickMu.Lock()
	m.nextTicks = append(m.nextTicks, tick)
	shouldSchedule := !m.nextTickScheduled && !m.nextTickDraining
	if shouldSchedule {
		m.nextTickScheduled = true
	}
	m.nextTickMu.Unlock()
	if !shouldSchedule {
		return
	}
	if !native.IsNil(m.nextTickSchedule) {
		if _, err := native.Call(vm, m.nextTickSchedule, moejs.Undefined()); err == nil {
			return
		}
	}
	m.runtime.Loop().SetTimeout(func(vm *moejs.Runtime) {
		m.drainNextTicks(vm)
	}, 0)
}
func (m *Module) drainNextTicks(vm *moejs.Runtime) {
	m.nextTickMu.Lock()
	m.nextTickDraining = true
	m.nextTickScheduled = false
	m.nextTickMu.Unlock()
	for {
		m.nextTickMu.Lock()
		if len(m.nextTicks) == 0 {
			m.nextTickDraining = false
			m.nextTickMu.Unlock()
			return
		}
		tick := m.nextTicks[0]
		m.nextTicks[0] = nodeNextTick{}
		m.nextTicks = m.nextTicks[1:]
		m.nextTickMu.Unlock()
		if _, err := native.Call(vm, tick.callback, moejs.Undefined(), tick.arguments...); err != nil {
			if m.reportError != nil {
				m.reportError(vm, []moejs.Value{native.From(vm, fmt.Sprintf("process.nextTick callback failed: %v", err))})
			}
		}
	}
}
func (m *Module) RunTurn(vm *moejs.Runtime, job func() error) error {
	if native.IsNil(m.nextTickTurn) {
		return job()
	}
	m.nextTickMu.Lock()
	m.nextTickScheduled = true
	m.nextTickMu.Unlock()
	var jobErr error
	jobValue := native.From(vm, native.Bind(vm, "", 0, func(_ *moejs.Realm, this moejs.Value, args []moejs.Value) (moejs.Value, error) {
		jobErr = job()
		return moejs.Undefined(), nil
	}))
	_, turnErr := native.Call(vm, m.nextTickTurn, moejs.Undefined(), jobValue)
	return errors.Join(jobErr, turnErr)
}

//go:embed nexttick.js
var nextTickSchedulerSource string
