package xhr

import (
	"fmt"

	"github.com/dop251/goja"
)

func Inject(vm *goja.Runtime) error {
	if _, err := vm.RunString(xhrAPISource); err != nil {
		return fmt.Errorf("inject XMLHttpRequest: %w", err)
	}
	return nil
}
