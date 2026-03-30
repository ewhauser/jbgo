package syntax

import "strings"

// compact specifies whether we allow spaces between expressions.
// This is true for let
func (p *Parser) arithmExpr(compact bool) ArithmExpr {
	return p.arithmExprComma(compact)
}

// These function names are inspired by Bash's expr.c

func (p *Parser) arithmExprComma(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprAssign, Comma)
}

func (p *Parser) arithmExprAssign(compact bool) ArithmExpr {
	// Assign is different from the other binary operators because it's
	// right-associative.
	value := p.arithmExprTernary(compact)
	switch BinAritOperator(p.tok) {
	case AddAssgn, SubAssgn, MulAssgn, QuoAssgn, RemAssgn, AndAssgn,
		OrAssgn, XorAssgn, ShlAssgn, ShrAssgn, Assgn,
		AndBoolAssgn, OrBoolAssgn, XorBoolAssgn, PowAssgn:
		if compact && p.spaced {
			return value
		}
		pos := p.pos
		tok := p.tok
		p.nextArithOp(compact)
		y := p.arithmExprAssign(compact)
		if y == nil {
			p.followErrExp(pos, tok)
		}
		return &BinaryArithm{
			OpPos: pos,
			Op:    BinAritOperator(tok),
			X:     value,
			Y:     y,
		}
	}
	return value
}

func (p *Parser) arithmExprTernary(compact bool) ArithmExpr {
	value := p.arithmExprLor(compact)
	if BinAritOperator(p.tok) != TernQuest || (compact && p.spaced) {
		return value
	}

	if value == nil {
		p.posErrWithMetadata(p.pos, parseErrorMetadata{
			kind:       ParseErrorKindMissing,
			construct:  parseErrorSymbolFromToken(p.tok),
			unexpected: parseErrorSymbolFromToken(p.tok),
			expected:   []ParseErrorSymbol{ParseErrorSymbolExpression},
		}, "%#q must follow an expression", p.tok)
	}
	questPos := p.pos
	p.nextArithOp(compact)
	if BinAritOperator(p.tok) == TernColon {
		p.followErrExp(questPos, TernQuest)
	}
	trueExpr := p.arithmExpr(compact)
	if trueExpr == nil {
		p.followErrExp(questPos, TernQuest)
	}
	if BinAritOperator(p.tok) != TernColon {
		p.posErr(questPos, "ternary operator missing %#q after %#q", colon, quest)
	}
	colonPos := p.pos
	p.nextArithOp(compact)
	falseExpr := p.arithmExprTernary(compact)
	if falseExpr == nil {
		p.followErrExp(colonPos, TernColon)
	}
	return &BinaryArithm{
		OpPos: questPos,
		Op:    TernQuest,
		X:     value,
		Y: &BinaryArithm{
			OpPos: colonPos,
			Op:    TernColon,
			X:     trueExpr,
			Y:     falseExpr,
		},
	}
}

func (p *Parser) arithmExprLor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprLand, OrArit, XorBool)
}

func (p *Parser) arithmExprLand(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBor, AndArit)
}

func (p *Parser) arithmExprBor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBxor, Or)
}

func (p *Parser) arithmExprBxor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBand, Xor)
}

func (p *Parser) arithmExprBand(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprEquality, And)
}

func (p *Parser) arithmExprEquality(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprComparison, Eql, Neq)
}

func (p *Parser) arithmExprComparison(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprShift, Lss, Gtr, Leq, Geq)
}

func (p *Parser) arithmExprShift(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprAddition, Shl, Shr)
}

func (p *Parser) arithmExprAddition(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprMultiplication, Add, Sub)
}

func (p *Parser) arithmExprMultiplication(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprPower, Mul, Quo, Rem)
}

func (p *Parser) arithmExprPower(compact bool) ArithmExpr {
	// Power is different from the other binary operators because it's right-associative
	value := p.arithmExprUnary(compact)
	if BinAritOperator(p.tok) != Pow || (compact && p.spaced) {
		return value
	}

	if value == nil {
		p.posErrWithMetadata(p.pos, parseErrorMetadata{
			kind:       ParseErrorKindMissing,
			construct:  parseErrorSymbolFromToken(p.tok),
			unexpected: parseErrorSymbolFromToken(p.tok),
			expected:   []ParseErrorSymbol{ParseErrorSymbolExpression},
		}, "%#q must follow an expression", p.tok)
	}

	op := p.tok
	pos := p.pos
	p.nextArithOp(compact)
	y := p.arithmExprPower(compact)
	if y == nil {
		p.followErrExp(pos, op)
	}
	return &BinaryArithm{
		OpPos: pos,
		Op:    BinAritOperator(op),
		X:     value,
		Y:     y,
	}
}

func (p *Parser) arithmExprUnary(compact bool) ArithmExpr {
	if !compact {
		p.got(_Newl)
	}

	switch UnAritOperator(p.tok) {
	case Not, BitNegation, Plus, Minus:
		ue := &UnaryArithm{OpPos: p.pos, Op: UnAritOperator(p.tok)}
		p.nextArithOp(compact)
		if ue.X = p.arithmExprUnary(compact); ue.X == nil {
			p.followErrExp(ue.OpPos, ue.Op)
		}
		return ue
	}
	return p.arithmExprValue(compact)
}

func (p *Parser) arithmExprValue(compact bool) ArithmExpr {
	var x ArithmExpr
	switch p.tok {
	case addAdd, subSub:
		ue := &UnaryArithm{OpPos: p.pos, Op: UnAritOperator(p.tok)}
		p.nextArith(compact)
		ue.X = p.arithmExprValue(compact)
		if ue.X == nil {
			p.followErrExp(ue.OpPos, ue.Op)
		}
		return ue
	case leftParen:
		if p.quote == paramExpArithm && p.lang.in(LangZsh) {
			x = p.zshSubFlags()
			break
		}
		pe := &ParenArithm{Lparen: p.pos}
		if spaced := p.nextArith(compact); spaced && !(compact && p.quote == arithmExprLet) {
			p.followErrExp(pe.Lparen, leftParen)
		}
		pe.X = p.followArithm(leftParen, pe.Lparen)
		pe.Rparen = p.matched(pe.Lparen, leftParen, rightParen)
		if p.quote == paramExpArithm && p.tok == _LitWord {
			p.checkLang(pe.Lparen, LangZsh, FeatureArithmeticSubscriptFlags)
		}
		x = pe
	case leftBrack:
		p.curErr("%#q must follow a name", p.tok)
	case colon:
		p.curErr("ternary operator missing %#q before %#q", quest, colon)
	case _LitWord:
		l := p.getLit()
		if p.tok != leftBrack {
			x = p.wordOne(l)
			break
		}
		pe := &ParamExp{Short: true, Param: l}
		pe.Index = p.eitherIndex()
		x = p.wordOne(pe)
	case bckQuote:
		if p.quote == arithmExprLet && p.openBquotes > 0 {
			return nil
		}
		fallthrough
	default:
		if w := p.getWord(); w != nil {
			x = w
		} else {
			return nil
		}
	}
	x = p.arithmContinueWord(x, compact)

	if compact && p.spaced {
		return x
	}
	if !compact {
		p.got(_Newl)
	}

	// we want real nil, not (*Word)(nil) as that
	// sets the type to non-nil and then x != nil
	if p.tok == addAdd || p.tok == subSub {
		u := &UnaryArithm{
			Post:  true,
			OpPos: p.pos,
			Op:    UnAritOperator(p.tok),
			X:     x,
		}
		p.nextArith(compact)
		return u
	}
	return x
}

func (p *Parser) arithmWordSuffixStart() bool {
	if p.spaced {
		return false
	}
	switch p.tok {
	case leftBrack, period, hash, _Lit, _LitWord:
		return true
	default:
		return false
	}
}

func (p *Parser) arithmWordSuffixBoundary() bool {
	if p.spaced || p.peekArithmEnd() || p.tok == _EOF {
		return true
	}
	switch p.tok {
	case rightBrack, rightParen, addAdd, subSub:
		return true
	}
	return token(BinAritOperator(p.tok)) != illegalTok
}

func (p *Parser) arithmWordSuffixEnd(compact bool) Pos {
	parenDepth := 0
	brackDepth := 0
	for {
		if parenDepth == 0 && brackDepth == 0 && p.arithmWordSuffixBoundary() {
			return p.pos
		}
		switch p.tok {
		case leftParen:
			parenDepth++
		case rightParen:
			if parenDepth == 0 {
				return p.pos
			}
			parenDepth--
		case leftBrack:
			brackDepth++
		case rightBrack:
			if brackDepth == 0 {
				return p.pos
			}
			brackDepth--
		}
		p.nextArith(compact)
	}
}

func (p *Parser) arithmSuffixWord(start, end Pos, src string) *Word {
	doc := NewParser(Variant(p.lang), KeepComments(p.keepComments))
	if p.parenAmbiguityDisabled {
		doc.parenAmbiguityDisabled = true
		doc.parenAmbiguityProbeDepth = p.parenAmbiguityProbeDepth
	}
	if len(p.stopAt) > 0 {
		doc.stopAt = append([]byte(nil), p.stopAt...)
	}
	if src == "" {
		return p.wordOne(p.rawLit(start, end, ""))
	}
	if word, err := doc.document(strings.NewReader(src), start); err == nil && word != nil {
		return word
	}
	return p.wordOne(p.rawLit(start, end, src))
}

func (p *Parser) appendArithmSuffix(expr ArithmExpr, start, end Pos, src string) ArithmExpr {
	if src == "" {
		return expr
	}
	word := p.arithmSuffixWord(start, end, src)
	switch expr := expr.(type) {
	case *Word:
		if expr == nil {
			return word
		}
		expr.Parts = append(expr.Parts, word.Parts...)
		return expr
	case *BinaryArithm:
		if expr == nil {
			return word
		}
		expr.Y = p.appendArithmSuffix(expr.Y, start, end, src)
		return expr
	case *UnaryArithm:
		if expr == nil {
			return word
		}
		expr.X = p.appendArithmSuffix(expr.X, start, end, src)
		return expr
	case *ParenArithm:
		if expr == nil {
			return word
		}
		expr.X = p.appendArithmSuffix(expr.X, start, end, src)
		return expr
	default:
		return word
	}
}

func (p *Parser) arithmContinueWord(expr ArithmExpr, compact bool) ArithmExpr {
	if _, ok := expr.(*Word); !ok || !p.arithmWordSuffixStart() {
		return expr
	}
	start := p.pos
	end := p.arithmWordSuffixEnd(compact)
	return p.appendArithmSuffix(expr, start, end, p.sourceRange(start, end))
}

func arithmExprEnd(expr ArithmExpr) Pos {
	switch expr := expr.(type) {
	case nil:
		return Pos{}
	case *Word:
		if expr == nil {
			return Pos{}
		}
		return expr.End()
	case *BinaryArithm:
		if expr == nil {
			return Pos{}
		}
		if end := arithmExprEnd(expr.Y); end.IsValid() {
			return end
		}
		return arithmExprEnd(expr.X)
	case *UnaryArithm:
		if expr == nil {
			return Pos{}
		}
		return arithmExprEnd(expr.X)
	case *ParenArithm:
		if expr == nil {
			return Pos{}
		}
		return arithmExprEnd(expr.X)
	default:
		return Pos{}
	}
}

// nextArith consumes a token.
// It returns true if compact and the token was followed by spaces
func (p *Parser) nextArith(compact bool) bool {
	p.next()
	if compact && p.spaced {
		return true
	}
	if !compact {
		p.got(_Newl)
	}
	return false
}

func (p *Parser) nextArithOp(compact bool) {
	pos := p.pos
	tok := p.tok
	if p.nextArith(compact) {
		p.followErrExp(pos, tok)
	}
}

// arithmExprBinary is used for all left-associative binary operators
func (p *Parser) arithmExprBinary(compact bool, nextOp func(bool) ArithmExpr, operators ...BinAritOperator) ArithmExpr {
	value := nextOp(compact)
	for {
		var foundOp BinAritOperator
		for _, op := range operators {
			if p.tok == token(op) {
				foundOp = op
				break
			}
		}

		if token(foundOp) == illegalTok || (compact && p.spaced) {
			return value
		}

		if value == nil {
			p.posErrWithMetadata(p.pos, parseErrorMetadata{
				kind:       ParseErrorKindMissing,
				construct:  parseErrorSymbolFromToken(p.tok),
				unexpected: parseErrorSymbolFromToken(p.tok),
				expected:   []ParseErrorSymbol{ParseErrorSymbolExpression},
			}, "%#q must follow an expression", p.tok)
		}

		pos := p.pos
		p.nextArithOp(compact)
		y := nextOp(compact)
		if y == nil {
			p.followErrExp(pos, foundOp)
		}

		value = &BinaryArithm{
			OpPos: pos,
			Op:    foundOp,
			X:     value,
			Y:     y,
		}
	}
}

func (p *Parser) followArithm(ftok token, fpos Pos) ArithmExpr {
	x := p.arithmExpr(false)
	if x == nil {
		switch {
		case ftok == dblLeftParen && p.peekArithmEnd():
			return p.emptyWord(p.pos)
		case ftok == dollDblParen && p.peekArithmEnd():
			return p.emptyWord(p.pos)
		case ftok == leftBrack && p.tok == rightBrack:
			return p.emptyWord(p.pos)
		case ftok == colon && (p.tok == colon || p.tok == rightBrace):
			return p.emptyWord(p.pos)
		default:
			p.followErrExp(fpos, ftok)
		}
	}
	return x
}

func (p *Parser) peekArithmEnd() bool {
	return p.tok == rightParen && p.r == ')'
}

func (p *Parser) arithmMatchingErr(pos Pos, left, right token) {
	switch p.tok {
	case _Lit, _LitWord:
		p.curErr("not a valid arithmetic operator: %#q", p.val)
	case leftBrack:
		p.curErr("%#q must follow a name", leftBrack)
	case colon:
		p.curErr("ternary operator missing %#q before %#q", quest, colon)
	case rightParen, _EOF:
		p.matchingErr(pos, left, right)
	case period:
		p.checkLang(p.pos, LangZsh, FeatureArithmeticFloatingPoint)
	default:
		if p.quote&allArithmExpr != 0 {
			p.curErr("not a valid arithmetic operator: %#q", p.tok)
		}
		p.matchingErr(pos, left, right)
	}
}

func (p *Parser) matchedArithm(lpos Pos, left, right token) {
	if !p.got(right) {
		p.arithmMatchingErr(lpos, left, right)
	}
}

func (p *Parser) arithmTailToEnd() (Pos, bool) {
	for !p.peekArithmEnd() {
		if p.tok == _EOF {
			return Pos{}, false
		}
		start := p.cursorSnapshot()
		p.nextArith(false)
		if !start.progressed(p) {
			p.posRecoverableErr(start.pos, "internal parser error: no progress scanning arithmetic tail")
			return Pos{}, false
		}
	}
	return p.pos, true
}

func (p *Parser) arithmTailToPos(end Pos) (Pos, bool) {
	for end.After(p.pos) {
		if p.tok == _EOF {
			return Pos{}, false
		}
		start := p.cursorSnapshot()
		p.nextArith(false)
		if !start.progressed(p) {
			p.posRecoverableErr(start.pos, "internal parser error: no progress scanning arithmetic tail")
			return Pos{}, false
		}
	}
	if p.pos != end {
		return Pos{}, false
	}
	return p.pos, true
}

func (p *Parser) arithmEndExpr(expr *ArithmExpr, ltok token, lpos Pos, old saveState, salvageTail bool, tailEnd Pos) Pos {
	pos := p.pos
	if !p.peekArithmEnd() {
		if p.recoverError() {
			pos = recoveredPos
		} else {
			if salvageTail && expr != nil && *expr != nil {
				tailStart := p.pos
				if exprEnd := arithmExprEnd(*expr); exprEnd.IsValid() && !exprEnd.After(tailStart) {
					tailStart = exprEnd
				}
				if tailEnd.IsValid() {
					if end, ok := p.arithmTailToPos(tailEnd); ok {
						*expr = p.appendArithmSuffix(*expr, tailStart, end, p.sourceRange(tailStart, end))
					} else {
						p.arithmMatchingErr(lpos, ltok, dblRightParen)
					}
				} else if end, ok := p.arithmTailToEnd(); ok {
					*expr = p.appendArithmSuffix(*expr, tailStart, end, p.sourceRange(tailStart, end))
				} else {
					p.arithmMatchingErr(lpos, ltok, dblRightParen)
				}
			} else {
				p.arithmMatchingErr(lpos, ltok, dblRightParen)
			}
		}
	}
	p.rune()
	p.postNested(old)
	if pos != recoveredPos {
		pos = p.pos
	}
	p.next()
	return pos
}

func (p *Parser) arithmEnd(ltok token, lpos Pos, old saveState) Pos {
	pos := p.pos
	if !p.peekArithmEnd() {
		if p.recoverError() {
			pos = recoveredPos
		} else {
			p.arithmMatchingErr(lpos, ltok, dblRightParen)
		}
	}
	p.rune()
	p.postNested(old)
	if pos != recoveredPos {
		pos = p.pos
	}
	p.next()
	return pos
}
