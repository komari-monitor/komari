//go:build !windows && !linux

package jsruntime

import (
	"fmt"
	"runtime"
)

func readNodeSystemMetrics() (nodeSystemMetrics, error) {
	return nodeSystemMetrics{}, fmt.Errorf("system metrics are unsupported on %s", runtime.GOOS)
}

func readNodeProcessMetrics() (nodeProcessMetrics, error) {
	return nodeProcessMetrics{}, fmt.Errorf("process metrics are unsupported on %s", runtime.GOOS)
}
