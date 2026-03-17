// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ewhauser/gbash/third_party/mvdan-sh/pattern"
	"github.com/ewhauser/gbash/third_party/mvdan-sh/syntax"
)

func nodeLit(node syntax.Node) string {
	if word, ok := node.(*syntax.Word); ok {
		return word.Lit()
	}
	return ""
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
}

func (cfg *Config) paramExpState(pe *syntax.ParamExp) (paramExpState, error) {
	state := paramExpState{
		name:       pe.Param.Value,
		callVarInd: true,
	}

	index := pe.Index
	switch state.name {
	case "@", "*":
		index = &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: state.name},
		}}
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
	_, state.vr = state.vr.Resolve(cfg.Env)
	if cfg.NoUnset && !state.vr.IsSet() && !overridingUnset(pe) {
		return state, UnsetParameterError{
			Node:    pe,
			Message: "unbound variable",
		}
	}

	switch nodeLit(index) {
	case "@", "*":
		switch state.vr.Kind {
		case Unknown:
			state.indexAllElements = true
		case Indexed:
			state.indexAllElements = true
			state.callVarInd = false
			state.elems = cfg.sliceElems(pe, state.vr.List, state.name == "@" || state.name == "*")
			state.str = strings.Join(state.elems, " ")
		case Associative:
			state.indexAllElements = true
			state.callVarInd = false
			state.elems = slices.Sorted(maps.Values(state.vr.Map))
			state.str = strings.Join(state.elems, " ")
		}
	}
	if state.callVarInd {
		var err error
		state.str, err = cfg.varInd(state.vr, index)
		if err != nil {
			return state, err
		}
	}
	if !state.indexAllElements {
		state.elems = []string{state.str}
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
		case *syntax.ProcSubst:
			path, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: path})
		case *syntax.ExtGlob:
			field = append(field, fieldPart{val: wp.Op.String() + wp.Pattern.Value + ")"})
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
		case name == "@", name == "*", nodeLit(pe.Index) == "@", nodeLit(pe.Index) == "*":
		default:
			n = utf8.RuneCountInString(str)
		}
		str = strconv.Itoa(n)
	case pe.Excl:
		var strs []string
		switch {
		case pe.Names != 0:
			strs = cfg.namesByPrefix(pe.Param.Value)
		case orig.Kind == NameRef:
			strs = append(strs, orig.Str)
		case pe.Index != nil && vr.Kind == Indexed:
			for i, e := range vr.List {
				if e != "" {
					strs = append(strs, strconv.Itoa(i))
				}
			}
		case pe.Index != nil && vr.Kind == Associative:
			strs = slices.AppendSeq(strs, maps.Keys(vr.Map))
		case !vr.IsSet():
			return "", fmt.Errorf("invalid indirect expansion")
		case str == "":
			return "", nil
		default:
			vr = cfg.Env.Get(str)
			strs = append(strs, vr.String())
		}
		slices.Sort(strs)
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
			arg, err := Literal(cfg, pe.Exp.Word)
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
				expr, err := pattern.Regexp(arg, 0)
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
	var mode pattern.Mode
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

func (cfg *Config) varInd(vr Variable, idx syntax.ArithmExpr) (string, error) {
	if idx == nil {
		return vr.String(), nil
	}
	switch vr.Kind {
	case String:
		n, err := Arithm(cfg, idx)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return vr.Str, nil
		}
	case Indexed:
		switch nodeLit(idx) {
		case "*", "@":
			return strings.Join(vr.List, " "), nil
		}
		i, err := Arithm(cfg, idx)
		if err != nil {
			return "", err
		}
		if i < 0 {
			return "", fmt.Errorf("negative array index")
		}
		if i < len(vr.List) {
			return vr.List[i], nil
		}
	case Associative:
		switch lit := nodeLit(idx); lit {
		case "@", "*":
			strs := slices.Sorted(maps.Values(vr.Map))
			if lit == "*" {
				return cfg.ifsJoin(strs), nil
			}
			return strings.Join(strs, " "), nil
		}
		val, err := Literal(cfg, idx.(*syntax.Word))
		if err != nil {
			return "", err
		}
		return vr.Map[val], nil
	}
	return "", nil
}

func (cfg *Config) namesByPrefix(prefix string) []string {
	var names []string
	for name := range cfg.Env.Each {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names
}
