package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/builtins"
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

	if parsed.Source == builtins.BashSourceStdin && (parsed.Interactive || stdinTTY) {
		return runInteractiveShell(ctx, rt, parsed, stdin, stdout, stderr)
	}
	if runtimeOpts.json {
		return runBashInvocationJSON(ctx, cfg.Name, rt, parsed, stdin, stdout)
	}
	return runBashInvocation(ctx, rt, parsed, stdin, stdout, stderr)
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

func runBashInvocation(ctx context.Context, rt *gbash.Runtime, parsed *builtins.BashInvocation, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}
	script, execStdin, exitCode, err := loadBashInvocationScript(parsed, stdin)
	if err != nil {
		return exitCode, err
	}

	req := &gbash.ExecutionRequest{
		Name:            parsed.ExecutionName,
		Interpreter:     parsed.Name,
		PassthroughArgs: append([]string(nil), parsed.RawArgs...),
		Script:          script,
		Args:            append([]string(nil), parsed.Args...),
		StartupOptions:  append([]string(nil), parsed.StartupOptions...),
		Interactive:     parsed.Interactive,
		Stdin:           execStdin,
		Stdout:          stdout,
		Stderr:          stderr,
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		return 1, fmt.Errorf("new session: %w", err)
	}

	result, err := session.Exec(ctx, req)
	if result != nil && result.ControlStderr != "" {
		_, _ = io.WriteString(stderr, result.ControlStderr+"\n")
	}
	if err != nil {
		return 1, fmt.Errorf("runtime error: %w", err)
	}
	if result == nil {
		return 1, fmt.Errorf("runtime returned no result")
	}
	return result.ExitCode, nil
}

func runBashInvocationJSON(ctx context.Context, name string, rt *gbash.Runtime, parsed *builtins.BashInvocation, stdin io.Reader, stdout io.Writer) (int, error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
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
		Script:          script,
		Args:            append([]string(nil), parsed.Args...),
		StartupOptions:  append([]string(nil), parsed.StartupOptions...),
		Interactive:     parsed.Interactive,
		Stdin:           execStdin,
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		if jsonErr := writeJSONExecutionResult(stdout, buildJSONExecutionResult(1, nil, formatCLIError(name, fmt.Errorf("new session: %w", err)))); jsonErr != nil {
			return 1, jsonErr
		}
		return 1, nil
	}

	result, err := session.Exec(ctx, req)
	if err != nil {
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

func loadBashInvocationScript(parsed *builtins.BashInvocation, stdin io.Reader) (script string, execStdin io.Reader, exitCode int, err error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}

	var (
		readErr     error
		missingPath string
	)
	execStdin = stdin
	switch parsed.Source {
	case builtins.BashSourceCommandString:
		script = parsed.CommandString
	case builtins.BashSourceFile:
		data, readFileErr := os.ReadFile(parsed.ScriptPath)
		if readFileErr != nil {
			readErr = readFileErr
			missingPath = parsed.ScriptPath
			break
		}
		script = string(data)
	default:
		var data []byte
		data, readErr = io.ReadAll(stdin)
		if readErr == nil {
			script = string(data)
		}
		execStdin = nil
	}
	if readErr != nil {
		if missingPath != "" {
			return "", nil, 127, fmt.Errorf("%s: No such file or directory", missingPath)
		}
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
