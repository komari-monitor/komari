//go:build windows

package fs

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func setNodeFileTimes(file *os.File, accessTime, modifyTime time.Time) error {
	access := windows.NsecToFiletime(accessTime.UnixNano())
	modified := windows.NsecToFiletime(modifyTime.UnixNano())
	return windows.SetFileTime(windows.Handle(file.Fd()), nil, &access, &modified)
}
