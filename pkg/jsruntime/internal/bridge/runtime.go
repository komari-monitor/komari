// Package bridge provides the small set of runtime services shared by host
// modules. It deliberately does not depend on the parent jsruntime package.
package bridge

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

var ErrExecutionTimeout = errors.New("JavaScript execution timeout")

type TurnRunner func(*goja.Runtime, func() error) error

type ErrorReporter func(*goja.Runtime, string, error)

// Runtime is the host-facing service boundary used by JavaScript modules.
type Runtime struct {
	loop    *eventloop.EventLoop
	timeout time.Duration

	callbackMu sync.RWMutex
	runTurn    TurnRunner
	report     ErrorReporter

	resourceMu      sync.Mutex
	resourceID      uint64
	resources       map[uint64]func()
	resourcesClosed bool
}

func New(loop *eventloop.EventLoop, timeout time.Duration) *Runtime {
	return &Runtime{
		loop:      loop,
		timeout:   timeout,
		resources: make(map[uint64]func()),
	}
}

func (r *Runtime) Loop() *eventloop.EventLoop { return r.loop }

func (r *Runtime) Timeout() time.Duration { return r.timeout }

func (r *Runtime) SetTurnRunner(run TurnRunner) {
	r.callbackMu.Lock()
	r.runTurn = run
	r.callbackMu.Unlock()
}

func (r *Runtime) SetErrorReporter(report ErrorReporter) {
	r.callbackMu.Lock()
	r.report = report
	r.callbackMu.Unlock()
}

func (r *Runtime) RunOnLoop(job func(*goja.Runtime)) bool {
	return r.loop.RunOnLoop(job)
}

func (r *Runtime) RunTurn(vm *goja.Runtime, job func() error) error {
	r.callbackMu.RLock()
	run := r.runTurn
	r.callbackMu.RUnlock()
	if run == nil {
		return job()
	}
	return run(vm, job)
}

// RunJob executes one asynchronous host callback as a bounded JavaScript turn.
func (r *Runtime) RunJob(vm *goja.Runtime, name string, job func() error) error {
	if !r.ResourcesOpen() {
		return nil
	}
	err := RunWithDeadline(vm, time.Now().Add(r.timeout), func() error {
		return r.RunTurn(vm, job)
	})
	if err != nil {
		if code, ok := ExitCodeFromError(err); ok && code == 0 {
			return nil
		}
		r.callbackMu.RLock()
		report := r.report
		r.callbackMu.RUnlock()
		if report != nil {
			report(vm, name, err)
		}
	}
	return err
}

func (r *Runtime) AddResource(close func()) uint64 {
	r.resourceMu.Lock()
	if r.resourcesClosed {
		r.resourceMu.Unlock()
		if close != nil {
			close()
		}
		return 0
	}
	r.resourceID++
	id := r.resourceID
	r.resources[id] = close
	r.resourceMu.Unlock()
	return id
}

func (r *Runtime) ResourcesOpen() bool {
	r.resourceMu.Lock()
	open := !r.resourcesClosed
	r.resourceMu.Unlock()
	return open
}

func (r *Runtime) RemoveResource(id uint64) {
	if id == 0 {
		return
	}
	r.resourceMu.Lock()
	delete(r.resources, id)
	r.resourceMu.Unlock()
}

func (r *Runtime) CloseResources() {
	r.resourceMu.Lock()
	if r.resourcesClosed {
		r.resourceMu.Unlock()
		return
	}
	r.resourcesClosed = true
	resources := make([]func(), 0, len(r.resources))
	for id, close := range r.resources {
		resources = append(resources, close)
		delete(r.resources, id)
	}
	r.resourceMu.Unlock()
	for _, close := range resources {
		if close != nil {
			close()
		}
	}
}

// RunWithDeadline interrupts synchronous JavaScript that exceeds deadline.
func RunWithDeadline(vm *goja.Runtime, deadline time.Time, fn func() error) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return ErrExecutionTimeout
	}

	var running atomic.Bool
	var timedOut atomic.Bool
	running.Store(true)
	timerDone := make(chan struct{})
	timer := time.AfterFunc(remaining, func() {
		defer close(timerDone)
		if running.CompareAndSwap(true, false) {
			timedOut.Store(true)
			vm.Interrupt(ErrExecutionTimeout)
		}
	})

	err := fn()
	if running.CompareAndSwap(true, false) {
		if !timer.Stop() {
			<-timerDone
		}
	} else {
		<-timerDone
		vm.ClearInterrupt()
	}
	if timedOut.Load() {
		return ErrExecutionTimeout
	}
	return err
}
