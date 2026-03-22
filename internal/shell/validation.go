package shell

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"github.com/ewhauser/gbash/policy"
)

type budgetViolation struct {
	message string
}

func (e *budgetViolation) Error() string {
	return e.message
}

type shellValidationError struct {
	message string
}

func (e *shellValidationError) Error() string {
	return e.message
}

func validateInterpreterSafety(program *syntax.File) error {
	if invalid := validateSupportedRedirections(program); invalid != nil {
		return invalid
	}
	return validateSupportedFunctionDeclarations(program)
}

func validateExecutionBudgets(program *syntax.File, pol policy.Policy) error {
	if program == nil || pol == nil {
		return nil
	}

	limits := pol.Limits()
	var (
		currentSubDepth int64
		maxSubDepth     int64
		stack           []syntax.Node
	)

	syntax.Walk(program, func(node syntax.Node) bool {
		if node == nil {
			if len(stack) == 0 {
				return true
			}
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, ok := last.(*syntax.CmdSubst); ok {
				currentSubDepth--
			}
			return true
		}

		stack = append(stack, node)

		if _, ok := node.(*syntax.CmdSubst); ok {
			currentSubDepth++
			if currentSubDepth > maxSubDepth {
				maxSubDepth = currentSubDepth
			}
		}
		return true
	})

	if limits.MaxSubstitutionDepth > 0 && maxSubDepth > limits.MaxSubstitutionDepth {
		return &budgetViolation{
			message: fmt.Sprintf("Command substitution nesting limit exceeded (%d), increase policy.Limits.MaxSubstitutionDepth", limits.MaxSubstitutionDepth),
		}
	}
	return nil
}

func estimateProgramGlobOps(program *syntax.File) int64 {
	if program == nil {
		return 0
	}
	var globOps int64
	for node := range syntax.All(program) {
		if word, ok := node.(*syntax.Word); ok {
			globOps += estimateWordGlobOps(word)
		}
	}
	return globOps
}

func validateSupportedRedirections(program *syntax.File) error {
	if program == nil {
		return nil
	}

	for node := range syntax.All(program) {
		redir, ok := node.(*syntax.Redirect)
		if !ok {
			continue
		}
		if redir.N != nil && !isSupportedRedirectFD(redir.N.Value) {
			return &shellValidationError{message: "invalid redirection"}
		}
		switch redir.Op {
		case syntax.AppClob, syntax.RdrAllClob, syntax.AppAllClob:
			return &shellValidationError{message: "invalid redirection"}
		}
	}
	return nil
}

func validateSupportedFunctionDeclarations(program *syntax.File) error {
	if program == nil {
		return nil
	}

	for node := range syntax.All(program) {
		fn, ok := node.(*syntax.FuncDecl)
		if !ok {
			continue
		}
		if fn.Body == nil || !hasSupportedFunctionName(fn) {
			return &shellValidationError{message: "invalid function declaration"}
		}
	}
	return nil
}

func hasSupportedFunctionName(fn *syntax.FuncDecl) bool {
	if fn == nil {
		return false
	}
	if hasFunctionNameLiteral(fn.Name) {
		return true
	}
	return slices.ContainsFunc(fn.Names, hasFunctionNameLiteral)
}

func hasFunctionNameLiteral(name *syntax.Lit) bool {
	return name != nil && strings.TrimSpace(name.Value) != ""
}

func isSupportedRedirectFD(fd string) bool {
	switch {
	case fd == "":
		return true
	case fd[0] == '{' && fd[len(fd)-1] == '}':
		return true
	case strings.HasPrefix(fd, "$"):
		return true
	default:
		_, err := strconv.Atoi(fd)
		return err == nil
	}
}

func estimateWordGlobOps(word *syntax.Word) int64 {
	lit := wordLiteral(word)
	return expand.EstimateGlobOperations(lit)
}
