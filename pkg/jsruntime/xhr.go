package jsruntime

import "fmt"

func (r *Runtime) injectXMLHttpRequest() error {
	if _, err := r.vm.RunString(xhrAPISource); err != nil {
		return fmt.Errorf("inject XMLHttpRequest: %w", err)
	}
	return nil
}
