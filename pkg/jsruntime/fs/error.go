package fs

import (
	"errors"
	"github.com/Calcium-Ion/moejs"
	"github.com/Calcium-Ion/moejs/engine"
	"github.com/komari-monitor/komari/pkg/jsruntime/native"
	"io/fs"
	"os"
	"strings"
)

func nodeErrorObject(vm *moejs.Runtime, err error, fallbackSyscall string) *engine.Object {
	operation, path, destination := fallbackSyscall, "", ""
	underlying := err
	var linkError *os.LinkError
	var pathError *os.PathError
	var syscallError *os.SyscallError
	switch {
	case errors.As(err, &linkError):
		operation, path, destination, underlying = linkError.Op, linkError.Old, linkError.New, linkError.Err
	case errors.As(err, &pathError):
		operation, path, underlying = pathError.Op, pathError.Path, pathError.Err
	case errors.As(err, &syscallError):
		operation, underlying = syscallError.Syscall, syscallError.Err
	}
	code, errno := nodeErrno(underlying)
	if code == "" {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			code = "ENOENT"
		case errors.Is(err, fs.ErrExist):
			code = "EEXIST"
		case errors.Is(err, fs.ErrPermission), strings.Contains(err.Error(), "escapes BaseDir"):
			code = "EACCES"
		case strings.Contains(err.Error(), "bad file descriptor"):
			code = "EBADF"
		default:
			code = "EIO"
		}
	}
	if errno == 0 {
		errno = nodeErrnoForCode(code)
	}
	object := native.Object(vm, native.GoError(vm, err))
	_ = native.Set(vm, object, "name", "Error")
	_ = native.Set(vm, object, "code", code)
	_ = native.Set(vm, object, "errno", errno)
	_ = native.Set(vm, object, "syscall", operation)
	if path != "" {
		_ = native.Set(vm, object, "path", path)
	}
	if destination != "" {
		_ = native.Set(vm, object, "dest", destination)
	}
	return object
}
