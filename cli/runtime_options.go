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
	"github.com/ewhauser/gbash/policy"
)

type runtimeOptions struct {
	root          string
	readWriteRoot string
	cwd           string
	json          bool
}

func parseRuntimeOptions(args []string) (runtimeOptions, []string, error) {
	var opts runtimeOptions
	rest := make([]string, 0, len(args))
	pendingShellValues := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if pendingShellValues > 0 {
			rest = append(rest, arg)
			pendingShellValues--
			continue
		}

		switch {
		case arg == "--root":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--root requires a path")
			}
			i++
			opts.root = args[i]
		case strings.HasPrefix(arg, "--root="):
			opts.root = strings.TrimPrefix(arg, "--root=")
		case arg == "--readwrite-root":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--readwrite-root requires a path")
			}
			i++
			opts.readWriteRoot = args[i]
		case strings.HasPrefix(arg, "--readwrite-root="):
			opts.readWriteRoot = strings.TrimPrefix(arg, "--readwrite-root=")
		case arg == "--cwd":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--cwd requires a path")
			}
			i++
			opts.cwd = args[i]
		case strings.HasPrefix(arg, "--cwd="):
			opts.cwd = strings.TrimPrefix(arg, "--cwd=")
		case arg == "--json":
			opts.json = true
		case arg == "--":
			rest = append(rest, args[i:]...)
			return opts, rest, nil
		default:
			rest = append(rest, arg)
			if !strings.HasPrefix(arg, "-") || arg == "-" {
				rest = append(rest, args[i+1:]...)
				return opts, rest, nil
			}
			pendingShellValues += bashInvocationValueCount(arg)
		}
	}
	return opts, rest, nil
}

func bashInvocationValueCount(arg string) int {
	if len(arg) < 2 || !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return 0
	}

	count := 0
	for _, ch := range arg[1:] {
		switch ch {
		case 'c', 'o':
			count++
		}
	}
	return count
}

func (opts runtimeOptions) gbashOptions() ([]gbash.Option, error) {
	rootValue := strings.TrimSpace(opts.root)
	readWriteRoot := strings.TrimSpace(opts.readWriteRoot)
	cwdValue := strings.TrimSpace(opts.cwd)
	if rootValue != "" && readWriteRoot != "" {
		return nil, fmt.Errorf("--root and --readwrite-root are mutually exclusive")
	}

	baseEnv := inheritedCLIEnv(nil)
	runtimeOpts := make([]gbash.Option, 0, 4)
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
		baseEnv = inheritedCLIEnv(map[string]string{
			"HOME": "/home",
			"PATH": "/bin",
		})
		registry := builtins.DefaultRegistry()
		runtimeOpts = append(runtimeOpts,
			gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(root, gbash.ReadWriteDirectoryOptions{})),
			gbash.WithRegistry(registry),
			gbash.WithPolicy(policy.NewStatic(&policy.Config{
				AllowedCommands: registry.Names(),
				ReadRoots:       []string{"/"},
				WriteRoots:      []string{"/"},
				Limits: policy.Limits{
					MaxCommandCount:      10000,
					MaxGlobOperations:    100000,
					MaxLoopIterations:    10000,
					MaxSubstitutionDepth: 50,
					MaxStdoutBytes:       1 << 20,
					MaxStderrBytes:       1 << 20,
					MaxFileBytes:         8 << 20,
				},
				SymlinkMode: policy.SymlinkFollow,
			})),
		)
		if cwdValue == "" {
			cwdValue = "/"
		}
	}
	runtimeOpts = append(runtimeOpts, gbash.WithBaseEnv(baseEnv))

	if cwdValue != "" {
		runtimeOpts = append(runtimeOpts, gbash.WithWorkingDir(normalizeSandboxPath(cwdValue)))
	}
	return runtimeOpts, nil
}

func inheritedCLIEnv(overrides map[string]string) map[string]string {
	env := make(map[string]string, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		env[key] = value
	}
	for key, value := range overrides {
		env[key] = value
	}
	return env
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
		"  --readwrite-root DIR  mount DIR as sandbox / with writes persisted back to the host filesystem\n"+
		"\nCLI output options:\n"+
		"  --json                emit one JSON result object for a non-interactive execution\n")
	return err
}
