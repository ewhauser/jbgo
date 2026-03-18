// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// arithExprSource returns the source text representation of an arithmetic expression.
func arithExprSource(expr syntax.ArithmExpr) string {
	var buf bytes.Buffer
	writeArithExpr(&buf, expr)
	return buf.String()
}

// writeArithExpr writes the source representation of an arithmetic expression to buf.
func writeArithExpr(buf *bytes.Buffer, expr syntax.ArithmExpr) {
	switch expr := expr.(type) {
	case *syntax.Word:
		// For a Word, concatenate all its parts
		for _, part := range expr.Parts {
			writeLiteral(buf, part)
		}
	case *syntax.BinaryArithm:
		writeArithExpr(buf, expr.X)
		buf.WriteString(expr.Op.String())
		writeArithExpr(buf, expr.Y)
	case *syntax.UnaryArithm:
		if expr.Post {
			writeArithExpr(buf, expr.X)
			buf.WriteString(expr.Op.String())
		} else {
			buf.WriteString(expr.Op.String())
			writeArithExpr(buf, expr.X)
		}
	case *syntax.ParenArithm:
		buf.WriteByte('(')
		writeArithExpr(buf, expr.X)
		buf.WriteByte(')')
	}
}

// writeLiteral writes a word part's literal representation.
func writeLiteral(buf *bytes.Buffer, part syntax.WordPart) {
	switch p := part.(type) {
	case *syntax.Lit:
		buf.WriteString(p.Value)
	case *syntax.SglQuoted:
		buf.WriteByte('\'')
		buf.WriteString(p.Value)
		buf.WriteByte('\'')
	case *syntax.DblQuoted:
		buf.WriteByte('"')
		for _, inner := range p.Parts {
			writeLiteral(buf, inner)
		}
		buf.WriteByte('"')
	case *syntax.ParamExp:
		buf.WriteByte('$')
		if p.Short {
			buf.WriteString(p.Param.Value)
		} else {
			buf.WriteByte('{')
			buf.WriteString(p.Param.Value)
			buf.WriteByte('}')
		}
	case *syntax.CmdSubst:
		buf.WriteString("$(")
		buf.WriteString("...") // simplified
		buf.WriteByte(')')
	case *syntax.ArithmExp:
		buf.WriteString("$((")
		writeArithExpr(buf, p.X)
		buf.WriteString("))")
	}
}

// TODO(v4): the arithmetic APIs should return int64 for portability with 32-bit systems,
// even if Bash only supports native int sizes.

// ArithmSyntaxError is returned when arithmetic evaluation encounters
// a syntax error such as a quoted string operand.
type ArithmSyntaxError struct {
	Expr  syntax.ArithmExpr // the expression being evaluated
	Token syntax.ArithmExpr // the invalid token within Expr
}

func (e ArithmSyntaxError) Error() string {
	token := syntax.ArithmExprString(e.Token)
	if expr := syntax.ArithmExprString(e.Expr); expr != "" {
		return fmt.Sprintf("%s: arithmetic syntax error: operand expected (error token is %q)", expr, token)
	}
	return fmt.Sprintf("arithmetic syntax error: operand expected (error token is %q)", token)
}

// hasSingleQuote checks if a word contains any single-quoted parts.
// Bash rejects both '...' and $'...' (ANSI-C) strings in arithmetic context.
func hasSingleQuote(word *syntax.Word) *syntax.SglQuoted {
	for _, part := range word.Parts {
		if sq, ok := part.(*syntax.SglQuoted); ok {
			return sq
		}
	}
	return nil
}

func Arithm(cfg *Config, expr syntax.ArithmExpr) (int, error) {
	return arithm(cfg, expr, expr)
}

func arithm(cfg *Config, root, expr syntax.ArithmExpr) (int, error) {
	switch expr := expr.(type) {
	case *syntax.Word:
		// Bash rejects single-quoted strings in arithmetic context.
		if hasSingleQuote(expr) != nil {
			return 0, ArithmSyntaxError{Expr: root, Token: expr}
		}
		str, err := Literal(cfg, expr)
		if err != nil {
			return 0, err
		}
		// recursively fetch vars
		i := 0
		for syntax.ValidName(str) {
			val := cfg.envGet(str)
			if val == "" {
				break
			}
			if i++; i >= maxNameRefDepth {
				break
			}
			str = val
		}
		// default to 0
		return int(atoi(str)), nil
	case *syntax.ParenArithm:
		return arithm(cfg, root, expr.X)
	case *syntax.UnaryArithm:
		switch expr.Op {
		case syntax.Inc, syntax.Dec:
			name := expr.X.(*syntax.Word).Lit()
			old := atoi(cfg.envGet(name))
			val := old
			if expr.Op == syntax.Inc {
				val++
			} else {
				val--
			}
			if err := cfg.envSet(name, strconv.FormatInt(val, 10)); err != nil {
				return 0, err
			}
			if expr.Post {
				return int(old), nil
			}
			return int(val), nil
		}
		val, err := arithm(cfg, root, expr.X)
		if err != nil {
			return 0, err
		}
		switch expr.Op {
		case syntax.Not:
			return oneIf(val == 0), nil
		case syntax.BitNegation:
			return ^val, nil
		case syntax.Plus:
			return val, nil
		case syntax.Minus:
			return -val, nil
		default:
			return 0, fmt.Errorf("unsupported unary arithmetic operator: %q", expr.Op)
		}
	case *syntax.BinaryArithm:
		switch expr.Op {
		case syntax.Assgn, syntax.AddAssgn, syntax.SubAssgn,
			syntax.MulAssgn, syntax.QuoAssgn, syntax.RemAssgn,
			syntax.AndAssgn, syntax.OrAssgn, syntax.XorAssgn,
			syntax.ShlAssgn, syntax.ShrAssgn:
			return cfg.assgnArit(root, expr)
		case syntax.TernQuest: // TernColon can't happen here
			cond, err := arithm(cfg, root, expr.X)
			if err != nil {
				return 0, err
			}
			b2 := expr.Y.(*syntax.BinaryArithm) // must have Op==TernColon
			if cond == 1 {
				return arithm(cfg, root, b2.X)
			}
			return arithm(cfg, root, b2.Y)
		}
		left, err := arithm(cfg, root, expr.X)
		if err != nil {
			return 0, err
		}
		right, err := arithm(cfg, root, expr.Y)
		if err != nil {
			return 0, err
		}
		// Check for division by zero with source tokens
		if right == 0 && (expr.Op == syntax.Quo || expr.Op == syntax.Rem) {
			return 0, divByZeroError(expr)
		}
		return binArit(expr.Op, left, right)
	default:
		panic(fmt.Sprintf("unexpected arithm expr: %T", expr))
	}
}

func oneIf(b bool) int {
	if b {
		return 1
	}
	return 0
}

// atoi is like [strconv.ParseInt](s, 10, 64), but it ignores errors and trims whitespace.
func atoi(s string) int64 {
	s = strings.TrimSpace(s)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// divByZeroError creates a division-by-zero error with source tokens matching bash's format.
func divByZeroError(expr *syntax.BinaryArithm) error {
	fullExpr := arithExprSource(expr)
	divisor := arithExprSource(expr.Y)
	return fmt.Errorf("%s: division by 0 (error token is \"%s\")", fullExpr, divisor)
}

// divByZeroErrorAssgn creates a division-by-zero error for assignment operators.
func divByZeroErrorAssgn(b *syntax.BinaryArithm, op string) error {
	lhs := arithExprSource(b.X)
	rhs := arithExprSource(b.Y)
	return fmt.Errorf("%s%s=%s: division by 0 (error token is \"%s\")", lhs, op, rhs, rhs)
}

func (cfg *Config) assgnArit(root syntax.ArithmExpr, b *syntax.BinaryArithm) (int, error) {
	name := b.X.(*syntax.Word).Lit()
	val := atoi(cfg.envGet(name))
	arg_, err := arithm(cfg, root, b.Y)
	if err != nil {
		return 0, err
	}
	arg := int64(arg_)
	switch b.Op {
	case syntax.Assgn:
		val = arg
	case syntax.AddAssgn:
		val += arg
	case syntax.SubAssgn:
		val -= arg
	case syntax.MulAssgn:
		val *= arg
	case syntax.QuoAssgn:
		if arg == 0 {
			return 0, divByZeroErrorAssgn(b, "/")
		}
		val /= arg
	case syntax.RemAssgn:
		if arg == 0 {
			return 0, divByZeroErrorAssgn(b, "%")
		}
		val %= arg
	case syntax.AndAssgn:
		val &= arg
	case syntax.OrAssgn:
		val |= arg
	case syntax.XorAssgn:
		val ^= arg
	case syntax.ShlAssgn:
		val <<= uint(arg)
	case syntax.ShrAssgn:
		val >>= uint(arg)
	}
	if err := cfg.envSet(name, strconv.FormatInt(val, 10)); err != nil {
		return 0, err
	}
	return int(val), nil
}

func intPow(a, b int) int {
	p := 1
	for b > 0 {
		if b&1 != 0 {
			p *= a
		}
		b >>= 1
		a *= a
	}
	return p
}

func binArit(op syntax.BinAritOperator, x, y int) (int, error) {
	switch op {
	case syntax.Add:
		return x + y, nil
	case syntax.Sub:
		return x - y, nil
	case syntax.Mul:
		return x * y, nil
	case syntax.Quo:
		// Division by zero is checked before calling binArit with source tokens
		return x / y, nil
	case syntax.Rem:
		// Division by zero is checked before calling binArit with source tokens
		return x % y, nil
	case syntax.Pow:
		return intPow(x, y), nil
	case syntax.Eql:
		return oneIf(x == y), nil
	case syntax.Gtr:
		return oneIf(x > y), nil
	case syntax.Lss:
		return oneIf(x < y), nil
	case syntax.Neq:
		return oneIf(x != y), nil
	case syntax.Leq:
		return oneIf(x <= y), nil
	case syntax.Geq:
		return oneIf(x >= y), nil
	case syntax.And:
		return x & y, nil
	case syntax.Or:
		return x | y, nil
	case syntax.Xor:
		return x ^ y, nil
	case syntax.Shr:
		return x >> uint(y), nil
	case syntax.Shl:
		return x << uint(y), nil
	case syntax.AndArit:
		return oneIf(x != 0 && y != 0), nil
	case syntax.OrArit:
		return oneIf(x != 0 || y != 0), nil
	case syntax.Comma:
		// x is executed but its result discarded
		return y, nil
	default:
		return 0, fmt.Errorf("unsupported binary arithmetic operator: %q", op)
	}
}
