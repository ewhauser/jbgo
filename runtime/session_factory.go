package runtime

import (
	"context"
	"fmt"
	"sync"

	gbfs "github.com/ewhauser/gbash/fs"
)

type sessionFactory interface {
	New(ctx context.Context) (gbfs.FileSystem, error)
	layoutReady() bool
}

type plainSessionFactory struct {
	base gbfs.Factory
}

func (f plainSessionFactory) New(ctx context.Context) (gbfs.FileSystem, error) {
	return f.base.New(ctx)
}

func (plainSessionFactory) layoutReady() bool {
	return false
}

type preparedMemorySessionFactory struct {
	base     gbfs.Factory
	env      map[string]string
	workDir  string
	commands []string

	mu    sync.Mutex
	ready *gbfs.MemoryFS
}

func (f *preparedMemorySessionFactory) New(ctx context.Context) (gbfs.FileSystem, error) {
	f.mu.Lock()
	ready := f.ready
	f.mu.Unlock()

	if ready == nil {
		baseFactory := f.base
		if baseFactory == nil {
			baseFactory = gbfs.Memory()
		}
		created, err := baseFactory.New(ctx)
		if err != nil {
			return nil, err
		}
		if err := initializeSandboxLayout(ctx, created, f.env, f.workDir, f.commands); err != nil {
			return nil, err
		}
		base, ok := created.(*gbfs.MemoryFS)
		if !ok {
			return nil, fmt.Errorf("default session base is %T, want *fs.MemoryFS", created)
		}
		f.mu.Lock()
		if f.ready == nil {
			f.ready = base
		}
		ready = f.ready
		f.mu.Unlock()
	}

	return ready.Clone(), nil
}

func (*preparedMemorySessionFactory) layoutReady() bool {
	return true
}
