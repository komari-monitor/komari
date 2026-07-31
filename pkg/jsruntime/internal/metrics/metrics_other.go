//go:build !windows && !linux

package metrics

import (
	"fmt"
	"runtime"
)

func ReadSystem() (System, error) {
	return System{}, fmt.Errorf("system metrics are unsupported on %s", runtime.GOOS)
}

func ReadProcess() (Process, error) {
	return Process{}, fmt.Errorf("process metrics are unsupported on %s", runtime.GOOS)
}
