package fs

import "context"

// HostOptions configures a read-only host directory mounted into the sandbox.
type HostOptions struct {
	Root             string
	VirtualRoot      string
	MaxFileReadBytes int64
}

const (
	defaultHostVirtualRoot      = "/home/agent/project"
	defaultHostMaxFileReadBytes = 10 << 20
)

const (
	// DefaultHostVirtualRoot is the default virtual mount point for host-backed project trees.
	DefaultHostVirtualRoot = defaultHostVirtualRoot
	// DefaultHostMaxFileReadBytes is the default regular-file read cap for HostFS.
	DefaultHostMaxFileReadBytes = defaultHostMaxFileReadBytes
)

// Host returns a factory that mounts a read-only host directory into the sandbox.
func Host(opts HostOptions) Factory {
	return FactoryFunc(func(context.Context) (FileSystem, error) {
		return NewHost(opts)
	})
}
