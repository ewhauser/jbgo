// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/ewhauser/gbash/internal/shell/pattern"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func nodeLit(node syntax.Node) string {
	if word, ok := node.(*syntax.Word); ok {
		return word.Lit()
	}
	return ""
}

func subscriptLit(sub *syntax.Subscript) string {
	if sub == nil {
		return ""
	}
	switch sub.Kind {
	case syntax.SubscriptAt:
		return "@"
	case syntax.SubscriptStar:
		return "*"
	default:
		return nodeLit(sub.Expr)
	}
}

func subscriptWord(sub *syntax.Subscript) (*syntax.Word, bool) {
	if sub == nil {
		return nil, false
	}
	word, ok := sub.Expr.(*syntax.Word)
	return word, ok
}

func badSubstitution(pe *syntax.ParamExp) error {
	if pe != nil && pe.Invalid != "" {
		return fmt.Errorf("%s: bad substitution", pe.Invalid)
	}
	src := printNode(pe)
	if src == "" {
		return fmt.Errorf("bad substitution")
	}
	return fmt.Errorf("%s: bad substitution", src)
}

func invalidParamExpansion(pe *syntax.ParamExp) error {
	if pe == nil {
		return nil
	}
	if pe.Slice != nil && pe.Slice.MissingOffset {
		return badSubstitution(pe)
	}
	if (pe.Length || pe.Width || pe.IsSet) &&
		(len(pe.Modifiers) > 0 || pe.Slice != nil || pe.Repl != nil || pe.Names != 0 || pe.Exp != nil) {
		return badSubstitution(pe)
	}
	return nil
}

type compiledParamPattern struct {
	rx         *regexp.Regexp
	byteLocale bool
}

func encodeByteLocaleString(str string) (string, map[int]int) {
	offsets := make(map[int]int, len(str)+1)
	var sb strings.Builder
	offsets[0] = 0
	for i := 0; i < len(str); i++ {
		offsets[sb.Len()] = i
		sb.WriteRune(rune(str[i]))
	}
	offsets[sb.Len()] = len(str)
	return sb.String(), offsets
}

func remapParamPatternIndices(locs []int, offsets map[int]int) []int {
	if locs == nil {
		return nil
	}
	remapped := make([]int, len(locs))
	for i, loc := range locs {
		remapped[i] = offsets[loc]
	}
	return remapped
}

func remapParamPatternLocs(locs [][]int, offsets map[int]int) [][]int {
	if locs == nil {
		return nil
	}
	remapped := make([][]int, len(locs))
	for i, loc := range locs {
		remapped[i] = remapParamPatternIndices(loc, offsets)
	}
	return remapped
}

func (m *compiledParamPattern) findAllStringIndex(name string, n int) [][]int {
	if !m.byteLocale {
		return m.rx.FindAllStringIndex(name, n)
	}
	encoded, offsets := encodeByteLocaleString(name)
	return remapParamPatternLocs(m.rx.FindAllStringIndex(encoded, n), offsets)
}

func (m *compiledParamPattern) findStringIndex(name string) []int {
	if !m.byteLocale {
		return m.rx.FindStringIndex(name)
	}
	encoded, offsets := encodeByteLocaleString(name)
	return remapParamPatternIndices(m.rx.FindStringIndex(encoded), offsets)
}

func (m *compiledParamPattern) findStringSubmatchIndex(name string) []int {
	if !m.byteLocale {
		return m.rx.FindStringSubmatchIndex(name)
	}
	encoded, offsets := encodeByteLocaleString(name)
	return remapParamPatternIndices(m.rx.FindStringSubmatchIndex(encoded), offsets)
}

func shouldQuoteParamPattern(pat string, err error) bool {
	switch pat {
	case "[", "[]", "[^]]":
		return true
	}
	var synErr *pattern.SyntaxError
	if errors.As(err, &synErr) {
		switch synErr.Error() {
		case "[ was not matched with a closing ]":
			return true
		}
		if strings.HasPrefix(synErr.Error(), "invalid range: ") {
			return true
		}
	}
	return err != nil && (err.Error() == "[ was not matched with a closing ]" || strings.HasPrefix(err.Error(), "invalid range: "))
}

func (cfg *Config) paramPatternExpr(pat string, mode pattern.Mode) (string, error) {
	source := pat
	if cfg.bashByteLocale() {
		source, _ = encodeByteLocaleString(pat)
	}
	expr, err := pattern.Regexp(source, mode)
	if err == nil && !shouldQuoteParamPattern(pat, nil) {
		return expr, nil
	}
	if err != nil && !shouldQuoteParamPattern(pat, err) {
		return "", err
	}
	quoted := pattern.QuoteMeta(source, pattern.ExtendedOperators)
	return pattern.Regexp(quoted, mode)
}

func (cfg *Config) compileParamPattern(expr string) *compiledParamPattern {
	return &compiledParamPattern{
		rx:         regexp.MustCompile(expr),
		byteLocale: cfg.bashByteLocale(),
	}
}

func consumeParamReplacementLiteralEscapes(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\\' {
			if i+1 >= len(s) {
				break
			}
			i++
			b = s[i]
		}
		sb.WriteByte(b)
	}
	return sb.String()
}

func collapseParamReplacementBackslashPairs(s string) string {
	if !strings.Contains(s, `\\`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '\\' {
			i++
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func (cfg *Config) replacementWord(word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	sb := cfg.strBuilder()
	for i, wp := range word.Parts {
		part, err := cfg.replacementWordPart(wp, i == 0, i+1 < len(word.Parts))
		if err != nil {
			return "", err
		}
		sb.WriteString(part)
	}
	return sb.String(), nil
}

func (cfg *Config) replacementWordPart(wp syntax.WordPart, leading, more bool) (string, error) {
	switch wp := wp.(type) {
	case *syntax.Lit:
		s := wp.Value
		if leading {
			if prefix, rest, expanded := cfg.expandUser(s, more); expanded {
				s = prefix + rest
			}
		}
		s = consumeParamReplacementLiteralEscapes(s)
		s, _, _ = strings.Cut(s, "\x00")
		return s, nil
	case *syntax.SglQuoted:
		s := wp.Value
		if wp.Dollar {
			s = cfg.decodeANSICString(s)
			s, _, _ = strings.Cut(s, "\x00")
		}
		return s, nil
	case *syntax.DblQuoted:
		field, err := cfg.wordField(wp.Parts, quoteDouble)
		if err != nil {
			return "", err
		}
		return cfg.fieldJoin(field), nil
	case *syntax.ParamExp:
		if parts, ok, err := cfg.paramExpWordField(wp, quoteNone); err != nil {
			return "", err
		} else if ok {
			return collapseParamReplacementBackslashPairs(cfg.fieldJoin(parts)), nil
		}
		val, err := cfg.paramExp(wp, quoteNone)
		if err != nil {
			return "", err
		}
		return collapseParamReplacementBackslashPairs(val), nil
	case *syntax.CmdSubst:
		val, err := cfg.cmdSubst(wp)
		if err != nil {
			return "", err
		}
		return collapseParamReplacementBackslashPairs(val), nil
	case *syntax.ArithmExp:
		sourceStart := wp.Left.Offset() + 3
		if wp.Bracket {
			sourceStart = wp.Left.Offset() + 2
		}
		n, err := ArithmWithSource(cfg, wp.X, wp.Source, sourceStart, wp.Right.Offset())
		if err != nil {
			if !cfg.swallowNonFatal(err) {
				return "", err
			}
			n = 0
		}
		return strconv.Itoa(n), nil
	case *syntax.BraceExp:
		parts, err := cfg.braceFieldParts(wp, quoteNone, func(word *syntax.Word, ql quoteLevel) ([]fieldPart, error) {
			val, err := cfg.replacementWord(word)
			if err != nil {
				return nil, err
			}
			return []fieldPart{{val: val}}, nil
		})
		if err != nil {
			return "", err
		}
		return cfg.fieldJoin(parts), nil
	case *syntax.ProcSubst:
		procPath, err := cfg.ProcSubst(wp)
		if err != nil {
			return "", err
		}
		return collapseParamReplacementBackslashPairs(procPath), nil
	case *syntax.ExtGlob:
		raw, err := cfg.extGlobLiteralString(wp)
		if err != nil {
			return "", err
		}
		return raw, nil
	default:
		panic(fmt.Sprintf("unhandled replacement word part: %T", wp))
	}
}

func (cfg *Config) findParamPatternAllIndex(pat, name string, n int, anchor syntax.ReplaceAnchor) ([][]int, error) {
	expr, err := cfg.paramPatternExpr(pat, pattern.ExtendedOperators)
	if err != nil {
		return nil, err
	}
	switch anchor {
	case syntax.ReplaceAnchorPrefix:
		loc := cfg.compileParamPattern("^(" + expr + ")").findStringSubmatchIndex(name)
		if loc == nil {
			return nil, nil
		}
		return [][]int{{loc[2], loc[3]}}, nil
	case syntax.ReplaceAnchorSuffix:
		loc := cfg.compileParamPattern("(" + expr + ")$").findStringSubmatchIndex(name)
		if loc == nil {
			return nil, nil
		}
		return [][]int{{loc[2], loc[3]}}, nil
	default:
		return cfg.compileParamPattern(expr).findAllStringIndex(name, n), nil
	}
}

func (cfg *Config) associativeSubscriptKey(sub *syntax.Subscript) (string, error) {
	if sub == nil {
		return "", nil
	}
	if word, ok := subscriptWord(sub); ok {
		return Literal(cfg, word)
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter(syntax.Minify(true)).Print(&buf, sub.Expr); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func resolvedSubscriptMode(sub *syntax.Subscript) syntax.SubscriptMode {
	if sub == nil || sub.AllElements() {
		return syntax.SubscriptAuto
	}
	switch sub.Mode {
	case syntax.SubscriptIndexed, syntax.SubscriptAssociative:
		return sub.Mode
	default:
		panic("expand: unresolved subscript mode")
	}
}

func indirectSpecialParam(name string) bool {
	switch name {
	case "@", "*", "#", "?", "-", "$", "!":
		return true
	default:
		return false
	}
}

func indirectPositionalParam(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func looseIndirectVarRef(name string) (*syntax.VarRef, error) {
	if ref, err := parseVarRef(name); err == nil {
		return ref, nil
	}
	if !strings.Contains(name, "[") {
		return nil, InvalidVariableNameError{Ref: name}
	}
	left := strings.IndexByte(name, '[')
	if left <= 0 || !strings.HasSuffix(name, "]") {
		return nil, InvalidVariableNameError{Ref: name}
	}
	base := name[:left]
	if !syntax.ValidName(base) {
		return nil, InvalidVariableNameError{Ref: name}
	}
	content := name[left+1 : len(name)-1]
	ref := &syntax.VarRef{Name: &syntax.Lit{Value: base}}
	switch content {
	case "@":
		ref.Index = &syntax.Subscript{
			Kind: syntax.SubscriptAt,
			Mode: syntax.SubscriptAuto,
			Expr: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "@"}}},
		}
		return ref, nil
	case "*":
		ref.Index = &syntax.Subscript{
			Kind: syntax.SubscriptStar,
			Mode: syntax.SubscriptAuto,
			Expr: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: "*"}}},
		}
		return ref, nil
	}

	word, ok := parseIndirectSubscriptWord(content)
	if !ok {
		return nil, InvalidVariableNameError{Ref: name}
	}
	ref.Index = &syntax.Subscript{
		Kind: syntax.SubscriptExpr,
		Mode: syntax.SubscriptAuto,
		Expr: word,
	}
	return ref, nil
}

func parseIndirectSubscriptWord(content string) (*syntax.Word, bool) {
	word, err := syntax.NewParser().Document(strings.NewReader(content))
	if err == nil {
		return word, true
	}
	var parseErr syntax.ParseError
	if !errors.As(err, &parseErr) {
		return nil, false
	}
	if strings.HasPrefix(parseErr.Text, "reached EOF without closing quote") {
		return nil, false
	}
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: content}}}, true
}

func indirectParamExp(name string) (*syntax.ParamExp, error) {
	ref, err := looseIndirectVarRef(name)
	if err == nil {
		return &syntax.ParamExp{
			Param: ref.Name,
			Index: ref.Index,
		}, nil
	}
	if indirectSpecialParam(name) || indirectPositionalParam(name) {
		return &syntax.ParamExp{
			Param: &syntax.Lit{Value: name},
		}, nil
	}
	return nil, InvalidVariableNameError{Ref: name}
}

func (cfg *Config) indirectValue(name string) (string, error) {
	target, err := indirectParamExp(name)
	if err != nil {
		return "", err
	}
	return cfg.paramExp(target, quoteNone)
}

func indirectHolderParamExp(pe *syntax.ParamExp) *syntax.ParamExp {
	if pe == nil {
		return nil
	}
	holder := *pe
	holder.Length = false
	holder.Width = false
	holder.IsSet = false
	holder.Names = 0
	holder.Slice = nil
	holder.Repl = nil
	holder.Exp = nil
	return &holder
}

func (cfg *Config) envSetParam(state paramExpState, value string) error {
	if state.ref != nil {
		return cfg.envSetRef(state.ref, value)
	}
	return cfg.envSet(state.name, value)
}

// fnv1Hash computes the FNV-1 hash for a string.
// This matches bash's internal hash function for associative arrays.
func fnv1Hash(s string) uint32 {
	const (
		fnvOffsetBasis = 2166136261
		fnvPrime       = 16777619
	)
	h := uint32(fnvOffsetBasis)
	for i := 0; i < len(s); i++ {
		h *= fnvPrime
		h ^= uint32(s[i])
	}
	return h
}

// sortedMapKeys returns the keys of an associative array in bash-compatible order.
// Bash uses FNV-1 hash with 1024 buckets, iterating buckets in ascending order
// and keys within each bucket by ascending hash value.
func sortedMapKeys(m map[string]string) []string {
	const bucketCount = 1024
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		ha, hb := fnv1Hash(a), fnv1Hash(b)
		ba, bb := ha%bucketCount, hb%bucketCount
		if ba != bb {
			if ba < bb {
				return -1
			}
			return 1
		}
		if ha < hb {
			return -1
		}
		if ha > hb {
			return 1
		}
		// Hash collision: fall back to lexicographic order for determinism.
		return strings.Compare(a, b)
	})
	return keys
}

// sortedMapValues returns the values of an associative array in bash-compatible order,
// matching the key order from sortedMapKeys.
func sortedMapValues(m map[string]string) []string {
	keys := sortedMapKeys(m)
	vals := make([]string, len(keys))
	for i, k := range keys {
		vals[i] = m[k]
	}
	return vals
}

// UnsetParameterError is returned when a parameter expansion encounters an
// unset variable and [Config.NoUnset] has been set.
type UnsetParameterError struct {
	Node    *syntax.ParamExp
	Message string
}

func (u UnsetParameterError) Error() string {
	return fmt.Sprintf("%s: %s", paramExpOperandString(u.Node), u.Message)
}

type UnboundVariableError struct {
	Name string
}

func (u UnboundVariableError) Error() string {
	return fmt.Sprintf("%s: unbound variable", u.Name)
}

func overridingUnset(pe *syntax.ParamExp) bool {
	if pe.Exp == nil {
		return false
	}
	switch pe.Exp.Op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull,
		syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
		syntax.ErrorUnset, syntax.ErrorUnsetOrNull,
		syntax.AssignUnset, syntax.AssignUnsetOrNull:
		return true
	}
	return false
}

type paramExpState struct {
	name             string
	orig             Variable
	ref              *syntax.VarRef
	vr               Variable
	str              string
	elems            []string
	indexAllElements bool
	callVarInd       bool
	swallowedError   bool
}

type indirectMode uint8

const (
	indirectNone indirectMode = iota
	indirectResolve
	indirectNames
	indirectKeys
	indirectNameRef
)

func paramExpHasOuterOps(pe *syntax.ParamExp) bool {
	return pe.Length || pe.Width || pe.IsSet || pe.Slice != nil || pe.Repl != nil || pe.Exp != nil
}

func indirectModeFor(pe *syntax.ParamExp, state paramExpState) indirectMode {
	if pe == nil || !pe.Excl {
		return indirectNone
	}
	if !paramExpHasOuterOps(pe) {
		switch {
		case pe.Names != 0:
			return indirectNames
		case state.orig.Kind == NameRef && pe.Index == nil:
			return indirectNameRef
		case pe.Index != nil && pe.Index.AllElements():
			switch state.vr.Kind {
			case Indexed, Associative:
				return indirectKeys
			}
		}
	}
	return indirectResolve
}

func simpleIndirectTarget(pe *syntax.ParamExp) bool {
	return pe != nil &&
		pe.Flags == nil &&
		!pe.Excl && !pe.Length && !pe.Width && !pe.IsSet &&
		pe.NestedParam == nil &&
		len(pe.Modifiers) == 0 &&
		pe.Slice == nil &&
		pe.Repl == nil &&
		pe.Names == 0 &&
		pe.Exp == nil
}

func (cfg *Config) resolveIndirectTargetState(state paramExpState) (paramExpState, *syntax.ParamExp, error) {
	name := state.str
	if state.indexAllElements {
		switch len(state.elems) {
		case 0:
			return state, nil, InvalidIndirectExpansionError{Ref: varRefString(state.ref)}
		case 1:
			name = state.elems[0]
		default:
			return state, nil, InvalidVariableNameError{Ref: strings.Join(state.elems, " ")}
		}
	} else if !state.orig.IsSet() {
		return state, nil, InvalidIndirectExpansionError{Ref: varRefString(state.ref)}
	}
	if name == "" {
		if state.orig.Kind == Indexed || state.orig.Kind == Associative {
			state.vr = Variable{Set: true, Kind: String, Str: ""}
			state.str = ""
			state.elems = []string{""}
			state.indexAllElements = false
			state.callVarInd = false
			state.swallowedError = false
			return state, nil, nil
		}
		return state, nil, InvalidVariableNameError{Ref: name}
	}

	target, err := indirectParamExp(name)
	if err != nil {
		return state, nil, err
	}
	if !simpleIndirectTarget(target) {
		return state, target, nil
	}
	targetState, err := cfg.paramExpState(target)
	return targetState, target, err
}

func arrayExpansionIsAt(pe *syntax.ParamExp) bool {
	return pe != nil && (pe.Param.Value == "@" || subscriptLit(pe.Index) == "@")
}

func arrayExpansionIsStar(pe *syntax.ParamExp) bool {
	return pe != nil && (pe.Param.Value == "*" || subscriptLit(pe.Index) == "*")
}

func arrayExpansionNull(pe *syntax.ParamExp, fields, elems []string) bool {
	hasElems := len(elems) > 0
	null := !hasElems
	if arrayExpansionIsAt(pe) && len(elems) == 1 && elems[0] == "" {
		null = true
	}
	if arrayExpansionIsStar(pe) && len(fields) == 1 && fields[0] == "" {
		null = true
	}
	return null
}

func (cfg *Config) joinArrayElemsForString(pe *syntax.ParamExp, elems []string) string {
	if arrayExpansionIsStar(pe) {
		return cfg.ifsJoin(elems)
	}
	return strings.Join(elems, " ")
}

func elemsAsFields(elems []string) [][]fieldPart {
	fields := make([][]fieldPart, len(elems))
	for i, elem := range elems {
		if elem == "" {
			fields[i] = []fieldPart{}
			continue
		}
		fields[i] = []fieldPart{{val: elem}}
	}
	return fields
}

func (cfg *Config) splitElemsAsFields(elems []string) [][]fieldPart {
	var fields [][]fieldPart
	for _, elem := range elems {
		elemFields := cfg.splitFieldParts([]fieldPart{{val: elem}})
		if len(elemFields) == 0 && elem == "" {
			elemFields = [][]fieldPart{{}}
		}
		for _, field := range elemFields {
			fields = append(fields, append([]fieldPart(nil), field...))
		}
	}
	return fields
}

func decodeParamOpEscapes(str string) string {
	tail := str
	var rns []rune
	for tail != "" {
		rn, _, rest, _ := strconv.UnquoteChar(tail, 0)
		rns = append(rns, rn)
		tail = rest
	}
	return string(rns)
}

func decodePromptEscapes(str string) string {
	var sb strings.Builder
	for i := 0; i < len(str); i++ {
		if str[i] != '\\' || i+1 >= len(str) {
			sb.WriteByte(str[i])
			continue
		}
		i++
		switch str[i] {
		case 'a':
			sb.WriteByte('\a')
		case 'e':
			sb.WriteByte('\x1b')
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case '$':
			sb.WriteByte('$')
		case '\\':
			sb.WriteByte('\\')
		default:
			sb.WriteByte('\\')
			sb.WriteByte(str[i])
		}
	}
	return sb.String()
}

func bashQuoteValue(str string) (string, error) {
	if _, err := syntax.Quote(str, syntax.LangBash); err != nil {
		return "", err
	}
	if !strings.Contains(str, "'") {
		return "'" + str + "'", nil
	}
	parts := strings.Split(str, "'")
	var sb strings.Builder
	for i, part := range parts {
		sb.WriteByte('\'')
		sb.WriteString(part)
		sb.WriteByte('\'')
		if i+1 < len(parts) {
			sb.WriteString("\\'")
		}
	}
	return sb.String(), nil
}

func transformCasePattern(arg string, op syntax.ParExpOperator) (func(string) string, error) {
	caseFunc := unicode.ToLower
	if op == syntax.UpperFirst || op == syntax.UpperAll {
		caseFunc = unicode.ToUpper
	}
	all := op == syntax.UpperAll || op == syntax.LowerAll
	expr, err := pattern.Regexp(arg, pattern.ExtendedOperators)
	if err != nil {
		return func(s string) string { return s }, err
	}
	rx := regexp.MustCompile(expr)
	return func(elem string) string {
		rs := []rune(elem)
		for ri, r := range rs {
			if rx.MatchString(string(r)) {
				rs[ri] = caseFunc(r)
				if !all {
					break
				}
			}
		}
		return string(rs)
	}, nil
}

func otherParamCaseTransform(arg string) (syntax.ParExpOperator, bool) {
	switch arg {
	case "u":
		return syntax.UpperFirst, true
	case "U":
		return syntax.UpperAll, true
	case "L":
		return syntax.LowerAll, true
	default:
		return 0, false
	}
}

func (cfg *Config) transformArrayElems(pe *syntax.ParamExp, state paramExpState, elems []string) ([]string, error) {
	elems = slices.Clone(elems)
	if pe.Repl != nil {
		orig, err := Pattern(cfg, pe.Repl.Orig)
		if err != nil {
			return nil, err
		}
		if orig == "" && pe.Repl.Anchor == syntax.ReplaceAnchorNone {
			return elems, nil
		}
		with, err := cfg.replacementWord(pe.Repl.With)
		if err != nil {
			return nil, err
		}
		n := 1
		if pe.Repl.All {
			n = -1
		}
		for i, elem := range elems {
			locs, err := cfg.findParamPatternAllIndex(orig, elem, n, pe.Repl.Anchor)
			if err != nil {
				return nil, err
			}
			sb := cfg.strBuilder()
			last := 0
			for _, loc := range locs {
				sb.WriteString(elem[last:loc[0]])
				sb.WriteString(with)
				last = loc[1]
			}
			sb.WriteString(elem[last:])
			elems[i] = sb.String()
		}
		return elems, nil
	}
	if pe.Exp == nil {
		return elems, nil
	}

	switch op := pe.Exp.Op; op {
	case syntax.RemSmallPrefix, syntax.RemLargePrefix,
		syntax.RemSmallSuffix, syntax.RemLargeSuffix:
		arg, err := Pattern(cfg, pe.Exp.Pattern)
		if err != nil {
			return nil, err
		}
		suffix := op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
		small := op == syntax.RemSmallPrefix || op == syntax.RemSmallSuffix
		for i, elem := range elems {
			elems[i], err = cfg.removePattern(elem, arg, suffix, small)
			if err != nil {
				return nil, err
			}
		}
	case syntax.UpperFirst, syntax.UpperAll,
		syntax.LowerFirst, syntax.LowerAll:
		arg, err := Pattern(cfg, pe.Exp.Pattern)
		if err != nil {
			return nil, err
		}
		transform, err := transformCasePattern(arg, op)
		if err != nil {
			return elems, err
		}
		for i, elem := range elems {
			elems[i] = transform(elem)
		}
	case syntax.OtherParamOps:
		arg, err := Literal(cfg, pe.Exp.Word)
		if err != nil {
			return nil, err
		}
		switch arg {
		case "Q", "K", "k":
			for i, elem := range elems {
				quoted, err := bashQuoteValue(elem)
				if err != nil {
					return nil, err
				}
				elems[i] = quoted
			}
		case "E":
			for i, elem := range elems {
				elems[i] = decodeParamOpEscapes(elem)
			}
		case "a":
			flags := state.orig.Flags()
			if state.name == "@" || state.name == "*" {
				flags = ""
			}
			for i := range elems {
				elems[i] = flags
			}
		case "P":
			for i, elem := range elems {
				elems[i] = decodePromptEscapes(elem)
			}
		case "u", "U", "L":
			caseOp, _ := otherParamCaseTransform(arg)
			transform, err := transformCasePattern("", caseOp)
			if err != nil {
				return nil, err
			}
			for i, elem := range elems {
				elems[i] = transform(elem)
			}
		default:
			return elems, nil
		}
	}
	return elems, nil
}

func (cfg *Config) arrayParamFields(pe *syntax.ParamExp, state paramExpState, elems []string) ([][]fieldPart, bool, error) {
	elems, err := cfg.transformArrayElems(pe, state, elems)
	if err != nil {
		return nil, false, err
	}
	return cfg.splitElemsAsFields(elems), true, nil
}

func (cfg *Config) indirectParamArrayFields(state paramExpState) ([][]fieldPart, bool, bool, error) {
	if state.orig.Kind == NameRef || state.str == "" {
		return nil, false, false, nil
	}
	target, err := indirectParamExp(state.str)
	if err != nil {
		return nil, false, false, err
	}
	if !quotedIndirectArrayTarget(target) {
		return nil, false, false, nil
	}
	if fields, ok, elideEmpty, err := cfg.paramExpFields(target); err != nil {
		return nil, false, false, err
	} else if ok {
		return fields, true, elideEmpty, nil
	}
	if parts, ok, err := cfg.paramExpSplitValue(target); err != nil {
		return nil, false, false, err
	} else if ok {
		return cfg.splitFieldParts(parts), true, false, nil
	}
	return nil, false, false, nil
}

func (cfg *Config) paramExpSplitValue(pe *syntax.ParamExp) ([]fieldPart, bool, error) {
	if cfg.ifs == "" || pe == nil || pe.Length || pe.Width || pe.IsSet || pe.Excl {
		return nil, false, nil
	}
	if err := invalidParamExpansion(pe); err != nil {
		return nil, false, err
	}

	fields0, elems, isArray := cfg.quotedArrayFields(pe)
	if !isArray {
		return nil, false, nil
	}

	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, err
	}
	if state.swallowedError && !state.indexAllElements {
		return nil, true, nil
	}

	wordParts := func(word *syntax.Word) ([]fieldPart, error) {
		if word == nil {
			return nil, nil
		}
		return cfg.paramArgField(word, quoteNone)
	}

	hasElems := len(elems) > 0
	null := arrayExpansionNull(pe, fields0, elems)
	if pe.Exp != nil {
		switch pe.Exp.Op {
		case syntax.AlternateUnset:
			if hasElems {
				parts, err := wordParts(pe.Exp.Word)
				return parts, true, err
			}
		case syntax.AlternateUnsetOrNull:
			if !null {
				parts, err := wordParts(pe.Exp.Word)
				return parts, true, err
			}
		case syntax.DefaultUnset:
			if !hasElems {
				parts, err := wordParts(pe.Exp.Word)
				return parts, true, err
			}
		case syntax.DefaultUnsetOrNull:
			if null {
				parts, err := wordParts(pe.Exp.Word)
				return parts, true, err
			}
		case syntax.AssignUnset:
			if !hasElems {
				parts, err := wordParts(pe.Exp.Word)
				if err != nil {
					return nil, false, err
				}
				val := cfg.fieldJoin(parts)
				if err := cfg.envSet(state.name, val); err != nil {
					return nil, false, err
				}
				return parts, true, nil
			}
		case syntax.AssignUnsetOrNull:
			if null {
				parts, err := wordParts(pe.Exp.Word)
				if err != nil {
					return nil, false, err
				}
				val := cfg.fieldJoin(parts)
				if err := cfg.envSet(state.name, val); err != nil {
					return nil, false, err
				}
				return parts, true, nil
			}
		case syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
			return nil, false, nil
		}
	}

	elems, err = cfg.transformArrayElems(pe, state, elems)
	if err != nil {
		return nil, false, err
	}
	return []fieldPart{{val: cfg.ifsJoin(elems)}}, true, nil
}

func (cfg *Config) paramExpState(pe *syntax.ParamExp) (paramExpState, error) {
	if pe == nil || pe.Param == nil {
		return paramExpState{}, badSubstitution(pe)
	}
	if pe.Invalid != "" {
		return paramExpState{}, badSubstitution(pe)
	}
	state := paramExpState{
		name:       pe.Param.Value,
		callVarInd: true,
	}
	if err := invalidParamExpansion(pe); err != nil {
		return state, err
	}

	index := pe.Index
	if emptySubscript(index) {
		return state, badSubstitution(pe)
	}
	switch state.name {
	case "@", "*":
		kind := syntax.SubscriptAt
		if state.name == "*" {
			kind = syntax.SubscriptStar
		}
		index = &syntax.Subscript{
			Kind: kind,
			Expr: &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: state.name},
			}},
		}
	}

	switch state.name {
	case "LINENO":
		// This is the only parameter expansion that the environment
		// interface cannot satisfy.
		line := uint64(0)
		if cfg.CurrentLine != nil {
			line = uint64(cfg.CurrentLine())
		}
		if line == 0 && cfg.curParam != nil {
			line = uint64(cfg.curParam.Pos().Line())
		}
		state.vr = Variable{Set: true, Kind: String, Str: strconv.FormatUint(line, 10)}
	default:
		state.vr = cfg.Env.Get(state.name)
	}
	state.orig = state.vr
	state.ref = &syntax.VarRef{
		Name:  pe.Param,
		Index: index,
	}
	resolution, err := state.vr.ResolveRefState(cfg.Env, &syntax.VarRef{
		Name:  pe.Param,
		Index: index,
	})
	if err != nil {
		return state, err
	}
	state.vr = resolution.Var
	if resolution.Status == RefTargetCircular {
		cfg.reportParamErrorOnce(pe, CircularNameRefError{Name: pe.Param.Value})
	}
	if resolution.Ref != nil {
		index = resolution.Ref.Index
		state.ref = resolution.Ref
	} else {
		index = nil
	}
	if cfg.NoUnset && !pe.Excl && !state.vr.IsSet() && !overridingUnset(pe) {
		return state, UnsetParameterError{
			Node:    pe,
			Message: "unbound variable",
		}
	}

	switch subscriptLit(index) {
	case "@", "*":
		switch state.vr.Kind {
		case Unknown:
			state.indexAllElements = true
		case Indexed:
			state.indexAllElements = true
			state.callVarInd = false
			state.elems = cfg.sliceElems(pe, state.vr.IndexedValues(), state.vr.IndexedIndices(), state.name == "@" || state.name == "*", false)
			state.str = cfg.joinArrayElemsForString(pe, state.elems)
		case Associative:
			state.indexAllElements = true
			state.callVarInd = false
			state.elems = cfg.sliceElems(pe, sortedMapValues(state.vr.Map), nil, false, true)
			state.str = cfg.joinArrayElemsForString(pe, state.elems)
		}
	}
	if index == nil && !state.indexAllElements {
		switch state.vr.Kind {
		case Indexed:
			if val, ok := state.vr.IndexedGet(0); ok {
				state.vr = Variable{Set: true, Kind: String, Str: val}
				state.str = val
			} else {
				state.vr = Variable{Kind: String}
				state.str = ""
			}
			state.callVarInd = false
		case Associative:
			if val, ok := state.vr.Map["0"]; ok {
				state.vr = Variable{Set: true, Kind: String, Str: val}
				state.str = val
			} else {
				state.vr = Variable{Kind: String}
				state.str = ""
			}
			state.callVarInd = false
		}
	}
	if index != nil && !state.indexAllElements {
		switch state.vr.Kind {
		case Indexed:
			i, err := Arithm(cfg, index.Expr)
			if err != nil {
				return state, err
			}
			if i < 0 {
				resolved, ok := state.vr.IndexedResolve(i)
				if ok {
					i = resolved
				} else {
					break
				}
			}
			if val, ok := state.vr.IndexedGet(i); ok {
				state.vr = Variable{Set: true, Kind: String, Str: val}
			} else {
				state.vr = Variable{Kind: String}
			}
			index = nil
		case Associative:
			key, err := cfg.associativeSubscriptKey(index)
			if err != nil {
				return state, err
			}
			if val, ok := state.vr.Map[key]; ok {
				state.vr = Variable{Set: true, Kind: String, Str: val}
			} else {
				state.vr = Variable{Kind: String}
			}
			index = nil
		}
	}
	if state.callVarInd {
		var err error
		state.str, err = cfg.varInd(state.name, state.vr, index)
		if err != nil {
			if cfg.swallowNonFatal(err) {
				state.vr = Variable{Kind: String}
				state.str = ""
				state.callVarInd = false
				state.swallowedError = true
			} else {
				return state, err
			}
		}
	}
	if !state.indexAllElements {
		if state.callVarInd {
			state.elems = []string{state.str}
		} else {
			state.elems = []string{""}
		}
	}
	return state, nil
}

func (cfg *Config) paramArgField(word *syntax.Word, ql quoteLevel) ([]fieldPart, error) {
	if word == nil {
		return nil, nil
	}
	var field []fieldPart
	for i, wp := range word.Parts {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 && ql == quoteNone {
				if prefix, rest, expanded := cfg.expandUser(s, len(word.Parts) > 1); expanded {
					s = prefix + rest
				}
			}
			if (ql == quoteDouble || ql == quoteHeredoc) && strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for i := 0; i < len(s); i++ {
					b := s[i]
					if b == '\\' && i+1 < len(s) {
						switch s[i+1] {
						case '"':
							if ql != quoteDouble {
								break
							}
							fallthrough
						case '\\', '$', '`':
							i++
							b = s[i]
						}
					}
					sb.WriteByte(b)
				}
				s = sb.String()
			}
			s, _, _ = strings.Cut(s, "\x00")
			field = append(field, fieldPart{val: s})
		case *syntax.SglQuoted:
			if ql == quoteDouble && !wp.Dollar {
				if expanded, ok, err := cfg.expandSingleQuotedParamArg(wp.Value); err != nil {
					return nil, err
				} else if ok {
					field = append(field, fieldPart{quote: quoteDouble, val: "'" + expanded + "'"})
					continue
				}
			}
			if ql == quoteDouble && !wp.Dollar {
				field = append(field, fieldPart{quote: quoteDouble, val: "'" + wp.Value + "'"})
				continue
			}
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val, _, _ = Format(cfg, fp.val, nil)
				fp.val, _, _ = strings.Cut(fp.val, "\x00")
			}
			field = append(field, fp)
		case *syntax.DblQuoted:
			if ql == quoteNone {
				if fields, ok, err := cfg.dblQuotedFields(wp.Parts); err != nil {
					return nil, err
				} else if ok {
					for fi, parts := range fields {
						if fi > 0 {
							field = append(field, fieldPart{val: " "})
						}
						field = append(field, fieldPart{val: cfg.fieldJoin(parts)})
					}
					continue
				}
			}
			wfield, err := cfg.paramArgField(&syntax.Word{Parts: wp.Parts}, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				field = append(field, part)
			}
			continue
		case *syntax.ParamExp:
			if parts, ok, err := cfg.paramExpWordField(wp, ql); err != nil {
				return nil, err
			} else if ok {
				field = append(field, parts...)
				continue
			}
			val, err := cfg.paramExp(wp, ql)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: val})
			continue
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: val})
		case *syntax.ArithmExp:
			sourceStart := wp.Left.Offset() + 3
			if wp.Bracket {
				sourceStart = wp.Left.Offset() + 2
			}
			n, err := ArithmWithSource(cfg, wp.X, wp.Source, sourceStart, wp.Right.Offset())
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: strconv.Itoa(n)})
		case *syntax.BraceExp:
			parts, err := cfg.braceFieldParts(wp, ql, cfg.paramArgField)
			if err != nil {
				return nil, err
			}
			field = append(field, parts...)
		case *syntax.ProcSubst:
			path, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: path})
		case *syntax.ExtGlob:
			pat, err := cfg.extGlobPatternString(wp)
			if err != nil {
				return nil, err
			}
			raw, err := cfg.extGlobLiteralString(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: raw, glob: pat})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	return field, nil
}

func (cfg *Config) expandSingleQuotedParamArg(src string) (string, bool, error) {
	if !strings.Contains(src, "$") {
		return "", false, nil
	}
	word, err := syntax.NewParser().Document(strings.NewReader(src))
	if err != nil {
		return "", false, err
	}
	if fields, ok, err := cfg.dblQuotedFields(word.Parts); err != nil {
		return "", false, err
	} else if ok {
		parts := make([]string, len(fields))
		for i, field := range fields {
			parts[i] = cfg.fieldJoin(field)
		}
		return strings.Join(parts, " "), true, nil
	}
	field, err := cfg.wordField(word.Parts, quoteDouble)
	if err != nil {
		return "", false, err
	}
	return cfg.fieldJoin(field), true, nil
}

func (cfg *Config) paramExpWordField(pe *syntax.ParamExp, ql quoteLevel) ([]fieldPart, bool, error) {
	if pe.Exp == nil || pe.Excl || pe.Length || pe.Width || pe.IsSet || pe.Repl != nil || pe.Slice != nil {
		return nil, false, nil
	}
	if ql == quoteDouble || ql == quoteHeredoc {
		if elems, ok, err := cfg.quotedElemFields(pe); err != nil {
			return nil, false, err
		} else if ok {
			if len(elems) == 0 {
				return []fieldPart{{quote: quoteDouble, val: ""}}, true, nil
			}
			return []fieldPart{{quote: quoteDouble, val: strings.Join(elems, " ")}}, true, nil
		}
	}
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()
	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, err
	}
	indirectState := state
	if pe.Excl {
		indirectState, err = cfg.paramExpState(indirectHolderParamExp(pe))
		if err != nil {
			return nil, false, err
		}
	}
	if indirectModeFor(pe, indirectState) == indirectResolve {
		resolved, target, err := cfg.resolveIndirectTargetState(indirectState)
		if err != nil {
			return nil, false, err
		}
		if target != nil && quotedIndirectArrayTarget(target) && pe.Exp != nil && (ql == quoteDouble || ql == quoteHeredoc) {
			targetState, err := cfg.paramExpState(target)
			if err != nil {
				return nil, false, err
			}
			hasElems := len(targetState.elems) > 0
			argField := func() ([]fieldPart, string, error) {
				return cfg.paramOpArg(pe.Exp.Word, ql)
			}
			current := func() ([]fieldPart, bool, error) {
				return []fieldPart{{quote: quoteDouble, val: targetState.str}}, true, nil
			}
			switch pe.Exp.Op {
			case syntax.AlternateUnset:
				if hasElems {
					parts, _, err := argField()
					return parts, true, err
				}
			case syntax.AlternateUnsetOrNull:
				if hasElems {
					parts, _, err := argField()
					return parts, true, err
				}
			case syntax.DefaultUnset:
				if !hasElems {
					parts, _, err := argField()
					return parts, true, err
				}
				return current()
			case syntax.DefaultUnsetOrNull:
				if !hasElems {
					parts, _, err := argField()
					return parts, true, err
				}
				return current()
			case syntax.AssignUnset, syntax.AssignUnsetOrNull:
				if hasElems {
					return current()
				}
			}
		}
		if target != nil && quotedIndirectArrayTarget(target) && pe.Exp != nil {
			targetCopy := *target
			targetCopy.Exp = pe.Exp
			return cfg.paramExpWordField(&targetCopy, ql)
		}
		if target != nil && !simpleIndirectTarget(target) {
			if fields, ok, _, err := cfg.paramExpFields(target); err != nil {
				return nil, false, err
			} else if ok {
				if len(fields) == 0 {
					return []fieldPart{}, true, nil
				}
				if len(fields) == 1 {
					return append([]fieldPart(nil), fields[0]...), true, nil
				}
			}
			val, err := cfg.paramExp(target, ql)
			if err != nil {
				return nil, false, err
			}
			return []fieldPart{{val: val}}, true, nil
		}
		state = resolved
	} else if pe.Excl {
		state = indirectState
	}
	argField := func() ([]fieldPart, string, error) {
		return cfg.paramOpArg(pe.Exp.Word, ql)
	}

	switch op := pe.Exp.Op; op {
	case syntax.AlternateUnset:
		if state.vr.IsSet() {
			parts, _, err := argField()
			return parts, true, err
		}
	case syntax.AlternateUnsetOrNull:
		if state.str != "" {
			parts, _, err := argField()
			return parts, true, err
		}
	case syntax.DefaultUnset:
		if !state.vr.IsSet() {
			parts, _, err := argField()
			return parts, true, err
		}
	case syntax.DefaultUnsetOrNull:
		if state.str == "" {
			parts, _, err := argField()
			return parts, true, err
		}
	case syntax.AssignUnset:
		if !state.vr.IsSet() {
			parts, arg, err := argField()
			if err != nil {
				return nil, false, err
			}
			if err := cfg.envSet(state.name, arg); err != nil {
				return nil, false, err
			}
			return parts, true, nil
		}
	case syntax.AssignUnsetOrNull:
		if state.str == "" {
			parts, arg, err := argField()
			if err != nil {
				return nil, false, err
			}
			if err := cfg.envSet(state.name, arg); err != nil {
				return nil, false, err
			}
			return parts, true, nil
		}
	}
	return nil, false, nil
}

func (cfg *Config) paramExpFields(pe *syntax.ParamExp) ([][]fieldPart, bool, bool, error) {
	if pe.Length || pe.Width || pe.IsSet {
		return nil, false, false, nil
	}
	if err := invalidParamExpansion(pe); err != nil {
		return nil, false, false, err
	}
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()
	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, false, err
	}
	indirectState := state
	if pe.Excl {
		indirectState, err = cfg.paramExpState(indirectHolderParamExp(pe))
		if err != nil {
			return nil, false, false, err
		}
	}
	indMode := indirectModeFor(pe, indirectState)
	if indMode == indirectResolve {
		resolved, target, err := cfg.resolveIndirectTargetState(indirectState)
		if err != nil {
			return nil, false, false, err
		}
		if target != nil && quotedIndirectArrayTarget(target) && pe.Exp != nil {
			targetCopy := *target
			targetCopy.Exp = pe.Exp
			return cfg.paramExpFields(&targetCopy)
		}
		if target != nil && quotedIndirectArrayTarget(target) && !paramExpHasOuterOps(pe) {
			if fields, ok, elideEmpty, err := cfg.paramExpFields(target); err != nil {
				return nil, false, false, err
			} else if ok {
				return fields, true, elideEmpty, nil
			}
			if parts, ok, err := cfg.paramExpSplitValue(target); err != nil {
				return nil, false, false, err
			} else if ok {
				return cfg.splitFieldParts(parts), true, false, nil
			}
			val, err := cfg.paramExp(target, quoteNone)
			if err != nil {
				return nil, false, false, err
			}
			return cfg.splitFieldParts([]fieldPart{{val: val}}), true, false, nil
		}
		if target != nil && !simpleIndirectTarget(target) {
			if fields, ok, elideEmpty, err := cfg.paramExpFields(target); err != nil {
				return nil, false, false, err
			} else if ok {
				return fields, true, elideEmpty, nil
			}
			val, err := cfg.paramExp(target, quoteNone)
			if err != nil {
				return nil, false, false, err
			}
			return cfg.splitFieldParts([]fieldPart{{val: val}}), true, false, nil
		}
		state = resolved
	} else if pe.Excl {
		state = indirectState
	}
	if state.swallowedError && !state.indexAllElements {
		return [][]fieldPart{}, true, false, nil
	}
	if pe.Excl && indMode != indirectResolve {
		if pe.Names == 0 && pe.Index == nil {
			if fields, ok, elideEmpty, err := cfg.indirectParamArrayFields(state); err != nil {
				return nil, false, false, err
			} else if ok {
				return fields, true, elideEmpty, nil
			}
		}
		switch pe.Names {
		case syntax.NamesPrefixWords:
			return cfg.splitElemsAsFields(cfg.namesByPrefix(pe.Param.Value)), true, false, nil
		case syntax.NamesPrefix:
			names := cfg.namesByPrefix(pe.Param.Value)
			if cfg.ifs == "" {
				if len(names) == 0 {
					return [][]fieldPart{}, true, false, nil
				}
				return [][]fieldPart{{{val: cfg.ifsJoin(names)}}}, true, false, nil
			}
			return cfg.splitElemsAsFields(names), true, false, nil
		}

		switch subscriptLit(pe.Index) {
		case "@":
			switch state.vr.Kind {
			case Indexed:
				keys := make([]string, 0, state.vr.IndexedCount())
				for _, key := range state.vr.IndexedIndices() {
					keys = append(keys, strconv.Itoa(key))
				}
				return cfg.splitElemsAsFields(keys), true, false, nil
			case Associative:
				return cfg.splitElemsAsFields(sortedMapKeys(state.vr.Map)), true, false, nil
			}
		case "*":
			switch state.vr.Kind {
			case Indexed:
				keys := make([]string, 0, state.vr.IndexedCount())
				for _, key := range state.vr.IndexedIndices() {
					keys = append(keys, strconv.Itoa(key))
				}
				if cfg.ifs == "" {
					if len(keys) == 0 {
						return [][]fieldPart{}, true, false, nil
					}
					return [][]fieldPart{{{val: strings.Join(keys, " ")}}}, true, false, nil
				}
				return cfg.splitElemsAsFields(keys), true, false, nil
			case Associative:
				keys := sortedMapKeys(state.vr.Map)
				if cfg.ifs == "" {
					if len(keys) == 0 {
						return [][]fieldPart{}, true, false, nil
					}
					return [][]fieldPart{{{val: strings.Join(keys, " ")}}}, true, false, nil
				}
				return cfg.splitElemsAsFields(keys), true, false, nil
			}
		}
	}
	fields0, elems, isArray := cfg.quotedArrayFields(pe)
	if isArray {
		if cfg.ifs != "" {
			return nil, false, false, nil
		}
		argFields := func() ([][]fieldPart, string, error) {
			return cfg.paramOpArgFields(pe.Exp.Word, quoteNone)
		}
		hasElems := len(elems) > 0
		null := arrayExpansionNull(pe, fields0, elems)
		if cfg.ifs == "" && pe.Param != nil && pe.Param.Value == "*" && len(elems) > 1 {
			null = false
		}
		if cfg.ifs == "" && pe.Param != nil && subscriptLit(pe.Index) == "*" && len(elems) > 1 {
			allEmpty := true
			for _, elem := range elems {
				if elem != "" {
					allEmpty = false
					break
				}
			}
			if allEmpty {
				null = false
			}
		}
		if cfg.ifs == "" && arrayExpansionIsStar(pe) && len(fields0) == 0 {
			null = true
		}
		if pe.Exp != nil {
			switch op := pe.Exp.Op; op {
			case syntax.AlternateUnset:
				if hasElems {
					fields, _, err := argFields()
					return fields, true, false, err
				}
				fields, ok, err := cfg.arrayParamFields(pe, state, elems)
				return fields, ok, true, err
			case syntax.AlternateUnsetOrNull:
				if !null {
					fields, _, err := argFields()
					return fields, true, false, err
				}
				fields, ok, err := cfg.arrayParamFields(pe, state, elems)
				return fields, ok, true, err
			case syntax.DefaultUnset:
				if !hasElems {
					fields, _, err := argFields()
					return fields, true, false, err
				}
			case syntax.DefaultUnsetOrNull:
				if null {
					fields, _, err := argFields()
					return fields, true, false, err
				}
			case syntax.AssignUnset:
				if !hasElems {
					fields, arg, err := argFields()
					if err != nil {
						return nil, false, false, err
					}
					if err := cfg.envSetParam(state, arg); err != nil {
						return nil, false, false, err
					}
					return fields, true, false, nil
				}
			case syntax.AssignUnsetOrNull:
				if null {
					fields, arg, err := argFields()
					if err != nil {
						return nil, false, false, err
					}
					if err := cfg.envSetParam(state, arg); err != nil {
						return nil, false, false, err
					}
					return fields, true, false, nil
				}
			case syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
				return nil, false, false, nil
			}
		}
		fields, ok, err := cfg.arrayParamFields(pe, state, elems)
		return fields, ok, true, err
	}
	if pe.Exp == nil || pe.Repl != nil || pe.Slice != nil {
		return nil, false, false, nil
	}
	argFields := func() ([][]fieldPart, string, error) {
		return cfg.paramOpArgFields(pe.Exp.Word, quoteNone)
	}

	switch op := pe.Exp.Op; op {
	case syntax.AlternateUnset:
		if state.vr.IsSet() {
			fields, _, err := argFields()
			return fields, true, false, err
		}
	case syntax.AlternateUnsetOrNull:
		if state.str != "" {
			fields, _, err := argFields()
			return fields, true, false, err
		}
	case syntax.DefaultUnset:
		if !state.vr.IsSet() {
			fields, _, err := argFields()
			return fields, true, false, err
		}
	case syntax.DefaultUnsetOrNull:
		if state.str == "" {
			fields, _, err := argFields()
			return fields, true, false, err
		}
	case syntax.AssignUnset:
		if !state.vr.IsSet() {
			fields, arg, err := argFields()
			if err != nil {
				return nil, false, false, err
			}
			if err := cfg.envSetParam(state, arg); err != nil {
				return nil, false, false, err
			}
			return fields, true, false, nil
		}
	case syntax.AssignUnsetOrNull:
		if state.str == "" {
			fields, arg, err := argFields()
			if err != nil {
				return nil, false, false, err
			}
			if err := cfg.envSetParam(state, arg); err != nil {
				return nil, false, false, err
			}
			return fields, true, false, nil
		}
	}
	return nil, false, false, nil
}

func (cfg *Config) paramExp(pe *syntax.ParamExp, ql quoteLevel) (string, error) {
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()

	state, err := cfg.paramExpState(pe)
	if err != nil {
		return "", err
	}
	indirectState := state
	if pe.Excl {
		indirectState, err = cfg.paramExpState(indirectHolderParamExp(pe))
		if err != nil {
			return "", err
		}
	}
	indMode := indirectModeFor(pe, indirectState)
	if indMode == indirectResolve {
		resolved, target, err := cfg.resolveIndirectTargetState(indirectState)
		if err != nil {
			return "", err
		}
		if target != nil && quotedIndirectArrayTarget(target) && pe.Exp != nil {
			targetCopy := *target
			targetCopy.Exp = pe.Exp
			return cfg.paramExp(&targetCopy, ql)
		}
		if target != nil && !simpleIndirectTarget(target) && !paramExpHasOuterOps(pe) {
			return cfg.paramExp(target, ql)
		}
		state = resolved
	} else if pe.Excl {
		state = indirectState
	}
	name := state.name
	orig := state.orig
	vr := state.vr

	var sliceOffset, sliceLen int
	if pe.Slice != nil {
		if pe.Slice.Offset != nil {
			sliceOffset, err = Arithm(cfg, pe.Slice.Offset)
			if err != nil {
				return "", err
			}
		}
		if pe.Slice.Length != nil {
			sliceLen, err = Arithm(cfg, pe.Slice.Length)
			if err != nil {
				return "", err
			}
		}
	}

	var (
		str        = state.str
		elems      = state.elems
		callVarInd = state.callVarInd
	)

	switch {
	case pe.Length:
		n := len(elems)
		switch {
		case name == "@", name == "*", subscriptLit(pe.Index) == "@", subscriptLit(pe.Index) == "*":
		default:
			n = cfg.bashStringLen(str)
		}
		str = strconv.Itoa(n)
	case indMode == indirectNames || indMode == indirectKeys || indMode == indirectNameRef:
		var strs []string
		assocKeys := false
		switch {
		case indMode == indirectNames:
			strs = cfg.namesByPrefix(pe.Param.Value)
		case indMode == indirectNameRef:
			strs = append(strs, orig.Str)
		case vr.Kind == Indexed:
			for _, index := range vr.IndexedIndices() {
				strs = append(strs, strconv.Itoa(index))
			}
		case vr.Kind == Associative:
			strs = sortedMapKeys(vr.Map)
			assocKeys = true
		}
		if !assocKeys {
			slices.Sort(strs)
		}
		str = strings.Join(strs, " ")
	case pe.Width:
		return "", fmt.Errorf("unsupported")
	case pe.IsSet:
		return "", fmt.Errorf("unsupported")
	case pe.Slice != nil:
		if callVarInd {
			str = cfg.bashStringSlice(str, pe.Slice.Offset != nil, sliceOffset, pe.Slice.Length != nil, sliceLen)
		} // else, elems are already sliced
	case pe.Repl != nil:
		orig, err := Pattern(cfg, pe.Repl.Orig)
		if err != nil {
			return "", err
		}
		if orig == "" && pe.Repl.Anchor == syntax.ReplaceAnchorNone {
			break // nothing to replace
		}
		with, err := cfg.replacementWord(pe.Repl.With)
		if err != nil {
			return "", err
		}
		n := 1
		if pe.Repl.All {
			n = -1
		}
		locs, err := cfg.findParamPatternAllIndex(orig, str, n, pe.Repl.Anchor)
		if err != nil {
			return "", err
		}
		sb := cfg.strBuilder()
		last := 0
		for _, loc := range locs {
			sb.WriteString(str[last:loc[0]])
			sb.WriteString(with)
			last = loc[1]
		}
		sb.WriteString(str[last:])
		str = sb.String()
	case pe.Exp != nil:
		switch op := pe.Exp.Op; op {
		case syntax.AlternateUnsetOrNull, syntax.AlternateUnset,
			syntax.DefaultUnset, syntax.DefaultUnsetOrNull,
			syntax.AssignUnset, syntax.AssignUnsetOrNull:
			switch op {
			case syntax.AlternateUnsetOrNull:
				if str == "" {
					break
				}
				fallthrough
			case syntax.AlternateUnset:
				if vr.IsSet() {
					_, arg, err := cfg.paramOpArg(pe.Exp.Word, ql)
					if err != nil {
						return "", err
					}
					str = arg
				}
			case syntax.DefaultUnset:
				if vr.IsSet() {
					break
				}
				fallthrough
			case syntax.DefaultUnsetOrNull:
				if str == "" {
					_, arg, err := cfg.paramOpArg(pe.Exp.Word, ql)
					if err != nil {
						return "", err
					}
					str = arg
				}
			case syntax.AssignUnset:
				if vr.IsSet() {
					break
				}
				fallthrough
			case syntax.AssignUnsetOrNull:
				if str == "" {
					_, arg, err := cfg.paramOpArg(pe.Exp.Word, ql)
					if err != nil {
						return "", err
					}
					if err := cfg.envSetParam(state, arg); err != nil {
						return "", err
					}
					str = arg
				}
			}
		default:
			var arg string
			switch op {
			case syntax.RemSmallPrefix, syntax.RemLargePrefix,
				syntax.RemSmallSuffix, syntax.RemLargeSuffix,
				syntax.UpperFirst, syntax.UpperAll,
				syntax.LowerFirst, syntax.LowerAll:
				arg, err = Pattern(cfg, pe.Exp.Pattern)
			default:
				arg, err = Literal(cfg, pe.Exp.Word)
			}
			if err != nil {
				return "", err
			}
			switch op {
			case syntax.ErrorUnset:
				if vr.IsSet() {
					break
				}
				fallthrough
			case syntax.ErrorUnsetOrNull:
				if str == "" {
					return "", UnsetParameterError{
						Node:    pe,
						Message: arg,
					}
				}
			case syntax.RemSmallPrefix, syntax.RemLargePrefix,
				syntax.RemSmallSuffix, syntax.RemLargeSuffix:
				suffix := op == syntax.RemSmallSuffix || op == syntax.RemLargeSuffix
				small := op == syntax.RemSmallPrefix || op == syntax.RemSmallSuffix
				for i, elem := range elems {
					elems[i], err = cfg.removePattern(elem, arg, suffix, small)
					if err != nil {
						return "", err
					}
				}
				str = cfg.joinArrayElemsForString(pe, elems)
			case syntax.UpperFirst, syntax.UpperAll,
				syntax.LowerFirst, syntax.LowerAll:

				caseFunc := unicode.ToLower
				if op == syntax.UpperFirst || op == syntax.UpperAll {
					caseFunc = unicode.ToUpper
				}
				all := op == syntax.UpperAll || op == syntax.LowerAll

				// empty string means '?'; nothing to do there
				expr, err := pattern.Regexp(arg, pattern.ExtendedOperators)
				if err != nil {
					return str, nil
				}
				rx := regexp.MustCompile(expr)

				for i, elem := range elems {
					rs := []rune(elem)
					for ri, r := range rs {
						if rx.MatchString(string(r)) {
							rs[ri] = caseFunc(r)
							if !all {
								break
							}
						}
					}
					elems[i] = string(rs)
				}
				str = cfg.joinArrayElemsForString(pe, elems)
			case syntax.OtherParamOps:
				switch arg {
				case "Q", "K", "k":
					if !vr.IsSet() {
						break
					}
					if ql == quoteDouble || ql == quoteHeredoc {
						if !arrayExpansionIsAt(pe) && !arrayExpansionIsStar(pe) {
							str, err = syntax.Quote(str, syntax.LangBash)
							if err != nil {
								return "", err
							}
							break
						}
					}
					if vr.IsSet() {
						str, err = bashQuoteValue(str)
						if err != nil {
							return "", err
						}
					}
				case "E":
					tail := str
					var rns []rune
					for tail != "" {
						var rn rune
						rn, _, tail, _ = strconv.UnquoteChar(tail, 0)
						rns = append(rns, rn)
					}
					str = string(rns)
				case "a":
					if name == "@" || name == "*" {
						str = ""
						break
					}
					if pe.Excl && indMode == indirectResolve &&
						pe.Index == nil && indirectState.str == "" &&
						(indirectState.orig.Kind == Indexed || indirectState.orig.Kind == Associative) {
						str = ""
						break
					}
					str = orig.Flags()
				case "A":
					if !vr.IsSet() {
						str = ""
						break
					}
					flags := orig.Flags()
					quoted, err := bashQuoteValue(str)
					if err != nil {
						return "", err
					}
					if flags == "" {
						str = fmt.Sprintf("%s=%s", name, quoted)
					} else {
						str = fmt.Sprintf("declare -%s %s=%s", flags, name, quoted)
					}
				case "P":
					str = decodePromptEscapes(str)
				case "u", "U", "L":
					caseOp, _ := otherParamCaseTransform(arg)
					transform, err := transformCasePattern("", caseOp)
					if err != nil {
						return "", err
					}
					for i, elem := range elems {
						elems[i] = transform(elem)
					}
					str = cfg.joinArrayElemsForString(pe, elems)
				default:
					panic(fmt.Sprintf("unexpected @%s param expansion", arg))
				}
			}
		}
	}
	return str, nil
}

func (cfg *Config) paramOpArg(word *syntax.Word, ql quoteLevel) ([]fieldPart, string, error) {
	if word == nil {
		return nil, "", nil
	}
	if ql == quoteNone {
		fields, arg, err := cfg.paramOpArgFields(word, ql)
		if err != nil {
			return nil, "", err
		}
		if len(fields) == 0 {
			return nil, arg, nil
		}
		if len(fields) == 1 {
			return append([]fieldPart(nil), fields[0]...), arg, nil
		}
		parts := make([]fieldPart, 0, len(fields)*2)
		for i, field := range fields {
			if i > 0 {
				parts = append(parts, fieldPart{val: " "})
			}
			parts = append(parts, field...)
		}
		return parts, arg, nil
	}
	parts, err := cfg.paramArgField(word, ql)
	if err != nil {
		return nil, "", err
	}
	return parts, cfg.fieldJoin(parts), nil
}

func (cfg *Config) paramOpArgFields(word *syntax.Word, ql quoteLevel) ([][]fieldPart, string, error) {
	if word == nil {
		return nil, "", nil
	}
	if ql == quoteNone {
		parts, err := cfg.paramArgField(word, ql)
		if err != nil {
			return nil, "", err
		}
		fields := cfg.splitFieldParts(parts)
		strs := make([]string, len(fields))
		for i, field := range fields {
			strs[i] = cfg.fieldJoin(field)
		}
		return fields, strings.Join(strs, " "), nil
	}
	parts, arg, err := cfg.paramOpArg(word, ql)
	if err != nil {
		return nil, "", err
	}
	if parts == nil {
		return nil, arg, nil
	}
	return [][]fieldPart{parts}, arg, nil
}

func (cfg *Config) removePattern(str, pat string, fromEnd, shortest bool) (string, error) {
	mode := pattern.ExtendedOperators
	if shortest {
		mode |= pattern.Shortest
	}
	expr, err := cfg.paramPatternExpr(pat, mode)
	if err != nil {
		return str, err
	}
	switch {
	case fromEnd && shortest:
		// use .* to get the right-most shortest match
		expr = ".*(" + expr + ")$"
	case fromEnd:
		// simple suffix
		expr = "(" + expr + ")$"
	default:
		// simple prefix
		expr = "^(" + expr + ")"
	}
	if loc := cfg.compileParamPattern(expr).findStringSubmatchIndex(str); loc != nil {
		// remove the original pattern (the submatch)
		str = str[:loc[2]] + str[loc[3]:]
	}
	return str, nil
}

func (cfg *Config) varInd(name string, vr Variable, idx *syntax.Subscript) (string, error) {
	if idx == nil {
		return vr.String(), nil
	}
	if idx.AllElements() {
		switch vr.Kind {
		case String:
			return vr.Str, nil
		case Indexed:
			if idx.Kind == syntax.SubscriptStar {
				return cfg.ifsJoin(vr.IndexedValues()), nil
			}
			return strings.Join(vr.IndexedValues(), " "), nil
		case Associative:
			// Iterate values in bash-compatible key order.
			keys := sortedMapKeys(vr.Map)
			strs := make([]string, len(keys))
			for i, k := range keys {
				strs[i] = vr.Map[k]
			}
			if idx.Kind == syntax.SubscriptStar {
				return cfg.ifsJoin(strs), nil
			}
			return strings.Join(strs, " "), nil
		default:
			return "", nil
		}
	}

	switch resolvedSubscriptMode(idx) {
	case syntax.SubscriptIndexed:
		switch vr.Kind {
		case String:
			n, err := Arithm(cfg, idx.Expr)
			if err != nil {
				return "", err
			}
			if n == 0 {
				return vr.Str, nil
			}
			return "", nil
		case Indexed:
			i, err := Arithm(cfg, idx.Expr)
			if err != nil {
				return "", err
			}
			if i < 0 {
				resolved, ok := vr.IndexedResolve(i)
				if !ok {
					return "", BadArraySubscriptError{Name: name}
				}
				i = resolved
			}
			if val, ok := vr.IndexedGet(i); ok {
				return val, nil
			}
		}
	case syntax.SubscriptAssociative:
		if vr.Kind != Associative {
			return "", nil
		}
		val, err := cfg.associativeSubscriptKey(idx)
		if err != nil {
			return "", err
		}
		return vr.Map[val], nil
	}
	return "", nil
}

func (cfg *Config) namesByPrefix(prefix string) []string {
	var names []string
	seen := make(map[string]struct{})
	for name := range cfg.Env.Each {
		if strings.HasPrefix(name, prefix) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			if cfg.Env.Get(name).IsSet() {
				names = append(names, name)
			}
		}
	}
	slices.Sort(names)
	return names
}
