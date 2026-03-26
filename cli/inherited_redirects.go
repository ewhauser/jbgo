package cli

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/commandutil"
)

func wrapInheritedStdin(reader io.Reader, opts *runtimeOptions) io.Reader {
	file, ok := reader.(*os.File)
	if !ok {
		return reader
	}
	wrapped, ok := wrapInheritedRedirectFile(file, opts)
	if !ok {
		return reader
	}
	return wrapped
}

func wrapInheritedStdout(writer io.Writer, opts *runtimeOptions) io.Writer {
	file, ok := writer.(*os.File)
	if !ok {
		return writer
	}
	wrapped, ok := wrapInheritedRedirectFile(file, opts)
	if !ok {
		return writer
	}
	return wrapped
}

func wrapInheritedRedirectFile(file *os.File, opts *runtimeOptions) (io.ReadWriteCloser, bool) {
	sandboxPath, ok := sandboxRedirectPathForFile(file, opts)
	if !ok {
		return nil, false
	}
	return commandutil.WrapRedirectedFile(file, sandboxPath, inheritedOSFileFlags(file)), true
}

func sandboxRedirectPathForFile(file *os.File, opts *runtimeOptions) (string, bool) {
	hostPath, ok := inheritedOSFilePath(file)
	if !ok {
		return "", false
	}
	return sandboxRedirectPath(opts, hostPath)
}

func sandboxRedirectPath(opts *runtimeOptions, hostPath string) (string, bool) {
	if opts == nil || hostPath == "" || !filepath.IsAbs(hostPath) {
		return "", false
	}
	hostPath = canonicalHostPath(hostPath)

	if root, ok := runtimeRootPath(opts.readWriteRoot); ok {
		if sandboxPath, ok := sandboxPathWithinRoot(hostPath, root, "/"); ok {
			return sandboxPath, true
		}
	}
	if root, ok := runtimeRootPath(opts.root); ok {
		if sandboxPath, ok := sandboxPathWithinRoot(hostPath, root, gbash.DefaultWorkspaceMountPoint); ok {
			return sandboxPath, true
		}
	}
	return "", false
}

func runtimeRootPath(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", false
	}
	return canonicalHostPath(root), true
}

func canonicalHostPath(value string) string {
	resolved, err := filepath.EvalSymlinks(value)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(value)
}

func sandboxPathWithinRoot(hostPath, root, sandboxRoot string) (string, bool) {
	if !pathWithinRoot(hostPath, root) {
		return "", false
	}
	rel, err := filepath.Rel(root, hostPath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return path.Clean(sandboxRoot), true
	}
	return path.Join(sandboxRoot, rel), true
}
