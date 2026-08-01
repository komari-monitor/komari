package process

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

func (m *Module) initNextTickScheduler(vm *goja.Runtime) error {
	factoryValue, err := vm.RunString(nextTickSchedulerSource)
	if err != nil {
		return fmt.Errorf("create process.nextTick scheduler: %w", err)
	}
	factory, _ := goja.AssertFunction(factoryValue)
	schedulerValue, err := factory(goja.Undefined(), vm.ToValue(func() { m.drainNextTicks(vm) }))
	if err != nil {
		return fmt.Errorf("initialize process.nextTick scheduler: %w", err)
	}
	scheduler := schedulerValue.ToObject(vm)
	m.nextTickTurn, _ = goja.AssertFunction(scheduler.Get("run"))
	m.nextTickSchedule, _ = goja.AssertFunction(scheduler.Get("schedule"))
	if m.nextTickTurn == nil || m.nextTickSchedule == nil {
		return errors.New("process.nextTick scheduler is incomplete")
	}
	return nil
}

func (m *Module) enqueueNextTick(vm *goja.Runtime, tick nodeNextTick) {
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
	if m.nextTickSchedule != nil {
		if _, err := m.nextTickSchedule(goja.Undefined()); err == nil {
			return
		}
	}
	m.runtime.Loop().SetTimeout(func(vm *goja.Runtime) { m.drainNextTicks(vm) }, 0)
}

func (m *Module) drainNextTicks(vm *goja.Runtime) {
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
		if _, err := tick.callback(goja.Undefined(), tick.arguments...); err != nil {
			if m.reportError != nil {
				m.reportError(vm, []goja.Value{vm.ToValue(fmt.Sprintf("process.nextTick callback failed: %v", err))})
			}
		}
	}
}

func (m *Module) RunTurn(vm *goja.Runtime, job func() error) error {
	if m.nextTickTurn == nil {
		return job()
	}
	m.nextTickMu.Lock()
	m.nextTickScheduled = true
	m.nextTickMu.Unlock()
	var jobErr error
	jobValue := vm.ToValue(func() { jobErr = job() })
	_, turnErr := m.nextTickTurn(goja.Undefined(), jobValue)
	return errors.Join(jobErr, turnErr)
}

//go:embed nexttick.js
var nextTickSchedulerSource string
