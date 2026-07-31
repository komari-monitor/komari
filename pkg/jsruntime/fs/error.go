package fs

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/dop251/goja"
)

func nodeErrorObject(vm *goja.Runtime, err error, fallbackSyscall string) *goja.Object {
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
	object := vm.NewGoError(err)
	_ = object.Set("name", "Error")
	_ = object.Set("code", code)
	_ = object.Set("errno", errno)
	_ = object.Set("syscall", operation)
	if path != "" {
		_ = object.Set("path", path)
	}
	if destination != "" {
		_ = object.Set("dest", destination)
	}
	return object
}
