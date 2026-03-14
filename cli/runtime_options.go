package cli

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/builtins"
)

type runtimeOptions struct {
	root          string
	readWriteRoot string
	cwd           string
}

func parseRuntimeOptions(args []string) (runtimeOptions, []string, error) {
	var opts runtimeOptions
	rest := append([]string(nil), args...)
	for len(rest) > 0 {
		arg := rest[0]
		switch {
		case arg == "--root":
			if len(rest) < 2 {
				return runtimeOptions{}, nil, fmt.Errorf("--root requires a path")
			}
			opts.root = rest[1]
			rest = rest[2:]
		case strings.HasPrefix(arg, "--root="):
			opts.root = strings.TrimPrefix(arg, "--root=")
			rest = rest[1:]
		case arg == "--readwrite-root":
			if len(rest) < 2 {
				return runtimeOptions{}, nil, fmt.Errorf("--readwrite-root requires a path")
			}
			opts.readWriteRoot = rest[1]
			rest = rest[2:]
		case strings.HasPrefix(arg, "--readwrite-root="):
			opts.readWriteRoot = strings.TrimPrefix(arg, "--readwrite-root=")
			rest = rest[1:]
		case arg == "--cwd":
			if len(rest) < 2 {
				return runtimeOptions{}, nil, fmt.Errorf("--cwd requires a path")
			}
			opts.cwd = rest[1]
			rest = rest[2:]
		case strings.HasPrefix(arg, "--cwd="):
			opts.cwd = strings.TrimPrefix(arg, "--cwd=")
			rest = rest[1:]
		case arg == "--":
			return opts, rest[1:], nil
		default:
			return opts, rest, nil
		}
	}
	return opts, nil, nil
}

func (opts runtimeOptions) gbashOptions() ([]gbash.Option, error) {
	rootValue := strings.TrimSpace(opts.root)
	readWriteRoot := strings.TrimSpace(opts.readWriteRoot)
	cwdValue := strings.TrimSpace(opts.cwd)
	if rootValue != "" && readWriteRoot != "" {
		return nil, fmt.Errorf("--root and --readwrite-root are mutually exclusive")
	}

	runtimeOpts := make([]gbash.Option, 0, 3)
	switch {
	case rootValue != "":
		root, err := filepath.Abs(rootValue)
		if err != nil {
			return nil, fmt.Errorf("resolve --root: %w", err)
		}
		runtimeOpts = append(runtimeOpts, gbash.WithFileSystem(gbash.HostDirectoryFileSystem(root, gbash.HostDirectoryOptions{})))
		if cwdValue == "" {
			cwdValue = gbash.DefaultWorkspaceMountPoint
		}
	case readWriteRoot != "":
		root, err := filepath.Abs(readWriteRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve --readwrite-root: %w", err)
		}
		if err := ensureReadWriteRootIsTemporary(root); err != nil {
			return nil, err
		}
		runtimeOpts = append(runtimeOpts,
			gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(root, gbash.ReadWriteDirectoryOptions{})),
			gbash.WithBaseEnv(map[string]string{
				"HOME": "/home",
				"PATH": "/bin",
			}),
		)
		if cwdValue == "" {
			cwdValue = "/"
		}
	}

	if cwdValue != "" {
		runtimeOpts = append(runtimeOpts, gbash.WithWorkingDir(normalizeSandboxPath(cwdValue)))
	}
	return runtimeOpts, nil
}

func ensureReadWriteRootIsTemporary(root string) error {
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve system temp directory: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve --readwrite-root: %w", err)
	}
	if !pathWithinRoot(filepath.Clean(canonicalRoot), filepath.Clean(tempRoot)) {
		return fmt.Errorf("--readwrite-root must be inside the system temp directory")
	}
	return nil
}

func pathWithinRoot(pathValue, root string) bool {
	rel, err := filepath.Rel(root, pathValue)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	parent := ".." + string(os.PathSeparator)
	return rel != ".." && !strings.HasPrefix(rel, parent)
}

func normalizeSandboxPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return path.Clean(value)
}

func newRuntime(cfg Config, opts runtimeOptions) (*gbash.Runtime, error) {
	runtimeOpts, err := opts.gbashOptions()
	if err != nil {
		return nil, err
	}
	allOpts := append([]gbash.Option(nil), cfg.BaseOptions...)
	allOpts = append(allOpts, runtimeOpts...)
	return gbash.New(allOpts...)
}

func renderHelp(w io.Writer, name string) error {
	spec := builtins.BashInvocationSpec(builtins.BashInvocationConfig{
		Name:             name,
		AllowInteractive: true,
		LongInteractive:  true,
	})
	if err := builtins.RenderCommandHelp(w, &spec); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nCLI filesystem options:\n"+
		"  --root DIR            mount DIR read-only at /home/agent/project with a writable in-memory overlay\n"+
		"  --cwd DIR             set the initial sandbox working directory\n"+
		"  --readwrite-root DIR  mount DIR as sandbox / with writes persisted back to the host filesystem\n")
	return err
}
