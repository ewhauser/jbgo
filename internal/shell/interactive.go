package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/interp"
	"github.com/ewhauser/gbash/internal/shellstate"
	"github.com/ewhauser/gbash/shell/syntax"
)

const (
	interactiveDefaultDir = "/home/agent"
	continuationPrompt    = "> "
)

func (m *core) Interact(ctx context.Context, exec *Execution) (*InteractiveResult, error) {
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
	if exec.CompletionState == nil {
		exec.CompletionState = shellstate.NewCompletionState()
	}
	ctx = shellstate.WithCompletionState(ctx, exec.CompletionState)
	exec.Interactive = true
	input, ok := exec.Stdin.(*bufio.Reader)
	if !ok {
		input = bufio.NewReader(exec.Stdin)
	}
	exec.Stdin = strings.NewReader("")
	runnerExec := *exec
	if err := resolveExecutionVariant(&runnerExec); err != nil {
		return nil, err
	}
	cleanupProcSubst := withProcSubstScope(&runnerExec)
	defer cleanupProcSubst()

	budget := newExecutionBudget(exec.Policy)
	runner, err := m.newRunner(&runnerExec, budget)
	if err != nil {
		return nil, err
	}
	runner.AnalysisRunStart()
	exitCode := 0
	analysisFinished := false
	finishAnalysis := func(err error) {
		if analysisFinished {
			return
		}
		status := runnerAnalysisStatus(runner, err)
		if exitCode != 0 && status.Code == 0 {
			status.Code = exitCode
		}
		runner.AnalysisRunFinish(status)
		analysisFinished = true
	}
	var pending strings.Builder

	_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
	for {
		line, readErr := input.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			finishAnalysis(readErr)
			return &InteractiveResult{ExitCode: exitCode}, readErr
		}
		if line == "" && readErr == io.EOF {
			break
		}

		pending.WriteString(line)
		rawScript := pending.String()

		parser := runner.NewParser()
		program, err := parser.Parse(strings.NewReader(rawScript), exec.Name)
		if err != nil {
			if interactiveParseIncomplete(err, parser) && readErr != io.EOF {
				_, _ = io.WriteString(exec.Stdout, continuationPrompt)
				continue
			}
			err = attachParseErrorSourceLine(err, rawScript)
			if code, ok := compilationExitStatus(err); ok {
				exitCode = code
				writeCompilationError(exec.Stderr, executionShellVariant(&runnerExec), err)
			} else {
				exitCode = 1
				_, _ = fmt.Fprintln(exec.Stderr, err)
			}
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		if len(program.Stmts) == 0 {
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		pipelineSubshells, err := compileChunk(program, exec.Policy, budget, budget.nextLoopNamespace(), executionShellVariant(&runnerExec))
		if err != nil {
			if code, ok := compilationExitStatus(err); ok {
				exitCode = code
				writeCompilationError(exec.Stderr, executionShellVariant(&runnerExec), err)
			} else {
				exitCode = 1
				_, _ = fmt.Fprintln(exec.Stderr, err)
			}
			pending.Reset()
			if readErr == io.EOF {
				break
			}
			_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
			continue
		}
		rememberInteractiveHistory(runner, rawScript) //nolint:contextcheck // interactive history is stored by direct runner state mutation
		runErr := runner.RunWithMetadata(ctx, program, "", pipelineSubshells)
		exitCode = ExitCode(runErr)
		pending.Reset()
		if runner.Exited() {
			finishAnalysis(runErr)
			return &InteractiveResult{ExitCode: exitCode}, normalizeInteractiveRunError(runErr)
		}
		if err := normalizeInteractiveRunError(runErr); err != nil {
			finishAnalysis(err)
			return &InteractiveResult{ExitCode: exitCode}, err
		}
		if readErr == io.EOF {
			break
		}
		_, _ = io.WriteString(exec.Stdout, interactivePrompt(interactiveEnv(exec, runner)))
	}
	finishAnalysis(nil)
	return &InteractiveResult{ExitCode: exitCode}, nil
}

func interactiveParseIncomplete(err error, parser *syntax.Parser) bool {
	if err == nil {
		return false
	}
	if syntax.IsIncomplete(err) || (parser != nil && parser.Incomplete()) {
		return true
	}
	var parseErr syntax.ParseError
	return errors.As(err, &parseErr) && strings.HasPrefix(parseErr.Text, "unclosed here-document")
}

func interactiveEnv(exec *Execution, runner *interp.Runner) map[string]string {
	if runner != nil {
		if env := runner.ShellEnv(); len(env) > 0 {
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
