// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
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

func arithExprPrinted(expr syntax.ArithmExpr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, expr); err == nil {
		return buf.String()
	}
	return arithExprSource(expr)
}

func bashQuoteErrorToken(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`
}

// writeArithExpr writes the source representation of an arithmetic expression to buf.
func writeArithExpr(buf *bytes.Buffer, expr syntax.ArithmExpr) {
	switch expr := expr.(type) {
	case *syntax.Word:
		// For a Word, concatenate all its parts
		for _, part := range expr.Parts { //nolint:nilaway // parse error is returned before reaching this; expr is never nil here
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
		if p.Dollar {
			buf.WriteByte('$')
		}
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
		var tmp bytes.Buffer
		if err := syntax.NewPrinter().Print(&tmp, &syntax.Word{Parts: []syntax.WordPart{p}}); err == nil {
			buf.WriteString(tmp.String())
		} else {
			buf.WriteByte('$')
			if p.Short {
				buf.WriteString(p.Param.Value)
			} else {
				buf.WriteByte('{')
				buf.WriteString(p.Param.Value)
				buf.WriteByte('}')
			}
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

	Source      string
	SourceStart uint
	SourceEnd   uint

	Replacements []arithDiagnosticReplacement
}

func (e ArithmSyntaxError) Error() string {
	exprText := arithExprSource(e.Expr)
	tokenText := arithExprSource(e.Token)
	if fromSource, ok := arithExprDiagnosticSource(e.Expr, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
		exprText = fromSource
	}
	if fromSource, ok := arithTokenDiagnosticSource(e.Token, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
		tokenText = fromSource
	}
	if exprText != "" {
		return fmt.Sprintf("%s: arithmetic syntax error: operand expected (error token is %s)", exprText, bashQuoteErrorToken(tokenText))
	}
	return fmt.Sprintf("arithmetic syntax error: operand expected (error token is %s)", bashQuoteErrorToken(tokenText))
}

// ArithmDiagnosticError preserves bash-style arithmetic diagnostics that are
// not simple operand-expected errors.
type ArithmDiagnosticError struct {
	Expr      syntax.ArithmExpr
	Token     syntax.ArithmExpr
	Message   string
	ExprText  string
	TokenText string

	Source      string
	SourceStart uint
	SourceEnd   uint

	Replacements []arithDiagnosticReplacement
}

func (e *ArithmDiagnosticError) Error() string {
	exprText := e.ExprText
	if exprText == "" && e.Expr != nil {
		exprText = arithExprSource(e.Expr)
	}
	tokenText := e.TokenText
	if tokenText == "" && e.Token != nil {
		tokenText = arithExprSource(e.Token)
	}
	if e.Expr != nil {
		if fromSource, ok := arithExprDiagnosticSource(e.Expr, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
			exprText = fromSource
		}
	}
	if tokenText == "" && e.Token != nil {
		if fromSource, ok := arithTokenDiagnosticSource(e.Token, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
			tokenText = fromSource
		}
	}
	if exprText == "" {
		return fmt.Sprintf("%s (error token is %s)", e.Message, bashQuoteErrorToken(tokenText))
	}
	return fmt.Sprintf("%s: %s (error token is %s)", exprText, e.Message, bashQuoteErrorToken(tokenText))
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

	Replacements []arithDiagnosticReplacement
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
	if fromSource, ok := arithExprDiagnosticSource(e.Expr, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
		exprText = fromSource
	}
	if fromSource, ok := arithTokenDiagnosticSource(e.Token, e.Source, e.SourceStart, e.SourceEnd, e.Replacements); ok {
		tokenText = fromSource
	}
	return fmt.Sprintf("%s: division by 0 (error token is %s)", exprText, bashQuoteErrorToken(tokenText))
}

// ArithmWithSource evaluates expr and, when it fails,
// prefers the original arithmetic source text for bash-compatible diagnostics.
func ArithmWithSource(cfg *Config, expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint) (int, error) {
	n, err := Arithm(cfg, expr)
	if err == nil {
		return n, nil
	}
	return 0, WithArithmSource(err, source, sourceStart, sourceEnd)
}

func WithArithmSource(err error, source string, sourceStart, sourceEnd uint) error {
	if err == nil || source == "" {
		return err
	}
	var divErr *ArithmDivByZeroError
	if errors.As(err, &divErr) {
		if divErr.Source == source && divErr.SourceStart == sourceStart && divErr.SourceEnd == sourceEnd {
			return err
		}
		cloned := *divErr
		cloned.Source = source
		cloned.SourceStart = sourceStart
		cloned.SourceEnd = sourceEnd
		return &cloned
	}
	var diagErr *ArithmDiagnosticError
	if errors.As(err, &diagErr) {
		if diagErr.Source == source && diagErr.SourceStart == sourceStart && diagErr.SourceEnd == sourceEnd {
			return err
		}
		cloned := *diagErr
		cloned.Source = source
		cloned.SourceStart = sourceStart
		cloned.SourceEnd = sourceEnd
		if cloned.Expr != nil && cloned.ExprText != "" && !arithExprUsesExpandedValue(cloned.Expr) {
			cloned.ExprText = source
		}
		return &cloned
	}
	var syntaxErr ArithmSyntaxError
	if errors.As(err, &syntaxErr) {
		if syntaxErr.Source == source && syntaxErr.SourceStart == sourceStart && syntaxErr.SourceEnd == sourceEnd {
			return err
		}
		syntaxErr.Source = source
		syntaxErr.SourceStart = sourceStart
		syntaxErr.SourceEnd = sourceEnd
		return syntaxErr
	}
	return err
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

func (cfg *Config) arithmStringValue(root, tokenExpr syntax.ArithmExpr, word *syntax.Word, str string, depth int) (int, error) {
	s := strings.TrimSpace(str)
	if s == "" || isEmptyArithWord(word) {
		return 0, nil
	}
	if s == "LINENO" {
		if cfg.CurrentLine != nil {
			if line := cfg.CurrentLine(); line != 0 {
				return int(line), nil
			}
		}
		if tokenExpr != nil {
			if line := tokenExpr.Pos().Line(); line != 0 {
				return int(line), nil
			}
		}
	}

	i := 0
	for syntax.ValidName(s) {
		vr := cfg.Env.Get(s)
		if !vr.IsSet() {
			if cfg.NoUnset {
				return 0, UnboundVariableError{Name: s}
			}
			break
		}
		val := vr.String()
		if val == "" {
			break
		}
		if i++; i >= maxNameRefDepth {
			break
		}
		s = val
	}

	if depth < maxNameRefDepth {
		p := syntax.NewParser()
		if nested, err := p.Arithmetic(strings.NewReader(s)); err == nil {
			if nested != nil {
				if word, ok := nested.(*syntax.Word); !ok || word.Lit() != s {
					return arithm(cfg, root, nested, depth+1)
				}
			}
		}
	}

	if n, ok, err := parseArithNumber(s, root, tokenExpr); ok || err != nil {
		if err != nil {
			err = arithExpandedWordError(err, root, tokenExpr, word, s)
		}
		return int(n), err
	}
	return 0, nil
}

func arithExpandedWordError(err error, root, tokenExpr syntax.ArithmExpr, word *syntax.Word, value string) error {
	if err == nil || root != tokenExpr || word == nil {
		return err
	}
	if len(word.Parts) != 1 {
		return err
	}
	part, ok := word.Parts[0].(*syntax.ParamExp)
	if !ok || !part.Short || part.Dollar.IsValid() || part.Index == nil {
		return err
	}
	var diagErr *ArithmDiagnosticError
	if !errors.As(err, &diagErr) {
		return err
	}
	cloned := *diagErr
	cloned.Expr = nil
	cloned.ExprText = value
	return &cloned
}

func arithm(cfg *Config, root, expr syntax.ArithmExpr, depth int) (int, error) {
	if depth < maxNameRefDepth {
		if parsed, ok, err := arithmRuntimeParse(cfg, expr); err != nil {
			return 0, err
		} else if ok {
			return arithm(cfg, root, parsed, depth+1)
		}
	}
	switch expr := expr.(type) {
	case *syntax.Word:
		// Bash rejects single-quoted strings in arithmetic context.
		if hasSingleQuote(expr) != nil {
			token := syntax.ArithmExpr(expr)
			if root != nil && root.Pos() == expr.Pos() {
				token = root
			}
			return 0, ArithmSyntaxError{Expr: root, Token: token}
		}
		if !containsShellExpansion(expr) {
			src := arithExprSource(expr)
			p := syntax.NewParser()
			if _, err := p.Arithmetic(strings.NewReader(src)); err != nil {
				var parseErr syntax.ParseError
				if errors.As(err, &parseErr) {
					if tokenText, ok := arithParseOperandExpectedToken(src, parseErr); ok {
						return 0, &ArithmDiagnosticError{
							Expr:      root,
							TokenText: tokenText,
							Message:   "arithmetic syntax error: operand expected",
						}
					}
				}
				tokenText := src
				if errors.As(err, &parseErr) {
					tokenText = arithParseErrorToken(src, parseErr.Pos)
				}
				return 0, &ArithmDiagnosticError{
					Expr:      root,
					TokenText: tokenText,
					Message:   "arithmetic syntax error: invalid arithmetic operator",
				}
			}
		}
		str, err := Document(cfg, expr)
		if err != nil {
			var unboundErr UnboundVariableError
			if errors.As(err, &unboundErr) {
				if ref, ok := arithmVarRef(expr); ok && ref.Index != nil && ref.Name != nil {
					unboundErr.Name = ref.Name.Value
					return 0, unboundErr
				}
			}
			var unsetErr UnsetParameterError
			if errors.As(err, &unsetErr) && unsetErr.Message == "unbound variable" {
				if ref, ok := arithmVarRef(expr); ok && ref.Index != nil && ref.Name != nil {
					return 0, UnboundVariableError{Name: ref.Name.Value}
				}
			}
			return 0, err
		}
		return cfg.arithmStringValue(root, expr, expr, str, depth)
	case *syntax.ParenArithm:
		return arithm(cfg, root, expr.X, depth)
	case *syntax.UnaryArithm:
		switch expr.Op {
		case syntax.Inc, syntax.Dec:
			ref, old, err := cfg.arithmLValue(root, expr.X)
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
			if cond != 0 {
				return arithm(cfg, root, b2.X, depth)
			}
			return arithm(cfg, root, b2.Y, depth)
		case syntax.AndArit:
			left, err := arithm(cfg, root, expr.X, depth)
			if err != nil {
				return 0, err
			}
			if left == 0 {
				return 0, nil
			}
			right, err := arithm(cfg, root, expr.Y, depth)
			if err != nil {
				return 0, err
			}
			return oneIf(right != 0), nil
		case syntax.OrArit:
			left, err := arithm(cfg, root, expr.X, depth)
			if err != nil {
				return 0, err
			}
			if left != 0 {
				return 1, nil
			}
			right, err := arithm(cfg, root, expr.Y, depth)
			if err != nil {
				return 0, err
			}
			return oneIf(right != 0), nil
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
			return 0, divByZeroErrorFor(root, expr, left, right)
		}
		return binArit(root, expr, expr.Op, left, right)
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

// containsShellExpansion reports whether a Word contains any shell expansion
// parts ($var, ${var}, $(cmd), etc.) that are pre-expanded before arithmetic.
func containsShellExpansion(w *syntax.Word) bool {
	for _, part := range w.Parts {
		switch part := part.(type) {
		case *syntax.ParamExp:
			if !(part.Short && part.Index != nil && !part.Dollar.IsValid()) {
				return true
			}
		case *syntax.CmdSubst, *syntax.ArithmExp:
			return true
		case *syntax.DblQuoted:
			// Double-quoted strings can contain expansions
			return true
		}
	}
	return false
}

type arithDiagnosticReplacement struct {
	Start uint
	End   uint
	Text  string
}

func arithExprDiagnosticSource(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint, replacements []arithDiagnosticReplacement) (string, bool) {
	if arithExprUsesExpandedValue(expr) && len(replacements) == 0 {
		return "", false
	}
	if fromSource, ok := arithSourceSpan(expr, source, sourceStart, sourceEnd, true, replacements); ok {
		if prefix := arithLeadingExprDiagnosticPrefix(expr, source, sourceStart); prefix != "" {
			fromSource = prefix + fromSource
		}
		return fromSource, true
	}
	return "", false
}

func arithTokenDiagnosticSource(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint, replacements []arithDiagnosticReplacement) (string, bool) {
	if arithExprUsesExpandedValue(expr) && len(replacements) == 0 {
		return "", false
	}
	if fromSource, ok := arithSourceSpan(expr, source, sourceStart, sourceEnd, true, replacements); ok {
		return fromSource, true
	}
	return "", false
}

func arithExprUsesExpandedValue(expr syntax.ArithmExpr) bool {
	switch expr := expr.(type) {
	case *syntax.Word:
		return containsShellExpansion(expr)
	case *syntax.BinaryArithm:
		return arithExprUsesExpandedValue(expr.X) || arithExprUsesExpandedValue(expr.Y)
	case *syntax.UnaryArithm:
		return arithExprUsesExpandedValue(expr.X)
	case *syntax.ParenArithm:
		return arithExprUsesExpandedValue(expr.X)
	default:
		return false
	}
}

func arithParseErrorToken(source string, pos syntax.Pos) string {
	if source == "" || !pos.IsValid() {
		return source
	}
	start := int(pos.Offset())
	if start < 0 || start >= len(source) {
		return source
	}
	if start > 0 {
		switch source[start-1] {
		case '#', '[', '.':
			start--
		}
	}
	return source[start:]
}

func arithParseOperandExpectedToken(source string, parseErr syntax.ParseError) (string, bool) {
	switch {
	case strings.Contains(parseErr.Text, "must be followed by an expression"),
		strings.Contains(parseErr.Text, "must follow an expression"):
		return arithParseErrorToken(source, parseErr.Pos), true
	default:
		return "", false
	}
}

func arithRuntimeErrorToken(source string, parseErr syntax.ParseError) string {
	if source == "" || !parseErr.Pos.IsValid() {
		return source
	}
	start := int(parseErr.Pos.Offset())
	if start < 0 || start >= len(source) {
		return source
	}
	return strings.TrimLeft(source[start:], " \t")
}

func arithRuntimeIsExpressionError(tokenText string) bool {
	i := 0
	for i < len(tokenText) && tokenText[i] >= '0' && tokenText[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(tokenText) {
		return false
	}
	j := i
	for j < len(tokenText) {
		switch tokenText[j] {
		case ' ', '\t', '\r', '\n':
			j++
		default:
			goto done
		}
	}
done:
	if j == i || j >= len(tokenText) {
		return false
	}
	b := tokenText[j]
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b == '_' || b == '\'' || b == '"'
}

func arithRuntimeParseError(src string, parseErr syntax.ParseError) error {
	if tokenText, ok := arithParseOperandExpectedToken(src, parseErr); ok {
		return &ArithmDiagnosticError{
			ExprText:  src,
			TokenText: tokenText,
			Message:   "arithmetic syntax error: operand expected",
		}
	}
	tokenText := arithRuntimeErrorToken(src, parseErr)
	message := "arithmetic syntax error: invalid arithmetic operator"
	if arithRuntimeIsExpressionError(tokenText) {
		message = "syntax error in expression"
	}
	return &ArithmDiagnosticError{
		ExprText:  src,
		TokenText: tokenText,
		Message:   message,
	}
}

func arithRuntimeSource(cfg *Config, expr syntax.ArithmExpr) (string, error) {
	switch expr := expr.(type) {
	case *syntax.Word:
		if arithExprUsesExpandedValue(expr) {
			return Literal(cfg, expr)
		}
		return arithExprSource(expr), nil
	case *syntax.BinaryArithm:
		left, err := arithRuntimeSource(cfg, expr.X)
		if err != nil {
			return "", err
		}
		right, err := arithRuntimeSource(cfg, expr.Y)
		if err != nil {
			return "", err
		}
		return left + expr.Op.String() + right, nil
	case *syntax.UnaryArithm:
		val, err := arithRuntimeSource(cfg, expr.X)
		if err != nil {
			return "", err
		}
		if expr.Post {
			return val + expr.Op.String(), nil
		}
		return expr.Op.String() + val, nil
	case *syntax.ParenArithm:
		val, err := arithRuntimeSource(cfg, expr.X)
		if err != nil {
			return "", err
		}
		return "(" + val + ")", nil
	default:
		return arithExprSource(expr), nil
	}
}

func arithmRuntimeParse(cfg *Config, expr syntax.ArithmExpr) (syntax.ArithmExpr, bool, error) {
	switch expr.(type) {
	case *syntax.Word, *syntax.BinaryArithm, *syntax.UnaryArithm, *syntax.ParenArithm:
	default:
		return nil, false, nil
	}
	if !arithExprUsesExpandedValue(expr) {
		return nil, false, nil
	}
	src, err := arithRuntimeSource(cfg, expr)
	if err != nil {
		return nil, false, err
	}
	p := syntax.NewParser()
	parsed, err := p.Arithmetic(strings.NewReader(src))
	if err != nil {
		var parseErr syntax.ParseError
		if errors.As(err, &parseErr) {
			return nil, false, arithRuntimeParseError(src, parseErr)
		}
		return nil, false, &ArithmDiagnosticError{
			ExprText:  src,
			TokenText: src,
			Message:   "arithmetic syntax error: invalid arithmetic operator",
		}
	}
	if parsed == nil {
		return nil, false, nil
	}
	if word, ok := parsed.(*syntax.Word); ok && word.Lit() == src {
		return nil, false, nil
	}
	return parsed, true, nil
}

func arithSourceSpan(expr syntax.ArithmExpr, source string, sourceStart, sourceEnd uint, includeTrailingSpaces bool, replacements []arithDiagnosticReplacement) (string, bool) {
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
				return arithSourceSpanSegment(source, sourceStart, relStart, relEnd, start, end, replacements), true
			}
		}
	}
	return arithSourceSpanSegment(source, sourceStart, relStart, relEnd, start, end, replacements), true
}

func arithLeadingExprDiagnosticPrefix(expr syntax.ArithmExpr, source string, sourceStart uint) string {
	if source == "" || expr == nil || !expr.Pos().IsValid() {
		return ""
	}
	relStart := int(expr.Pos().Offset() - sourceStart)
	if relStart <= 0 || relStart > len(source) {
		return ""
	}
	prefix := source[:relStart]
	if strings.Trim(prefix, " \t\r\n") != "" {
		return ""
	}
	idx := strings.IndexByte(prefix, '\n')
	if idx < 0 {
		return ""
	}
	return prefix[idx:]
}

func arithSourceSpanSegment(source string, sourceStart uint, relStart, relEnd int, start, end uint, replacements []arithDiagnosticReplacement) string {
	if len(replacements) == 0 {
		return source[relStart:relEnd]
	}
	var b strings.Builder
	cursor := start
	for _, repl := range replacements {
		if repl.Start < start || repl.End > end || repl.Start < cursor {
			continue
		}
		b.WriteString(source[int(cursor-sourceStart):int(repl.Start-sourceStart)])
		b.WriteString(repl.Text)
		cursor = repl.End
	}
	b.WriteString(source[int(cursor-sourceStart):relEnd])
	return b.String()
}

func arithDiagnosticReplacementForExpr(expr syntax.ArithmExpr, text string) (arithDiagnosticReplacement, bool) {
	if expr == nil || !expr.Pos().IsValid() || !expr.End().IsValid() {
		return arithDiagnosticReplacement{}, false
	}
	return arithDiagnosticReplacement{
		Start: expr.Pos().Offset(),
		End:   expr.End().Offset(),
		Text:  text,
	}, true
}

// divByZeroError creates a division-by-zero error with source tokens matching bash's format.
// For shell expansions ($y), bash reports the expanded value; for bare variables (x), it shows the name.
func divByZeroError(expr *syntax.BinaryArithm, evaluatedLeft, evaluatedDivisor int) error {
	// Build full expression: expand $-style expansions like bash does
	var leftStr, divisor string
	var replacements []arithDiagnosticReplacement
	if arithExprUsesExpandedValue(expr.X) {
		leftStr = strconv.Itoa(evaluatedLeft)
	} else {
		leftStr = arithExprSource(expr.X)
	}
	if arithExprUsesExpandedValue(expr.X) {
		if repl, ok := arithDiagnosticReplacementForExpr(expr.X, strconv.Itoa(evaluatedLeft)); ok {
			replacements = append(replacements, repl)
		}
	}
	if arithExprUsesExpandedValue(expr.Y) {
		divisor = strconv.Itoa(evaluatedDivisor)
	} else {
		divisor = arithExprSource(expr.Y)
	}
	if arithExprUsesExpandedValue(expr.Y) {
		if repl, ok := arithDiagnosticReplacementForExpr(expr.Y, strconv.Itoa(evaluatedDivisor)); ok {
			replacements = append(replacements, repl)
		}
	}
	fullExpr := leftStr + expr.Op.String() + divisor
	return &ArithmDivByZeroError{
		Expr:         expr,
		Token:        expr.Y,
		ExprText:     fullExpr,
		TokenText:    divisor,
		Replacements: replacements,
	}
}

func divByZeroErrorFor(root syntax.ArithmExpr, expr *syntax.BinaryArithm, evaluatedLeft, evaluatedDivisor int) error {
	if rootExpr, ok := root.(*syntax.BinaryArithm); ok && rootExpr.Op == expr.Op {
		return divByZeroError(rootExpr, evaluatedLeft, evaluatedDivisor)
	}
	return divByZeroError(expr, evaluatedLeft, evaluatedDivisor)
}

// divByZeroErrorAssgn creates a division-by-zero error for assignment operators.
func divByZeroErrorAssgn(b *syntax.BinaryArithm, op string) error {
	lhs := arithExprSource(b.X)
	rhs := arithExprSource(b.Y)
	return fmt.Errorf("%s%s=%s: division by 0 (error token is \"%s\")", lhs, op, rhs, rhs)
}

func (cfg *Config) assgnArit(root syntax.ArithmExpr, b *syntax.BinaryArithm) (int, error) {
	ref, val, err := cfg.arithmLValue(root, b.X)
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
		acc = int64(int(acc) << maskedShift(int(arg)))
	case syntax.ShrAssgn:
		acc = int64(int(acc) >> maskedShift(int(arg)))
	}
	if err := cfg.envSetRef(ref, strconv.FormatInt(acc, 10)); err != nil {
		return 0, err
	}
	return int(acc), nil
}

func arithmVarRef(expr syntax.ArithmExpr) (*syntax.VarRef, bool) {
	word, ok := expr.(*syntax.Word)
	if !ok {
		return nil, false
	}
	if len(word.Parts) == 1 {
		switch part := word.Parts[0].(type) {
		case *syntax.Lit:
			if syntax.ValidName(part.Value) {
				return &syntax.VarRef{Name: part}, true
			}
		case *syntax.ParamExp:
			if part.Short && part.Index != nil && !part.Dollar.IsValid() && syntax.ValidName(part.Param.Value) {
				return &syntax.VarRef{Name: part.Param, Index: part.Index}, true
			}
		}
	}
	if containsShellExpansion(word) {
		return nil, false
	}
	ref, err := parseVarRef(arithExprSource(word))
	if err != nil || ref == nil {
		return nil, false
	}
	if ref.Name == nil || !syntax.ValidName(ref.Name.Value) {
		return nil, false
	}
	if ref.Index != nil && emptySubscript(ref.Index) {
		return nil, false
	}
	return ref, true
}

func isEmptyArithWord(word *syntax.Word) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	return ok && lit.Value == ""
}

func isEmptyArithExpr(expr syntax.ArithmExpr) bool {
	word, ok := expr.(*syntax.Word)
	return ok && isEmptyArithWord(word)
}

func arithWordDiagnostic(root, tokenExpr syntax.ArithmExpr, exprText, tokenText, message string) error {
	diag := &ArithmDiagnosticError{
		ExprText:  exprText,
		TokenText: tokenText,
		Message:   message,
	}
	if root == nil {
		return diag
	}
	diag.Expr = root
	if root != tokenExpr && !arithExprUsesExpandedValue(root) {
		diag.ExprText = arithExprPrinted(root)
	}
	return diag
}

func isArithWordChar(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		b == '_' || b == '@'
}

func arithDigitValue(base int, b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'z':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'Z':
		if base <= 36 {
			return int(b-'A') + 10, true
		}
		return int(b-'A') + 36, true
	case b == '@':
		return 62, true
	case b == '_':
		return 63, true
	default:
		return 0, false
	}
}

func parseArithNumberPrefix(s string) (int64, int, string, string, bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, 0, "", "", false
	}

	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && s[i] == '#' {
		basePart := s[:i]
		if len(basePart) > 1 && basePart[0] == '0' {
			return 0, len(s), "invalid number", s, true
		}
		base, err := strconv.Atoi(basePart)
		if err != nil || base < 2 || base > 64 || i+1 >= len(s) {
			return 0, len(s), "invalid number", s, true
		}
		var n int64
		j := i + 1
		for ; j < len(s); j++ {
			d, ok := arithDigitValue(base, s[j])
			if !ok {
				break
			}
			if d >= base {
				return 0, len(s), "value too great for base", s, true
			}
			n = n*int64(base) + int64(d)
		}
		if j == i+1 {
			return 0, len(s), "invalid number", s, true
		}
		if j < len(s) && isArithWordChar(s[j]) {
			return 0, len(s), "value too great for base", s, true
		}
		return n, j, "", "", true
	}

	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		var n int64
		j := 2
		for ; j < len(s); j++ {
			d, ok := arithDigitValue(16, s[j])
			if !ok || d >= 16 {
				break
			}
			n = n*16 + int64(d)
		}
		if j == 2 {
			return 0, len(s), "value too great for base", s, true
		}
		if j < len(s) && isArithWordChar(s[j]) {
			return 0, len(s), "value too great for base", s, true
		}
		return n, j, "", "", true
	}

	base := 10
	if len(s) > 1 && s[0] == '0' {
		base = 8
	}
	var n int64
	j := 0
	for ; j < len(s); j++ {
		b := s[j]
		if b < '0' || b > '9' {
			break
		}
		d := int(b - '0')
		if d >= base {
			return 0, len(s), "value too great for base", s, true
		}
		n = n*int64(base) + int64(d)
	}
	if j < len(s) && isArithWordChar(s[j]) {
		return 0, len(s), "value too great for base", s, true
	}
	return n, j, "", "", true
}

func parseArithNumber(s string, root, tokenExpr syntax.ArithmExpr) (int64, bool, error) {
	n, consumed, msg, token, ok := parseArithNumberPrefix(s)
	if !ok {
		return 0, false, nil
	}
	if msg != "" {
		return 0, true, arithWordDiagnostic(root, tokenExpr, s, token, msg)
	}
	rest := s[consumed:]
	if strings.TrimSpace(rest) == "" {
		return n, true, nil
	}
	trimmed := strings.TrimLeft(rest, " \t")
	message := "arithmetic syntax error: invalid arithmetic operator"
	if trimmed != "" {
		switch trimmed[0] {
		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
			'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
			'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
			'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z',
			'_', '\'', '"':
			if rest != trimmed {
				message = "arithmetic syntax error in expression"
			}
		}
	}
	return 0, true, arithWordDiagnostic(root, tokenExpr, s, trimmed, message)
}

func arithmWordValue(root syntax.ArithmExpr, expr *syntax.Word, str string) (int, error) {
	s := strings.TrimSpace(str)
	if s == "" || isEmptyArithWord(expr) {
		return 0, nil
	}
	if n, ok, err := parseArithNumber(s, root, expr); ok || err != nil {
		return int(n), err
	}
	return 0, nil
}

func maskedShift(n int) uint {
	return uint(n) & (bits.UintSize - 1)
}

func arithInvalidRefTail(text string) string {
	if text == "" {
		return text
	}
	i := 0
	for i < len(text) && ((text[i] >= 'a' && text[i] <= 'z') || (text[i] >= 'A' && text[i] <= 'Z') || text[i] == '_' || (i > 0 && text[i] >= '0' && text[i] <= '9')) {
		i++
	}
	if i == 0 {
		if len(text) > 1 {
			return text[1:]
		}
		return text
	}
	if i < len(text) && text[i] == '[' {
		depth := 0
		for ; i < len(text); i++ {
			switch text[i] {
			case '[':
				depth++
			case ']':
				if depth > 0 {
					depth--
					if depth == 0 {
						i++
						if i < len(text) {
							return text[i:]
						}
						return ""
					}
				}
			}
		}
	}
	if i < len(text) {
		return text[i:]
	}
	return ""
}

func (cfg *Config) arithmLValue(root, expr syntax.ArithmExpr) (*syntax.VarRef, int, error) {
	ref, ok := arithmVarRef(expr)
	if !ok {
		word, wordOK := expr.(*syntax.Word)
		if !wordOK {
			tokenText := "="
			if b, ok := root.(*syntax.BinaryArithm); ok && b.X == expr {
				tokenText = b.Op.String() + " " + arithExprSource(b.Y) + " "
			}
			return nil, 0, &ArithmDiagnosticError{
				Expr:      root,
				TokenText: tokenText,
				Message:   "attempted assignment to non-variable",
			}
		}
		var (
			text string
			err  error
		)
		if !containsShellExpansion(word) {
			text = arithExprSource(word)
		} else {
			text, err = Literal(cfg, word)
			if err != nil {
				return nil, 0, err
			}
		}
		ref, err = parseVarRef(text)
		if err != nil {
			tail := arithInvalidRefTail(text)
			if tail == "" {
				tail = text
			}
			if b, ok := root.(*syntax.BinaryArithm); ok && b.X == expr {
				tail += " " + b.Op.String() + " " + arithExprSource(b.Y) + " "
			}
			return nil, 0, &ArithmDiagnosticError{
				Expr:      root,
				TokenText: tail,
				Message:   "arithmetic syntax error: invalid arithmetic operator",
			}
		}
		if ref.Index != nil && isEmptyArithExpr(ref.Index.Expr) {
			return nil, 0, BadArraySubscriptError{Name: text}
		}
	}
	resolvedRef, vr, err := cfg.resolveVarRef(ref)
	if err != nil {
		return ref, 0, err
	}
	if resolvedRef != nil {
		ref = resolvedRef
	}
	if cfg.NoUnset && !vr.IsSet() {
		return ref, 0, UnboundVariableError{Name: ref.Name.Value}
	}
	val, err := cfg.varRef(ref)
	if err != nil {
		return ref, 0, err
	}
	n, err := cfg.arithmStringValue(expr, expr, nil, val, 0)
	if err != nil {
		return ref, 0, err
	}
	return ref, n, nil
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

func binArit(root syntax.ArithmExpr, expr *syntax.BinaryArithm, op syntax.BinAritOperator, x, y int) (int, error) {
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
		if y < 0 {
			tokenText := arithExprSource(expr.Y)
			if b, ok := root.(*syntax.BinaryArithm); ok && b.X == expr {
				tokenText = b.Op.String() + " " + arithExprSource(b.Y) + " "
			}
			return 0, &ArithmDiagnosticError{
				Expr:      root,
				TokenText: tokenText,
				Message:   "exponent less than 0",
			}
		}
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
		return x >> maskedShift(y), nil
	case syntax.Shl:
		return x << maskedShift(y), nil
	case syntax.Comma:
		// x is executed but its result discarded
		return y, nil
	default:
		return 0, fmt.Errorf("unsupported binary arithmetic operator: %q", op)
	}
}
