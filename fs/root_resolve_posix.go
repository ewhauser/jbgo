//go:build !windows && !js

package fs

import (
	"errors"
	stdfs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type rootResolvedPath struct {
	rel        string
	info       stdfs.FileInfo
	linkTarget string
}

func openResolvedRoot(rootPath, canonicalRoot, rel string, followFinal, allowMissingFinal bool) (*os.Root, rootResolvedPath, error) {
	if rootPath != "" {
		currentRoot, err := filepath.EvalSymlinks(rootPath)
		if err != nil {
			return nil, rootResolvedPath{}, err
		}
		currentRoot = filepath.Clean(currentRoot)
		if !withinHostRoot(currentRoot, canonicalRoot) {
			return nil, rootResolvedPath{}, stdfs.ErrPermission
		}
	}

	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return nil, rootResolvedPath{}, err
	}
	resolved, err := resolveWithinRoot(root, rootPath, canonicalRoot, rel, followFinal, allowMissingFinal)
	if err != nil {
		_ = root.Close()
		return nil, rootResolvedPath{}, err
	}
	return root, resolved, nil
}

func resolveWithinRoot(root *os.Root, rootPath, canonicalRoot, rel string, followFinal, allowMissingFinal bool) (rootResolvedPath, error) {
	return resolveWithinRootDepth(root, rootPath, canonicalRoot, rel, followFinal, allowMissingFinal, 0)
}

func resolveWithinRootDepth(root *os.Root, rootPath, canonicalRoot, rel string, followFinal, allowMissingFinal bool, depth int) (rootResolvedPath, error) {
	if root == nil {
		return rootResolvedPath{}, stdfs.ErrInvalid
	}
	if depth > maxSymlinkDepth {
		return rootResolvedPath{}, errTooManySymlinks
	}

	rel = cleanRootRelative(rel)
	if rel == "" {
		info, err := root.Stat(".")
		if err != nil {
			return rootResolvedPath{}, err
		}
		return rootResolvedPath{info: info}, nil
	}

	parts := strings.Split(rel, "/")
	currentRoot := root
	currentHostDir := canonicalRoot
	currentRel := ""
	opened := make([]*os.Root, 0, len(parts))
	defer func() {
		for i := len(opened) - 1; i >= 0; i-- {
			_ = opened[i].Close()
		}
	}()

	for i, part := range parts {
		last := i == len(parts)-1

		info, err := currentRoot.Lstat(part)
		if err != nil {
			if errors.Is(err, stdfs.ErrNotExist) && allowMissingFinal {
				return rootResolvedPath{rel: joinRootRelative(currentRel, filepath.ToSlash(filepath.Join(parts[i:]...)))}, nil
			}
			return rootResolvedPath{}, err
		}

		nextRel := joinRootRelative(currentRel, part)
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := currentRoot.Readlink(part)
			if err != nil {
				return rootResolvedPath{}, err
			}
			if last && !followFinal {
				return rootResolvedPath{
					rel:        nextRel,
					info:       info,
					linkTarget: target,
				}, nil
			}

			nextHostPath, err := resolveContainedSymlinkTarget(currentHostDir, target, rootPath, canonicalRoot)
			if err != nil {
				return rootResolvedPath{}, err
			}
			nextRel, err = relativeFromContainedHostPath(canonicalRoot, nextHostPath)
			if err != nil {
				return rootResolvedPath{}, err
			}
			if !last {
				nextRel = joinRootRelative(nextRel, filepath.ToSlash(filepath.Join(parts[i+1:]...)))
			}
			return resolveWithinRootDepth(root, rootPath, canonicalRoot, nextRel, followFinal, allowMissingFinal, depth+1)
		}

		if last {
			return rootResolvedPath{rel: nextRel, info: info}, nil
		}
		if !info.IsDir() {
			return rootResolvedPath{}, stdfs.ErrInvalid
		}

		nextRoot, err := currentRoot.OpenRoot(part)
		if err != nil {
			return rootResolvedPath{}, err
		}
		opened = append(opened, nextRoot)
		currentRoot = nextRoot
		currentHostDir = filepath.Join(currentHostDir, part)
		currentRel = nextRel
	}

	return rootResolvedPath{}, stdfs.ErrNotExist
}

func cleanRootRelative(rel string) string {
	rel = filepath.ToSlash(filepath.Clean("/" + rel))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "." {
		return ""
	}
	return rel
}

func joinRootRelative(base, next string) string {
	base = cleanRootRelative(base)
	next = cleanRootRelative(next)
	switch {
	case base == "":
		return next
	case next == "":
		return base
	default:
		return base + "/" + next
	}
}

func resolveContainedSymlinkTarget(currentHostDir, target, rootPath, canonicalRoot string) (string, error) {
	var next string
	if filepath.IsAbs(target) {
		next = filepath.Clean(target)
		switch {
		case withinHostRoot(next, canonicalRoot):
			return next, nil
		case rootPath != "" && withinHostRoot(next, rootPath):
			canonical, err := filepath.EvalSymlinks(next)
			if err != nil {
				return "", err
			}
			canonical = filepath.Clean(canonical)
			if withinHostRoot(canonical, canonicalRoot) {
				return canonical, nil
			}
		default:
			canonical, err := filepath.EvalSymlinks(next)
			if err == nil {
				canonical = filepath.Clean(canonical)
				if withinHostRoot(canonical, canonicalRoot) {
					return canonical, nil
				}
			}
		}
		return "", stdfs.ErrPermission
	}

	next = filepath.Clean(filepath.Join(currentHostDir, target))
	if !withinHostRoot(next, canonicalRoot) {
		return "", stdfs.ErrPermission
	}
	return next, nil
}

func relativeFromContainedHostPath(canonicalRoot, target string) (string, error) {
	rel, err := filepath.Rel(canonicalRoot, target)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel), nil
}

func rootRelativeOpenPath(rel string) string {
	if cleanRootRelative(rel) == "" {
		return "."
	}
	return cleanRootRelative(rel)
}

func hostPathFromRootRelative(canonicalRoot, rel string) string {
	rel = cleanRootRelative(rel)
	if rel == "" {
		return canonicalRoot
	}
	return filepath.Join(canonicalRoot, filepath.FromSlash(rel))
}

func openResolvedParentFile(root *os.Root, rel string) (*os.File, string, error) {
	if root == nil {
		return nil, "", stdfs.ErrInvalid
	}

	rel = cleanRootRelative(rel)
	parentRel := rootRelativeOpenPath(path.Dir(rel))
	base := path.Base(rel)
	if rel == "" {
		base = "."
	}

	dir, err := root.Open(parentRel)
	if err != nil {
		return nil, "", err
	}
	return dir, base, nil
}
