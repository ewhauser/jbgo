package cli

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/policy"
)

type runtimeOptions struct {
	root          string
	readWriteRoot string
	copyScript    bool
	cwd           string
	maxFileBytes  int64
	json          bool
	server        bool
	socket        string
	listen        string
	sessionTTL    time.Duration
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
		case arg == "--copy-script":
			opts.copyScript = true
		case arg == "--cwd":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--cwd requires a path")
			}
			i++
			opts.cwd = args[i]
		case strings.HasPrefix(arg, "--cwd="):
			opts.cwd = strings.TrimPrefix(arg, "--cwd=")
		case arg == "--max-file-bytes":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--max-file-bytes requires a byte count")
			}
			i++
			value, err := parseMaxFileBytes(args[i])
			if err != nil {
				return opts, nil, err
			}
			opts.maxFileBytes = value
		case strings.HasPrefix(arg, "--max-file-bytes="):
			value, err := parseMaxFileBytes(strings.TrimPrefix(arg, "--max-file-bytes="))
			if err != nil {
				return opts, nil, err
			}
			opts.maxFileBytes = value
		case arg == "--json":
			opts.json = true
		case arg == "--server":
			opts.server = true
		case arg == "--socket":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--socket requires a path")
			}
			i++
			opts.socket = args[i]
		case strings.HasPrefix(arg, "--socket="):
			opts.socket = strings.TrimPrefix(arg, "--socket=")
		case arg == "--listen":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--listen requires a host:port")
			}
			i++
			opts.listen = args[i]
		case strings.HasPrefix(arg, "--listen="):
			opts.listen = strings.TrimPrefix(arg, "--listen=")
		case arg == "--session-ttl":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("--session-ttl requires a duration")
			}
			i++
			ttl, err := time.ParseDuration(args[i])
			if err != nil {
				return opts, nil, fmt.Errorf("parse --session-ttl: %w", err)
			}
			opts.sessionTTL = ttl
		case strings.HasPrefix(arg, "--session-ttl="):
			ttl, err := time.ParseDuration(strings.TrimPrefix(arg, "--session-ttl="))
			if err != nil {
				return opts, nil, fmt.Errorf("parse --session-ttl: %w", err)
			}
			opts.sessionTTL = ttl
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

func parseMaxFileBytes(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse --max-file-bytes: %w", err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("--max-file-bytes must be a positive integer")
	}
	return parsed, nil
}

func (opts *runtimeOptions) gbashOptions() ([]gbash.Option, error) {
	if opts == nil {
		return nil, nil
	}
	rootValue := strings.TrimSpace(opts.root)
	readWriteRoot := strings.TrimSpace(opts.readWriteRoot)
	cwdValue := strings.TrimSpace(opts.cwd)
	if rootValue != "" && readWriteRoot != "" {
		return nil, fmt.Errorf("--root and --readwrite-root are mutually exclusive")
	}

	runtimeOpts := make([]gbash.Option, 0, 4)
	switch {
	case rootValue != "":
		root, err := filepath.Abs(rootValue)
		if err != nil {
			return nil, fmt.Errorf("resolve --root: %w", err)
		}
		runtimeOpts = append(runtimeOpts, gbash.WithFileSystem(gbash.HostDirectoryFileSystem(root, gbash.HostDirectoryOptions{
			MaxFileReadBytes: opts.maxFileBytes,
		})))
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
			gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(root, gbash.ReadWriteDirectoryOptions{
				MaxFileReadBytes: opts.maxFileBytes,
			})),
			gbash.WithBaseEnv(readWriteRootBaseEnv()),
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

func readWriteRootBaseEnv() map[string]string {
	env := map[string]string{
		"HOME": "/home",
		"PATH": "/bin",
	}

	user := strings.TrimSpace(os.Getenv("USER"))
	logname := strings.TrimSpace(os.Getenv("LOGNAME"))
	switch {
	case user == "" && logname != "":
		user = logname
	case logname == "" && user != "":
		logname = user
	}
	if user != "" {
		env["USER"] = user
	}
	if logname != "" {
		env["LOGNAME"] = logname
	}
	if group := strings.TrimSpace(os.Getenv("GROUP")); group != "" {
		env["GROUP"] = group
	} else if user != "" {
		env["GROUP"] = user
	}
	for _, key := range []string{"UID", "EUID", "GID", "EGID", "GROUPS"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env[key] = value
		}
	}
	for _, key := range []string{"LANG", "LANGUAGE", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_NUMERIC", "LC_TIME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			env[key] = value
		}
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

type scriptPathPlan struct {
	sandboxPath string
	copySource  string
}

func (opts *runtimeOptions) planScriptPath(scriptPath string) (scriptPathPlan, error) {
	if scriptPath == "" {
		return scriptPathPlan{}, nil
	}
	if sandboxPath, ok, err := opts.mapMountedHostScriptPath(scriptPath); err != nil {
		return scriptPathPlan{}, err
	} else if ok {
		return scriptPathPlan{sandboxPath: sandboxPath}, nil
	}
	if opts == nil || !opts.copyScript {
		return scriptPathPlan{sandboxPath: scriptPath}, nil
	}
	source, err := filepath.Abs(scriptPath)
	if err != nil {
		return scriptPathPlan{}, fmt.Errorf("resolve --copy-script source: %w", err)
	}
	return scriptPathPlan{
		sandboxPath: stagedSandboxScriptPath(filepath.Base(source)),
		copySource:  source,
	}, nil
}

func (opts *runtimeOptions) mapMountedHostScriptPath(scriptPath string) (string, bool, error) {
	if opts == nil || !filepath.IsAbs(scriptPath) {
		return "", false, nil
	}
	switch {
	case strings.TrimSpace(opts.root) != "":
		return sandboxPathForMountedHostPath(scriptPath, opts.root, gbash.DefaultWorkspaceMountPoint)
	case strings.TrimSpace(opts.readWriteRoot) != "":
		return sandboxPathForMountedHostPath(scriptPath, opts.readWriteRoot, "/")
	default:
		return "", false, nil
	}
}

func sandboxPathForMountedHostPath(scriptPath, hostRoot, mountPoint string) (string, bool, error) {
	resolvedRoot, err := filepath.Abs(hostRoot)
	if err != nil {
		return "", false, fmt.Errorf("resolve mounted root: %w", err)
	}
	resolvedScript, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", false, fmt.Errorf("resolve script path: %w", err)
	}
	rootVolume := filepath.VolumeName(resolvedRoot)
	scriptVolume := filepath.VolumeName(resolvedScript)
	if rootVolume != "" && scriptVolume != "" && !strings.EqualFold(rootVolume, scriptVolume) {
		return "", false, nil
	}

	rel, err := filepath.Rel(filepath.Clean(resolvedRoot), filepath.Clean(resolvedScript))
	if err != nil {
		return "", false, err
	}
	if rel == "." {
		return normalizeSandboxPath(mountPoint), true, nil
	}
	parent := ".." + string(os.PathSeparator)
	if rel == ".." || strings.HasPrefix(rel, parent) {
		return "", false, nil
	}
	return normalizeSandboxPath(path.Join(mountPoint, filepath.ToSlash(rel))), true, nil
}

func stagedSandboxScriptPath(base string) string {
	switch base {
	case "", ".", string(os.PathSeparator):
		base = "script.sh"
	}
	return path.Join("/tmp/.gbash-host-script", base)
}

func newRuntime(cfg Config, opts *runtimeOptions) (*gbash.Runtime, error) {
	runtimeOpts, err := opts.gbashOptions()
	if err != nil {
		return nil, err
	}
	allOpts := append([]gbash.Option(nil), cfg.BaseOptions...)
	allOpts = append(allOpts, runtimeOpts...)
	if opts != nil && opts.maxFileBytes > 0 {
		allOpts = append(allOpts, gbash.WithLimitOverrides(policy.Limits{
			MaxFileBytes: opts.maxFileBytes,
		}))
	}
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
		"  --copy-script         copy the positional host script into the sandbox before execution\n"+
		"  --max-file-bytes N    override the sandbox file-size/read-size limit in bytes\n"+
		"\nCLI output options:\n"+
		"  --json                emit one JSON result object for a non-interactive execution\n"+
		"\nCLI server options:\n"+
		"  --server                run the shared gbash JSON-RPC server instead of executing a script\n"+
		"  --socket PATH           listen on PATH for Unix domain socket clients\n"+
		"  --listen HOST:PORT      listen on loopback HOST:PORT for TCP clients\n"+
		"  --session-ttl DURATION  keep idle sessions alive for DURATION before expiry\n")
	return err
}
