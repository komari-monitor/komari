package jsruntime

import (
	"testing"
	"time"

	"github.com/gookit/event"
)

func TestWithEvent_CrossRuntimeTrigger(t *testing.T) {
	rtA, err := NewBuilder().WithEvent().Build()
	if err != nil {
		t.Fatalf("build runtime A: %v", err)
	}
	defer rtA.Stop()

	rtB, err := NewBuilder().WithEvent().Build()
	if err != nil {
		t.Fatalf("build runtime B: %v", err)
	}
	defer rtB.Stop()

	_, err = rtA.RunScript(`
		var called = 0;
		event.On("jsruntime.test.hello", function(e) {
			called += e.data.n;
		});
	`)
	if err != nil {
		t.Fatalf("install listener: %v", err)
	}

	_, err = rtB.RunScript(`
		Event.Trigger("jsruntime.test.hello", { n: 2 });
	`)
	if err != nil {
		t.Fatalf("trigger from B: %v", err)
	}

	v, err := rtA.RunScript("called")
	if err != nil {
		t.Fatalf("read called: %v", err)
	}

	got := v.Export()
	if got != int64(2) && got != 2 {
		t.Fatalf("unexpected called: %#v", got)
	}
}

func TestWithEvent_StopCleansListeners(t *testing.T) {
	name := "jsruntime.test.stop." + time.Now().Format("150405.000")
	before := event.Std().ListenersCount(name)

	rt, err := NewBuilder().WithEvent().Build()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	_, err = rt.RunScript(`
		event.On("` + name + `", function(e) {});
	`)
	if err != nil {
		rt.Stop()
		t.Fatalf("install listener: %v", err)
	}

	afterAdd := event.Std().ListenersCount(name)
	if afterAdd != before+1 {
		rt.Stop()
		t.Fatalf("expected listeners +1, before=%d after=%d", before, afterAdd)
	}

	rt.Stop()

	afterStop := event.Std().ListenersCount(name)
	if afterStop != before {
		t.Fatalf("expected listeners cleaned on Stop, before=%d afterStop=%d", before, afterStop)
	}
}
