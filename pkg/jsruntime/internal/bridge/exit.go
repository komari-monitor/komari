package bridge

import (
	"errors"
	"fmt"
	moe "github.com/Calcium-Ion/moejs"
)

type ExitRequestedError struct{ Code int64 }

func (e *ExitRequestedError) Error() string { return fmt.Sprintf("process.exit(%d) requested", e.Code) }
func ExitCodeFromError(err error) (int64, bool) {
	var exit *ExitRequestedError
	if !errors.As(err, &exit) {
		return 0, false
	}
	return exit.Code, true
}
func ExitCodeFromValue(value moe.Value) (int64, bool) {
	return ExitCodeFromError(&moe.Exception{Value: value})
}
