// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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

func indirectParamExp(name string) (*syntax.ParamExp, error) {
	ref, err := parseVarRef(name)
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
	return nil, fmt.Errorf("invalid indirect expansion")
}

func (cfg *Config) indirectValue(name string) (string, error) {
	target, err := indirectParamExp(name)
	if err != nil {
		return "", err
	}
	switch target.Param.Value {
	case "@", "*":
		return cfg.ifsJoin(cfg.Env.Get(target.Param.Value).IndexedValues()), nil
	}
	ref := &syntax.VarRef{
		Name:  target.Param,
		Index: target.Index,
	}
	return cfg.varRef(ref)
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
	return fmt.Sprintf("%s: %s", u.Node.Param.Value, u.Message)
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
	vr               Variable
	str              string
	elems            []string
	indexAllElements bool
	callVarInd       bool
	swallowedError   bool
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

func (cfg *Config) transformArrayElems(pe *syntax.ParamExp, state paramExpState, elems []string) ([]string, error) {
	elems = slices.Clone(elems)
	if pe.Repl != nil {
		orig, err := Pattern(cfg, pe.Repl.Orig)
		if err != nil {
			return nil, err
		}
		if orig == "" {
			return elems, nil
		}
		with, err := Literal(cfg, pe.Repl.With)
		if err != nil {
			return nil, err
		}
		n := 1
		if pe.Repl.All {
			n = -1
		}
		for i, elem := range elems {
			locs := findAllIndex(orig, elem, n)
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
			elems[i] = removePattern(elem, arg, suffix, small)
		}
	case syntax.UpperFirst, syntax.UpperAll,
		syntax.LowerFirst, syntax.LowerAll:
		arg, err := Pattern(cfg, pe.Exp.Pattern)
		if err != nil {
			return nil, err
		}
		transform, err := transformCasePattern(arg, op)
		if err != nil {
			return elems, nil
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
		case "Q":
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
			for i := range elems {
				elems[i] = flags
			}
		case "P":
			// TODO: implement prompt expansion (\u, \h, \w, etc.).
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
	if arrayExpansionIsStar(pe) {
		fields := cfg.splitFieldParts([]fieldPart{{val: cfg.ifsJoin(elems)}})
		out := make([][]fieldPart, 0, len(fields))
		for _, field := range fields {
			out = append(out, append([]fieldPart(nil), field...))
		}
		return out, true, nil
	}
	var fields [][]fieldPart
	for _, elem := range elems {
		for _, field := range cfg.splitFieldParts([]fieldPart{{val: elem}}) {
			fields = append(fields, append([]fieldPart(nil), field...))
		}
	}
	return fields, true, nil
}

func (cfg *Config) paramExpState(pe *syntax.ParamExp) (paramExpState, error) {
	state := paramExpState{
		name:       pe.Param.Value,
		callVarInd: true,
	}

	index := pe.Index
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
		line := uint64(cfg.curParam.Pos().Line())
		state.vr = Variable{Set: true, Kind: String, Str: strconv.FormatUint(line, 10)}
	default:
		state.vr = cfg.Env.Get(state.name)
	}
	state.orig = state.vr
	resolvedRef, resolvedVar, err := state.vr.ResolveRef(cfg.Env, &syntax.VarRef{
		Name:  pe.Param,
		Index: index,
	})
	if err != nil {
		return state, err
	}
	state.vr = resolvedVar
	if resolvedRef != nil {
		index = resolvedRef.Index
	} else {
		index = nil
	}
	if cfg.NoUnset && !state.vr.IsSet() && !overridingUnset(pe) {
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
			state.elems = cfg.sliceElems(pe, state.vr.IndexedValues(), state.vr.IndexedIndices(), state.name == "@" || state.name == "*")
			state.str = strings.Join(state.elems, " ")
		case Associative:
			state.indexAllElements = true
			state.callVarInd = false
			state.elems = cfg.sliceElems(pe, sortedMapValues(state.vr.Map), nil, false)
			state.str = strings.Join(state.elems, " ")
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
				if prefix, rest := cfg.expandUser(s, len(word.Parts) > 1); prefix != "" {
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
			n, err := Arithm(cfg, wp.X)
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
			pat, err := cfg.extGlobString(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: pat})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	return field, nil
}

func (cfg *Config) paramExpWordField(pe *syntax.ParamExp, ql quoteLevel) ([]fieldPart, bool, error) {
	if pe.Exp == nil || pe.Excl || pe.Length || pe.Width || pe.IsSet || pe.Repl != nil || pe.Slice != nil {
		return nil, false, nil
	}
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()
	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, err
	}
	argField := func() ([]fieldPart, string, error) {
		var parts []fieldPart
		if pe.Exp.Word != nil {
			parts, err = cfg.paramArgField(pe.Exp.Word, ql)
			if err != nil {
				return nil, "", err
			}
		}
		return parts, cfg.fieldJoin(parts), nil
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

func (cfg *Config) paramExpFields(pe *syntax.ParamExp) ([][]fieldPart, bool, error) {
	if pe.Excl || pe.Length || pe.Width || pe.IsSet {
		return nil, false, nil
	}
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()
	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, err
	}
	if state.swallowedError && !state.indexAllElements {
		return [][]fieldPart{}, true, nil
	}
	fields0, elems, isArray := cfg.quotedArrayFields(pe)
	if isArray {
		argFields := func() ([][]fieldPart, string, error) {
			var fields [][]fieldPart
			arg := ""
			if pe.Exp != nil && pe.Exp.Word != nil {
				parts, err := cfg.paramArgField(pe.Exp.Word, quoteNone)
				if err != nil {
					return nil, "", err
				}
				arg = cfg.fieldJoin(parts)
				fields = cfg.splitFieldParts(parts)
			}
			return fields, arg, nil
		}
		hasElems := len(elems) > 0
		null := arrayExpansionNull(pe, fields0, elems)
		if pe.Exp != nil {
			switch op := pe.Exp.Op; op {
			case syntax.AlternateUnset:
				if hasElems {
					fields, _, err := argFields()
					return fields, true, err
				}
				return cfg.arrayParamFields(pe, state, elems)
			case syntax.AlternateUnsetOrNull:
				if !null {
					fields, _, err := argFields()
					return fields, true, err
				}
				return cfg.arrayParamFields(pe, state, elems)
			case syntax.DefaultUnset:
				if !hasElems {
					fields, _, err := argFields()
					return fields, true, err
				}
			case syntax.DefaultUnsetOrNull:
				if null {
					fields, _, err := argFields()
					return fields, true, err
				}
			case syntax.AssignUnset:
				if !hasElems {
					fields, arg, err := argFields()
					if err != nil {
						return nil, false, err
					}
					if err := cfg.envSet(state.name, arg); err != nil {
						return nil, false, err
					}
					return fields, true, nil
				}
			case syntax.AssignUnsetOrNull:
				if null {
					fields, arg, err := argFields()
					if err != nil {
						return nil, false, err
					}
					if err := cfg.envSet(state.name, arg); err != nil {
						return nil, false, err
					}
					return fields, true, nil
				}
			case syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
				return nil, false, nil
			}
		}
		return cfg.arrayParamFields(pe, state, elems)
	}
	if pe.Exp == nil || pe.Repl != nil || pe.Slice != nil {
		return nil, false, nil
	}
	argFields := func() ([][]fieldPart, string, error) {
		var fields [][]fieldPart
		arg := ""
		if pe.Exp.Word != nil {
			parts, err := cfg.paramArgField(pe.Exp.Word, quoteNone)
			if err != nil {
				return nil, "", err
			}
			arg = cfg.fieldJoin(parts)
			fields = cfg.splitFieldParts(parts)
		}
		return fields, arg, nil
	}

	switch op := pe.Exp.Op; op {
	case syntax.AlternateUnset:
		if state.vr.IsSet() {
			fields, _, err := argFields()
			return fields, true, err
		}
	case syntax.AlternateUnsetOrNull:
		if state.str != "" {
			fields, _, err := argFields()
			return fields, true, err
		}
	case syntax.DefaultUnset:
		if !state.vr.IsSet() {
			fields, _, err := argFields()
			return fields, true, err
		}
	case syntax.DefaultUnsetOrNull:
		if state.str == "" {
			fields, _, err := argFields()
			return fields, true, err
		}
	case syntax.AssignUnset:
		if !state.vr.IsSet() {
			fields, arg, err := argFields()
			if err != nil {
				return nil, false, err
			}
			if err := cfg.envSet(state.name, arg); err != nil {
				return nil, false, err
			}
			return fields, true, nil
		}
	case syntax.AssignUnsetOrNull:
		if state.str == "" {
			fields, arg, err := argFields()
			if err != nil {
				return nil, false, err
			}
			if err := cfg.envSet(state.name, arg); err != nil {
				return nil, false, err
			}
			return fields, true, nil
		}
	}
	return nil, false, nil
}

func (cfg *Config) paramExp(pe *syntax.ParamExp, ql quoteLevel) (string, error) {
	oldParam := cfg.curParam
	cfg.curParam = pe
	defer func() { cfg.curParam = oldParam }()

	state, err := cfg.paramExpState(pe)
	if err != nil {
		return "", err
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
			n = utf8.RuneCountInString(str)
		}
		str = strconv.Itoa(n)
	case pe.Excl:
		var strs []string
		assocKeys := false
		switch {
		case pe.Names != 0:
			strs = cfg.namesByPrefix(pe.Param.Value)
		case orig.Kind == NameRef:
			strs = append(strs, orig.Str)
		case pe.Index != nil && vr.Kind == Indexed:
			for _, index := range vr.IndexedIndices() {
				strs = append(strs, strconv.Itoa(index))
			}
		case pe.Index != nil && vr.Kind == Associative:
			strs = sortedMapKeys(vr.Map)
			assocKeys = true
		case !vr.IsSet():
			return "", fmt.Errorf("invalid indirect expansion")
		case str == "":
			return "", nil
		default:
			val, err := cfg.indirectValue(str)
			if err != nil {
				return "", err
			}
			strs = append(strs, val)
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
			slicePos := func(n int) int {
				if n < 0 {
					n = len(str) + n
					if n < 0 {
						n = len(str)
					}
				} else if n > len(str) {
					n = len(str)
				}
				return n
			}
			if pe.Slice.Offset != nil {
				str = str[slicePos(sliceOffset):]
			}
			if pe.Slice.Length != nil {
				str = str[:slicePos(sliceLen)]
			}
		} // else, elems are already sliced
	case pe.Repl != nil:
		orig, err := Pattern(cfg, pe.Repl.Orig)
		if err != nil {
			return "", err
		}
		if orig == "" {
			break // nothing to replace
		}
		with, err := Literal(cfg, pe.Repl.With)
		if err != nil {
			return "", err
		}
		n := 1
		if pe.Repl.All {
			n = -1
		}
		locs := findAllIndex(orig, str, n)
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
			var argField []fieldPart
			if pe.Exp.Word != nil {
				argField, err = cfg.paramArgField(pe.Exp.Word, ql)
				if err != nil {
					return "", err
				}
			}
			arg := cfg.fieldJoin(argField)
			switch op {
			case syntax.AlternateUnsetOrNull:
				if str == "" {
					break
				}
				fallthrough
			case syntax.AlternateUnset:
				if vr.IsSet() {
					str = arg
				}
			case syntax.DefaultUnset:
				if vr.IsSet() {
					break
				}
				fallthrough
			case syntax.DefaultUnsetOrNull:
				if str == "" {
					str = arg
				}
			case syntax.AssignUnset:
				if vr.IsSet() {
					break
				}
				fallthrough
			case syntax.AssignUnsetOrNull:
				if str == "" {
					if err := cfg.envSet(name, arg); err != nil {
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
					elems[i] = removePattern(elem, arg, suffix, small)
				}
				str = strings.Join(elems, " ")
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
				str = strings.Join(elems, " ")
			case syntax.OtherParamOps:
				switch arg {
				case "Q":
					str, err = syntax.Quote(str, syntax.LangBash)
					if err != nil {
						panic(err)
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
					str = orig.Flags()
				case "A":
					flags := orig.Flags()
					quoted, err := syntax.Quote(str, syntax.LangBash)
					if err != nil {
						return "", err
					}
					if flags == "" {
						str = fmt.Sprintf("%s=%s", name, quoted)
					} else {
						str = fmt.Sprintf("declare -%s %s=%s", flags, name, quoted)
					}
				case "P":
					// TODO: implement prompt expansion (\u, \h, \w, etc.).
				default:
					panic(fmt.Sprintf("unexpected @%s param expansion", arg))
				}
			}
		}
	}
	return str, nil
}

func removePattern(str, pat string, fromEnd, shortest bool) string {
	mode := pattern.ExtendedOperators
	if shortest {
		mode |= pattern.Shortest
	}
	expr, err := pattern.Regexp(pat, mode)
	if err != nil {
		return str
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
	// no need to check error as Translate returns one
	rx := regexp.MustCompile(expr)
	if loc := rx.FindStringSubmatchIndex(str); loc != nil {
		// remove the original pattern (the submatch)
		str = str[:loc[2]] + str[loc[3]:]
	}
	return str
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
	return names
}
