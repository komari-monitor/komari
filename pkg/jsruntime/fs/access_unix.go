//go:build !windows

package fs

import (
	"os"
	"syscall"
)

func checkNodeAccess(info os.FileInfo, mode int) error {
	if mode == 0 {
		return nil
	}

	permissions := int(info.Mode().Perm())
	if os.Geteuid() == 0 {
		if mode&4 != 0 && permissions&0444 == 0 {
			return syscall.EACCES
		}
		if mode&2 != 0 && permissions&0222 == 0 {
			return syscall.EACCES
		}
		if mode&1 != 0 && permissions&0111 == 0 {
			return syscall.EACCES
		}
		return nil
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		uid := uint32(os.Geteuid())
		gid := uint32(os.Getegid())
		switch {
		case uid == stat.Uid:
			permissions = (permissions >> 6) & 7
		case gid == stat.Gid || nodeSupplementaryGroup(stat.Gid):
			permissions = (permissions >> 3) & 7
		default:
			permissions &= 7
		}
	} else {
		permissions &= 7
	}

	if mode&4 != 0 && permissions&4 == 0 {
		return syscall.EACCES
	}
	if mode&2 != 0 && permissions&2 == 0 {
		return syscall.EACCES
	}
	if mode&1 != 0 && permissions&1 == 0 {
		return syscall.EACCES
	}
	return nil
}

func nodeSupplementaryGroup(group uint32) bool {
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, candidate := range groups {
		if uint32(candidate) == group {
			return true
		}
	}
	return false
}
