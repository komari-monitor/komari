package jsruntime

import "github.com/dop251/goja"

// drainMicrotasks pushes goja's job queue (Promise callbacks, etc.).
// goja runs queued jobs after executing a program/script, so we run a tiny no-op.
func drainMicrotasks(vm *goja.Runtime) {
	if vm == nil {
		return
	}
	_, _ = vm.RunString("void 0")
}
