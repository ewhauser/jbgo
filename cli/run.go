package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/builtins"
	"github.com/ewhauser/gbash/internal/commandutil"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	gbserver "github.com/ewhauser/gbash/server"
	"golang.org/x/term"
)

// Run executes the shared gbash CLI frontend with the supplied configuration.
func Run(ctx context.Context, cfg Config, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cfg = normalizeConfig(cfg)
	ttyDetector := cfg.TTYDetector
	if ttyDetector == nil {
		ttyDetector = stdinIsTTY
	}
	return run(ctx, cfg, args, stdin, stdout, stderr, ttyDetector(stdin))
}

func run(ctx context.Context, cfg Config, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	cfg = normalizeConfig(cfg)

	runtimeOpts, args, err := parseRuntimeOptions(args)
	if err != nil {
		if runtimeOpts.json {
			if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(2, nil, formatCLIError(cfg.Name, err))); jsonErr != nil {
				return 1, jsonErr
			}
			return 2, nil
		}
		return 2, err
	}

	parsed, err := builtins.ParseBashInvocation(args, builtins.BashInvocationConfig{
		Name:             cfg.Name,
		AllowInteractive: true,
		LongInteractive:  true,
	})
	if err != nil {
		if runtimeOpts.json {
			if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(2, nil, formatCLIError(cfg.Name, err))); jsonErr != nil {
				return 1, jsonErr
			}
			return 2, nil
		}
		return 2, err
	}
	switch parsed.Action {
	case "help":
		if err := renderHelp(stdout, cfg.Name); err != nil {
			return 1, err
		}
		return 0, nil
	case "version":
		_, _ = io.WriteString(stdout, versionText(cfg))
		return 0, nil
	}
	if runtimeOpts.server {
		if runtimeOpts.json {
			return writeCLIJSONError(stdout, cfg.Name, 2, fmt.Errorf("--server and --json are mutually exclusive"))
		}
		if parsed.Source != builtins.BashSourceStdin || parsed.Interactive {
			return 2, fmt.Errorf("--server cannot be combined with script execution or interactive shell flags")
		}
		socketPath := strings.TrimSpace(runtimeOpts.socket)
		listenAddr := strings.TrimSpace(runtimeOpts.listen)
		switch {
		case socketPath == "" && listenAddr == "":
			return 2, fmt.Errorf("either --socket or --listen is required when --server is set")
		case socketPath != "" && listenAddr != "":
			return 2, fmt.Errorf("--socket and --listen are mutually exclusive")
		case listenAddr != "":
			if err := validateLoopbackListenAddress(listenAddr); err != nil {
				return 2, err
			}
		}
	}
	if runtimeOpts.json && parsed.Source == builtins.BashSourceStdin && (parsed.Interactive || stdinTTY) {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(2, nil, formatCLIError(cfg.Name, fmt.Errorf("--json is only supported for non-interactive executions")))); jsonErr != nil {
			return 1, jsonErr
		}
		return 2, nil
	}

	//nolint:contextcheck // gbash.New does not accept context; runtime use remains scoped to this ctx-bound CLI invocation.
	rt, err := newRuntime(cfg, &runtimeOpts)
	if err != nil {
		if runtimeOpts.json {
			if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(1, nil, formatCLIError(cfg.Name, fmt.Errorf("init runtime: %w", err)))); jsonErr != nil {
				return 1, jsonErr
			}
			return 1, nil
		}
		return 1, fmt.Errorf("init runtime: %w", err)
	}
	if runtimeOpts.server {
		meta := currentBuildInfo(cfg.Build)
		err = serveServer(ctx, rt, cfg.Name, meta.Version, &runtimeOpts)
		if err != nil {
			return 1, fmt.Errorf("server error: %w", err)
		}
		return 0, nil
	}

	stdin = wrapInheritedStdin(stdin, &runtimeOpts)
	stdout = wrapInheritedStdout(stdout, &runtimeOpts)

	if parsed.Source == builtins.BashSourceStdin && (parsed.Interactive || stdinTTY) {
		return runInteractiveShell(ctx, rt, parsed, stdin, stdout, stderr)
	}
	if runtimeOpts.json {
		return runBashInvocationJSON(ctx, cfg.Name, rt, parsed, &runtimeOpts, stdin, stdout)
	}
	return runBashInvocation(ctx, rt, parsed, &runtimeOpts, stdin, stdout, stderr)
}

func serveServer(ctx context.Context, rt *gbash.Runtime, name, version string, runtimeOpts *runtimeOptions) error {
	cfg := gbserver.Config{
		Runtime: rt,
		Name:    name,
		Version: version,
	}
	var socketPath string
	var listenAddr string
	if runtimeOpts != nil {
		cfg.SessionTTL = runtimeOpts.sessionTTL
		socketPath = strings.TrimSpace(runtimeOpts.socket)
		listenAddr = strings.TrimSpace(runtimeOpts.listen)
	}
	if socketPath != "" {
		return gbserver.ListenAndServeUnix(ctx, socketPath, cfg)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen on tcp address: %w", err)
	}
	defer func() { _ = ln.Close() }()
	return gbserver.Serve(ctx, ln, cfg)
}

func validateLoopbackListenAddress(addr string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return fmt.Errorf("parse --listen: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("--listen must use a loopback host such as 127.0.0.1, [::1], or localhost")
}

func runBashInvocation(ctx context.Context, rt *gbash.Runtime, parsed *builtins.BashInvocation, runtimeOpts *runtimeOptions, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		return 1, fmt.Errorf("new session: %w", err)
	}
	if exitCode, err := prepareBashInvocationScriptPath(ctx, session, parsed, runtimeOpts); err != nil {
		return exitCode, err
	}
	script, execStdin, exitCode, err := loadBashInvocationScript(parsed, stdin)
	if err != nil {
		return exitCode, err
	}

	req := &gbash.ExecutionRequest{
		Name:            parsed.ExecutionName,
		Interpreter:     parsed.Name,
		PassthroughArgs: append([]string(nil), parsed.RawArgs...),
		ScriptPath:      parsed.ScriptPath,
		Script:          script,
		Args:            append([]string(nil), parsed.Args...),
		StartupOptions:  append([]string(nil), parsed.StartupOptions...),
		Interactive:     parsed.Interactive,
		Stdin:           wrapInheritedStdin(execStdin, runtimeOpts),
		Stdout:          stdout,
		Stderr:          stderr,
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}

	result, err := session.Exec(ctx, req)
	if result != nil && result.ControlStderr != "" {
		_, _ = io.WriteString(stderr, result.ControlStderr+"\n")
	}
	if err != nil {
		var parseErr syntax.ParseError
		if errors.As(err, &parseErr) {
			return 2, errors.New(parseErr.BashError())
		}
		if exitCode, ok := commands.ExitCode(err); ok {
			return exitCode, err
		}
		return 1, fmt.Errorf("runtime error: %w", err)
	}
	if result == nil {
		return 1, fmt.Errorf("runtime returned no result")
	}
	return result.ExitCode, nil
}

func runBashInvocationJSON(ctx context.Context, name string, rt *gbash.Runtime, parsed *builtins.BashInvocation, runtimeOpts *runtimeOptions, stdin io.Reader, stdout io.Writer) (int, error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(1, nil, formatCLIError(name, fmt.Errorf("new session: %w", err)))); jsonErr != nil {
			return 1, jsonErr
		}
		return 1, nil
	}
	if exitCode, err := prepareBashInvocationScriptPath(ctx, session, parsed, runtimeOpts); err != nil {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(exitCode, nil, formatCLIError(name, err))); jsonErr != nil {
			return 1, jsonErr
		}
		return exitCode, nil
	}
	script, execStdin, exitCode, err := loadBashInvocationScript(parsed, stdin)
	if err != nil {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(exitCode, nil, formatCLIError(name, err))); jsonErr != nil {
			return 1, jsonErr
		}
		return exitCode, nil
	}

	req := &gbash.ExecutionRequest{
		Name:            parsed.ExecutionName,
		Interpreter:     parsed.Name,
		PassthroughArgs: append([]string(nil), parsed.RawArgs...),
		ScriptPath:      parsed.ScriptPath,
		Script:          script,
		Args:            append([]string(nil), parsed.Args...),
		StartupOptions:  append([]string(nil), parsed.StartupOptions...),
		Interactive:     parsed.Interactive,
		Stdin:           wrapInheritedStdin(execStdin, runtimeOpts),
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}

	result, err := session.Exec(ctx, req)
	if err != nil {
		var parseErr syntax.ParseError
		if errors.As(err, &parseErr) {
			errMsg := formatCLIError(name, errors.New(parseErr.BashError()))
			if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(2, result, errMsg)); jsonErr != nil {
				return 1, jsonErr
			}
			return 2, nil
		}
		if exitCode, ok := commands.ExitCode(err); ok {
			if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(exitCode, result, formatCLIError(name, err))); jsonErr != nil {
				return 1, jsonErr
			}
			return exitCode, nil
		}
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(1, result, formatCLIError(name, fmt.Errorf("runtime error: %w", err)))); jsonErr != nil {
			return 1, jsonErr
		}
		return 1, nil
	}
	if result == nil {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(1, nil, formatCLIError(name, fmt.Errorf("runtime returned no result")))); jsonErr != nil {
			return 1, jsonErr
		}
		return 1, nil
	}
	if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(result.ExitCode, result, "")); jsonErr != nil {
		return 1, jsonErr
	}
	return result.ExitCode, nil
}

func prepareBashInvocationScriptPath(ctx context.Context, session *gbash.Session, parsed *builtins.BashInvocation, runtimeOpts *runtimeOptions) (int, error) {
	if parsed == nil || parsed.Source != builtins.BashSourceFile {
		return 0, nil
	}
	plan, err := runtimeOpts.planScriptPath(parsed.ScriptPath)
	if err != nil {
		return 1, err
	}
	if plan.sandboxPath != "" {
		parsed.ScriptPath = plan.sandboxPath
		parsed.ExecutionName = plan.sandboxPath
	}
	if plan.copySource == "" {
		return 0, nil
	}
	if err := stageHostScriptFile(ctx, session, plan.copySource, plan.sandboxPath); err != nil {
		return scriptStageExitCode(err), err
	}
	return 0, nil
}

func stageHostScriptFile(ctx context.Context, session *gbash.Session, sourcePath, sandboxPath string) error {
	if session == nil {
		return errors.New("session unavailable")
	}
	fsys := session.FileSystem()
	if fsys == nil {
		return errors.New("session filesystem unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: No such file or directory", sourcePath)
		}
		return fmt.Errorf("stat host script %s: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: copy-script source must be a regular file", sourcePath)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s: No such file or directory", sourcePath)
		}
		return fmt.Errorf("open host script %s: %w", sourcePath, err)
	}
	defer func() { _ = source.Close() }()

	mode := info.Mode().Perm()
	data, err := readHostScriptData(ctx, source, sourcePath, session.Limits().MaxFileBytes)
	if err != nil {
		return err
	}

	if err := fsys.MkdirAll(ctx, path.Dir(sandboxPath), 0o755); err != nil {
		return fmt.Errorf("create staged script directory: %w", err)
	}
	target, err := fsys.OpenFile(ctx, sandboxPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create staged script %s: %w", sandboxPath, err)
	}
	defer func() { _ = target.Close() }()

	if _, err := target.Write(data); err != nil {
		return fmt.Errorf("copy host script to sandbox: %w", err)
	}
	return nil
}

func readHostScriptData(ctx context.Context, source *os.File, sourcePath string, maxFileBytes int64) ([]byte, error) {
	reader := commandutil.ReaderWithContext(ctx, source)
	if maxFileBytes <= 0 || maxFileBytes == math.MaxInt64 {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read host script %s: %w", sourcePath, err)
		}
		return data, nil
	}

	data, err := io.ReadAll(io.LimitReader(reader, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read host script %s: %w", sourcePath, err)
	}
	if int64(len(data)) > maxFileBytes {
		return nil, fmt.Errorf("%s: %w", sourcePath, commands.Diagnosticf("input exceeds maximum file size of %d bytes", maxFileBytes))
	}
	return data, nil
}

func scriptStageExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 124
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if strings.Contains(strings.ToLower(err.Error()), "no such file or directory") {
		return 127
	}
	return 1
}

func loadBashInvocationScript(parsed *builtins.BashInvocation, stdin io.Reader) (script string, execStdin io.Reader, exitCode int, err error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}

	var readErr error
	execStdin = stdin
	switch parsed.Source {
	case builtins.BashSourceCommandString:
		script = parsed.CommandString
	case builtins.BashSourceFile:
		// File-backed executions are loaded inside the runtime from the sandbox
		// filesystem so the CLI never reads host files directly by path.
	default:
		var data []byte
		data, readErr = io.ReadAll(stdin)
		if readErr == nil {
			script = string(data)
		}
		execStdin = nil
	}
	if readErr != nil {
		return "", nil, 1, fmt.Errorf("read script: %w", readErr)
	}
	return script, execStdin, 0, nil
}

func stdinIsTTY(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
