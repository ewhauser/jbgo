package shell

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/policy"
)

const procSubstProbeName = ".gbash-procsub-probe"

type procSubstDirection uint8

const (
	procSubstRead procSubstDirection = iota + 1
	procSubstWrite
)

type procSubstManager struct {
	mu          sync.Mutex
	nonce       string
	counter     uint64
	closed      bool
	policy      policy.Policy
	defaultDir  string
	defaultHome string
	entries     map[string]*procSubstEntry
}

type procSubstEntry struct {
	path      string
	name      string
	mode      stdfs.FileMode
	modTime   time.Time
	direction procSubstDirection
	// pipeReader and pipeWriter form a buffered in-memory virtual pipe.
	// For CmdIn (read): consumer reads from pipeReader, subprocess writes to pipeWriter.
	// For CmdOut (write): consumer writes to pipeWriter, subprocess reads from pipeReader.
	pipeReader io.ReadCloser
	pipeWriter io.WriteCloser
	manager    *procSubstManager

	mu     sync.Mutex
	hidden bool
}

type procSubstFS struct {
	inner   gbfs.FileSystem
	manager *procSubstManager
}

type procSubstFile struct {
	entry  *procSubstEntry
	reader io.ReadCloser
	writer io.WriteCloser
	closed atomic.Bool
}

type procSubstFileInfo struct {
	name    string
	mode    stdfs.FileMode
	modTime time.Time
}

func newProcSubstManager(pol policy.Policy, defaultDir, defaultHome string) *procSubstManager {
	return &procSubstManager{
		nonce:       newProcSubstNonce(),
		policy:      pol,
		defaultDir:  gbfs.Clean(defaultDir),
		defaultHome: gbfs.Clean(defaultHome),
		entries:     make(map[string]*procSubstEntry),
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
	manager := newProcSubstManager(exec.Policy, procSubstDefaultDir(exec), procSubstDefaultHome(exec))
	inner := exec.FS
	exec.procSubst = manager
	exec.FS = newProcSubstFS(inner, manager)
	return func() {
		manager.Close()
		exec.FS = inner
		exec.procSubst = nil
	}
}

func (m *procSubstManager) endpoint(ctx context.Context, ps *syntax.ProcSubst) (*interp.ProcSubstEndpoint, error) {
	if m == nil {
		return nil, fmt.Errorf("process substitution manager is nil")
	}

	procSubstPath, err := m.nextPath(ctx, ps.Op)
	if err != nil {
		return nil, err
	}

	// Create a buffered in-memory virtual pipe instead of an OS pipe.
	// The buffer allows writes to proceed without blocking (up to buffer size),
	// matching OS pipe semantics while avoiding host kernel resources.
	pipeReader, pipeWriter := interp.NewVirtualPipe()

	entry := &procSubstEntry{
		path:       procSubstPath,
		name:       path.Base(procSubstPath),
		mode:       stdfs.ModeNamedPipe | 0o600,
		modTime:    time.Now().UTC(),
		pipeReader: pipeReader,
		pipeWriter: pipeWriter,
		manager:    m,
	}

	switch ps.Op {
	case syntax.CmdIn:
		entry.direction = procSubstRead
	case syntax.CmdOut:
		entry.direction = procSubstWrite
	default:
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return nil, fmt.Errorf("unsupported process substitution operator: %q", ps.Op)
	}

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
		// <(cmd): subprocess writes to pipeWriter, consumer reads from pipeReader
		endpoint.Writer = pipeWriter
	case syntax.CmdOut:
		// >(cmd): consumer writes to pipeWriter, subprocess reads from pipeReader
		endpoint.Reader = pipeReader
	}
	return endpoint, nil
}

func (m *procSubstManager) nextPath(ctx context.Context, op syntax.ProcOperator) (string, error) {
	root, err := m.pathRoot(ctx, op)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nextPathLocked(root), nil
}

func (m *procSubstManager) nextPathLocked(root string) string {
	m.counter++
	return path.Join(root, fmt.Sprintf(".gbash-procsub-%s-%d", m.nonce, m.counter))
}

func (m *procSubstManager) entry(name string, includeHidden bool) (*procSubstEntry, bool) {
	if m == nil {
		return nil, false
	}
	abs := gbfs.Clean(name)
	m.mu.Lock()
	entry, ok := m.entries[abs]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	if !includeHidden {
		entry.mu.Lock()
		hidden := entry.hidden
		entry.mu.Unlock()
		if hidden {
			return nil, false
		}
	}
	return entry, true
}

func (m *procSubstManager) open(abs string, flag int) (gbfs.File, error) {
	entry, ok := m.entry(abs, false)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: abs, Err: stdfs.ErrNotExist}
	}
	return entry.open(flag)
}

func (m *procSubstManager) stat(abs string) (stdfs.FileInfo, bool) {
	entry, ok := m.entry(abs, false)
	if !ok {
		return nil, false
	}
	return entry.info(), true
}

func (m *procSubstManager) contains(abs string) bool {
	_, ok := m.entry(abs, true)
	return ok
}

func (m *procSubstManager) release(entry *procSubstEntry) {
	if m == nil || entry == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		return
	}
	current, ok := m.entries[entry.path]
	if !ok || current != entry {
		return
	}
	delete(m.entries, entry.path)
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
	if e.hidden {
		return nil, &os.PathError{Op: "open", Path: e.path, Err: stdfs.ErrNotExist}
	}
	e.hidden = true

	f := &procSubstFile{entry: e}
	switch e.direction {
	case procSubstRead:
		f.reader = e.pipeReader
	case procSubstWrite:
		f.writer = e.pipeWriter
	}
	return f, nil
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
	if e.pipeReader != nil {
		_ = e.pipeReader.Close()
	}
	if e.pipeWriter != nil {
		_ = e.pipeWriter.Close()
	}
}

func (e *procSubstEntry) closeConsumer() {
	if e == nil {
		return
	}
	// Close the consumer end based on direction.
	switch e.direction {
	case procSubstRead:
		if e.pipeReader != nil {
			_ = e.pipeReader.Close()
		}
	case procSubstWrite:
		if e.pipeWriter != nil {
			_ = e.pipeWriter.Close()
		}
	}
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
	ok := f.manager.contains(abs)
	return abs, ok
}

func (f *procSubstFile) Read(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if f.reader == nil {
		return 0, stdfs.ErrPermission
	}
	return f.reader.Read(p)
}

func (f *procSubstFile) Write(p []byte) (int, error) {
	if f.closed.Load() {
		return 0, stdfs.ErrClosed
	}
	if f.writer == nil {
		return 0, stdfs.ErrPermission
	}
	return f.writer.Write(p)
}

func (f *procSubstFile) Close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return nil
	}
	var err error
	if f.reader != nil {
		err = f.reader.Close()
	}
	if f.writer != nil {
		if werr := f.writer.Close(); werr != nil && err == nil {
			err = werr
		}
	}
	if f.entry != nil && f.entry.manager != nil {
		f.entry.manager.release(f.entry)
	}
	return err
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

func procSubstDefaultDir(exec *Execution) string {
	if exec == nil || strings.TrimSpace(exec.Dir) == "" {
		return "/"
	}
	return exec.Dir
}

func procSubstDefaultHome(exec *Execution) string {
	if exec == nil {
		return procSubstDefaultDir(nil)
	}
	if home := strings.TrimSpace(exec.Env["HOME"]); home != "" {
		return home
	}
	return procSubstDefaultDir(exec)
}

func (m *procSubstManager) pathRoot(ctx context.Context, op syntax.ProcOperator) (string, error) {
	for _, candidate := range m.pathRootCandidates(ctx) {
		probe := path.Join(candidate, procSubstProbeName)
		if !m.pathAllowed(ctx, policy.FileActionRead, probe) {
			continue
		}
		if op == syntax.CmdOut && !m.pathAllowed(ctx, policy.FileActionWrite, probe) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("process substitution denied: no policy-allowed sandbox root")
}

func (m *procSubstManager) pathRootCandidates(ctx context.Context) []string {
	var raw []string
	if hc, ok := procSubstHandlerContext(ctx); ok {
		raw = append(raw, hc.Env.Get("PWD").String(), hc.Env.Get("HOME").String(), hc.Dir)
	}
	raw = append(raw, m.defaultDir, m.defaultHome, "/")
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		candidate = gbfs.Clean(candidate)
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func (m *procSubstManager) pathAllowed(ctx context.Context, action policy.FileAction, target string) bool {
	if m == nil || m.policy == nil {
		return true
	}
	return m.policy.AllowPath(ctx, action, target) == nil
}

func procSubstHandlerContext(ctx context.Context) (hc interp.HandlerContext, ok bool) {
	if ctx == nil {
		return interp.HandlerContext{}, false
	}
	return interp.LookupHandlerContext(ctx)
}
