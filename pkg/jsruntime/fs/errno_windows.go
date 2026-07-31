//go:build windows

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
	code, uvErrno := "", -int64(errno)
	switch uint32(errno) {
	case 2, 3:
		code, uvErrno = "ENOENT", -4058
	case 5, 13, uint32(syscall.EACCES):
		code, uvErrno = "EACCES", -4092
	case 6:
		code, uvErrno = "EBADF", -4083
	case 32:
		code, uvErrno = "EBUSY", -4082
	case 80, 183:
		code, uvErrno = "EEXIST", -4075
	case 22, 87, uint32(syscall.EINVAL):
		code, uvErrno = "EINVAL", -4071
	case 109:
		code, uvErrno = "EPIPE", -4047
	case 112:
		code, uvErrno = "ENOSPC", -4055
	case 145:
		code, uvErrno = "ENOTEMPTY", -4051
	case 267:
		code, uvErrno = "ENOTDIR", -4052
	}
	return code, uvErrno
}

func nodeErrnoForCode(code string) int64 {
	return map[string]int64{
		"EACCES": -4092, "EBADF": -4083, "EBUSY": -4082, "EEXIST": -4075,
		"EINVAL": -4071, "EIO": -4070, "EISDIR": -4068, "ELOOP": -4067,
		"EMFILE": -4066, "ENAMETOOLONG": -4064, "ENFILE": -4060, "ENOENT": -4058,
		"ENOSPC": -4055, "ENOTDIR": -4052, "ENOTEMPTY": -4051, "EPERM": -4048,
		"EPIPE": -4047, "EROFS": -4044,
	}[code]
}
