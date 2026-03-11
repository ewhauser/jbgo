package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	jbruntime "github.com/ewhauser/jbgo/runtime"
)

const defaultWorkDir = "/home/agent"

func main() {
	exitCode, err := run(context.Background(), os.Stdin, os.Stdout, os.Stderr, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func run(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args []string) (int, error) {
	opts, err := parseCLIOptions(stderr, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 1, err
	}

	rt, err := jbruntime.New(&jbruntime.Config{
		FileSystem: jbruntime.CustomFileSystem(
			sqliteFSFactory{dbPath: opts.dbPath},
			opts.workDir,
		),
	})
	if err != nil {
		return 1, fmt.Errorf("create runtime: %w", err)
	}

	if opts.repl || (opts.script == "" && stdinIsTTY(stdin)) {
		return runInteractiveShell(ctx, rt, stdin, stdout, stderr, opts.workDir)
	}

	script := opts.script
	if script == "" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return 1, fmt.Errorf("read stdin: %w", err)
		}
		script = string(data)
	}

	result, err := rt.Run(ctx, &jbruntime.ExecutionRequest{
		Name:   "sqlite-backed-fs",
		Script: script,
	})
	if err != nil {
		return 1, fmt.Errorf("run script: %w", err)
	}

	if stdout != nil {
		if _, err := io.WriteString(stdout, result.Stdout); err != nil {
			return 1, fmt.Errorf("write stdout: %w", err)
		}
	}
	if stderr != nil {
		if _, err := io.WriteString(stderr, result.Stderr); err != nil {
			return 1, fmt.Errorf("write stderr: %w", err)
		}
	}

	return result.ExitCode, nil
}

type cliOptions struct {
	dbPath  string
	workDir string
	repl    bool
	script  string
}

func parseCLIOptions(stderr io.Writer, args []string) (cliOptions, error) {
	var opts cliOptions

	fs := flag.NewFlagSet("sqlite-backed-fs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&opts.dbPath, "db", "", "host SQLite database file used to persist the sandbox filesystem")
	fs.StringVar(&opts.workDir, "workdir", defaultWorkDir, "sandbox working directory")
	fs.BoolVar(&opts.repl, "i", false, "run an interactive shell session")
	fs.BoolVar(&opts.repl, "repl", false, "run an interactive shell session")
	fs.StringVar(&opts.script, "script", "", "shell script to run; when empty the example reads from stdin")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if opts.dbPath == "" {
		return cliOptions{}, errors.New("--db is required")
	}
	if opts.repl && opts.script != "" {
		return cliOptions{}, errors.New("--repl and --script cannot be used together")
	}

	return opts, nil
}
