package shell

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"sync"
	"sync/atomic"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const procSubstDir = "/tmp"

type procSubstDirection uint8

const (
	procSubstRead procSubstDirection = iota + 1
	procSubstWrite
)

type procSubstManager struct {
	mu      sync.Mutex
	nonce   string
	counter uint64
	closed  bool
	entries map[string]*procSubstEntry
}

type procSubstEntry struct {
	path      string
	name      string
	mode      stdfs.FileMode
	modTime   time.Time
	direction procSubstDirection
	consumer  *os.File
	producer  *os.File

	mu     sync.Mutex
	opened bool
}

type procSubstFS struct {
	inner   gbfs.FileSystem
	manager *procSubstManager
}

type procSubstFile struct {
	entry  *procSubstEntry
	file   *os.File
	closed atomic.Bool
}

type procSubstFileInfo struct {
	name    string
	mode    stdfs.FileMode
	modTime time.Time
}

func newProcSubstManager() *procSubstManager {
	return &procSubstManager{
		nonce:   newProcSubstNonce(),
		entries: make(map[string]*procSubstEntry),
	}
}

func newProcSubstFS(inner gbfs.FileSystem, manager *procSubstManager) gbfs.FileSystem {
	if inner == nil || manager == nil {
		return inner
	}
	return &procSubstFS{inner: inner, manager: manager}
}

func withProcSubstScope(exec *Execution) func() {
	if exec == nil {
		return func() {}
	}
	manager := newProcSubstManager()
	inner := exec.FS
	exec.procSubst = manager
	exec.FS = newProcSubstFS(inner, manager)
	return func() {
		manager.Close()
		exec.FS = inner
		exec.procSubst = nil
	}
}

func (m *procSubstManager) endpoint(_ context.Context, ps *syntax.ProcSubst) (*interp.ProcSubstEndpoint, error) {
	if m == nil {
		return nil, fmt.Errorf("process substitution manager is nil")
	}

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create process substitution pipe: %w", err)
	}
	procSubstPath := m.nextPath()

	entry := &procSubstEntry{
		path:    procSubstPath,
		name:    path.Base(procSubstPath),
		mode:    stdfs.ModeNamedPipe | 0o600,
		modTime: time.Now().UTC(),
	}

	switch ps.Op {
	case syntax.CmdIn:
		entry.direction = procSubstRead
		entry.consumer = readEnd
		entry.producer = writeEnd
	case syntax.CmdOut:
		entry.direction = procSubstWrite
		entry.consumer = writeEnd
		entry.producer = readEnd
	default:
		_ = readEnd.Close()
		_ = writeEnd.Close()
		return nil, fmt.Errorf("unsupported process substitution operator: %q", ps.Op)
	}

	entry.name = path.Base(entry.path)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		entry.closeAll()
		return nil, fmt.Errorf("process substitution manager is closed")
	}
	m.entries[entry.path] = entry
	m.mu.Unlock()

	endpoint := &interp.ProcSubstEndpoint{
		Path: entry.path,
	}
	switch ps.Op {
	case syntax.CmdIn:
		endpoint.Writer = entry.producer
	case syntax.CmdOut:
		endpoint.Reader = entry.producer
	}
	return endpoint, nil
}

func (m *procSubstManager) nextPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nextPathLocked()
}

func (m *procSubstManager) nextPathLocked() string {
	m.counter++
	return path.Join(procSubstDir, fmt.Sprintf(".gbash-procsub-%s-%d", m.nonce, m.counter))
}

func (m *procSubstManager) entry(name string) (*procSubstEntry, bool) {
	if m == nil {
		return nil, false
	}
	abs := gbfs.Clean(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[abs]
	return entry, ok
}

func (m *procSubstManager) open(abs string, flag int) (gbfs.File, error) {
	entry, ok := m.entry(abs)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
	}
	return entry.open(flag)
}

func (m *procSubstManager) stat(abs string) (stdfs.FileInfo, bool) {
	entry, ok := m.entry(abs)
	if !ok {
		return nil, false
	}
	return entry.info(), true
}

func (m *procSubstManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	entries := make([]*procSubstEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.entries = nil
	m.mu.Unlock()

	for _, entry := range entries {
		entry.closeConsumer()
	}
}

func (e *procSubstEntry) info() stdfs.FileInfo {
	return procSubstFileInfo{
		name:    e.name,
		mode:    e.mode,
		modTime: e.modTime,
	}
}

func (e *procSubstEntry) open(flag int) (gbfs.File, error) {
	if e == nil {
		return nil, &os.PathError{Op: "open", Path: "", Err: stdfs.ErrNotExist}
	}
	if err := e.checkFlags(flag); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.opened {
		return nil, &os.PathError{Op: "open", Path: e.path, Err: stdfs.ErrNotExist}
	}
	e.opened = true
	return &procSubstFile{entry: e, file: e.consumer}, nil
}

func (e *procSubstEntry) checkFlags(flag int) error {
	canRead := flag&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY
	canWrite := flag&(os.O_WRONLY|os.O_RDWR) != 0

	switch e.direction {
	case procSubstRead:
		if canWrite || !canRead {
			return &os.PathError{Op: "open", Path: e.path, Err: stdfs.ErrPermission}
		}
	case procSubstWrite:
		if !canWrite || canRead {
			return &os.PathError{Op: "open", Path: e.path, Err: stdfs.ErrPermission}
		}
	default:
		return &os.PathError{Op: "open", Path: e.path, Err: stdfs.ErrPermission}
	}
	return nil
}

func (e *procSubstEntry) closeAll() {
	if e == nil {
		return
	}
	if e.consumer != nil {
		_ = e.consumer.Close()
	}
	if e.producer != nil {
		_ = e.producer.Close()
	}
}

func (e *procSubstEntry) closeConsumer() {
	if e == nil || e.consumer == nil {
		return
	}
	_ = e.consumer.Close()
}

func newProcSubstNonce() string {
	var raw [8]byte
	if _, err := crand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func (f *procSubstFS) Open(ctx context.Context, name string) (gbfs.File, error) {
	return f.OpenFile(ctx, name, os.O_RDONLY, 0)
}

func (f *procSubstFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	if abs, ok := f.procSubstPath(name); ok {
		return f.manager.open(abs, flag)
	}
	return f.inner.OpenFile(ctx, name, flag, perm)
}

func (f *procSubstFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	if abs, ok := f.procSubstPath(name); ok {
		if info, ok := f.manager.stat(abs); ok {
			return info, nil
		}
		return nil, &os.PathError{Op: "stat", Path: abs, Err: stdfs.ErrNotExist}
	}
	return f.inner.Stat(ctx, name)
}

func (f *procSubstFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	if abs, ok := f.procSubstPath(name); ok {
		if info, ok := f.manager.stat(abs); ok {
			return info, nil
		}
		return nil, &os.PathError{Op: "lstat", Path: abs, Err: stdfs.ErrNotExist}
	}
	return f.inner.Lstat(ctx, name)
}

func (f *procSubstFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	return f.inner.ReadDir(ctx, name)
}

func (f *procSubstFS) Readlink(ctx context.Context, name string) (string, error) {
	return f.inner.Readlink(ctx, name)
}

func (f *procSubstFS) Realpath(ctx context.Context, name string) (string, error) {
	if abs, ok := f.procSubstPath(name); ok {
		if _, ok := f.manager.stat(abs); ok {
			return abs, nil
		}
		return "", &os.PathError{Op: "realpath", Path: abs, Err: stdfs.ErrNotExist}
	}
	return f.inner.Realpath(ctx, name)
}

func (f *procSubstFS) Symlink(ctx context.Context, target, linkName string) error {
	if abs, ok := f.procSubstPath(linkName); ok {
		return &os.PathError{Op: "symlink", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Symlink(ctx, target, linkName)
}

func (f *procSubstFS) Link(ctx context.Context, oldName, newName string) error {
	if abs, ok := f.procSubstPath(oldName); ok {
		return &os.PathError{Op: "link", Path: abs, Err: stdfs.ErrPermission}
	}
	if abs, ok := f.procSubstPath(newName); ok {
		return &os.PathError{Op: "link", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Link(ctx, oldName, newName)
}

func (f *procSubstFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "chown", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Chown(ctx, name, uid, gid, follow)
}

func (f *procSubstFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "chmod", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Chmod(ctx, name, mode)
}

func (f *procSubstFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "chtimes", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Chtimes(ctx, name, atime, mtime)
}

func (f *procSubstFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "mkdir", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.MkdirAll(ctx, name, perm)
}

func (f *procSubstFS) Remove(ctx context.Context, name string, recursive bool) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "remove", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Remove(ctx, name, recursive)
}

func (f *procSubstFS) Rename(ctx context.Context, oldName, newName string) error {
	if abs, ok := f.procSubstPath(oldName); ok {
		return &os.PathError{Op: "rename", Path: abs, Err: stdfs.ErrPermission}
	}
	if abs, ok := f.procSubstPath(newName); ok {
		return &os.PathError{Op: "rename", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Rename(ctx, oldName, newName)
}

func (f *procSubstFS) Getwd() string {
	return f.inner.Getwd()
}

func (f *procSubstFS) Chdir(name string) error {
	if abs, ok := f.procSubstPath(name); ok {
		return &os.PathError{Op: "chdir", Path: abs, Err: stdfs.ErrPermission}
	}
	return f.inner.Chdir(name)
}

func (f *procSubstFS) procSubstPath(name string) (string, bool) {
	if f == nil || f.inner == nil || f.manager == nil {
		return "", false
	}
	abs := gbfs.Resolve(f.inner.Getwd(), name)
	_, ok := f.manager.stat(abs)
	return abs, ok
}

func (f *procSubstFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if f.entry.direction != procSubstRead {
		return 0, stdfs.ErrPermission
	}
	return f.file.Read(p)
}

func (f *procSubstFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if f.entry.direction != procSubstWrite {
		return 0, stdfs.ErrPermission
	}
	return f.file.Write(p)
}

func (f *procSubstFile) Close() error {
	f.closed.Store(true)
	if f.file == nil {
		return nil
	}
	return f.file.Close()
}

func (f *procSubstFile) Stat() (stdfs.FileInfo, error) {
	if f.closed.Load() {
		return nil, stdfs.ErrClosed
	}
	return f.entry.info(), nil
}

func (fi procSubstFileInfo) Name() string         { return fi.name }
func (fi procSubstFileInfo) Size() int64          { return 0 }
func (fi procSubstFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi procSubstFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi procSubstFileInfo) IsDir() bool          { return false }
func (fi procSubstFileInfo) Sys() any             { return nil }
