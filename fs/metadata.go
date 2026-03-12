package fs

import stdfs "io/fs"

const (
	DefaultOwnerUID uint32 = 1000
	DefaultOwnerGID uint32 = 1000
)

type FileOwnership struct {
	UID uint32
	GID uint32
}

type ownershipProvider interface {
	Ownership() (FileOwnership, bool)
}

func DefaultOwnership() FileOwnership {
	return FileOwnership{UID: DefaultOwnerUID, GID: DefaultOwnerGID}
}

func OwnershipFromFileInfo(info stdfs.FileInfo) (FileOwnership, bool) {
	if info == nil {
		return FileOwnership{}, false
	}
	if provider, ok := info.(ownershipProvider); ok {
		if ownership, ok := provider.Ownership(); ok {
			return ownership, true
		}
	}
	switch ownership := info.Sys().(type) {
	case FileOwnership:
		return ownership, true
	case *FileOwnership:
		if ownership != nil {
			return *ownership, true
		}
	}
	return OwnershipFromSys(info.Sys())
}
