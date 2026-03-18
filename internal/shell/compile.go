package shell

import (
	"errors"
	"io"

	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/policy"
)

type compiledProgram struct {
	program           *syntax.File
	pipelineSubshells map[*syntax.Stmt]*syntax.Stmt
}

func (m *core) compileProgram(name, script string, pol policy.Policy) (*compiledProgram, error) {
	program, err := m.parseUserProgram(name, script)
	if err != nil {
		return nil, err
	}
	if invalid := validateInterpreterSafety(program); invalid != nil {
		return nil, invalid
	}
	if violation := validateExecutionBudgets(program, pol); violation != nil {
		return nil, violation
	}
	compiled := &compiledProgram{
		program:           program,
		pipelineSubshells: normalizeExecutionProgram(program),
	}
	if err := instrumentLoopBudgets(program, pol); err != nil {
		return nil, err
	}
	return compiled, nil
}

func compilationExitStatus(err error) (int, bool) {
	var violation *budgetViolation
	if errors.As(err, &violation) {
		return 126, true
	}
	var invalid *shellValidationError
	if errors.As(err, &invalid) {
		return 2, true
	}
	return 0, false
}

func writeCompilationError(stderr io.Writer, err error) {
	if stderr == nil || err == nil {
		return
	}
	_, _ = io.WriteString(stderr, err.Error())
	_, _ = io.WriteString(stderr, "\n")
}
