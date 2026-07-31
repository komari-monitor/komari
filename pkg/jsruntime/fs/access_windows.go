//go:build windows

package fs

import (
	"os"
	"syscall"
)

func checkNodeAccess(info os.FileInfo, mode int) error {
	if mode&4 != 0 && info.Mode().Perm()&0444 == 0 {
		return syscall.EACCES
	}
	if mode&2 != 0 && info.Mode().Perm()&0222 == 0 {
		return syscall.EACCES
	}
	// Windows does not expose a portable executable/search permission bit.
	return nil
}
