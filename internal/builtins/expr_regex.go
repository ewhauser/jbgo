package builtins

import (
	"math/big"
	"strconv"
	"unicode"
	"unicode/utf8"
)

type exprRegexNodeKind uint8

const (
	exprRegexLiteral exprRegexNodeKind = iota
	exprRegexAny
	exprRegexNodeCharClass
	exprRegexBegin
	exprRegexEnd
	exprRegexGroup
	exprRegexBackref
	exprRegexRepeat
)

type exprRegexExpr struct {
	alternatives []exprRegexSeq
}

type exprRegexSeq []*exprRegexNode

type exprRegexNode struct {
	kind    exprRegexNodeKind
	literal string
	class   exprRegexCharClass
	group   int
	expr    *exprRegexExpr
	child   *exprRegexNode
	min     int
	max     int
}

type exprRegexCharClass struct {
	negated  bool
	elements []exprRegexCharClassElem
}

type exprRegexCharClassElem struct {
	literal    string
	posixClass string
	low        rune
	high       rune
	isRange    bool
}

type exprRegexUnit struct {
	text  string
	ch    rune
	start int
	end   int
}

const (
	exprRegexPOSIXClassInvalid   = "__invalid__"
	exprRegexPOSIXClassUnmatched = "__unmatched__"
)

type exprRegexParser struct {
	units      []exprRegexUnit
	pos        int
	nextGroup  int
	openGroups []int
}

type exprRegexCapture struct {
	set   bool
	start int
	end   int
}

type exprRegexCaptures [10]exprRegexCapture

type exprRegexState struct {
	pos      int
	captures *exprRegexCaptures
}

type exprRegexMatcher struct {
	input []exprRegexUnit
	steps int
}

const exprRegexMaxSteps = 200000

func exprRegexMatch(text, pattern string, locale builtinLocaleContext) (exprValue, error) {
	root, captureCount, err := exprParseBRE(pattern, locale.byteLocale)
	if err != nil {
		return exprValue{}, err
	}

	if locale.byteLocale {
		return exprRunRegex(root, captureCount, exprRegexUnits(text, true), text, false)
	}

	units := exprRegexUnits(text, false)
	if utf8.ValidString(text) {
		return exprRunRegex(root, captureCount, units, text, false)
	}

	return exprRunRegex(root, captureCount, exprRegexUnits(text, true), text, true)
}

func exprRunRegex(root *exprRegexExpr, captureCount int, units []exprRegexUnit, original string, invalidUTF8Fallback bool) (exprValue, error) {
	matcher := exprRegexMatcher{
		input: units,
		steps: exprRegexMaxSteps,
	}
	states, err := matcher.matchExpr(root, exprRegexState{})
	if err != nil {
		return exprValue{}, err
	}
	if len(states) == 0 {
		if captureCount > 0 {
			return newExprString(""), nil
		}
		return exprZeroValue(), nil
	}

	best := states[0]
	for i := 1; i < len(states); i++ {
		if states[i].pos > best.pos {
			best = states[i]
		}
	}

	if captureCount == 0 {
		if invalidUTF8Fallback {
			return exprZeroValue(), nil
		}
		return newExprInt(strconv.Itoa(best.pos)), nil
	}

	capture := best.capture(1)
	if !capture.set {
		return newExprString(""), nil
	}
	result := exprUnitsSlice(original, units, capture.start, capture.end)
	if invalidUTF8Fallback && !utf8.ValidString(result) {
		return newExprString(""), nil
	}
	return newExprString(result), nil
}

func exprParseBRE(pattern string, byteMode bool) (*exprRegexExpr, int, error) {
	parser := exprRegexParser{
		units: exprRegexUnits(pattern, byteMode),
	}
	root, err := parser.parseExpression(false)
	if err != nil {
		return nil, 0, err
	}
	if parser.hasMore() {
		if parser.peekEscaped(')') {
			return nil, 0, exprUnmatchedClosingParenError()
		}
		return nil, 0, exprInvalidRegexExpressionError()
	}
	return root, parser.nextGroup, nil
}

func (p *exprRegexParser) parseExpression(inGroup bool) (*exprRegexExpr, error) {
	alternatives := make([]exprRegexSeq, 0, 1)
	for {
		sequence, err := p.parseSequence(inGroup)
		if err != nil {
			return nil, err
		}
		alternatives = append(alternatives, sequence)
		if !p.acceptEscaped('|') {
			break
		}
	}
	return &exprRegexExpr{alternatives: alternatives}, nil
}

func (p *exprRegexParser) parseSequence(inGroup bool) (exprRegexSeq, error) {
	sequence := make(exprRegexSeq, 0, 8)
	atExprStart := true
	for p.hasMore() {
		if p.peekEscaped('|') {
			break
		}
		if p.peekEscaped(')') {
			if inGroup {
				break
			}
			return nil, exprUnmatchedClosingParenError()
		}

		node, err := p.parseAtom(atExprStart, inGroup)
		if err != nil {
			return nil, err
		}
		node, err = p.parseQuantifiers(node)
		if err != nil {
			return nil, err
		}
		sequence = append(sequence, node)
		atExprStart = false
	}
	return sequence, nil
}

func (p *exprRegexParser) parseAtom(atExprStart, inGroup bool) (*exprRegexNode, error) {
	unit := p.current()
	if unit.isByte('\\') {
		if p.pos+1 >= len(p.units) {
			return nil, exprTrailingBackslashError()
		}

		next := p.units[p.pos+1]
		switch {
		case next.isByte('('):
			p.pos += 2
			p.nextGroup++
			group := p.nextGroup
			p.openGroups = append(p.openGroups, group)
			expr, err := p.parseExpression(true)
			p.openGroups = p.openGroups[:len(p.openGroups)-1]
			if err != nil {
				return nil, err
			}
			if !p.acceptEscaped(')') {
				return nil, exprUnmatchedOpeningParenError()
			}
			return &exprRegexNode{
				kind:  exprRegexGroup,
				group: group,
				expr:  expr,
			}, nil
		case next.isByte(')'):
			if inGroup {
				return nil, exprInvalidRegexExpressionError()
			}
			return nil, exprUnmatchedClosingParenError()
		case next.isDigit():
			group := int(next.ch - '0')
			if group == 0 || group > p.nextGroup || p.groupIsOpen(group) {
				return nil, exprInvalidBackReferenceError()
			}
			p.pos += 2
			return &exprRegexNode{
				kind:  exprRegexBackref,
				group: group,
			}, nil
		default:
			p.pos += 2
			return &exprRegexNode{
				kind:    exprRegexLiteral,
				literal: next.text,
			}, nil
		}
	}

	switch {
	case unit.isByte('['):
		return p.parseCharClass()
	case unit.isByte('.'):
		p.pos++
		return &exprRegexNode{kind: exprRegexAny}, nil
	case unit.isByte('^') && atExprStart:
		p.pos++
		return &exprRegexNode{kind: exprRegexBegin}, nil
	case unit.isByte('$') && p.atEndOfExpression():
		p.pos++
		return &exprRegexNode{kind: exprRegexEnd}, nil
	default:
		p.pos++
		return &exprRegexNode{
			kind:    exprRegexLiteral,
			literal: unit.text,
		}, nil
	}
}

func (p *exprRegexParser) parseQuantifiers(node *exprRegexNode) (*exprRegexNode, error) {
	for p.hasMore() {
		if !exprRegexNodeCanRepeat(node) {
			return node, nil
		}
		switch {
		case p.current().isByte('*'):
			p.pos++
			node = &exprRegexNode{
				kind:  exprRegexRepeat,
				child: node,
				min:   0,
				max:   -1,
			}
		case p.peekEscaped('+'):
			p.pos += 2
			node = &exprRegexNode{
				kind:  exprRegexRepeat,
				child: node,
				min:   1,
				max:   -1,
			}
		case p.peekEscaped('?'):
			p.pos += 2
			node = &exprRegexNode{
				kind:  exprRegexRepeat,
				child: node,
				min:   0,
				max:   1,
			}
		case p.peekEscaped('{'):
			minCount, maxCount, consumed, err := p.parseBoundedRepeat()
			if err != nil {
				return nil, err
			}
			p.pos += consumed
			node = &exprRegexNode{
				kind:  exprRegexRepeat,
				child: node,
				min:   minCount,
				max:   maxCount,
			}
		default:
			return node, nil
		}
	}
	return node, nil
}

func exprRegexNodeCanRepeat(node *exprRegexNode) bool {
	if node == nil {
		return false
	}
	switch node.kind {
	case exprRegexBegin, exprRegexEnd:
		return false
	default:
		return true
	}
}

func (p *exprRegexParser) parseBoundedRepeat() (int, int, int, error) {
	start := p.pos
	pos := p.pos + 2
	contentUnits := make([]exprRegexUnit, 0, 8)
	for pos < len(p.units) {
		if pos+1 < len(p.units) && p.units[pos].isByte('\\') && p.units[pos+1].isByte('}') {
			content, err := exprBoundedRepeat(contentUnits)
			if err != nil {
				return 0, 0, 0, err
			}
			return content.min, content.max, pos + 2 - start, nil
		}
		contentUnits = append(contentUnits, p.units[pos])
		pos++
	}
	return 0, 0, 0, exprUnmatchedOpeningBraceError()
}

type exprRepeatBounds struct {
	min int
	max int
}

func exprBoundedRepeat(units []exprRegexUnit) (exprRepeatBounds, error) {
	content := make([]rune, 0, len(units))
	for _, unit := range units {
		if !unit.isASCII() {
			return exprRepeatBounds{}, exprInvalidBraceContentError()
		}
		content = append(content, unit.ch)
	}
	text := string(content)
	comma := -1
	for i, r := range content {
		if r == ',' {
			if comma >= 0 {
				return exprRepeatBounds{}, exprInvalidBraceContentError()
			}
			comma = i
			continue
		}
		if r < '0' || r > '9' {
			return exprRepeatBounds{}, exprInvalidBraceContentError()
		}
	}

	if comma < 0 {
		if text == "" {
			return exprRepeatBounds{}, exprInvalidBraceContentError()
		}
		value, err := exprRepeatBoundValue(text)
		if err != nil {
			return exprRepeatBounds{}, err
		}
		return exprRepeatBounds{min: value, max: value}, nil
	}

	lowerText := text[:comma]
	upperText := text[comma+1:]
	lower := 0
	if lowerText != "" {
		value, err := exprRepeatBoundValue(lowerText)
		if err != nil {
			return exprRepeatBounds{}, err
		}
		lower = value
	}

	upper := -1
	if upperText != "" {
		value, err := exprRepeatBoundValue(upperText)
		if err != nil {
			return exprRepeatBounds{}, err
		}
		upper = value
	}
	if upper >= 0 && lower > upper {
		return exprRepeatBounds{}, exprInvalidBraceContentError()
	}
	return exprRepeatBounds{min: lower, max: upper}, nil
}

func exprRepeatBoundValue(text string) (int, error) {
	if text == "" {
		return 0, nil
	}
	value, ok := parseDecimalBigInt(text)
	if !ok {
		return 0, exprInvalidBraceContentError()
	}
	if value.Cmp(big.NewInt(32767)) > 0 {
		return 0, exprRegexTooBigError()
	}
	return int(value.Int64()), nil
}

func (p *exprRegexParser) parseCharClass() (*exprRegexNode, error) {
	p.pos++
	class := exprRegexCharClass{}
	first := true
	if p.hasMore() && p.current().isByte('^') {
		class.negated = true
		p.pos++
	}

	for p.hasMore() {
		if p.current().isByte(']') && !first {
			p.pos++
			return &exprRegexNode{
				kind:  exprRegexNodeCharClass,
				class: class,
			}, nil
		}

		element, err := p.parseCharClassElement()
		if err != nil {
			return nil, err
		}
		class.elements = append(class.elements, element...)
		first = false
	}

	return nil, exprInvalidRegexExpressionError()
}

func (p *exprRegexParser) parseCharClassElement() ([]exprRegexCharClassElem, error) {
	first, err := p.parseClassAtom()
	if err != nil {
		return nil, err
	}
	if !p.hasMore() || !p.current().isByte('-') {
		return []exprRegexCharClassElem{first}, nil
	}
	if p.pos+1 >= len(p.units) || p.units[p.pos+1].isByte(']') {
		p.pos++
		return []exprRegexCharClassElem{
			first,
			{literal: "-"},
		}, nil
	}

	p.pos++
	second, err := p.parseClassAtom()
	if err != nil {
		return nil, err
	}
	if first.isRange || second.isRange || first.literal == "" || second.literal == "" {
		return []exprRegexCharClassElem{first, {literal: "-"}, second}, nil
	}
	if utf8.RuneCountInString(first.literal) != 1 || utf8.RuneCountInString(second.literal) != 1 {
		return []exprRegexCharClassElem{first, {literal: "-"}, second}, nil
	}
	low, _ := utf8.DecodeRuneInString(first.literal)
	high, _ := utf8.DecodeRuneInString(second.literal)
	return []exprRegexCharClassElem{{
		low:     low,
		high:    high,
		isRange: true,
	}}, nil
}

func (p *exprRegexParser) parseClassAtom() (exprRegexCharClassElem, error) {
	if !p.hasMore() {
		return exprRegexCharClassElem{}, exprInvalidRegexExpressionError()
	}
	if element, ok := p.tryParsePOSIXClass(); ok {
		switch element.posixClass {
		case exprRegexPOSIXClassInvalid:
			return exprRegexCharClassElem{}, exprInvalidCharacterClassNameError()
		case exprRegexPOSIXClassUnmatched:
			return exprRegexCharClassElem{}, exprUnmatchedBracketExpressionError()
		}
		return element, nil
	}
	current := p.current()
	if current.isByte('\\') && p.pos+1 < len(p.units) {
		p.pos += 2
		return exprRegexCharClassElem{literal: p.units[p.pos-1].text}, nil
	}
	p.pos++
	return exprRegexCharClassElem{literal: current.text}, nil
}

func (p *exprRegexParser) tryParsePOSIXClass() (exprRegexCharClassElem, bool) {
	if !p.current().isByte('[') || p.pos+1 >= len(p.units) || !p.units[p.pos+1].isByte(':') {
		return exprRegexCharClassElem{}, false
	}

	name := make([]byte, 0, 8)
	invalidName := false
	for i := p.pos + 2; i < len(p.units); i++ {
		if i+1 < len(p.units) && p.units[i].isByte(':') && p.units[i+1].isByte(']') {
			if len(name) == 0 {
				return exprRegexCharClassElem{posixClass: exprRegexPOSIXClassUnmatched}, true
			}
			className := string(name)
			if invalidName || !exprRegexIsPOSIXClass(className) {
				return exprRegexCharClassElem{posixClass: exprRegexPOSIXClassInvalid}, true
			}
			p.pos = i + 2
			return exprRegexCharClassElem{posixClass: className}, true
		}
		if !p.units[i].isASCIIAlpha() {
			invalidName = true
			continue
		}
		if !invalidName {
			name = append(name, p.units[i].text[0])
		}
	}

	return exprRegexCharClassElem{posixClass: exprRegexPOSIXClassUnmatched}, true
}

func (m *exprRegexMatcher) matchExpr(expr *exprRegexExpr, state exprRegexState) ([]exprRegexState, error) {
	if err := m.step(); err != nil {
		return nil, err
	}

	results := make([]exprRegexState, 0, 4)
	for i := range expr.alternatives {
		states, err := m.matchSeq(expr.alternatives[i], state)
		if err != nil {
			return nil, err
		}
		results = append(results, states...)
	}
	return results, nil
}

func (m *exprRegexMatcher) matchSeq(seq exprRegexSeq, state exprRegexState) ([]exprRegexState, error) {
	states := []exprRegexState{state}
	for _, node := range seq {
		nextStates := make([]exprRegexState, 0, len(states))
		for i := range states {
			matched, err := m.matchNode(node, states[i])
			if err != nil {
				return nil, err
			}
			nextStates = append(nextStates, matched...)
		}
		if len(nextStates) == 0 {
			return nil, nil
		}
		states = nextStates
	}
	return states, nil
}

func (m *exprRegexMatcher) matchNode(node *exprRegexNode, state exprRegexState) ([]exprRegexState, error) {
	if err := m.step(); err != nil {
		return nil, err
	}

	switch node.kind {
	case exprRegexLiteral:
		if state.pos >= len(m.input) || m.input[state.pos].text != node.literal {
			return nil, nil
		}
		state.pos++
		return []exprRegexState{state}, nil
	case exprRegexAny:
		if state.pos >= len(m.input) {
			return nil, nil
		}
		state.pos++
		return []exprRegexState{state}, nil
	case exprRegexNodeCharClass:
		if state.pos >= len(m.input) || !node.class.matches(m.input[state.pos]) {
			return nil, nil
		}
		state.pos++
		return []exprRegexState{state}, nil
	case exprRegexBegin:
		if state.pos != 0 {
			return nil, nil
		}
		return []exprRegexState{state}, nil
	case exprRegexEnd:
		if state.pos != len(m.input) {
			return nil, nil
		}
		return []exprRegexState{state}, nil
	case exprRegexBackref:
		capture := state.capture(node.group)
		if !capture.set {
			return nil, nil
		}
		length := capture.end - capture.start
		if state.pos+length > len(m.input) {
			return nil, nil
		}
		for i := range length {
			if m.input[capture.start+i].text != m.input[state.pos+i].text {
				return nil, nil
			}
		}
		state.pos += length
		return []exprRegexState{state}, nil
	case exprRegexGroup:
		start := state.pos
		states, err := m.matchExpr(node.expr, state)
		if err != nil {
			return nil, err
		}
		results := make([]exprRegexState, 0, len(states))
		for i := range states {
			matched := states[i]
			matched = matched.withCapture(node.group, exprRegexCapture{
				set:   true,
				start: start,
				end:   matched.pos,
			})
			results = append(results, matched)
		}
		return results, nil
	case exprRegexRepeat:
		return m.matchRepeat(node, state, 0)
	default:
		return nil, exprInvalidRegexExpressionError()
	}
}

func (m *exprRegexMatcher) matchRepeat(node *exprRegexNode, state exprRegexState, count int) ([]exprRegexState, error) {
	if err := m.step(); err != nil {
		return nil, err
	}

	results := make([]exprRegexState, 0, 4)
	if node.max < 0 || count < node.max {
		states, err := m.matchNode(node.child, state)
		if err != nil {
			return nil, err
		}
		for i := range states {
			matched := states[i]
			if matched.pos == state.pos {
				if count+1 >= node.min {
					results = append(results, matched)
				}
				continue
			}
			next, err := m.matchRepeat(node, matched, count+1)
			if err != nil {
				return nil, err
			}
			results = append(results, next...)
		}
	}
	if count >= node.min {
		results = append(results, state)
	}
	return results, nil
}

func (m *exprRegexMatcher) step() error {
	m.steps--
	if m.steps < 0 {
		return exprInvalidRegexExpressionError()
	}
	return nil
}

func (s exprRegexState) capture(group int) exprRegexCapture {
	if s.captures == nil || group < 0 || group >= len(*s.captures) {
		return exprRegexCapture{}
	}
	return (*s.captures)[group]
}

func (s exprRegexState) withCapture(group int, capture exprRegexCapture) exprRegexState {
	if group < 0 || group >= len(exprRegexCaptures{}) {
		return s
	}

	next := s
	cloned := exprRegexCaptures{}
	if s.captures != nil {
		cloned = *s.captures
	}
	cloned[group] = capture
	next.captures = &cloned
	return next
}

func (c exprRegexCharClass) matches(unit exprRegexUnit) bool {
	matched := false
	for _, element := range c.elements {
		switch {
		case element.isRange:
			matched = unit.ch >= element.low && unit.ch <= element.high
		case element.posixClass != "":
			matched = exprRegexMatchesPOSIXClass(element.posixClass, unit)
		default:
			matched = unit.text == element.literal
		}
		if matched {
			break
		}
	}
	if c.negated {
		return !matched
	}
	return matched
}

func exprRegexMatchesPOSIXClass(name string, unit exprRegexUnit) bool {
	if name == "digit" {
		if len(unit.text) != 1 {
			return false
		}
		b := unit.text[0]
		return '0' <= b && b <= '9'
	}

	if len(unit.text) == 1 {
		b := unit.text[0]
		if b < utf8.RuneSelf {
			return exprRegexMatchesASCIIPOSIXClass(name, b)
		}
		return false
	}

	switch name {
	case "alnum":
		return unicode.IsLetter(unit.ch) || unicode.IsDigit(unit.ch)
	case "alpha":
		return unicode.IsLetter(unit.ch)
	case "blank":
		return unit.ch == ' ' || unit.ch == '\t'
	case "cntrl":
		return unicode.IsControl(unit.ch)
	case "digit":
		return unicode.IsDigit(unit.ch)
	case "graph":
		return unicode.IsGraphic(unit.ch) && !unicode.IsSpace(unit.ch)
	case "lower":
		return unicode.IsLower(unit.ch)
	case "print":
		return unicode.IsPrint(unit.ch)
	case "punct":
		return unicode.IsPunct(unit.ch)
	case "space":
		return unicode.IsSpace(unit.ch)
	case "upper":
		return unicode.IsUpper(unit.ch)
	case "xdigit":
		return ('0' <= unit.ch && unit.ch <= '9') || ('a' <= unit.ch && unit.ch <= 'f') || ('A' <= unit.ch && unit.ch <= 'F')
	default:
		return false
	}
}

func exprRegexMatchesASCIIPOSIXClass(name string, b byte) bool {
	switch name {
	case "alnum":
		return ('0' <= b && b <= '9') || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
	case "alpha":
		return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
	case "blank":
		return b == ' ' || b == '\t'
	case "cntrl":
		return b < 0x20 || b == 0x7f
	case "digit":
		return '0' <= b && b <= '9'
	case "graph":
		return 0x21 <= b && b <= 0x7e
	case "lower":
		return 'a' <= b && b <= 'z'
	case "print":
		return 0x20 <= b && b <= 0x7e
	case "punct":
		return (0x21 <= b && b <= 0x2f) || (0x3a <= b && b <= 0x40) || (0x5b <= b && b <= 0x60) || (0x7b <= b && b <= 0x7e)
	case "space":
		return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
	case "upper":
		return 'A' <= b && b <= 'Z'
	case "xdigit":
		return ('0' <= b && b <= '9') || ('a' <= b && b <= 'f') || ('A' <= b && b <= 'F')
	default:
		return false
	}
}

func exprRegexIsPOSIXClass(name string) bool {
	switch name {
	case "alnum", "alpha", "blank", "cntrl", "digit", "graph", "lower", "print", "punct", "space", "upper", "xdigit":
		return true
	default:
		return false
	}
}

func exprRegexUnits(text string, byteMode bool) []exprRegexUnit {
	raw := []byte(text)
	units := make([]exprRegexUnit, 0, len(raw))
	for i := 0; i < len(raw); {
		if byteMode {
			units = append(units, exprRegexUnit{
				text:  string(raw[i : i+1]),
				ch:    rune(raw[i]),
				start: i,
				end:   i + 1,
			})
			i++
			continue
		}

		r, size := utf8.DecodeRune(raw[i:])
		if r == utf8.RuneError && size == 1 {
			units = append(units, exprRegexUnit{
				text:  string(raw[i : i+1]),
				ch:    rune(raw[i]),
				start: i,
				end:   i + 1,
			})
			i++
			continue
		}
		units = append(units, exprRegexUnit{
			text:  string(raw[i : i+size]),
			ch:    r,
			start: i,
			end:   i + size,
		})
		i += size
	}
	return units
}

func exprUnitsSlice(original string, units []exprRegexUnit, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start >= len(units) {
		return ""
	}
	if end > len(units) {
		end = len(units)
	}
	if start == end {
		return ""
	}
	return original[units[start].start:units[end-1].end]
}

func (p *exprRegexParser) hasMore() bool {
	return p.pos < len(p.units)
}

func (p *exprRegexParser) current() exprRegexUnit {
	return p.units[p.pos]
}

func (p *exprRegexParser) peekEscaped(ch byte) bool {
	return p.pos+1 < len(p.units) && p.units[p.pos].isByte('\\') && p.units[p.pos+1].isByte(ch)
}

func (p *exprRegexParser) acceptEscaped(ch byte) bool {
	if !p.peekEscaped(ch) {
		return false
	}
	p.pos += 2
	return true
}

func (p *exprRegexParser) atEndOfExpression() bool {
	if p.pos+1 >= len(p.units) {
		return true
	}
	pos := p.pos + 1
	return pos+1 < len(p.units) && p.units[pos].isByte('\\') && (p.units[pos+1].isByte(')') || p.units[pos+1].isByte('|'))
}

func (u exprRegexUnit) isByte(ch byte) bool {
	return len(u.text) == 1 && u.text[0] == ch
}

func (u exprRegexUnit) isASCII() bool {
	return len(u.text) == 1 && u.text[0] < utf8.RuneSelf
}

func (u exprRegexUnit) isDigit() bool {
	return u.isASCII() && u.text[0] >= '0' && u.text[0] <= '9'
}

func (u exprRegexUnit) isASCIIAlpha() bool {
	return u.isASCII() && ((u.text[0] >= 'a' && u.text[0] <= 'z') || (u.text[0] >= 'A' && u.text[0] <= 'Z'))
}

func (p *exprRegexParser) groupIsOpen(group int) bool {
	for i := len(p.openGroups) - 1; i >= 0; i-- {
		if p.openGroups[i] == group {
			return true
		}
	}
	return false
}
