package fs

import (
	"context"
	"sync"
)

type reusableFactory struct {
	source Factory

	mu   sync.Mutex
	base FileSystem
}

func (f *reusableFactory) New(ctx context.Context) (FileSystem, error) {
	f.mu.Lock()
	base := f.base
	f.mu.Unlock()

	if base == nil {
		created, err := reusableSource(f.source).New(ctx)
		if err != nil {
			return nil, err
		}
		f.mu.Lock()
		if f.base == nil {
			f.base = created
		}
		base = f.base
		f.mu.Unlock()
	}

	return NewOverlay(base), nil
}

func reusableSource(source Factory) Factory {
	if source != nil {
		return source
	}
	return Memory()
}
