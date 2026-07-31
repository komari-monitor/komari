//go:build !windows

package fs

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func setNodeFileTimes(file *os.File, accessTime, modifyTime time.Time) error {
	times := []unix.Timeval{
		unix.NsecToTimeval(accessTime.UnixNano()),
		unix.NsecToTimeval(modifyTime.UnixNano()),
	}
	return unix.Futimes(int(file.Fd()), times)
}
