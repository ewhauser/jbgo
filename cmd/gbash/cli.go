package main

import (
	"context"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/builtins"
	"golang.org/x/term"
)

type compatInvocation struct {
	utility    string
	args       []string
	commandDir string
}

func runCLI(ctx context.Context, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	runtimeOpts, args, err := parseCLIRuntimeOptions(args)
	if err != nil {
		return 2, err
	}

	compat, err := parseCompatInvocation(argv0, args)
	if err != nil {
		return 2, err
	}
	if compat != nil {
		if runtimeOpts.hasRuntimeConfiguration() {
			return 2, fmt.Errorf("filesystem flags are unsupported with gbash compat exec")
		}
		return runCompatInvocation(ctx, argv0, *compat, stdin, stdout, stderr)
	}

	parsed, err := builtins.ParseBashInvocation(args, builtins.BashInvocationConfig{
		Name:             "gbash",
		AllowInteractive: true,
		LongInteractive:  true,
	})
	if err != nil {
		return 2, err
	}
	switch parsed.Action {
	case "help":
		if err := renderCLIHelp(stdout); err != nil {
			return 1, err
		}
		return 0, nil
	case "version":
		_, _ = io.WriteString(stdout, versionText())
		return 0, nil
	}

	rt, err := newCLIRuntime(runtimeOpts)
	if err != nil {
		return 1, fmt.Errorf("init runtime: %w", err)
	}

	if parsed.Source == builtins.BashSourceStdin && (parsed.Interactive || stdinTTY) {
		return runInteractiveShell(ctx, rt, parsed, stdin, stdout, stderr)
	}
	return runBashInvocation(ctx, rt, parsed, stdin, stdout, stderr)
}

func parseCompatInvocation(argv0 string, args []string) (*compatInvocation, error) {
	if utility := multicallUtilityName(argv0); utility != "" {
		commandDir, err := resolveCompatCommandDir(argv0)
		if err != nil {
			return nil, err
		}
		return &compatInvocation{
			utility:    utility,
			args:       append([]string(nil), args...),
			commandDir: commandDir,
		}, nil
	}
	if len(args) == 0 || args[0] != "compat" {
		return nil, nil
	}
	if len(args) < 2 || args[1] != "exec" {
		return nil, fmt.Errorf("usage: gbash compat exec <utility> [args...]")
	}
	if len(args) < 3 {
		return nil, fmt.Errorf("gbash compat exec requires a utility name")
	}
	return &compatInvocation{
		utility: args[2],
		args:    append([]string(nil), args[3:]...),
	}, nil
}

func multicallUtilityName(argv0 string) string {
	base := strings.TrimSpace(filepath.Base(argv0))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "gbash" {
		return ""
	}
	return base
}

func resolveCommandDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(resolved), nil
}

func resolveCompatCommandDir(argv0 string) (string, error) {
	if strings.Contains(argv0, string(os.PathSeparator)) {
		return resolveCommandDir(filepath.Dir(argv0))
	}

	resolved, err := osexec.LookPath(argv0)
	if err != nil {
		return "", err
	}
	return resolveCommandDir(filepath.Dir(resolved))
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
	}
	if len(req.PassthroughArgs) == 0 {
		req.PassthroughArgs = []string{"-s"}
	}

	result, err := rt.Run(ctx, req)
	if result != nil {
		_, _ = io.WriteString(stdout, result.Stdout)
		_, _ = io.WriteString(stderr, result.Stderr)
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
