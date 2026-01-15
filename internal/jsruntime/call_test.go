package jsruntime

import (
	"testing"
	"time"
)

func TestCallSyncAndAsync(t *testing.T) {
	rt, err := NewBuilder().WithNodejs().Build()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Stop()

	_, err = rt.RunScript(`
		function add(a, b) { return a + b; }
		async function asyncValue() { return 42; }
		async function asyncReject() { throw new Error("boom"); }
	`)
	if err != nil {
		t.Fatalf("load script: %v", err)
	}

	v, err := rt.Call("add", 1, 2)
	if err != nil {
		t.Fatalf("call add: %v", err)
	}
	if got := v.Export(); got != int64(3) && got != 3 {
		t.Fatalf("unexpected add result: %#v", got)
	}

	v, err = rt.Call("asyncValue")
	if err != nil {
		t.Fatalf("call asyncValue: %v", err)
	}
	if got := v.Export(); got != int64(42) && got != 42 {
		t.Fatalf("unexpected asyncValue result: %#v", got)
	}

	_, err = rt.Call("asyncReject")
	if err == nil {
		t.Fatalf("expected error from asyncReject")
	}
}

func TestCallAsyncPromise(t *testing.T) {
	rt, err := NewBuilder().WithNodejs().Build()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	defer rt.Stop()

	_, err = rt.RunScript(`
		function delay(ms) { return new Promise(resolve => setTimeout(resolve, ms)); }
		async function delayed() { await delay(30); return "ok"; }
	`)
	if err != nil {
		t.Fatalf("load script: %v", err)
	}

	ch := rt.CallAsync("delayed")
	select {
	case res := <-ch:
		if res.Err != nil {
			t.Fatalf("callAsync delayed err: %v", res.Err)
		}
		if got := res.Value.String(); got != "ok" {
			t.Fatalf("unexpected delayed result: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for CallAsync result")
	}
}
