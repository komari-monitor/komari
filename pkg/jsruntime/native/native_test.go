package native

import (
	"errors"
	moe "github.com/Calcium-Ion/moejs"
	"testing"
)

func TestGoErrorNative(t *testing.T) {
	rt := moe.NewRuntime(moe.Options{})
	v := GoError(rt, errors.New("sentinel"))
	if !v.IsObject() {
		t.Fatalf("error value %v", v)
	}
	if String(rt, Get(rt, v.AsObject(), "message")) != "sentinel" {
		t.Fatal("message lost")
	}
}
