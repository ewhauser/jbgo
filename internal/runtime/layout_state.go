package runtime

import (
	"context"
	"slices"
	"strings"
	"sync"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/trace"
)

type sandboxLayoutState struct {
	mu          sync.Mutex
	dirty       bool
	initialized bool
	home        string
	path        string
	workDir     string
	commands    []string
	dirs        map[string]struct{}
	stubs       map[string]struct{}
}

func newSandboxLayoutState(env map[string]string, workDir string) *sandboxLayoutState {
	return &sandboxLayoutState{
		initialized: true,
		home:        strings.TrimSpace(env["HOME"]),
		path:        strings.TrimSpace(env["PATH"]),
		workDir:     gbfs.Clean(workDir),
	}
}

func (s *sandboxLayoutState) ensure(ctx context.Context, fsys gbfs.FileSystem, env map[string]string, workDir string, commands []string) error {
	if s == nil {
		return initializeSandboxLayout(ctx, fsys, env, workDir, commands)
	}

	home := strings.TrimSpace(env["HOME"])
	pathValue := strings.TrimSpace(env["PATH"])
	workDir = gbfs.Clean(workDir)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized && !s.dirty &&
		s.home == home &&
		s.path == pathValue &&
		s.workDir == workDir {
		if len(s.commands) == 0 {
			s.commands = append(s.commands[:0], commands...)
			return nil
		}
		if slices.Equal(s.commands, commands) {
			return nil
		}
	}
	if s.dirty {
		s.dirty = false
		s.dirs = nil
		s.stubs = nil
	}

	layoutDirs := layoutDirectoriesForValues(home, pathValue, workDir)
	commandDirs := commandDirectoriesForPath(pathValue)
	commandNames := publicCommandNames(commands)
	if s.dirs == nil {
		s.dirs = make(map[string]struct{}, len(layoutDirs))
	}
	if s.stubs == nil {
		s.stubs = make(map[string]struct{}, len(commandDirs)*len(commandNames))
	}

	for _, dir := range layoutDirs {
		if _, ok := s.dirs[dir]; ok {
			continue
		}
		if err := fsys.MkdirAll(ctx, dir, 0o755); err != nil {
			return err
		}
		s.dirs[dir] = struct{}{}
	}

	for _, dir := range commandDirs {
		for _, name := range commandNames {
			fullPath := gbfs.Resolve(dir, name)
			if _, ok := s.stubs[fullPath]; ok {
				continue
			}
			if err := ensureCommandStub(ctx, fsys, dir, name); err != nil {
				return err
			}
			s.stubs[fullPath] = struct{}{}
		}
	}

	s.initialized = true
	s.home = home
	s.path = pathValue
	s.workDir = workDir
	s.commands = append(s.commands[:0], commands...)

	return nil
}

func (s *sandboxLayoutState) seedTrackedPathsLocked() {
	layoutDirs := layoutDirectoriesForValues(s.home, s.path, s.workDir)
	commandDirs := commandDirectoriesForPath(s.path)
	commandNames := publicCommandNames(s.commands)
	s.dirs = make(map[string]struct{}, len(layoutDirs))
	for _, dir := range layoutDirs {
		s.dirs[dir] = struct{}{}
	}
	s.stubs = make(map[string]struct{}, len(commandDirs)*len(commandNames))
	for _, dir := range commandDirs {
		for _, name := range commandNames {
			s.stubs[gbfs.Resolve(dir, name)] = struct{}{}
		}
	}
}

func (s *sandboxLayoutState) invalidateForEvents(events []trace.Event) {
	if s == nil || len(events) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dirty || !s.initialized {
		return
	}
	if len(s.commands) == 0 {
		for i := range events {
			event := events[i]
			if event.Kind != trace.EventFileMutation || event.File == nil {
				continue
			}
			if s.mutationAffectsUnseededLayout(event.File.Path) ||
				s.mutationAffectsUnseededLayout(event.File.FromPath) ||
				s.mutationAffectsUnseededLayout(event.File.ToPath) {
				s.dirty = true
				return
			}
		}
		return
	}
	if s.dirs == nil || s.stubs == nil {
		s.seedTrackedPathsLocked()
	}
	for i := range events {
		event := events[i]
		if event.Kind != trace.EventFileMutation || event.File == nil {
			continue
		}
		if s.mutationAffectsLayout(event.File.Path) ||
			s.mutationAffectsLayout(event.File.FromPath) ||
			s.mutationAffectsLayout(event.File.ToPath) {
			s.dirty = true
			return
		}
	}
}

func (s *sandboxLayoutState) mutationAffectsUnseededLayout(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	cleanPath := gbfs.Clean(path)
	for _, dir := range layoutDirectoriesForValues(s.home, s.path, s.workDir) {
		if layoutMutationTouchesPath(cleanPath, dir) {
			return true
		}
	}
	for _, dir := range commandDirectoriesForPath(s.path) {
		if layoutMutationTouchesPath(cleanPath, dir) || strings.HasPrefix(cleanPath, dir+"/") {
			return true
		}
	}
	return false
}

func (s *sandboxLayoutState) mutationAffectsLayout(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}

	cleanPath := gbfs.Clean(path)
	for dir := range s.dirs {
		if layoutMutationTouchesPath(cleanPath, dir) {
			return true
		}
	}
	for stub := range s.stubs {
		if layoutMutationTouchesPath(cleanPath, stub) {
			return true
		}
	}
	return false
}

func layoutMutationTouchesPath(mutationPath, trackedPath string) bool {
	if mutationPath == trackedPath {
		return true
	}
	return strings.HasPrefix(trackedPath, mutationPath+"/")
}
