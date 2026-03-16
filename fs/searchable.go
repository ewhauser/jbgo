package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NewSearchableFileSystem wraps fsys with the experimental filesystem search
// capability.
//
// When provider is nil, the built-in in-memory provider is used.
//
// Experimental: This API is experimental and subject to change.
func NewSearchableFileSystem(ctx context.Context, fsys FileSystem, provider SearchIndexer) (FileSystem, error) {
	if fsys == nil {
		return nil, stdfs.ErrInvalid
	}
	if provider == nil {
		provider = NewInMemorySearchProvider()
	}

	snapshot, err := buildSearchSnapshot(ctx, fsys)
	if err != nil {
		return nil, err
	}
	if loader, ok := provider.(snapshotSearchIndexer); ok {
		if err := loader.loadSnapshot(snapshot); err != nil {
			return nil, err
		}
	} else {
		for name, data := range snapshot {
			if err := provider.ApplySearchMutation(ctx, &SearchMutation{
				Kind: SearchMutationWrite,
				Path: name,
				Data: data,
			}); err != nil {
				return nil, err
			}
		}
	}

	searchable := &searchableFS{
		inner:                fsys,
		search:               provider,
		hardLinkGroupByPath:  make(map[string]uint64),
		hardLinkGroups:       make(map[uint64]map[string]struct{}),
		symlinkTargetByPath:  make(map[string]string),
		symlinkPathsByTarget: make(map[string]map[string]struct{}),
	}
	if err := searchable.bootstrapSearchTracking(ctx); err != nil {
		return nil, err
	}
	return searchable, nil
}

// NewSearchableFactory wraps each filesystem created by base with the
// experimental filesystem search capability.
//
// When newProvider is nil, the built-in in-memory provider is used for each
// filesystem instance.
//
// Experimental: This API is experimental and subject to change.
func NewSearchableFactory(base Factory, newProvider func() SearchIndexer) Factory {
	if base == nil {
		base = Memory()
	}
	return FactoryFunc(func(ctx context.Context) (FileSystem, error) {
		fsys, err := base.New(ctx)
		if err != nil {
			return nil, err
		}
		var provider SearchIndexer
		if newProvider != nil {
			provider = newProvider()
		}
		return NewSearchableFileSystem(ctx, fsys, provider)
	})
}

type searchableFS struct {
	inner  FileSystem
	search SearchIndexer

	mu                   sync.Mutex
	nextHardLinkGroup    uint64
	hardLinkGroupByPath  map[string]uint64
	hardLinkGroups       map[uint64]map[string]struct{}
	symlinkTargetByPath  map[string]string
	symlinkPathsByTarget map[string]map[string]struct{}
}

func (s *searchableFS) SearchProviderForPath(string) (SearchProvider, bool) {
	if s == nil || s.search == nil {
		return nil, false
	}
	return s.search, true
}

func (s *searchableFS) Open(ctx context.Context, name string) (File, error) {
	return s.inner.Open(ctx, name)
}

func (s *searchableFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (File, error) {
	abs := Resolve(s.inner.Getwd(), name)
	existedBefore := false
	if flag&os.O_CREATE != 0 || flag&os.O_TRUNC != 0 {
		_, err := s.inner.Lstat(ctx, abs)
		switch {
		case err == nil:
			existedBefore = true
		case errors.Is(err, stdfs.ErrNotExist):
		default:
			return nil, err
		}
	}

	file, err := s.inner.OpenFile(ctx, abs, flag, perm)
	if err != nil {
		return nil, err
	}
	if !canWrite(flag) {
		return file, nil
	}

	return &searchableFile{
		file:       file,
		owner:      s,
		path:       abs,
		dirtyOnEnd: flag&os.O_TRUNC != 0 || (flag&os.O_CREATE != 0 && !existedBefore),
	}, nil
}

func (s *searchableFS) Stat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return s.inner.Stat(ctx, name)
}

func (s *searchableFS) Lstat(ctx context.Context, name string) (stdfs.FileInfo, error) {
	return s.inner.Lstat(ctx, name)
}

func (s *searchableFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	return s.inner.ReadDir(ctx, name)
}

func (s *searchableFS) Readlink(ctx context.Context, name string) (string, error) {
	return s.inner.Readlink(ctx, name)
}

func (s *searchableFS) Realpath(ctx context.Context, name string) (string, error) {
	return s.inner.Realpath(ctx, name)
}

func (s *searchableFS) Symlink(ctx context.Context, target, linkName string) error {
	abs := Resolve(s.inner.Getwd(), linkName)
	if err := s.inner.Symlink(ctx, target, abs); err != nil {
		return err
	}
	if err := s.refreshTrackedSymlink(ctx, abs); err != nil {
		return err
	}
	return s.syncSearchPathAndAliases(ctx, abs)
}

func (s *searchableFS) Link(ctx context.Context, oldName, newName string) error {
	oldAbs := Resolve(s.inner.Getwd(), oldName)
	newAbs := Resolve(s.inner.Getwd(), newName)
	if err := s.inner.Link(ctx, oldAbs, newAbs); err != nil {
		return err
	}
	s.recordHardLink(oldAbs, newAbs)
	if err := s.refreshTrackedSymlink(ctx, newAbs); err != nil {
		return err
	}
	return s.syncSearchPathAndAliases(ctx, newAbs)
}

func (s *searchableFS) Chown(ctx context.Context, name string, uid, gid uint32, follow bool) error {
	if err := s.inner.Chown(ctx, name, uid, gid, follow); err != nil {
		return err
	}
	return s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationMetadata,
		Path: Resolve(s.inner.Getwd(), name),
	})
}

func (s *searchableFS) Chmod(ctx context.Context, name string, mode stdfs.FileMode) error {
	if err := s.inner.Chmod(ctx, name, mode); err != nil {
		return err
	}
	return s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationMetadata,
		Path: Resolve(s.inner.Getwd(), name),
	})
}

func (s *searchableFS) Chtimes(ctx context.Context, name string, atime, mtime time.Time) error {
	if err := s.inner.Chtimes(ctx, name, atime, mtime); err != nil {
		return err
	}
	return s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationMetadata,
		Path: Resolve(s.inner.Getwd(), name),
	})
}

func (s *searchableFS) MkdirAll(ctx context.Context, name string, perm stdfs.FileMode) error {
	if err := s.inner.MkdirAll(ctx, name, perm); err != nil {
		return err
	}
	return s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationMetadata,
		Path: Resolve(s.inner.Getwd(), name),
	})
}

func (s *searchableFS) Remove(ctx context.Context, name string, recursive bool) error {
	abs := Resolve(s.inner.Getwd(), name)
	if err := s.inner.Remove(ctx, abs, recursive); err != nil {
		return err
	}
	if err := s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationRemove,
		Path: abs,
	}); err != nil {
		return err
	}
	for _, related := range s.removeTrackedPath(abs) {
		if err := s.syncSearchPath(ctx, related); err != nil {
			return err
		}
		if err := s.refreshTrackedSymlink(ctx, related); err != nil {
			return err
		}
	}
	return nil
}

func (s *searchableFS) Rename(ctx context.Context, oldName, newName string) error {
	oldAbs := Resolve(s.inner.Getwd(), oldName)
	newAbs := Resolve(s.inner.Getwd(), newName)
	if err := s.inner.Rename(ctx, oldAbs, newAbs); err != nil {
		return err
	}
	if err := s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind:    SearchMutationRename,
		OldPath: oldAbs,
		NewPath: newAbs,
	}); err != nil {
		return err
	}
	for _, related := range s.renameTrackedPaths(oldAbs, newAbs) {
		if err := s.syncSearchPath(ctx, related); err != nil {
			return err
		}
		if err := s.refreshTrackedSymlink(ctx, related); err != nil {
			return err
		}
	}
	return nil
}

func (s *searchableFS) Getwd() string {
	return s.inner.Getwd()
}

func (s *searchableFS) Chdir(name string) error {
	return s.inner.Chdir(name)
}

type searchableFile struct {
	file       File
	owner      *searchableFS
	path       string
	dirty      atomic.Bool
	dirtyOnEnd bool
}

func (f *searchableFile) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *searchableFile) Write(p []byte) (int, error) {
	n, err := f.file.Write(p)
	if n > 0 {
		f.dirty.Store(true)
	}
	return n, err
}

func (f *searchableFile) Close() error {
	if err := f.file.Close(); err != nil {
		return err
	}
	if !f.dirty.Load() && !f.dirtyOnEnd {
		return nil
	}
	return f.owner.syncSearchPathAndAliases(context.Background(), f.path)
}

func (f *searchableFile) Stat() (stdfs.FileInfo, error) {
	return f.file.Stat()
}

func buildSearchSnapshot(ctx context.Context, fsys FileSystem) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if err := walkSearchSnapshot(ctx, fsys, "/", out); err != nil {
		return nil, err
	}
	return out, nil
}

func walkSearchSnapshot(ctx context.Context, fsys FileSystem, current string, out map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	linfo, err := fsys.Lstat(ctx, current)
	if err != nil {
		return err
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, err := fsys.Stat(ctx, current)
		if err != nil || !searchIndexableFileInfo(info) {
			return nil
		}
		data, err := readAllSearchFile(ctx, fsys, current)
		if err != nil {
			return err
		}
		out[Clean(current)] = data
		return nil
	}
	if !linfo.IsDir() {
		if !searchIndexableFileInfo(linfo) {
			return nil
		}
		data, err := readAllSearchFile(ctx, fsys, current)
		if err != nil {
			return err
		}
		out[Clean(current)] = data
		return nil
	}

	entries, err := fsys.ReadDir(ctx, current)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := walkSearchSnapshot(ctx, fsys, joinChildPath(current, entry.Name()), out); err != nil {
			return err
		}
	}
	return nil
}

func readAllSearchFile(ctx context.Context, fsys FileSystem, name string) ([]byte, error) {
	file, err := fsys.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func searchIndexableFileInfo(info stdfs.FileInfo) bool {
	return info != nil && info.Mode().IsRegular()
}

func (s *searchableFS) bootstrapSearchTracking(ctx context.Context) error {
	state := bootstrapSearchState{
		hardLinkPathsByID: make(map[string][]string),
	}
	if err := s.walkSearchTracking(ctx, "/", &state); err != nil {
		return err
	}
	s.bootstrapHardLinkGroups(state.hardLinkPathsByID)
	return nil
}

type bootstrapSearchState struct {
	hardLinkPathsByID map[string][]string
}

func (s *searchableFS) walkSearchTracking(ctx context.Context, current string, state *bootstrapSearchState) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	linfo, err := s.inner.Lstat(ctx, current)
	if err != nil {
		return err
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		return s.refreshTrackedSymlink(ctx, current)
	}
	if !linfo.IsDir() {
		if state != nil {
			if identity, ok := searchFileIdentity(linfo); ok {
				state.hardLinkPathsByID[identity] = append(state.hardLinkPathsByID[identity], Clean(current))
			}
		}
		return nil
	}

	entries, err := s.inner.ReadDir(ctx, current)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := s.walkSearchTracking(ctx, joinChildPath(current, entry.Name()), state); err != nil {
			return err
		}
	}
	return nil
}

func (s *searchableFS) bootstrapHardLinkGroups(pathsByID map[string][]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, paths := range pathsByID {
		if len(paths) < 2 {
			continue
		}
		s.nextHardLinkGroup++
		groupID := s.nextHardLinkGroup
		group := make(map[string]struct{}, len(paths))
		for _, pathValue := range paths {
			group[pathValue] = struct{}{}
			s.hardLinkGroupByPath[pathValue] = groupID
		}
		s.hardLinkGroups[groupID] = group
	}
}

func searchFileIdentity(info stdfs.FileInfo) (string, bool) {
	if info == nil || info.IsDir() {
		return "", false
	}
	sys := info.Sys()
	if sys == nil {
		return "", false
	}
	sysType := reflect.TypeOf(sys)
	if nodeID, ok := searchIdentityField(sys, "NodeID"); ok {
		return sysType.String() + ":node:" + nodeID, true
	}
	dev, okDev := searchIdentityField(sys, "Dev")
	ino, okIno := searchIdentityField(sys, "Ino")
	if okDev && okIno {
		return sysType.String() + ":devino:" + dev + ":" + ino, true
	}
	return "", false
}

func searchIdentityField(value any, name string) (string, bool) {
	v := reflect.ValueOf(value)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", false
	}
	field := v.FieldByName(name)
	if !field.IsValid() || !field.CanInterface() {
		return "", false
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", field.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprintf("%d", field.Uint()), true
	case reflect.String:
		return field.String(), true
	default:
		return "", false
	}
}

func (s *searchableFS) syncSearchPath(ctx context.Context, name string) error {
	abs := Resolve(s.inner.Getwd(), name)
	linfo, err := s.inner.Lstat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return s.search.ApplySearchMutation(ctx, &SearchMutation{
				Kind: SearchMutationRemove,
				Path: abs,
			})
		}
		return err
	}
	if linfo.IsDir() {
		return s.search.ApplySearchMutation(ctx, &SearchMutation{
			Kind: SearchMutationRemove,
			Path: abs,
		})
	}
	if linfo.Mode()&stdfs.ModeSymlink != 0 {
		info, err := s.inner.Stat(ctx, abs)
		switch {
		case errors.Is(err, stdfs.ErrNotExist), errors.Is(err, stdfs.ErrInvalid):
			return s.search.ApplySearchMutation(ctx, &SearchMutation{
				Kind: SearchMutationRemove,
				Path: abs,
			})
		case err != nil:
			return err
		case !searchIndexableFileInfo(info):
			return s.search.ApplySearchMutation(ctx, &SearchMutation{
				Kind: SearchMutationRemove,
				Path: abs,
			})
		}
	} else if !searchIndexableFileInfo(linfo) {
		return s.search.ApplySearchMutation(ctx, &SearchMutation{
			Kind: SearchMutationRemove,
			Path: abs,
		})
	}

	data, err := readAllSearchFile(ctx, s.inner, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return s.search.ApplySearchMutation(ctx, &SearchMutation{
				Kind: SearchMutationRemove,
				Path: abs,
			})
		}
		return err
	}
	return s.search.ApplySearchMutation(ctx, &SearchMutation{
		Kind: SearchMutationWrite,
		Path: abs,
		Data: data,
	})
}

func (s *searchableFS) syncSearchPathAndAliases(ctx context.Context, name string) error {
	abs := Resolve(s.inner.Getwd(), name)
	primary := map[string]struct{}{
		abs: {},
	}
	if resolved, err := s.inner.Realpath(ctx, abs); err == nil {
		primary[Clean(resolved)] = struct{}{}
	} else if !errors.Is(err, stdfs.ErrNotExist) && !errors.Is(err, stdfs.ErrInvalid) {
		return err
	}

	for pathValue := range primary {
		if err := s.syncSearchPath(ctx, pathValue); err != nil {
			return err
		}
		if err := s.refreshTrackedSymlink(ctx, pathValue); err != nil {
			return err
		}
	}

	for _, related := range s.trackedRelatedPaths(primary) {
		if _, ok := primary[related]; ok {
			continue
		}
		if err := s.syncSearchPath(ctx, related); err != nil {
			return err
		}
		if err := s.refreshTrackedSymlink(ctx, related); err != nil {
			return err
		}
	}
	return nil
}

func (s *searchableFS) trackedRelatedPaths(paths map[string]struct{}) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	related := make(map[string]struct{})
	for pathValue := range paths {
		if groupID, ok := s.hardLinkGroupByPath[pathValue]; ok {
			for member := range s.hardLinkGroups[groupID] {
				related[member] = struct{}{}
			}
		}
		if aliases, ok := s.symlinkPathsByTarget[pathValue]; ok {
			for alias := range aliases {
				related[alias] = struct{}{}
			}
		}
	}

	out := make([]string, 0, len(related))
	for pathValue := range related {
		out = append(out, pathValue)
	}
	return out
}

func (s *searchableFS) refreshTrackedSymlink(ctx context.Context, name string) error {
	abs := Resolve(s.inner.Getwd(), name)
	linfo, err := s.inner.Lstat(ctx, abs)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			s.removeTrackedSymlink(abs)
			return nil
		}
		return err
	}
	if linfo.Mode()&stdfs.ModeSymlink == 0 {
		s.removeTrackedSymlink(abs)
		return nil
	}
	targetHint, err := s.readTrackedSymlinkTarget(ctx, abs)
	if err != nil {
		s.removeTrackedSymlink(abs)
		return err
	}

	info, err := s.inner.Stat(ctx, abs)
	switch {
	case errors.Is(err, stdfs.ErrNotExist), errors.Is(err, stdfs.ErrInvalid):
		s.setTrackedSymlink(abs, targetHint)
		return nil
	case err != nil:
		return err
	case info.IsDir():
		s.setTrackedSymlink(abs, targetHint)
		return nil
	}

	target, err := s.inner.Realpath(ctx, abs)
	switch {
	case errors.Is(err, stdfs.ErrNotExist), errors.Is(err, stdfs.ErrInvalid):
		s.setTrackedSymlink(abs, targetHint)
		return nil
	case err != nil:
		return err
	}
	s.setTrackedSymlink(abs, target)
	return nil
}

func (s *searchableFS) readTrackedSymlinkTarget(ctx context.Context, abs string) (string, error) {
	target, err := s.inner.Readlink(ctx, abs)
	if err != nil {
		return "", err
	}
	return Resolve(parentDir(abs), target), nil
}

func (s *searchableFS) recordHardLink(oldPath, newPath string) {
	oldPath = Clean(oldPath)
	newPath = Clean(newPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	groupID, ok := s.hardLinkGroupByPath[oldPath]
	if !ok {
		s.nextHardLinkGroup++
		groupID = s.nextHardLinkGroup
		s.hardLinkGroupByPath[oldPath] = groupID
		s.hardLinkGroups[groupID] = map[string]struct{}{
			oldPath: {},
		}
	}
	group := s.hardLinkGroups[groupID]
	group[oldPath] = struct{}{}
	group[newPath] = struct{}{}
	s.hardLinkGroupByPath[newPath] = groupID
}

func (s *searchableFS) removeTrackedPath(pathValue string) []string {
	pathValue = Clean(pathValue)

	s.mu.Lock()
	defer s.mu.Unlock()

	affected := make(map[string]struct{})
	for target, aliases := range s.symlinkPathsByTarget {
		if !pathWithinSearchRoot(target, pathValue) {
			continue
		}
		for alias := range aliases {
			if !pathWithinSearchRoot(alias, pathValue) {
				affected[alias] = struct{}{}
			}
		}
	}

	for alias := range s.symlinkTargetByPath {
		if pathWithinSearchRoot(alias, pathValue) {
			s.removeTrackedSymlinkLocked(alias)
		}
	}
	for alias := range s.hardLinkGroupByPath {
		if pathWithinSearchRoot(alias, pathValue) {
			s.removeHardLinkPathLocked(alias)
		}
	}

	out := make([]string, 0, len(affected))
	for alias := range affected {
		out = append(out, alias)
	}
	return out
}

func (s *searchableFS) renameTrackedPaths(oldPath, newPath string) []string {
	oldPath = Clean(oldPath)
	newPath = Clean(newPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	affected := make(map[string]struct{})
	for alias, target := range s.symlinkTargetByPath {
		switch {
		case pathWithinSearchRoot(alias, oldPath):
			affected[renameTrackedPath(alias, oldPath, newPath)] = struct{}{}
			s.removeTrackedSymlinkLocked(alias)
		case pathWithinSearchRoot(target, oldPath):
			affected[alias] = struct{}{}
		}
	}

	for alias, groupID := range s.hardLinkGroupByPath {
		if !pathWithinSearchRoot(alias, oldPath) {
			continue
		}
		renamed := renameTrackedPath(alias, oldPath, newPath)
		group := s.hardLinkGroups[groupID]
		delete(group, alias)
		group[renamed] = struct{}{}
		delete(s.hardLinkGroupByPath, alias)
		s.hardLinkGroupByPath[renamed] = groupID
	}

	out := make([]string, 0, len(affected))
	for alias := range affected {
		out = append(out, alias)
	}
	return out
}

func renameTrackedPath(pathValue, oldPrefix, newPrefix string) string {
	if pathValue == oldPrefix {
		return newPrefix
	}
	return Clean(newPrefix + strings.TrimPrefix(pathValue, oldPrefix))
}

func (s *searchableFS) removeTrackedSymlink(pathValue string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeTrackedSymlinkLocked(Clean(pathValue))
}

func (s *searchableFS) setTrackedSymlink(pathValue, target string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pathValue = Clean(pathValue)
	target = Clean(target)
	s.removeTrackedSymlinkLocked(pathValue)
	s.symlinkTargetByPath[pathValue] = target
	paths := s.symlinkPathsByTarget[target]
	if paths == nil {
		paths = make(map[string]struct{})
		s.symlinkPathsByTarget[target] = paths
	}
	paths[pathValue] = struct{}{}
}

func (s *searchableFS) removeTrackedSymlinkLocked(pathValue string) {
	target, ok := s.symlinkTargetByPath[pathValue]
	if !ok {
		return
	}
	delete(s.symlinkTargetByPath, pathValue)
	paths := s.symlinkPathsByTarget[target]
	delete(paths, pathValue)
	if len(paths) == 0 {
		delete(s.symlinkPathsByTarget, target)
	}
}

func (s *searchableFS) removeHardLinkPathLocked(pathValue string) {
	groupID, ok := s.hardLinkGroupByPath[pathValue]
	if !ok {
		return
	}
	delete(s.hardLinkGroupByPath, pathValue)
	group := s.hardLinkGroups[groupID]
	delete(group, pathValue)
	if len(group) > 1 {
		return
	}
	for alias := range group {
		delete(s.hardLinkGroupByPath, alias)
	}
	delete(s.hardLinkGroups, groupID)
}

// SearchProviderForPath returns a translated provider for the mounted
// filesystem or base filesystem responsible for name.
//
// Experimental: This API is experimental and subject to change.
func (m *MountableFS) SearchProviderForPath(name string) (SearchProvider, bool) {
	abs := m.resolve(name)
	entry, rel, mounted, synthetic := m.route(abs)
	if synthetic {
		return nil, false
	}
	if mounted {
		capable, ok := entry.fs.(SearchCapable)
		if !ok {
			return nil, false
		}
		provider, ok := capable.SearchProviderForPath(rel)
		if !ok {
			return nil, false
		}
		return translatedSearchProvider{
			inner:      provider,
			mountPoint: entry.mountPoint,
		}, true
	}
	capable, ok := m.base.(SearchCapable)
	if !ok {
		return nil, false
	}
	return capable.SearchProviderForPath(abs)
}

type translatedSearchProvider struct {
	inner      SearchProvider
	mountPoint string
}

func (p translatedSearchProvider) Search(ctx context.Context, query *SearchQuery) (SearchResult, error) {
	if query == nil {
		return SearchResult{}, fmt.Errorf("fs: search query is required")
	}
	innerQuery := *query
	root, err := p.translateRoot(query.Root)
	if err != nil {
		return SearchResult{}, err
	}
	innerQuery.Root = root

	result, err := p.inner.Search(ctx, &innerQuery)
	if err != nil {
		return SearchResult{}, err
	}
	for i := range result.Hits {
		result.Hits[i].Path = p.translatePath(result.Hits[i].Path)
	}
	return result, nil
}

func (p translatedSearchProvider) SearchCapabilities() SearchCapabilities {
	return p.inner.SearchCapabilities()
}

func (p translatedSearchProvider) IndexStatus() IndexStatus {
	return p.inner.IndexStatus()
}

func (p translatedSearchProvider) translateRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "/", nil
	}
	root = Clean(root)
	if root == p.mountPoint {
		return "/", nil
	}
	if strings.HasPrefix(root, p.mountPoint+"/") {
		return Clean(strings.TrimPrefix(root, p.mountPoint)), nil
	}
	return "", ErrSearchUnsupported
}

func (p translatedSearchProvider) translatePath(innerPath string) string {
	innerPath = Clean(innerPath)
	if innerPath == "/" {
		return p.mountPoint
	}
	return Clean(path.Join(p.mountPoint, strings.TrimPrefix(innerPath, "/")))
}
