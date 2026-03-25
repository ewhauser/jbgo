package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/internal/commandutil"
)

type commandResolution struct {
	Name           string
	Path           string
	InvocationPath string
}

const (
	compatWrapperMarker       = "build-aux/gbash-harness/gbash"
	compatDisabledBuiltinText = "jbgo_disabled_builtins"
	compatWrapperReadwriteArg = `--readwrite-root "$root_dir" --cwd "$sandbox_cwd"`
	maxCompatWrapperProbeSize = 4096
)

func executeCommand(ctx context.Context, inv *Invocation, opts *executeCommandOptions) (*ExecutionResult, error) {
	if opts == nil {
		opts = &executeCommandOptions{}
	}
	if inv.Exec == nil {
		return nil, fmt.Errorf("subexec callback missing")
	}
	if len(opts.Argv) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	searchEnv := opts.SearchEnv
	if searchEnv == nil {
		searchEnv = inv.Env
	}
	env := opts.Env
	if env == nil {
		env = inv.Env
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir = inv.Cwd
	}

	resolved, ok, err := resolveCommand(ctx, inv, searchEnv, workDir, opts.Argv[0])
	if err != nil {
		return nil, err
	}
	if !ok {
		return &ExecutionResult{ExitCode: 127, CommandNotFound: true}, nil
	}
	if resolved == nil {
		return nil, fmt.Errorf("command resolution returned nil result")
	}

	argv0 := resolved.InvocationPath
	if argv0 == "" {
		argv0 = resolved.Path
	}
	argv := append([]string{argv0}, opts.Argv[1:]...) //nolint:nilaway // resolved is non-nil when ok is true
	return inv.Exec(ctx, &ExecutionRequest{
		Command:     argv,
		CommandPath: resolved.Path,
		Env:         env,
		WorkDir:     workDir,
		ReplaceEnv:  opts.ReplaceEnv,
		Stdin:       opts.Stdin,
		Stdout:      opts.Stdout,
		Stderr:      opts.Stderr,
		Timeout:     opts.Timeout,
	})
}

type executeCommandOptions struct {
	Argv       []string
	Env        map[string]string
	SearchEnv  map[string]string
	WorkDir    string
	ReplaceEnv bool
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Timeout    time.Duration
}

func resolveCommand(ctx context.Context, inv *Invocation, env map[string]string, dir, name string) (*commandResolution, bool, error) {
	name = remapCompatHostPath(inv, name)
	if strings.Contains(name, "/") {
		return resolveCommandPath(ctx, inv, name)
	}

	for _, pathDir := range commandSearchDirs(inv, env, dir) {
		candidate := gbfs.Resolve(pathDir, name)
		resolved, ok, err := resolveCommandPath(ctx, inv, candidate)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return resolved, true, nil
		}
	}
	return nil, false, nil
}

func resolveCommandPath(ctx context.Context, inv *Invocation, candidate string) (*commandResolution, bool, error) {
	resolvedPath, invocationPath, exists, err := resolveExecutableCommandPath(ctx, inv, candidate)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	if compatPath, ok, err := maybeCompatWrapperCommand(ctx, inv, invocationPath); ok || err != nil {
		if err != nil {
			return nil, false, err
		}
		return &commandResolution{
			Name:           path.Base(compatPath),
			Path:           compatPath,
			InvocationPath: compatPath,
		}, true, nil
	}
	return &commandResolution{
		Name:           path.Base(invocationPath),
		Path:           resolvedPath,
		InvocationPath: invocationPath,
	}, true, nil
}

func resolveExecutableCommandPath(ctx context.Context, inv *Invocation, candidate string) (string, string, bool, error) {
	info, abs, exists, err := lstatMaybe(ctx, inv, candidate)
	if err != nil {
		return "", "", false, err
	}
	if !exists || info.IsDir() {
		return "", "", false, nil
	}
	if info.Mode()&stdfs.ModeSymlink == 0 {
		return abs, abs, true, nil
	}

	resolved, err := canonicalizeReadlink(ctx, inv, abs, readlinkModeCanonicalizeExisting)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return "", "", false, nil
		}
		return "", "", false, err
	}

	info, resolvedAbs, exists, err := lstatMaybe(ctx, inv, resolved)
	if err != nil {
		return "", "", false, err
	}
	if !exists || info.IsDir() {
		return "", "", false, nil
	}
	return resolvedAbs, abs, true, nil
}

func maybeCompatWrapperCommand(ctx context.Context, inv *Invocation, fullPath string) (string, bool, error) {
	name := path.Base(fullPath)
	if !registeredCommand(inv, name) || inv == nil || inv.FS == nil {
		return "", false, nil
	}
	file, err := inv.FS.Open(ctx, fullPath)
	if err != nil {
		return "", false, nil
	}
	defer func() { _ = file.Close() }()

	reader := commandutil.ReaderWithContext(ctx, file)
	data := make([]byte, maxCompatWrapperProbeSize+1)
	n, err := io.ReadFull(reader, data)
	switch {
	case err == nil:
		return "", false, nil
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		data = data[:n]
	default:
		return "", false, err
	}
	text := string(data)
	if !looksLikeCompatWrapper(text, name) {
		return "", false, nil
	}
	return path.Join("/bin", name), true, nil
}

func looksLikeCompatWrapper(text, name string) bool {
	if !strings.Contains(text, compatWrapperMarker) || !strings.Contains(text, compatDisabledBuiltinText) {
		return false
	}
	if !strings.Contains(text, compatWrapperReadwriteArg) {
		return false
	}
	return strings.Contains(text, `exec "/bin/`+name+`"`)
}

func registeredCommand(inv *Invocation, name string) bool {
	if inv == nil || inv.GetRegisteredCommands == nil {
		return false
	}
	return slices.Contains(inv.GetRegisteredCommands(), name)
}

func resolveAllCommands(ctx context.Context, inv *Invocation, env map[string]string, dir, name string) ([]string, error) {
	name = remapCompatHostPath(inv, name)
	if strings.Contains(name, "/") {
		_, invocationPath, exists, err := resolveExecutableCommandPath(ctx, inv, name)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, nil
		}
		return []string{invocationPath}, nil
	}

	matches := make([]string, 0)
	seen := make(map[string]struct{})
	for _, pathDir := range commandSearchDirs(inv, env, dir) {
		candidate := gbfs.Resolve(pathDir, name)
		_, invocationPath, exists, err := resolveExecutableCommandPath(ctx, inv, candidate)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		if _, ok := seen[invocationPath]; ok {
			continue
		}
		seen[invocationPath] = struct{}{}
		matches = append(matches, invocationPath)
	}
	return matches, nil
}

func commandSearchDirs(inv *Invocation, env map[string]string, dir string) []string {
	pathValue := strings.TrimSpace(env["PATH"])
	if pathValue == "" {
		return nil
	}
	dirs := make([]string, 0, strings.Count(pathValue, ":")+1)
	seen := make(map[string]struct{})
	for entry := range strings.SplitSeq(pathValue, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			entry = "."
		}
		resolved := gbfs.Resolve(dir, entry)
		resolved = remapCompatHostPath(inv, resolved)
		if _, ok := seen[resolved]; ok {
			continue
		}
		seen[resolved] = struct{}{}
		dirs = append(dirs, resolved)
	}
	return dirs
}

func remapCompatHostPath(inv *Invocation, name string) string {
	if inv == nil || inv.Env == nil || !strings.HasPrefix(name, "/") {
		return name
	}
	candidates := compatHostPathCandidates(name)
	for _, key := range []string{"GBASH_COMPAT_ROOT", "abs_top_builddir", "abs_top_srcdir"} {
		root := strings.TrimSpace(inv.Env[key])
		if root == "" || !strings.HasPrefix(root, "/") {
			continue
		}
		for _, prefix := range compatHostPathCandidates(root) {
			for _, candidate := range candidates {
				rel, ok := trimCompatHostPrefix(candidate, prefix)
				if !ok {
					continue
				}
				if rel == "" {
					return "/"
				}
				return path.Join("/", rel)
			}
		}
	}
	return name
}

func compatHostPathCandidates(name string) []string {
	out := []string{path.Clean(name)}
	resolved, err := filepath.EvalSymlinks(filepath.FromSlash(name))
	if err != nil {
		return out
	}
	resolved = filepath.ToSlash(resolved)
	resolved = path.Clean(resolved)
	if resolved != out[0] {
		out = append(out, resolved)
	}
	return out
}

func trimCompatHostPrefix(name, root string) (string, bool) {
	name = path.Clean(name)
	root = path.Clean(root)
	switch {
	case name == root:
		return "", true
	case root == "/":
		return strings.TrimPrefix(name, "/"), strings.HasPrefix(name, "/")
	case strings.HasPrefix(name, root+"/"):
		return strings.TrimPrefix(name, root+"/"), true
	default:
		return "", false
	}
}

func shellJoinArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteForDisplay(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteForDisplay(value string) string {
	if value == "" {
		return "''"
	}
	if !shellNeedsCompositeQuote(value) {
		return shellQuoteWholeArg(value)
	}

	parts := make([]string, 0, len(value))
	start := 0
	for i := 0; i < len(value); i++ {
		if !shellNeedsANSICQuote(value[i]) {
			continue
		}
		if start < i {
			parts = append(parts, shellQuoteCompositeChunk(value[start:i]))
		}
		parts = append(parts, shellANSICQuote(value[i]))
		start = i + 1
	}
	if start < len(value) {
		parts = append(parts, shellQuoteCompositeChunk(value[start:]))
	}
	return strings.Join(parts, "")
}

func shellQuoteWholeArg(value string) string {
	if shellIsBareword(value) {
		return value
	}
	return shellQuoteCompositeChunk(value)
}

func shellQuoteCompositeChunk(value string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "'") {
		return "'" + value + "'"
	}
	if !strings.ContainsAny(value, "\"\\$`") {
		return `"` + value + `"`
	}
	return shellSingleQuote(value)
}

func shellNeedsCompositeQuote(value string) bool {
	for i := 0; i < len(value); i++ {
		if shellNeedsANSICQuote(value[i]) {
			return true
		}
	}
	return false
}

func shellNeedsANSICQuote(ch byte) bool {
	switch ch {
	case '\a', '\b', '\f', '\n', '\r', '\t', '\v':
		return true
	default:
		return ch < 0x20 || ch == 0x7f
	}
}

func shellANSICQuote(ch byte) string {
	switch ch {
	case '\a':
		return `$'\a'`
	case '\b':
		return `$'\b'`
	case '\f':
		return `$'\f'`
	case '\n':
		return `$'\n'`
	case '\r':
		return `$'\r'`
	case '\t':
		return `$'\t'`
	case '\v':
		return `$'\v'`
	default:
		return fmt.Sprintf("$'\\x%02x'", ch)
	}
}

func shellIsBareword(value string) bool {
	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case strings.ContainsRune("%+,-./:=@_^", rune(ch)):
		default:
			return false
		}
	}
	return true
}

func shellSingleQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func writeExecutionOutputs(inv *Invocation, result *ExecutionResult) error {
	if result == nil {
		return nil
	}
	// Write stderr before stdout so that trace output (xtrace) precedes
	// command output when both streams share the same writer (e.g. 2>&1).
	if result.Stderr != "" {
		if _, err := fmt.Fprint(inv.Stderr, result.Stderr); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if result.Stdout != "" {
		if _, err := fmt.Fprint(inv.Stdout, result.Stdout); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func exitForExecutionResult(result *ExecutionResult) error {
	if result == nil || result.ExitCode == 0 {
		return nil
	}
	return &ExitError{Code: result.ExitCode}
}

func sortedEnvPairs(env map[string]string) []string {
	pairs := make([]string, 0, len(env))
	for key, value := range env {
		pairs = append(pairs, key+"="+value)
	}
	slices.Sort(pairs)
	return pairs
}
