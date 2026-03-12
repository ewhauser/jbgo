//go:build !windows

package fs

import "syscall"

func OwnershipFromSys(sys any) (FileOwnership, bool) {
	if stat, ok := sys.(*syscall.Stat_t); ok && stat != nil {
		return FileOwnership{UID: stat.Uid, GID: stat.Gid}, true
	}
	return FileOwnership{}, false
}
