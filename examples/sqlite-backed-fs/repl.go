package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"

	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"golang.org/x/term"
)

const sqliteContinuationPrompt = "> "

type interactiveState struct {
	workDir string
	env     map[string]string
}

func runInteractiveShell(ctx context.Context, gb *gbash.Runtime, stdin io.Reader, stdout, stderr io.Writer, workDir string) (int, error) {
	session, err := gb.NewSession(ctx)
	if err != nil {
		return 1, fmt.Errorf("init session: %w", err)
	}

	parser := syntax.NewParser()
	printer := syntax.NewPrinter()
	state := interactiveState{
		workDir: workDir,
	}
	exitCode := 0

	_, _ = io.WriteString(stdout, promptForState(state))
	for stmts, err := range parser.InteractiveSeq(stdin) {
		if err != nil {
			exitCode = 1
			_, _ = fmt.Fprintln(stderr, err)
			_, _ = io.WriteString(stdout, promptForState(state))
			continue
		}
		if parser.Incomplete() {
			_, _ = io.WriteString(stdout, sqliteContinuationPrompt)
			continue
		}
		if len(stmts) == 0 {
			_, _ = io.WriteString(stdout, promptForState(state))
			continue
		}

		script, err := renderStatements(printer, stmts)
		if err != nil {
			return 1, fmt.Errorf("render interactive statements: %w", err)
		}

		result, err := session.Exec(ctx, &gbash.ExecutionRequest{
			Name:       "sqlite-backed-fs-repl",
			Script:     script,
			Env:        cloneEnv(state.env),
			WorkDir:    state.workDir,
			ReplaceEnv: state.env != nil,
		})
		if result != nil {
			_, _ = io.WriteString(stdout, result.Stdout)
			_, _ = io.WriteString(stderr, result.Stderr)
			exitCode = result.ExitCode
			state = nextInteractiveState(state, result)
			if result.ShellExited {
				return exitCode, nil
			}
		}
		if err != nil {
			return 1, fmt.Errorf("runtime error: %w", err)
		}
		_, _ = io.WriteString(stdout, promptForState(state))
	}

	return exitCode, nil
}

func renderStatements(printer *syntax.Printer, stmts []*syntax.Stmt) (string, error) {
	if printer == nil {
		printer = syntax.NewPrinter()
	}

	var buf bytes.Buffer
	file := &syntax.File{
		Name:  "sqlite-backed-fs-repl",
		Stmts: stmts,
	}
	if err := printer.Print(&buf, file); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func promptForState(state interactiveState) string {
	workDir := state.workDir
	if workDir == "" {
		workDir = defaultWorkDir
	}

	home := defaultWorkDir
	if state.env != nil && state.env["HOME"] != "" {
		home = state.env["HOME"]
	}

	return fmt.Sprintf("%s$ ", displayDir(home, workDir))
}

func displayDir(home, workDir string) string {
	switch {
	case home == "" || workDir == "":
		return workDir
	case workDir == home:
		return "~"
	case strings.HasPrefix(workDir, home+"/"):
		return "~" + strings.TrimPrefix(workDir, home)
	default:
		return workDir
	}
}

func nextInteractiveState(current interactiveState, result *gbash.ExecutionResult) interactiveState {
	if result == nil {
		return current
	}

	next := current
	if result.FinalEnv != nil {
		next.env = cloneEnv(result.FinalEnv)
		if pwd := strings.TrimSpace(result.FinalEnv["PWD"]); pwd != "" {
			next.workDir = pwd
		}
		if next.workDir == "" {
			next.workDir = defaultWorkDir
		}
	}
	return next
}

func cloneEnv(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	maps.Copy(out, src)
	return out
}

func stdinIsTTY(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
