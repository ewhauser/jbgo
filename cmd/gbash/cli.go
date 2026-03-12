package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"

	gbruntime "github.com/ewhauser/gbash/runtime"
	"golang.org/x/term"
)

type cliOptions struct {
	interactive bool
	showVersion bool
}

type compatInvocation struct {
	utility    string
	args       []string
	commandDir string
}

func runCLI(ctx context.Context, argv0 string, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	compat, err := parseCompatInvocation(argv0, args)
	if err != nil {
		return 2, err
	}
	if compat != nil {
		return runCompatInvocation(ctx, argv0, *compat, stdin, stdout, stderr)
	}

	opts, err := parseCLIOptions(args, stderr)
	if err != nil {
		return 2, err
	}
	if opts.showVersion {
		_, _ = io.WriteString(stdout, versionText())
		return 0, nil
	}

	rt, err := gbruntime.New()
	if err != nil {
		return 1, fmt.Errorf("init runtime: %w", err)
	}

	if opts.interactive || stdinTTY {
		return runInteractiveShell(ctx, rt, stdin, stdout, stderr)
	}
	return runScript(ctx, rt, stdin, stdout, stderr)
}

func parseCLIOptions(args []string, stderr io.Writer) (cliOptions, error) {
	fs := flag.NewFlagSet("gbash", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts cliOptions
	fs.BoolVar(&opts.interactive, "i", false, "run an interactive shell session")
	fs.BoolVar(&opts.interactive, "interactive", false, "run an interactive shell session")
	fs.BoolVar(&opts.showVersion, "version", false, "print version information")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if fs.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return opts, nil
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

func runScript(ctx context.Context, rt *gbruntime.Runtime, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	src, err := io.ReadAll(stdin)
	if err != nil {
		return 1, fmt.Errorf("read stdin: %w", err)
	}

	result, err := rt.Run(ctx, &gbruntime.ExecutionRequest{
		Name:   "stdin",
		Script: string(src),
	})
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
