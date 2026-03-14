package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ewhauser/gbash"
)

type options struct {
	workspace string
	cwd       string
	command   string
}

func main() {
	os.Exit(realMain(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func realMain(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gbash-runner: %v\n", err)
		return 2
	}

	exitCode, err := run(ctx, opts, stdin, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "gbash-runner: %v\n", err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	return exitCode
}

func parseOptions(args []string) (options, error) {
	var opts options
	fs := flag.NewFlagSet("gbash-runner", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&opts.workspace, "workspace", "", "host workspace to mount into the sandbox")
	fs.StringVar(&opts.cwd, "cwd", "", "initial working directory inside the sandbox")
	fs.StringVar(&opts.command, "c", "", "script to execute")
	fs.StringVar(&opts.command, "command", "", "script to execute")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}

	opts.workspace = strings.TrimSpace(opts.workspace)
	opts.cwd = strings.TrimSpace(opts.cwd)
	if strings.TrimSpace(opts.command) == "" {
		return options{}, fmt.Errorf("missing required -c/--command")
	}
	return opts, nil
}

func run(ctx context.Context, opts options, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	runtimeOpts := make([]gbash.Option, 0, 2)
	if opts.workspace != "" {
		runtimeOpts = append(runtimeOpts, gbash.WithWorkspace(opts.workspace))
	}
	if opts.cwd != "" {
		runtimeOpts = append(runtimeOpts, gbash.WithWorkingDir(opts.cwd))
	}

	rt, err := gbash.New(runtimeOpts...)
	if err != nil {
		return 1, fmt.Errorf("init runtime: %w", err)
	}

	result, err := rt.Run(ctx, &gbash.ExecutionRequest{
		Name:   "benchmark",
		Script: opts.command,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		return 1, fmt.Errorf("run benchmark command: %w", err)
	}
	if result == nil {
		return 1, fmt.Errorf("runtime returned no result")
	}
	return result.ExitCode, nil
}
