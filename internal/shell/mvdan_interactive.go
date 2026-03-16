package shell

import (
	"bufio"
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

	exitCode := 0
	var pending strings.Builder

	_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
	for {
		line, readErr := input.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return &InteractiveResult{ExitCode: exitCode}, readErr
		}
		if line == "" && readErr == io.EOF {
			break
		}

		pending.WriteString(line)
		rawScript := pending.String()

		file, err := m.parseUserProgram(exec.Name, rawScript)
		if err != nil {
			if syntax.IsIncomplete(err) && readErr != io.EOF {
				_, _ = io.WriteString(exec.Stdout, continuationPrompt)
				continue
			}
			exitCode = 1
			_, _ = fmt.Fprintln(exec.Stderr, err)
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if len(file.Stmts) == 0 {
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}

		executionFile, err := m.parseUserProgram(exec.Name, withInteractiveHistory(runner, rawScript))
		if err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		normalizeExecutionProgram(executionFile)
		if violation := validateExecutionBudgets(executionFile, exec.Policy); violation != nil {
			exitCode = 126
			_, _ = fmt.Fprintln(exec.Stderr, violation.Error())
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if invalid := validateInterpreterSafety(executionFile); invalid != nil {
			exitCode = 2
			_, _ = fmt.Fprintln(exec.Stderr, invalid.Error())
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if err := instrumentLoopBudgets(executionFile, exec.Policy); err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		runErr := runner.Run(ctx, executionFile)
		exitCode = ExitCode(runErr)
		pending.Reset()
		if runner.Exited() {
			return &InteractiveResult{ExitCode: exitCode}, normalizeInteractiveRunError(runErr)
		}
		if err := normalizeInteractiveRunError(runErr); err != nil {
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		if readErr == io.EOF {
			break
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
