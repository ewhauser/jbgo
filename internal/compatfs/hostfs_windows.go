//go:build windows

package compatfs

import "errors"

var errUnsupported = errors.New("host compatibility filesystem is unsupported on Windows")

type HostFS struct{}

func New() (*HostFS, error) {
	return nil, errUnsupported
}
