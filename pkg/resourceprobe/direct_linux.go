//go:build linux

package resourceprobe

import (
	"os"

	"golang.org/x/sys/unix"
)

func openUnbufferedBenchmarkFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_TRUNC|unix.O_DIRECT, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
