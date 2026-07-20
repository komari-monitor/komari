//go:build !linux && !windows

package resourceprobe

import "os"

func openUnbufferedBenchmarkFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
}
