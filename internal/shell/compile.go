package shell

import (
	"errors"
	"io"

	"github.com/ewhauser/gbash/policy"
	"github.com/ewhauser/gbash/shell/syntax"
)

func compileChunk(program *syntax.File, pol policy.Policy, budget *executionBudget, loopNamespace string) (map[*syntax.Stmt]*syntax.Stmt, error) {
	if invalid := validateInterpreterSafety(program); invalid != nil {
		return nil, invalid
	}
	if violation := validateExecutionBudgets(program, pol); violation != nil {
		return nil, violation
	}
	if err := budget.beforeGlob(estimateProgramGlobOps(program)); err != nil {
		return nil, err
	}
	synthetic := normalizeExecutionProgram(program)
	if err := instrumentLoopBudgets(program, pol, loopNamespace); err != nil {
		return nil, err
	}
	return synthetic, nil
}

func compilationExitStatus(err error) (int, bool) {
	var violation *budgetViolation
	if errors.As(err, &violation) {
		return 126, true
	}
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		return 2, true
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
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		_, _ = io.WriteString(stderr, parseErr.BashError())
	} else {
		_, _ = io.WriteString(stderr, err.Error())
	}
	_, _ = io.WriteString(stderr, "\n")
}
