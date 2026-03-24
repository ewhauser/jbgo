package builtins

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

type exprNodeKind uint8

const (
	exprNodeGroup exprNodeKind = iota
	exprNodeLeaf
	exprNodeBinary
	exprNodeLength
	exprNodeSubstr
)

type exprBinaryOp uint8

const (
	exprBinaryOr exprBinaryOp = iota
	exprBinaryAnd
	exprBinaryLess
	exprBinaryLessEqual
	exprBinaryEqual
	exprBinaryNotEqual
	exprBinaryGreaterEqual
	exprBinaryGreater
	exprBinaryAdd
	exprBinarySub
	exprBinaryMul
	exprBinaryDiv
	exprBinaryMod
	exprBinaryMatch
	exprBinaryIndex
)

type exprNode struct {
	kind  exprNodeKind
	value exprValue
	op    exprBinaryOp
	left  *exprNode
	right *exprNode
	third *exprNode
}

type exprParser struct {
	tokens []string
	pos    int
	locale builtinLocaleContext
}

type exprBinaryToken struct {
	token string
	op    exprBinaryOp
}

var exprPrecedence = [][]exprBinaryToken{
	{{token: "|", op: exprBinaryOr}},
	{{token: "&", op: exprBinaryAnd}},
	{
		{token: "<", op: exprBinaryLess},
		{token: "<=", op: exprBinaryLessEqual},
		{token: "=", op: exprBinaryEqual},
		{token: "!=", op: exprBinaryNotEqual},
		{token: ">=", op: exprBinaryGreaterEqual},
		{token: ">", op: exprBinaryGreater},
	},
	{
		{token: "+", op: exprBinaryAdd},
		{token: "-", op: exprBinarySub},
	},
	{
		{token: "*", op: exprBinaryMul},
		{token: "/", op: exprBinaryDiv},
		{token: "%", op: exprBinaryMod},
	},
	{{token: ":", op: exprBinaryMatch}},
}

func (p *exprParser) parse() (*exprNode, error) {
	root, err := p.parseExpression()
	if err != nil {
		return nil, err
	}
	if p.hasMore() {
		return nil, exprUnexpectedArgumentError(p.tokens[p.pos])
	}
	return root, nil
}

func (p *exprParser) parseExpression() (*exprNode, error) {
	return p.parsePrecedence(0)
}

func (p *exprParser) parsePrecedence(level int) (*exprNode, error) {
	if level >= len(exprPrecedence) {
		return p.parseSimpleExpression()
	}

	left, err := p.parsePrecedence(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		op, ok := p.parseBinaryOp(level)
		if !ok {
			return left, nil
		}
		right, err := p.parsePrecedence(level + 1)
		if err != nil {
			return nil, err
		}
		left = &exprNode{
			kind:  exprNodeBinary,
			op:    op,
			left:  left,
			right: right,
		}
	}
}

func (p *exprParser) parseBinaryOp(level int) (exprBinaryOp, bool) {
	if !p.hasMore() {
		return 0, false
	}
	current := p.tokens[p.pos]
	for _, candidate := range exprPrecedence[level] {
		if current == candidate.token {
			p.pos++
			return candidate.op, true
		}
	}
	return 0, false
}

func (p *exprParser) parseSimpleExpression() (*exprNode, error) {
	token, err := p.next()
	if err != nil {
		return nil, err
	}

	switch token {
	case "match":
		left, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		right, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		return &exprNode{
			kind:  exprNodeBinary,
			op:    exprBinaryMatch,
			left:  left,
			right: right,
		}, nil
	case "index":
		left, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		right, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		return &exprNode{
			kind:  exprNodeBinary,
			op:    exprBinaryIndex,
			left:  left,
			right: right,
		}, nil
	case "substr":
		text, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		pos, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		length, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		return &exprNode{
			kind:  exprNodeSubstr,
			left:  text,
			right: pos,
			third: length,
		}, nil
	case "length":
		text, err := p.parseSimpleExpression()
		if err != nil {
			return nil, err
		}
		return &exprNode{
			kind: exprNodeLength,
			left: text,
		}, nil
	case "+":
		next, err := p.next()
		if err != nil {
			return nil, err
		}
		return &exprNode{
			kind:  exprNodeLeaf,
			value: newExprString(next),
		}, nil
	case ")":
		return nil, exprUnexpectedClosingParenTokenError()
	case "(":
		group, err := p.parseExpression()
		if err != nil {
			return nil, err
		}
		next, err := p.next()
		if err != nil {
			if _, evalErr := exprEvaluateNode(group, p.locale); evalErr != nil {
				return nil, evalErr
			}
			return nil, exprExpectedClosingParenAfterError(p.tokens[p.pos-1])
		}
		if next != ")" {
			if _, evalErr := exprEvaluateNode(group, p.locale); evalErr != nil {
				return nil, evalErr
			}
			return nil, exprExpectedClosingParenInsteadOfError(next)
		}
		return &exprNode{
			kind: exprNodeGroup,
			left: group,
		}, nil
	default:
		return &exprNode{
			kind:  exprNodeLeaf,
			value: newExprString(token),
		}, nil
	}
}

func (p *exprParser) hasMore() bool {
	return p.pos < len(p.tokens)
}

func (p *exprParser) next() (string, error) {
	if p.pos >= len(p.tokens) {
		if len(p.tokens) == 0 {
			return "", exprMissingOperandError()
		}
		return "", exprMissingArgumentAfterError(p.tokens[p.pos-1])
	}
	token := p.tokens[p.pos]
	p.pos++
	return token, nil
}

type exprEvalFrame struct {
	node   *exprNode
	stage  uint8
	left   exprValue
	middle exprValue
}

type exprEvalResult struct {
	value exprValue
	err   error
}

func exprEvaluateNode(root *exprNode, locale builtinLocaleContext) (exprValue, error) {
	if root == nil {
		return exprZeroValue(), nil
	}

	frames := []exprEvalFrame{{node: root}}
	results := make([]exprEvalResult, 0, 8)

	for len(frames) > 0 {
		frameIndex := len(frames) - 1
		frame := &frames[frameIndex]

		switch frame.node.kind {
		case exprNodeLeaf:
			results = append(results, exprEvalResult{value: frame.node.value})
			frames = frames[:frameIndex]
		case exprNodeGroup:
			switch frame.stage {
			case 0:
				frame.stage = 1
				frames = append(frames, exprEvalFrame{node: frame.node.left})
			default:
				value := exprPopEvalResult(&results)
				frames = frames[:frameIndex]
				results = append(results, value)
			}
		case exprNodeBinary:
			switch frame.stage {
			case 0:
				frame.stage = 1
				frames = append(frames, exprEvalFrame{node: frame.node.left})
			case 1:
				left := exprPopEvalResult(&results)
				if left.err != nil {
					frames = frames[:frameIndex]
					results = append(results, left)
					continue
				}
				frame.left = left.value

				switch frame.node.op {
				case exprBinaryOr:
					if exprIsTruthy(frame.left) {
						frames = frames[:frameIndex]
						results = append(results, exprEvalResult{value: frame.left})
						continue
					}
				case exprBinaryAnd:
					if !exprIsTruthy(frame.left) {
						frames = frames[:frameIndex]
						results = append(results, exprEvalResult{value: exprZeroValue()})
						continue
					}
				}

				frame.stage = 2
				frames = append(frames, exprEvalFrame{node: frame.node.right})
			default:
				right := exprPopEvalResult(&results)
				frames = frames[:frameIndex]
				if right.err != nil {
					results = append(results, right)
					continue
				}
				value, err := exprApplyBinary(frame.node.op, frame.left, right.value, locale)
				results = append(results, exprEvalResult{value: value, err: err})
			}
		case exprNodeLength:
			switch frame.stage {
			case 0:
				frame.stage = 1
				frames = append(frames, exprEvalFrame{node: frame.node.left})
			default:
				value := exprPopEvalResult(&results)
				frames = frames[:frameIndex]
				if value.err != nil {
					results = append(results, value)
					continue
				}
				results = append(results, exprEvalResult{
					value: newExprInt(strconv.Itoa(exprLocaleLength(value.value.text, locale.byteLocale))),
				})
			}
		case exprNodeSubstr:
			switch frame.stage {
			case 0:
				frame.stage = 1
				frames = append(frames, exprEvalFrame{node: frame.node.left})
			case 1:
				text := exprPopEvalResult(&results)
				if text.err != nil {
					frames = frames[:frameIndex]
					results = append(results, text)
					continue
				}
				frame.left = text.value
				frame.stage = 2
				frames = append(frames, exprEvalFrame{node: frame.node.right})
			case 2:
				pos := exprPopEvalResult(&results)
				if pos.err != nil {
					frames = frames[:frameIndex]
					results = append(results, pos)
					continue
				}
				frame.middle = pos.value
				frame.stage = 3
				frames = append(frames, exprEvalFrame{node: frame.node.third})
			default:
				length := exprPopEvalResult(&results)
				frames = frames[:frameIndex]
				if length.err != nil {
					results = append(results, length)
					continue
				}
				results = append(results, exprEvalResult{
					value: newExprString(exprLocaleSubstr(frame.left.text, frame.middle.text, length.value.text, locale.byteLocale)),
				})
			}
		default:
			frames = frames[:frameIndex]
			results = append(results, exprEvalResult{err: exprSyntaxError()})
		}
	}

	result := exprPopEvalResult(&results)
	return result.value, result.err
}

func exprPopEvalResult(results *[]exprEvalResult) exprEvalResult {
	last := (*results)[len(*results)-1]
	*results = (*results)[:len(*results)-1]
	return last
}

func exprApplyBinary(op exprBinaryOp, left, right exprValue, locale builtinLocaleContext) (exprValue, error) {
	switch op {
	case exprBinaryOr:
		if exprIsTruthy(right) {
			return right, nil
		}
		return exprZeroValue(), nil
	case exprBinaryAnd:
		if exprIsTruthy(right) {
			return left, nil
		}
		return exprZeroValue(), nil
	case exprBinaryAdd, exprBinarySub, exprBinaryMul, exprBinaryDiv, exprBinaryMod:
		return exprApplyArithmetic(op, left, right)
	case exprBinaryMatch:
		return exprRegexMatch(left.text, right.text, locale)
	case exprBinaryIndex:
		return newExprInt(strconv.Itoa(exprLocaleIndex(left.text, right.text, locale.byteLocale))), nil
	default:
		cmp := exprCompareValues(left, right, locale)
		ok := false
		switch op {
		case exprBinaryLess:
			ok = cmp < 0
		case exprBinaryLessEqual:
			ok = cmp <= 0
		case exprBinaryEqual:
			ok = cmp == 0
		case exprBinaryNotEqual:
			ok = cmp != 0
		case exprBinaryGreaterEqual:
			ok = cmp >= 0
		case exprBinaryGreater:
			ok = cmp > 0
		default:
			return exprValue{}, exprSyntaxError()
		}
		if ok {
			return newExprInt("1"), nil
		}
		return exprZeroValue(), nil
	}
}

func exprApplyArithmetic(op exprBinaryOp, left, right exprValue) (exprValue, error) {
	leftInt, err := left.bigint()
	if err != nil {
		return exprValue{}, err
	}
	rightInt, err := right.bigint()
	if err != nil {
		return exprValue{}, err
	}

	switch op {
	case exprBinaryAdd:
		return newExprInt(new(big.Int).Add(leftInt, rightInt).String()), nil
	case exprBinarySub:
		return newExprInt(new(big.Int).Sub(leftInt, rightInt).String()), nil
	case exprBinaryMul:
		return newExprInt(new(big.Int).Mul(leftInt, rightInt).String()), nil
	case exprBinaryDiv:
		if rightInt.Sign() == 0 {
			return exprValue{}, exprDivisionByZeroError()
		}
		return newExprInt(new(big.Int).Quo(leftInt, rightInt).String()), nil
	case exprBinaryMod:
		if rightInt.Sign() == 0 {
			return exprValue{}, exprDivisionByZeroError()
		}
		return newExprInt(new(big.Int).Rem(leftInt, rightInt).String()), nil
	default:
		return exprValue{}, exprSyntaxError()
	}
}

func exprCompareValues(left, right exprValue, locale builtinLocaleContext) int {
	if leftInt, ok := parseDecimalBigInt(left.text); ok {
		if rightInt, ok := parseDecimalBigInt(right.text); ok {
			return leftInt.Cmp(rightInt)
		}
	}

	if locale.collator != nil && utf8.ValidString(left.text) && utf8.ValidString(right.text) {
		return locale.collator.CompareString(left.text, right.text)
	}
	return strings.Compare(left.text, right.text)
}

type exprUnitRange struct {
	start int
	end   int
}

func exprTextUnits(text string, byteLocale bool) []exprUnitRange {
	if byteLocale {
		units := make([]exprUnitRange, 0, len(text))
		for i := 0; i < len(text); i++ {
			units = append(units, exprUnitRange{start: i, end: i + 1})
		}
		return units
	}

	raw := []byte(text)
	units := make([]exprUnitRange, 0, len(raw))
	for i := 0; i < len(raw); {
		_, size := utf8.DecodeRune(raw[i:])
		if size <= 0 {
			size = 1
		}
		units = append(units, exprUnitRange{start: i, end: i + size})
		i += size
	}
	return units
}

func exprLocaleLength(text string, byteLocale bool) int {
	return len(exprTextUnits(text, byteLocale))
}

func exprLocaleIndex(text, chars string, byteLocale bool) int {
	if chars == "" {
		return 0
	}

	charSet := make(map[string]struct{})
	for _, unit := range exprTextUnits(chars, byteLocale) {
		charSet[chars[unit.start:unit.end]] = struct{}{}
	}
	for idx, unit := range exprTextUnits(text, byteLocale) {
		if _, ok := charSet[text[unit.start:unit.end]]; ok {
			return idx + 1
		}
	}
	return 0
}

func exprLocaleSubstr(text, posText, lengthText string, byteLocale bool) string {
	pos, ok := parseDecimalBigInt(posText)
	if !ok || pos.Sign() <= 0 {
		return ""
	}
	length, ok := parseDecimalBigInt(lengthText)
	if !ok || length.Sign() <= 0 {
		return ""
	}

	units := exprTextUnits(text, byteLocale)
	if len(units) == 0 {
		return ""
	}

	maxPos := big.NewInt(int64(len(units)))
	if pos.Cmp(maxPos) > 0 {
		return ""
	}

	startIndex := int(pos.Int64()) - 1
	if startIndex < 0 || startIndex >= len(units) {
		return ""
	}

	span := len(units) - startIndex
	if length.IsInt64() {
		requested := int(length.Int64())
		if requested < span {
			span = requested
		}
	}

	endIndex := startIndex + span - 1
	if endIndex >= len(units) {
		endIndex = len(units) - 1
	}
	return text[units[startIndex].start:units[endIndex].end]
}

type exprStaticError string

func (e exprStaticError) Error() string {
	return string(e)
}

func exprMissingOperandError() error {
	return exprStaticError("missing operand")
}

func exprSyntaxError() error {
	return exprStaticError("syntax error")
}

func exprUnexpectedArgumentError(token string) error {
	return fmt.Errorf("syntax error: unexpected argument %s", quoteGNUOperand(token))
}

func exprMissingArgumentAfterError(token string) error {
	return fmt.Errorf("syntax error: missing argument after %s", quoteGNUOperand(token))
}

func exprExpectedClosingParenAfterError(token string) error {
	return fmt.Errorf("syntax error: expecting ')' after %s", quoteGNUOperand(token))
}

func exprExpectedClosingParenInsteadOfError(token string) error {
	return fmt.Errorf("syntax error: expecting ')' instead of %s", quoteGNUOperand(token))
}

func exprUnexpectedClosingParenTokenError() error {
	return exprStaticError("syntax error: unexpected ')'")
}

func exprNonIntegerArgumentError() error {
	return exprStaticError("non-integer argument")
}

func exprDivisionByZeroError() error {
	return exprStaticError("division by zero")
}

func exprUnmatchedOpeningParenError() error {
	return exprStaticError("Unmatched ( or \\(")
}

func exprUnmatchedClosingParenError() error {
	return exprStaticError("Unmatched ) or \\)")
}

func exprUnmatchedOpeningBraceError() error {
	return exprStaticError("Unmatched \\{")
}

func exprInvalidBraceContentError() error {
	return exprStaticError("Invalid content of \\{\\}")
}

func exprTrailingBackslashError() error {
	return exprStaticError("Trailing backslash")
}

func exprInvalidBackReferenceError() error {
	return exprStaticError("Invalid back reference")
}

func exprUnmatchedBracketExpressionError() error {
	return exprStaticError("Unmatched [, [^, [:, [., or [=")
}

func exprInvalidCharacterClassNameError() error {
	return exprStaticError("Invalid character class name")
}

func exprRegexTooBigError() error {
	return exprStaticError("Regular expression too big")
}

func exprInvalidRegexExpressionError() error {
	return exprStaticError("Invalid regular expression")
}
