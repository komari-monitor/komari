package loop

import (
	"github.com/Calcium-Ion/moejs"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueAndTerminate(t *testing.T) {
	l := NewEventLoop()
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	if !l.RunOnLoop(func(vm *moejs.Runtime) { close(done) }) {
		t.Fatal("rejected")
	}
	<-done
	l.Terminate()
	if l.RunOnLoop(func(*moejs.Runtime) {}) {
		t.Fatal("accepted after close")
	}
	if l.SetTimeout(func(*moejs.Runtime) {}, time.Millisecond) != nil {
		t.Fatal("timer after close")
	}
}
func TestHostAndJSTimers(t *testing.T) {
	l := NewEventLoop()
	defer l.Terminate()
	var count atomic.Int32
	timer := l.SetTimeout(func(*moejs.Runtime) { count.Add(100) }, time.Millisecond)
	l.ClearTimeout(timer)
	l.SetTimeout(func(*moejs.Runtime) { count.Add(1) }, time.Millisecond)
	if err := l.Run(func(vm *moejs.Runtime) {
		if _, err := native.Run(vm, "var n=0;var t=setInterval(function(){n++;if(n===2)clearInterval(t)},1)"); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
	if n := native.Int(l.Runtime(), native.Global(l.Runtime(), "n")); n != 2 {
		t.Fatal(n)
	}
}
func TestTerminateInterrupts(t *testing.T) {
	l := NewEventLoop()
	if err := l.Start(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	l.RunOnLoop(func(vm *moejs.Runtime) { close(started); _, _ = native.Run(vm, "for(;;){}") })
	<-started
	done := make(chan struct{})
	go func() { l.Terminate(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second * 3):
		t.Fatal("terminate did not interrupt")
	}
}
