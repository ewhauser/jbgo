package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/builtins"
	"golang.org/x/term"
)

// Run executes the shared gbash CLI frontend with the supplied configuration.
func Run(ctx context.Context, cfg Config, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cfg = normalizeConfig(cfg)
	ttyDetector := cfg.TTYDetector
	if ttyDetector == nil {
		ttyDetector = stdinIsTTY
	}
	return run(ctx, cfg, argv0, args, stdin, stdout, stderr, ttyDetector(stdin))
}

func run(ctx context.Context, cfg Config, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	cfg = normalizeConfig(cfg)

	runtimeOpts, args, err := parseRuntimeOptions(args)
	if err != nil {
		return 2, err
	}

	parsed, err := builtins.ParseBashInvocation(args, builtins.BashInvocationConfig{
		Name:             cfg.Name,
		AllowInteractive: true,
		LongInteractive:  true,
	})
	if err != nil {
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

	rt, err := newRuntime(cfg, runtimeOpts)
	if err != nil {
		return 1, fmt.Errorf("init runtime: %w", err)
	}

	if parsed.Source == builtins.BashSourceStdin && (parsed.Interactive || stdinTTY) {
		return runInteractiveShell(ctx, rt, parsed, stdin, stdout, stderr)
	}
	return runBashInvocation(ctx, rt, parsed, stdin, stdout, stderr)
}

func runBashInvocation(ctx context.Context, rt *gbash.Runtime, parsed *builtins.BashInvocation, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if parsed == nil {
		parsed = &builtins.BashInvocation{Name: "gbash", Source: builtins.BashSourceStdin}
	}

	var (
		script      string
		execStdin   = stdin
		readErr     error
		missingPath string
	)
	switch parsed.Source {
	case builtins.BashSourceCommandString:
		script = parsed.CommandString
	case builtins.BashSourceFile:
		data, err := os.ReadFile(parsed.ScriptPath)
		if err != nil {
			readErr = err
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
			return 127, fmt.Errorf("%s: No such file or directory", missingPath)
		}
		return 1, fmt.Errorf("read script: %w", readErr)
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

func stdinIsTTY(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
