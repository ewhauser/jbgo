//go:build windows

package fs

import (
	"context"
	"errors"
)

var errHostUnsupported = errors.New("host-backed filesystem is unsupported on Windows")

type HostFS struct{}

func NewHost(HostOptions) (*HostFS, error) {
	return nil, errHostUnsupported
}

func (HostFactory) New(context.Context) (FileSystem, error) {
	return nil, errHostUnsupported
}
