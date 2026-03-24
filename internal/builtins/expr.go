package builtins

import (
	"context"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Expr struct{}

func NewExpr() *Expr {
	return &Expr{}
}

func (c *Expr) Name() string {
	return "expr"
}

func (c *Expr) Run(_ context.Context, inv *Invocation) error {
	args := inv.Args
	if len(args) == 0 {
		return exitf(inv, 2, "expr: missing operand")
	}
	if args[0] == "--help" {
		_, _ = fmt.Fprintln(inv.Stdout, "usage: expr EXPRESSION")
		return nil
	}
	if args[0] == "--version" {
		_, _ = fmt.Fprintln(inv.Stdout, "expr (gbash)")
		return nil
	}
	if args[0] == "--" {
		args = args[1:]
	}

	parser := exprParser{tokens: args}
	value, err := parser.parseExpression()
	if err != nil {
		return exitf(inv, 2, "expr: %v", err)
	}
	if parser.hasMore() {
		return exitf(inv, 2, "expr: syntax error")
	}
	if _, err := fmt.Fprintln(inv.Stdout, value.String()); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if value.Truthy() {
		return nil
	}
	return &ExitError{Code: 1}
}

type exprParser struct {
	tokens []string
	pos    int
}

type exprValue struct {
	text  string
	isInt bool
}

func newExprInt(v *big.Int) exprValue {
	return exprValue{text: v.String(), isInt: true}
}

func newExprString(text string) exprValue {
	return exprValue{text: text}
}

func (v exprValue) String() string {
	return v.text
}

func (v exprValue) Truthy() bool {
	return v.text != "" && v.text != "0"
}

func (v exprValue) BigInt() (*big.Int, error) {
	n, ok := new(big.Int).SetString(v.text, 10)
	if !ok {
		return nil, fmt.Errorf("non-integer argument %q", v.text)
	}
	return n, nil
}

func (p *exprParser) parseExpression() (exprValue, error) {
	return p.parseOr()
}

func (p *exprParser) parseOr() (exprValue, error) {
	left, err := p.parseAnd()
	if err != nil {
		return exprValue{}, err
	}
	for p.match("|") {
		right, err := p.parseAnd()
		if err != nil {
			return exprValue{}, err
		}
		if left.Truthy() {
			continue
		}
		left = right
	}
	return left, nil
}

func (p *exprParser) parseAnd() (exprValue, error) {
	left, err := p.parseCompare()
	if err != nil {
		return exprValue{}, err
	}
	for p.match("&") {
		right, err := p.parseCompare()
		if err != nil {
			return exprValue{}, err
		}
		if left.Truthy() && right.Truthy() {
			continue
		}
		left = newExprInt(big.NewInt(0))
	}
	return left, nil
}

func (p *exprParser) parseCompare() (exprValue, error) {
	left, err := p.parseAdd()
	if err != nil {
		return exprValue{}, err
	}

	for {
		switch {
		case p.match(":"):
			right, err := p.parseAdd()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprRegexMatch(left, right)
			if err != nil {
				return exprValue{}, err
			}
		case p.match("="), p.match("=="), p.match("!="), p.match("<"), p.match("<="), p.match(">"), p.match(">="):
			op := p.tokens[p.pos-1]
			right, err := p.parseAdd()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprCompare(left, right, op)
			if err != nil {
				return exprValue{}, err
			}
		default:
			return left, nil
		}
	}
}

func (p *exprParser) parseAdd() (exprValue, error) {
	left, err := p.parseMul()
	if err != nil {
		return exprValue{}, err
	}
	for {
		switch {
		case p.match("+"):
			right, err := p.parseMul()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprArithmetic(left, right, "+")
			if err != nil {
				return exprValue{}, err
			}
		case p.match("-"):
			right, err := p.parseMul()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprArithmetic(left, right, "-")
			if err != nil {
				return exprValue{}, err
			}
		default:
			return left, nil
		}
	}
}

func (p *exprParser) parseMul() (exprValue, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return exprValue{}, err
	}
	for {
		switch {
		case p.match("*"):
			right, err := p.parsePrimary()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprArithmetic(left, right, "*")
			if err != nil {
				return exprValue{}, err
			}
		case p.match("/"):
			right, err := p.parsePrimary()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprArithmetic(left, right, "/")
			if err != nil {
				return exprValue{}, err
			}
		case p.match("%"):
			right, err := p.parsePrimary()
			if err != nil {
				return exprValue{}, err
			}
			left, err = exprArithmetic(left, right, "%")
			if err != nil {
				return exprValue{}, err
			}
		default:
			return left, nil
		}
	}
}

func (p *exprParser) parsePrimary() (exprValue, error) {
	if !p.hasMore() {
		return exprValue{}, fmt.Errorf("syntax error")
	}
	if p.match("(") {
		value, err := p.parseExpression()
		if err != nil {
			return exprValue{}, err
		}
		if !p.match(")") {
			return exprValue{}, fmt.Errorf("syntax error")
		}
		return value, nil
	}
	token := p.consume()
	return newExprString(token), nil
}

func (p *exprParser) hasMore() bool {
	return p.pos < len(p.tokens)
}

func (p *exprParser) match(token string) bool {
	if p.hasMore() && p.tokens[p.pos] == token {
		p.pos++
		return true
	}
	return false
}

func (p *exprParser) consume() string {
	token := p.tokens[p.pos]
	p.pos++
	return token
}

func exprArithmetic(left, right exprValue, op string) (exprValue, error) {
	lhs, err := left.BigInt()
	if err != nil {
		return exprValue{}, err
	}
	rhs, err := right.BigInt()
	if err != nil {
		return exprValue{}, err
	}

	switch op {
	case "+":
		return newExprInt(new(big.Int).Add(lhs, rhs)), nil
	case "-":
		return newExprInt(new(big.Int).Sub(lhs, rhs)), nil
	case "*":
		return newExprInt(new(big.Int).Mul(lhs, rhs)), nil
	case "/":
		if rhs.Sign() == 0 {
			return exprValue{}, fmt.Errorf("division by zero")
		}
		return newExprInt(new(big.Int).Quo(lhs, rhs)), nil
	case "%":
		if rhs.Sign() == 0 {
			return exprValue{}, fmt.Errorf("division by zero")
		}
		return newExprInt(new(big.Int).Rem(lhs, rhs)), nil
	default:
		return exprValue{}, fmt.Errorf("unsupported operator %q", op)
	}
}

func exprCompare(left, right exprValue, op string) (exprValue, error) {
	cmp := strings.Compare(left.text, right.text)
	if lhs, err := left.BigInt(); err == nil {
		if rhs, err := right.BigInt(); err == nil {
			cmp = lhs.Cmp(rhs)
		}
	}

	result := false
	switch op {
	case "=", "==":
		result = cmp == 0
	case "!=":
		result = cmp != 0
	case "<":
		result = cmp < 0
	case "<=":
		result = cmp <= 0
	case ">":
		result = cmp > 0
	case ">=":
		result = cmp >= 0
	default:
		return exprValue{}, fmt.Errorf("unsupported operator %q", op)
	}
	if result {
		return newExprInt(big.NewInt(1)), nil
	}
	return newExprInt(big.NewInt(0)), nil
}

func exprRegexMatch(left, right exprValue) (exprValue, error) {
	re, err := regexp.Compile("^" + translateBasicRegexp(right.text))
	if err != nil {
		return exprValue{}, fmt.Errorf("invalid regular expression")
	}
	match := re.FindStringSubmatch(left.text)
	if match == nil {
		if re.NumSubexp() > 0 {
			return newExprString(""), nil
		}
		return newExprInt(big.NewInt(0)), nil
	}
	if re.NumSubexp() > 0 {
		return newExprString(match[1]), nil
	}
	return newExprInt(big.NewInt(int64(utf8.RuneCountInString(match[0])))), nil
}

var _ Command = (*Expr)(nil)
