//go:build linux

package resourceprobe

import "golang.org/x/sys/unix"

func systemMemoryBytes() (uint64, error) {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0, err
	}
	return info.Totalram * uint64(info.Unit), nil
}

func systemDiskFreeBytes(path string) (uint64, error) {
	abs, err := absoluteDir(path)
	if err != nil {
		return 0, err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(abs, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
