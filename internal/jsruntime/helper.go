package jsruntime

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// curGID returns the current goroutine id.
//
// NOTE: This uses a runtime.Stack parsing trick. It's OK for internal coordination
// (e.g. detecting whether we're on the eventloop goroutine) but should not be
// relied on for security.
func curGID() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// format: "goroutine 123 [running]:\n"
	line := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	idField := strings.Fields(line)
	if len(idField) == 0 {
		return -1
	}
	id, err := strconv.ParseInt(idField[0], 10, 64)
	if err != nil {
		return -1
	}
	return id
}

// drainMicrotasks pushes goja's job queue (Promise callbacks, etc.).
// goja runs queued jobs after executing a program/script, so we run a tiny no-op.
func drainMicrotasks(vm *goja.Runtime) {
	if vm == nil {
		return
	}
	_, _ = vm.RunString("void 0")
}
