package builtins

import (
	"context"
	"fmt"
	"math/big"
)

type Expr struct{}

func NewExpr() *Expr {
	return &Expr{}
}

func (c *Expr) Name() string {
	return "expr"
}

func (c *Expr) Run(_ context.Context, inv *Invocation) error {
	args := append([]string(nil), inv.Args...)
	if len(args) == 1 && args[0] == "--help" {
		_, _ = fmt.Fprint(inv.Stdout, exprHelpText)
		return nil
	}
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprint(inv.Stdout, exprVersionText)
		return nil
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return exitf(inv, 2, "expr: missing operand\nTry 'expr --help' for more information.")
	}

	locale := newBuiltinLocaleContext(inv.Env)
	parser := exprParser{tokens: args, locale: locale}
	root, err := parser.parse()
	if err != nil {
		return exitf(inv, 2, "expr: %v", err)
	}
	value, err := exprEvaluateNode(root, locale)
	if err != nil {
		return exitf(inv, 2, "expr: %v", err)
	}

	if _, err := fmt.Fprintln(inv.Stdout, value.text); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if exprIsTruthy(value) {
		return nil
	}
	return &ExitError{Code: 1}
}

type exprValue struct {
	text string
}

func newExprString(text string) exprValue {
	return exprValue{text: text}
}

func newExprInt(value string) exprValue {
	return exprValue{text: value}
}

func exprZeroValue() exprValue {
	return newExprInt("0")
}

func exprIsTruthy(value exprValue) bool {
	if value.text == "" || value.text == "-" {
		return value.text != ""
	}

	text := value.text
	if text[0] == '-' {
		text = text[1:]
	}
	if text == "" {
		return true
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '0' {
			return true
		}
	}
	return false
}

func (v exprValue) bigint() (*big.Int, error) {
	value, ok := parseDecimalBigInt(v.text)
	if !ok {
		return nil, exprNonIntegerArgumentError()
	}
	return value, nil
}

const exprHelpText = `Usage: expr EXPRESSION
  or:  expr OPTION
Print the value of EXPRESSION to standard output.
`

const exprVersionText = "expr (gbash) dev\n"

var _ Command = (*Expr)(nil)
