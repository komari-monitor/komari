//go:build !windows

package fs

import (
	"errors"
	"syscall"
)

func nodeErrno(err error) (string, int64) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", 0
	}
	code := map[syscall.Errno]string{
		syscall.EACCES: "EACCES", syscall.EBADF: "EBADF", syscall.EBUSY: "EBUSY",
		syscall.EEXIST: "EEXIST", syscall.EINVAL: "EINVAL", syscall.EIO: "EIO",
		syscall.EISDIR: "EISDIR", syscall.ELOOP: "ELOOP", syscall.EMFILE: "EMFILE",
		syscall.ENAMETOOLONG: "ENAMETOOLONG", syscall.ENFILE: "ENFILE", syscall.ENOENT: "ENOENT",
		syscall.ENOSPC: "ENOSPC", syscall.ENOTDIR: "ENOTDIR", syscall.ENOTEMPTY: "ENOTEMPTY",
		syscall.EPERM: "EPERM", syscall.EPIPE: "EPIPE", syscall.EROFS: "EROFS",
	}[errno]
	return code, -int64(errno)
}

func nodeErrnoForCode(code string) int64 {
	errno := map[string]syscall.Errno{
		"EACCES": syscall.EACCES, "EBADF": syscall.EBADF, "EBUSY": syscall.EBUSY,
		"EEXIST": syscall.EEXIST, "EINVAL": syscall.EINVAL, "EIO": syscall.EIO,
		"EISDIR": syscall.EISDIR, "ELOOP": syscall.ELOOP, "EMFILE": syscall.EMFILE,
		"ENAMETOOLONG": syscall.ENAMETOOLONG, "ENFILE": syscall.ENFILE, "ENOENT": syscall.ENOENT,
		"ENOSPC": syscall.ENOSPC, "ENOTDIR": syscall.ENOTDIR, "ENOTEMPTY": syscall.ENOTEMPTY,
		"EPERM": syscall.EPERM, "EPIPE": syscall.EPIPE, "EROFS": syscall.EROFS,
	}[code]
	return -int64(errno)
}
