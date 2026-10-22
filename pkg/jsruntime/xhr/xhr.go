package xhr

import (
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
)
import (
	"fmt"
	"github.com/Calcium-Ion/moejs"
)

func Inject(vm *moejs.Runtime) error {
	if _, err := native.Run(vm, xhrAPISource); err != nil {
		return fmt.Errorf("inject XMLHttpRequest: %w", err)
	}
	return nil
}
