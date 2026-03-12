//go:build windows

package fs

func OwnershipFromSys(_ any) (FileOwnership, bool) {
	return FileOwnership{}, false
}
