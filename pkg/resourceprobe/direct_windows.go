//go:build windows

package resourceprobe

import (
	"os"

	"golang.org/x/sys/windows"
)

func openUnbufferedBenchmarkFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_TEMPORARY|windows.FILE_FLAG_NO_BUFFERING,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
