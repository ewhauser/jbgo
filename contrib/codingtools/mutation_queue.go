package codingtools

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	gbfs "github.com/ewhauser/gbash/fs"
)

type mutationQueue struct {
	mu    sync.Mutex
	tails map[string]chan struct{}
}

func newMutationQueue() *mutationQueue {
	return &mutationQueue{tails: make(map[string]chan struct{})}
}

func withMutationQueue[T any](ctx context.Context, q *mutationQueue, key string, fn func() (T, error)) (T, error) {
	var zero T
	if q == nil {
		return fn()
	}

	q.mu.Lock()
	prev := q.tails[key]
	next := make(chan struct{})
	q.tails[key] = next
	q.mu.Unlock()

	if prev != nil {
		if ctx == nil {
			<-prev
		} else {
			select {
			case <-prev:
			case <-ctx.Done():
				releaseMutationQueueAfterPrev(q, key, prev, next)
				return zero, ctx.Err()
			}
		}
	}

	defer func() {
		releaseMutationQueue(q, key, next)
	}()

	return fn()
}

func releaseMutationQueueAfterPrev(q *mutationQueue, key string, prev, next chan struct{}) {
	if prev == nil {
		releaseMutationQueue(q, key, next)
		return
	}
	go func() {
		<-prev
		releaseMutationQueue(q, key, next)
	}()
}

func releaseMutationQueue(q *mutationQueue, key string, next chan struct{}) {
	close(next)
	q.mu.Lock()
	if q.tails[key] == next {
		delete(q.tails, key)
	}
	q.mu.Unlock()
}

func mutationQueueKey(ctx context.Context, fsys gbfs.FileSystem, resolvedPath string) string {
	keyPath := resolvedPath
	if fsys != nil {
		if realPath, err := fsys.Realpath(ctx, resolvedPath); err == nil {
			keyPath = gbfs.Clean(realPath)
		}
	}
	return filesystemIdentity(fsys) + ":" + keyPath
}

func filesystemIdentity(fsys gbfs.FileSystem) string {
	if fsys == nil {
		return "<nil>"
	}
	value := reflect.ValueOf(fsys)
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return fmt.Sprintf("%T:%x", fsys, value.Pointer())
	default:
		return fmt.Sprintf("%T:%v", fsys, fsys)
	}
}
