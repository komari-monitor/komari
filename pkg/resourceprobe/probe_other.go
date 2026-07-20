//go:build !linux && !windows

package resourceprobe

import "errors"

func systemMemoryBytes() (uint64, error) {
	return 0, errors.New("system memory probe is unsupported on this platform")
}

func systemDiskFreeBytes(string) (uint64, error) {
	return 0, errors.New("disk space probe is unsupported on this platform")
}
