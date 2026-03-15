package shell

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	interactiveDefaultDir = "/home/agent"
	continuationPrompt    = "> "
)

func (m *MVdan) Interact(ctx context.Context, exec *Execution) (*InteractiveResult, error) {
	if exec == nil {
		exec = &Execution{}
	}
	if exec.Dir == "" {
		exec.Dir = interactiveDefaultDir
	}
	if exec.Stdin == nil {
		exec.Stdin = strings.NewReader("")
	}
	if exec.Stdout == nil {
		exec.Stdout = io.Discard
	}
	if exec.Stderr == nil {
		exec.Stderr = io.Discard
	}
	exec.Interactive = true
	input := bufio.NewReader(exec.Stdin)
	exec.Stdin = strings.NewReader("")

	budget := newExecutionBudget(exec.Policy, runtimePreludeLineCount())
	runner, err := interp.New(m.runnerOptions(exec, budget)...)
	if err != nil {
		return nil, err
	}
	if err := m.bootstrapRunner(ctx, runner, exec); err != nil {
		return &InteractiveResult{ExitCode: ExitCode(err)}, normalizeInteractiveRunError(err)
	}
	if err := applyRunnerParams(runner, exec.StartupOptions, exec.Args); err != nil {
		return nil, err
	}

	parser := syntax.NewParser()
	printer := syntax.NewPrinter()
	exitCode := 0

	_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
	for stmts, err := range parser.InteractiveSeq(input) {
		if err != nil {
			exitCode = 1
			_, _ = fmt.Fprintln(exec.Stderr, err)
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if parser.Incomplete() {
			_, _ = io.WriteString(exec.Stdout, continuationPrompt)
			continue
		}
		if len(stmts) == 0 {
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}

		script, err := renderInteractiveStatements(printer, exec.Name, stmts)
		if err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		script = withInteractiveHistory(runner, script)
		file, err := m.parseUserProgram(exec.Name, script)
		if err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		if err := rewriteLetClauses(file); err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		if violation := validateExecutionBudgets(file, exec.Policy); violation != nil {
			exitCode = 126
			_, _ = fmt.Fprintln(exec.Stderr, violation.Error())
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if invalid := validateInterpreterSafety(file); invalid != nil {
			exitCode = 2
			_, _ = fmt.Fprintln(exec.Stderr, invalid.Error())
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if err := instrumentLoopBudgets(file, exec.Policy); err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		runErr := runner.Run(ctx, file)
		exitCode = ExitCode(runErr)
		if runner.Exited() {
			return &InteractiveResult{ExitCode: exitCode}, normalizeInteractiveRunError(runErr)
		}
		if err := normalizeInteractiveRunError(runErr); err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
	}
	return &InteractiveResult{ExitCode: exitCode}, nil
}

func interactiveEnv(exec *Execution, runner *interp.Runner) map[string]string {
	if runner != nil {
		if env := envMapFromVars(runner.Vars); len(env) > 0 {
			return env
		}
	}
	if exec == nil {
		return nil
	}
	return exec.Env
}

func interactivePrompt(env map[string]string) string {
	workDir := strings.TrimSpace(env["PWD"])
	if workDir == "" {
		workDir = interactiveDefaultDir
	}
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home = interactiveDefaultDir
	}
	return fmt.Sprintf("%s$ ", interactiveDisplayDir(home, workDir))
}

func interactiveDisplayDir(home, workDir string) string {
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

func normalizeInteractiveRunError(err error) error {
	if err == nil || IsExitStatus(err) {
		return nil
	}
	return err
}

func renderInteractiveStatements(printer *syntax.Printer, name string, stmts []*syntax.Stmt) (string, error) {
	if printer == nil {
		printer = syntax.NewPrinter()
	}

	var buf bytes.Buffer
	if err := printer.Print(&buf, &syntax.File{
		Name:  name,
		Stmts: stmts,
	}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
