// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"errors"
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
	case *syntax.BraceExp:
		buf.WriteByte('{')
		for i, elem := range p.Elems {
			if i > 0 {
				if p.Sequence {
					buf.WriteString("..")
				} else {
					buf.WriteByte(',')
				}
			}
			for _, inner := range elem.Parts {
				writeLiteral(buf, inner)
			}
		}
		buf.WriteByte('}')
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

// ArithmDivByZeroError preserves the AST context for bash-style
// division-by-zero diagnostics and can optionally render exact source slices.
type ArithmDivByZeroError struct {
	Expr      syntax.ArithmExpr
	Token     syntax.ArithmExpr
	ExprText  string
	TokenText string

	Source      string
	SourceStart uint
	SourceEnd   uint
}

func (e *ArithmDivByZeroError) Error() string {
	exprText := e.ExprText
	if exprText == "" {
		exprText = arithExprSource(e.Expr)
	}
	tokenText := e.TokenText
	if tokenText == "" {
		tokenText = arithExprSource(e.Token)
	}
	if fromSource, ok := arithExprDiagnosticSource(e.Expr, e.Source, e.SourceStart, e.SourceEnd); ok {
		exprText = fromSource
	}
	if fromSource, ok := arithTokenDiagnosticSource(e.Token, e.Source, e.SourceStart, e.SourceEnd); ok {
		tokenText = fromSource
	}
	return fmt.Sprintf("%s: division by 0 (error token is %q)", exprText, tokenText)
}

// ArithmWithSource evaluates expr and, when it fails with division by zero,
// prefers the original arithmetic source text for bash-compatible diagnostics.
func ArithmWithSource(cfg *Config, expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint) (int, error) {
	n, err := Arithm(cfg, expr)
	if err == nil {
		return n, nil
	}
	var divErr *ArithmDivByZeroError
	if !errors.As(err, &divErr) {
		return 0, err
	}
	cloned := *divErr
	cloned.Source = source
	cloned.SourceStart = sourceStart
	cloned.SourceEnd = sourceEnd
	return 0, &cloned
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
	return arithm(cfg, expr, expr, 0)
}

func arithm(cfg *Config, root, expr syntax.ArithmExpr, depth int) (int, error) {
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
		if depth < maxNameRefDepth {
			p := syntax.NewParser()
			if nested, err := p.Arithmetic(strings.NewReader(str)); err == nil {
				if nested != nil {
					if word, ok := nested.(*syntax.Word); !ok || word.Lit() != str {
						return arithm(cfg, root, nested, depth+1)
					}
				}
			}
		}
		// default to 0
		return int(atoi(str)), nil
	case *syntax.ParenArithm:
		return arithm(cfg, root, expr.X, depth)
	case *syntax.UnaryArithm:
		switch expr.Op {
		case syntax.Inc, syntax.Dec:
			ref, old, err := cfg.arithmLValue(expr.X)
			if err != nil {
				return 0, err
			}
			val := old
			if expr.Op == syntax.Inc {
				val++
			} else {
				val--
			}
			if err := cfg.envSetRef(ref, strconv.FormatInt(int64(val), 10)); err != nil {
				return 0, err
			}
			if expr.Post {
				return old, nil
			}
			return val, nil
		}
		val, err := arithm(cfg, root, expr.X, depth)
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
			cond, err := arithm(cfg, root, expr.X, depth)
			if err != nil {
				return 0, err
			}
			b2 := expr.Y.(*syntax.BinaryArithm) // must have Op==TernColon
			if cond == 1 {
				return arithm(cfg, root, b2.X, depth)
			}
			return arithm(cfg, root, b2.Y, depth)
		}
		left, err := arithm(cfg, root, expr.X, depth)
		if err != nil {
			return 0, err
		}
		right, err := arithm(cfg, root, expr.Y, depth)
		if err != nil {
			return 0, err
		}
		// Check for division by zero with source tokens
		if right == 0 && (expr.Op == syntax.Quo || expr.Op == syntax.Rem) {
			return 0, divByZeroError(expr, left, right)
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
	n, _ := strconv.ParseInt(s, 0, 64)
	return n
}

// containsShellExpansion reports whether a Word contains any shell expansion
// parts ($var, ${var}, $(cmd), etc.) that are pre-expanded before arithmetic.
func containsShellExpansion(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp:
			return true
		case *syntax.DblQuoted:
			// Double-quoted strings can contain expansions
			return true
		}
	}
	return false
}

func arithExprDiagnosticSource(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint) (string, bool) {
	if arithExprUsesExpandedValue(expr) {
		return "", false
	}
	if fromSource, ok := arithSourceSpan(expr, source, sourceStart, sourceEnd, true); ok {
		return fromSource, true
	}
	return "", false
}

func arithTokenDiagnosticSource(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint) (string, bool) {
	if arithExprUsesExpandedValue(expr) {
		return "", false
	}
	if fromSource, ok := arithSourceSpan(expr, source, sourceStart, sourceEnd, true); ok {
		return fromSource, true
	}
	return "", false
}

func arithExprUsesExpandedValue(expr syntax.ArithmExpr) bool {
	w, ok := expr.(*syntax.Word)
	return ok && containsShellExpansion(w)
}

func arithSourceSpan(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint, includeTrailingSpaces bool) (string, bool) {
	if source == "" || expr == nil || !expr.Pos().IsValid() || !expr.End().IsValid() {
		return "", false
	}
	start := expr.Pos().Offset()
	end := expr.End().Offset()
	if start < sourceStart || end < start || end > sourceEnd {
		return "", false
	}
	relStart := int(start - sourceStart)
	relEnd := int(end - sourceStart)
	if relStart < 0 || relEnd < relStart || relEnd > len(source) {
		return "", false
	}
	if includeTrailingSpaces {
		for relEnd < len(source) {
			switch source[relEnd] {
			case ' ', '\t':
				relEnd++
			default:
				return source[relStart:relEnd], true
			}
		}
	}
	return source[relStart:relEnd], true
}

// divByZeroError creates a division-by-zero error with source tokens matching bash's format.
// For shell expansions ($y), bash reports the expanded value; for bare variables (x), it shows the name.
func divByZeroError(expr *syntax.BinaryArithm, evaluatedLeft, evaluatedDivisor int) error {
	// Build full expression: expand $-style expansions like bash does
	var leftStr, divisor string
	if w, ok := expr.X.(*syntax.Word); ok && containsShellExpansion(w) {
		leftStr = strconv.Itoa(evaluatedLeft)
	} else {
		leftStr = arithExprSource(expr.X)
	}
	if w, ok := expr.Y.(*syntax.Word); ok && containsShellExpansion(w) {
		divisor = strconv.Itoa(evaluatedDivisor)
	} else {
		divisor = arithExprSource(expr.Y)
	}
	fullExpr := leftStr + expr.Op.String() + divisor
	return &ArithmDivByZeroError{
		Expr:      expr,
		Token:     expr.Y,
		ExprText:  fullExpr,
		TokenText: divisor,
	}
}

// divByZeroErrorAssgn creates a division-by-zero error for assignment operators.
func divByZeroErrorAssgn(b *syntax.BinaryArithm, op string) error {
	lhs := arithExprSource(b.X)
	rhs := arithExprSource(b.Y)
	return fmt.Errorf("%s%s=%s: division by 0 (error token is \"%s\")", lhs, op, rhs, rhs)
}

func (cfg *Config) assgnArit(root syntax.ArithmExpr, b *syntax.BinaryArithm) (int, error) {
	ref, val, err := cfg.arithmLValue(b.X)
	if err != nil {
		return 0, err
	}
	arg_, err := arithm(cfg, root, b.Y, 0)
	if err != nil {
		return 0, err
	}
	arg := int64(arg_)
	acc := int64(val)
	switch b.Op {
	case syntax.Assgn:
		acc = arg
	case syntax.AddAssgn:
		acc += arg
	case syntax.SubAssgn:
		acc -= arg
	case syntax.MulAssgn:
		acc *= arg
	case syntax.QuoAssgn:
		if arg == 0 {
			return 0, divByZeroErrorAssgn(b, "/")
		}
		acc /= arg
	case syntax.RemAssgn:
		if arg == 0 {
			return 0, divByZeroErrorAssgn(b, "%")
		}
		acc %= arg
	case syntax.AndAssgn:
		acc &= arg
	case syntax.OrAssgn:
		acc |= arg
	case syntax.XorAssgn:
		acc ^= arg
	case syntax.ShlAssgn:
		acc <<= uint(arg)
	case syntax.ShrAssgn:
		acc >>= uint(arg)
	}
	if err := cfg.envSetRef(ref, strconv.FormatInt(acc, 10)); err != nil {
		return 0, err
	}
	return int(acc), nil
}

func arithmVarRef(expr syntax.ArithmExpr) (*syntax.VarRef, bool) {
	word, ok := expr.(*syntax.Word)
	if !ok || len(word.Parts) != 1 {
		return nil, false
	}
	switch part := word.Parts[0].(type) {
	case *syntax.Lit:
		if syntax.ValidName(part.Value) {
			return &syntax.VarRef{Name: part}, true
		}
	case *syntax.ParamExp:
		if part.Short && part.Index != nil && !part.Dollar.IsValid() {
			return &syntax.VarRef{Name: part.Param, Index: part.Index}, true
		}
	}
	return nil, false
}

func (cfg *Config) arithmLValue(expr syntax.ArithmExpr) (*syntax.VarRef, int, error) {
	ref, ok := arithmVarRef(expr)
	if !ok {
		return nil, 0, fmt.Errorf("invalid arithmetic lvalue")
	}
	val, err := cfg.varRef(ref)
	if err != nil {
		return ref, 0, err
	}
	return ref, int(atoi(val)), nil
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
