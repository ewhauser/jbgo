// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pattern

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Match reports whether name matches pat under the supplied shell pattern mode.
// It falls back to the recursive matcher for patterns or inputs containing
// invalid UTF-8 bytes, since Go's regexp engine operates on UTF-8 code points.
func Match(pat, name string, mode Mode) (bool, error) {
	if utf8.ValidString(pat) && utf8.ValidString(name) || mode&Filenames != 0 {
		matcher, err := ExtendedPatternMatcher(pat, mode)
		if err != nil {
			return false, err
		}
		return matcher(name), nil
	}
	root, err := parseExtPattern(pat, mode)
	if err != nil {
		return false, err
	}
	m := extMatcher{
		mode:       mode,
		memo:       make(map[extMemoKey][]int),
		repeatMemo: make(map[extMemoKey][]int),
		stack:      make(map[extMemoKey]struct{}),
	}
	for _, end := range m.ends(root, 0, name, 0) {
		if end == len(name) {
			return true, nil
		}
	}
	return false, nil
}

// ExtendedPatternMatcher returns a [regexp.Regexp.MatchString]-like function.
// It falls back to a recursive matcher when [Regexp] reports a negated extglob
// !(...) group, since Go's regexp package cannot express that directly.
func ExtendedPatternMatcher(pat string, mode Mode) (func(string) bool, error) {
	if mode&ExtendedOperators != 0 && mode&EntireString == 0 {
		panic("ExtendedOperators is only supported with EntireString")
	}

	expr, err := Regexp(pat, mode)
	if err == nil {
		rx := regexp.MustCompile(expr)
		return rx.MatchString, nil
	}
	var negErr *NegExtGlobError
	if !errors.As(err, &negErr) {
		return nil, err
	}
	root, err := parseExtPattern(pat, mode)
	if err != nil {
		return nil, err
	}
	return func(name string) bool {
		m := extMatcher{
			mode:       mode,
			memo:       make(map[extMemoKey][]int),
			repeatMemo: make(map[extMemoKey][]int),
			stack:      make(map[extMemoKey]struct{}),
		}
		for _, end := range m.ends(root, 0, name, 0) {
			if end == len(name) {
				return true
			}
		}
		return false
	}, nil
}

type extTokenKind uint8

const (
	extLiteral extTokenKind = iota
	extAnyOne
	extAnyMany
	extCharClass
	extGroup
)

type extSeq struct {
	id     int
	tokens []extToken
}

type extToken struct {
	kind  extTokenKind
	lit   string
	class *regexp.Regexp
	op    byte
	alts  []*extSeq
}

type extParser struct {
	pat    string
	mode   Mode
	nextID int
}

func parseExtPattern(pat string, mode Mode) (*extSeq, error) {
	p := &extParser{
		pat:    pat,
		mode:   mode,
		nextID: 1,
	}
	seq, i, err := p.parseSeq(0, "")
	if err != nil {
		return nil, err
	}
	if i != len(pat) {
		return nil, &SyntaxError{msg: "unexpected trailing pattern data"}
	}
	return seq, nil
}

func (p *extParser) parseSeq(i int, stops string) (*extSeq, int, error) {
	seq := &extSeq{id: p.nextID}
	p.nextID++
	var lit strings.Builder
	flushLit := func() {
		if lit.Len() == 0 {
			return
		}
		seq.tokens = append(seq.tokens, extToken{kind: extLiteral, lit: lit.String()})
		lit.Reset()
	}
	for i < len(p.pat) {
		c := p.pat[i]
		if strings.IndexByte(stops, c) >= 0 {
			break
		}
		if p.mode&ExtendedOperators != 0 && i+1 < len(p.pat) && p.pat[i+1] == '(' && strings.ContainsRune("!?*+@", rune(c)) {
			flushLit()
			tok, next, err := p.parseGroup(i)
			if err != nil {
				return nil, 0, err
			}
			seq.tokens = append(seq.tokens, tok)
			i = next
			continue
		}
		switch c {
		case '\\':
			if i+1 >= len(p.pat) {
				return nil, 0, &SyntaxError{msg: `\ at end of pattern`}
			}
			lit.WriteByte(p.pat[i+1])
			i += 2
		case '*':
			flushLit()
			if i+1 < len(p.pat) && p.pat[i+1] == '*' {
				i++
			}
			seq.tokens = append(seq.tokens, extToken{kind: extAnyMany})
			i++
		case '?':
			flushLit()
			seq.tokens = append(seq.tokens, extToken{kind: extAnyOne})
			i++
		case '[':
			flushLit()
			raw, next, err := scanCharClassToken(p.pat, i)
			if err != nil {
				return nil, 0, err
			}
			expr := "^" + raw + "$"
			if p.mode&NoGlobCase != 0 {
				expr = "(?i)" + expr
			}
			class, err := regexp.Compile(expr)
			if err != nil {
				return nil, 0, err
			}
			seq.tokens = append(seq.tokens, extToken{kind: extCharClass, class: class})
			i = next
		default:
			_, size := utf8.DecodeRuneInString(p.pat[i:])
			if size <= 0 {
				size = 1
			}
			lit.WriteString(p.pat[i : i+size])
			i += size
		}
	}
	flushLit()
	return seq, i, nil
}

func (p *extParser) parseGroup(i int) (extToken, int, error) {
	op := p.pat[i]
	i += 2 // skip op + '('
	var alts []*extSeq
	for {
		alt, next, err := p.parseSeq(i, "|)")
		if err != nil {
			return extToken{}, 0, err
		}
		alts = append(alts, alt)
		if next >= len(p.pat) {
			return extToken{}, 0, &SyntaxError{msg: "unterminated extglob"}
		}
		switch p.pat[next] {
		case '|':
			i = next + 1
		case ')':
			return extToken{kind: extGroup, op: op, alts: alts}, next + 1, nil
		default:
			return extToken{}, 0, &SyntaxError{msg: "unterminated extglob"}
		}
	}
}

func scanCharClassToken(pat string, i int) (string, int, error) {
	if i >= len(pat) || pat[i] != '[' {
		return "", 0, &SyntaxError{msg: "expected ["}
	}
	j := i + 1
	if j >= len(pat) {
		return "", 0, &SyntaxError{msg: "[ was not matched with a closing ]"}
	}
	if pat[j] == '!' || pat[j] == '^' {
		j++
	}
	if j < len(pat) && pat[j] == ']' {
		j++
	}
	for j < len(pat) {
		switch pat[j] {
		case '\\':
			j += 2
			continue
		case '[':
			if name, consumed, err := charClass(pat[j+1:]); err != nil {
				return "", 0, err
			} else if name != "" {
				j += 1 + consumed
				continue
			}
		case ']':
			return pat[i : j+1], j + 1, nil
		}
		j++
	}
	return "", 0, &SyntaxError{msg: "[ was not matched with a closing ]"}
}

type extMemoKey struct {
	seqID int
	ti    int
	ni    int
}

type extMatcher struct {
	mode       Mode
	memo       map[extMemoKey][]int
	repeatMemo map[extMemoKey][]int
	stack      map[extMemoKey]struct{}
}

func (m *extMatcher) ends(seq *extSeq, ti int, name string, ni int) []int {
	key := extMemoKey{seqID: seq.id, ti: ti, ni: ni}
	if cached, ok := m.memo[key]; ok {
		return cached
	}
	if _, ok := m.stack[key]; ok {
		return nil
	}
	m.stack[key] = struct{}{}
	defer delete(m.stack, key)

	var out []int
	if ti >= len(seq.tokens) {
		out = append(out, ni)
		m.memo[key] = out
		return out
	}

	tok := seq.tokens[ti]
	switch tok.kind {
	case extLiteral:
		if end, ok := m.matchLiteral(tok.lit, name, ni); ok {
			out = append(out, m.ends(seq, ti+1, name, end)...)
		}
	case extAnyOne:
		if end, ok := m.matchSingle(name, ni, true); ok {
			out = append(out, m.ends(seq, ti+1, name, end)...)
		}
	case extAnyMany:
		for _, end := range m.candidateEnds(name, ni, true) {
			out = append(out, m.ends(seq, ti+1, name, end)...)
		}
	case extCharClass:
		if end, ok := m.matchCharClass(tok.class, name, ni); ok {
			out = append(out, m.ends(seq, ti+1, name, end)...)
		}
	case extGroup:
		switch tok.op {
		case '@':
			out = append(out, m.groupOnce(tok, seq, ti, name, ni)...)
		case '?':
			out = append(out, m.ends(seq, ti+1, name, ni)...)
			out = append(out, m.groupOnce(tok, seq, ti, name, ni)...)
		case '*':
			out = append(out, m.ends(seq, ti+1, name, ni)...)
			out = append(out, m.groupRepeat(tok, seq, ti, name, ni)...)
		case '+':
			out = append(out, m.groupRepeat(tok, seq, ti, name, ni)...)
		case '!':
			for _, end := range m.candidateEnds(name, ni, true) {
				if m.groupAltMatchesExactly(tok, name, ni, end) {
					continue
				}
				out = append(out, m.ends(seq, ti+1, name, end)...)
			}
		}
	}

	out = uniqSortedInts(out)
	m.memo[key] = out
	return out
}

func (m *extMatcher) groupOnce(tok extToken, seq *extSeq, ti int, name string, ni int) []int {
	var out []int
	for _, alt := range tok.alts {
		for _, end := range m.ends(alt, 0, name, ni) {
			out = append(out, m.ends(seq, ti+1, name, end)...)
		}
	}
	return out
}

func (m *extMatcher) groupRepeat(tok extToken, seq *extSeq, ti int, name string, ni int) []int {
	key := extMemoKey{seqID: seq.id, ti: ti, ni: ni}
	if cached, ok := m.repeatMemo[key]; ok {
		return cached
	}
	var out []int
	for _, alt := range tok.alts {
		for _, end := range m.ends(alt, 0, name, ni) {
			if end == ni {
				continue
			}
			out = append(out, m.ends(seq, ti+1, name, end)...)
			out = append(out, m.groupRepeat(tok, seq, ti, name, end)...)
		}
	}
	out = uniqSortedInts(out)
	m.repeatMemo[key] = out
	return out
}

func (m *extMatcher) groupAltMatchesExactly(tok extToken, name string, start, end int) bool {
	for _, alt := range tok.alts {
		for _, altEnd := range m.ends(alt, 0, name, start) {
			if altEnd == end {
				return true
			}
		}
	}
	return false
}

func (m *extMatcher) matchLiteral(lit, name string, ni int) (int, bool) {
	if ni+len(lit) > len(name) {
		return 0, false
	}
	part := name[ni : ni+len(lit)]
	if m.mode&NoGlobCase != 0 {
		if !strings.EqualFold(part, lit) {
			return 0, false
		}
	} else if part != lit {
		return 0, false
	}
	return ni + len(lit), true
}

func (m *extMatcher) matchSingle(name string, ni int, respectLeadingDot bool) (int, bool) {
	if ni >= len(name) {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(name[ni:])
	if size == 0 {
		return 0, false
	}
	if m.mode&Filenames != 0 && r == '/' {
		return 0, false
	}
	if respectLeadingDot && m.leadingDotBlocked(name, ni) && r == '.' {
		return 0, false
	}
	return ni + size, true
}

func (m *extMatcher) matchCharClass(class *regexp.Regexp, name string, ni int) (int, bool) {
	end, ok := m.matchSingle(name, ni, true)
	if !ok {
		return 0, false
	}
	return end, class.MatchString(name[ni:end])
}

func (m *extMatcher) candidateEnds(name string, ni int, respectLeadingDot bool) []int {
	ends := []int{ni}
	if ni >= len(name) {
		return ends
	}
	if respectLeadingDot && m.leadingDotBlocked(name, ni) && name[ni] == '.' {
		return ends
	}
	for i := ni; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		if size == 0 {
			break
		}
		if m.mode&Filenames != 0 && r == '/' {
			break
		}
		i += size
		ends = append(ends, i)
	}
	return ends
}

func (m *extMatcher) leadingDotBlocked(name string, ni int) bool {
	if m.mode&Filenames == 0 || m.mode&GlobLeadingDot != 0 || ni >= len(name) || name[ni] != '.' {
		return false
	}
	return ni == 0 || name[ni-1] == '/'
}

func uniqSortedInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	slices.Sort(values)
	return slices.Compact(values)
}
