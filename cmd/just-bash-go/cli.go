package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	jbruntime "github.com/cadencerpm/just-bash-go/runtime"
	"golang.org/x/term"
)

type cliOptions struct {
	interactive bool
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, stdinTTY bool) (int, error) {
	opts, err := parseCLIOptions(args, stderr)
	if err != nil {
		return 2, err
	}

	rt, err := jbruntime.New(&jbruntime.Config{})
	if err != nil {
		return 1, fmt.Errorf("init runtime: %w", err)
	}

	if opts.interactive || stdinTTY {
		return runInteractiveShell(ctx, rt, stdin, stdout, stderr)
	}
	return runScript(ctx, rt, stdin, stdout, stderr)
}

func parseCLIOptions(args []string, stderr io.Writer) (cliOptions, error) {
	fs := flag.NewFlagSet("just-bash-go", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts cliOptions
	fs.BoolVar(&opts.interactive, "i", false, "run an interactive shell session")
	fs.BoolVar(&opts.interactive, "interactive", false, "run an interactive shell session")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if fs.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return opts, nil
}

func runScript(ctx context.Context, rt *jbruntime.Runtime, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	src, err := io.ReadAll(stdin)
	if err != nil {
		return 1, fmt.Errorf("read stdin: %w", err)
	}

	result, err := rt.Run(ctx, &jbruntime.ExecutionRequest{
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
