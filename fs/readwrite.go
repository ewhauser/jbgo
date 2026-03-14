package fs

import "context"

// ReadWriteOptions configures a read-write host directory mounted as the
// sandbox root.
type ReadWriteOptions struct {
	Root             string
	MaxFileReadBytes int64
}

// ReadWrite returns a factory that mounts a real host directory as a mutable
// sandbox root.
func ReadWrite(opts ReadWriteOptions) Factory {
	return FactoryFunc(func(context.Context) (FileSystem, error) {
		return NewReadWrite(opts)
	})
}
