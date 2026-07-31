package bridge

import (
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

// ExitRequestedError stops a script turn with the requested process exit code.
// Code 0 is treated as a normal termination; nonzero codes are surfaced as
// failures.
type ExitRequestedError struct {
	Code int64
}

func (e *ExitRequestedError) Error() string {
	return fmt.Sprintf("process.exit(%d) requested", e.Code)
}

// ExitCodeFromError returns the exit code when err was thrown by process.exit.
func ExitCodeFromError(err error) (int64, bool) {
	var exitErr *ExitRequestedError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	return exitErr.Code, true
}

// ExitCodeFromValue returns the exit code when a rejected Promise carries the
// process.exit error object.
func ExitCodeFromValue(value goja.Value) (int64, bool) {
	object, ok := value.(*goja.Object)
	if !ok {
		return 0, false
	}
	raw := object.Get("value")
	if raw == nil || goja.IsUndefined(raw) || goja.IsNull(raw) {
		return 0, false
	}
	err, ok := raw.Export().(error)
	if !ok {
		return 0, false
	}
	return ExitCodeFromError(err)
}
