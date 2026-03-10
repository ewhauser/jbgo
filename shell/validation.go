package shell

import (
	"fmt"
	"strings"

	"github.com/cadencerpm/just-bash-go/policy"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

type budgetViolation struct {
	message string
}

func (e *budgetViolation) Error() string {
	return e.message
}

func validateExecutionBudgets(program *syntax.File, pol policy.Policy) error {
	if program == nil || pol == nil {
		return nil
	}

	limits := pol.Limits()
	var (
		currentSubDepth int64
		maxSubDepth     int64
		globOps         int64
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

		switch node := node.(type) {
		case *syntax.CmdSubst:
			currentSubDepth++
			if currentSubDepth > maxSubDepth {
				maxSubDepth = currentSubDepth
			}
		case *syntax.Word:
			globOps += estimateWordGlobOps(node)
		}
		return true
	})

	if limits.MaxSubstitutionDepth > 0 && maxSubDepth > limits.MaxSubstitutionDepth {
		return &budgetViolation{
			message: fmt.Sprintf("Command substitution nesting limit exceeded (%d), increase policy.Limits.MaxSubstitutionDepth", limits.MaxSubstitutionDepth),
		}
	}
	if limits.MaxGlobOperations > 0 && globOps > limits.MaxGlobOperations {
		return &budgetViolation{
			message: fmt.Sprintf("Glob operation limit exceeded (%d), increase policy.Limits.MaxGlobOperations", limits.MaxGlobOperations),
		}
	}
	return nil
}

func estimateWordGlobOps(word *syntax.Word) int64 {
	lit := wordLiteral(word)
	if lit == "" || !pattern.HasMeta(lit, 0) {
		return 0
	}

	ops := int64(1)
	for _, part := range strings.Split(lit, "/") {
		if part == "" {
			continue
		}
		if pattern.HasMeta(part, 0) {
			ops++
		}
	}
	return ops
}
