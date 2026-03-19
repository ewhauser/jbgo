// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"maps"
	mathrand "math/rand/v2"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func newOverlayEnviron(parent expand.Environ, background bool) *overlayEnviron {
	oenv := &overlayEnviron{}
	if !background {
		oenv.parent = parent
	} else {
		// We could do better here if the parent is also an overlayEnviron;
		// measure with profiles or benchmarks before we choose to do so.
		for name, vr := range parent.Each {
			oenv.Set(name, vr)
		}
	}
	return oenv
}

// overlayEnviron is our main implementation of [expand.WriteEnviron].
type overlayEnviron struct {
	// parent is non-nil if [values] is an overlay over a parent environment
	// which we can safely reuse without data races, such as non-background subshells
	// or function calls.
	parent expand.Environ

	// values maps normalized variable names, per [overlayEnviron.normalize].
	values map[string]namedVariable

	// We need to know if the current scope is a function's scope, because
	// functions can modify global variables. When true, [parent] must not be nil.
	funcScope bool
}

// namedVariable records the original name of a variable for platforms
// where variable names are matched in a case-insensitive way.
type namedVariable struct {
	// TODO(v4): consider adding this field to [expand.Variable],
	// as a general way for a variable to report its original name.
	// This can be useful for GOOS=windows with case insensitive env vars,
	// as otherwise it's not possible to Environ.Get a var
	// and know what was its original name without looping over Environ.Each.
	Name string
	expand.Variable
}

func (o *overlayEnviron) normalize(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func (o *overlayEnviron) Get(name string) expand.Variable {
	normalized := o.normalize(name)
	if vr, ok := o.values[normalized]; ok {
		return vr.Variable
	}
	if o.parent != nil {
		return o.parent.Get(name)
	}
	return expand.Variable{}
}

func (o *overlayEnviron) Set(name string, vr expand.Variable) error {
	normalized := o.normalize(name)
	prev, inOverlay := o.values[normalized]
	// Manipulation of a global var inside a function.
	if o.funcScope && !vr.Local && !prev.Local {
		// In a function, the parent environment is ours, so it's always read-write.
		return o.parent.(expand.WriteEnviron).Set(name, vr)
	}
	if !inOverlay && o.parent != nil {
		prev.Variable = o.parent.Get(name)
	}

	if o.values == nil {
		o.values = make(map[string]namedVariable)
	}
	if vr.Kind == expand.KeepValue {
		vr.Kind = prev.Kind
		vr.Str = prev.Str
		vr.List = prev.List
		vr.Map = prev.Map
	} else if prev.ReadOnly {
		return fmt.Errorf("readonly variable")
	}
	if !vr.IsSet() { // unsetting
		if prev.Local {
			vr.Local = true
			o.values[normalized] = namedVariable{name, vr}
			return nil
		}
		o.values[normalized] = namedVariable{name, vr}
		return nil
	}
	// modifying the entire variable
	vr.Local = prev.Local || vr.Local
	o.values[normalized] = namedVariable{name, vr}
	return nil
}

func (o *overlayEnviron) Each(f func(name string, vr expand.Variable) bool) {
	if o.parent != nil {
		o.parent.Each(f)
	}
	for _, vr := range o.values {
		if !f(vr.Name, vr.Variable) {
			return
		}
	}
}

func execEnv(env expand.Environ) []string {
	list := make([]string, 0, 64)
	for name, vr := range env.Each {
		if !vr.IsSet() {
			// If a variable is set globally but unset in the
			// runner, we need to ensure it's not part of the final
			// list. Seems like zeroing the element is enough.
			// This is a linear search, but this scenario should be
			// rare, and the number of variables shouldn't be large.
			for i, kv := range list {
				if strings.HasPrefix(kv, name+"=") {
					list[i] = ""
				}
			}
		}
		if vr.Exported && vr.Kind == expand.String {
			list = append(list, name+"="+vr.String())
		}
	}
	return list
}

func (r *Runner) lookupVar(name string) expand.Variable {
	if name == "" {
		panic("variable name must not be empty")
	}
	var vr expand.Variable
	if i, ok := positionalParamIndex(name); ok {
		switch {
		case i == 0:
			vr.Kind = expand.String
			if r.filename != "" {
				vr.Str = r.filename
			} else {
				vr.Str = "gosh"
			}
			vr.Set = true
			return vr
		case i-1 < len(r.Params):
			vr.Kind = expand.String
			vr.Str = r.Params[i-1]
			vr.Set = true
			return vr
		}
	}
	switch name {
	case "#":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(len(r.Params))
	case "@", "*":
		vr.Kind = expand.Indexed
		if r.Params == nil {
			// r.Params may be nil but positional parameters always exist
			vr.List = []string{}
		} else {
			vr.List = r.Params
		}
	case "!":
		if n := len(r.bgProcs); n > 0 {
			vr.Kind, vr.Str = expand.String, "g"+strconv.Itoa(n)
		}
	case "?":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.lastExit.code))
	case "$":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.pid)
	case "PPID":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.ppid)
	case "RANDOM": // not for cryptographic use
		vr.Kind, vr.Str = expand.String, strconv.Itoa(mathrand.IntN(32767))
		// TODO: support setting RANDOM to seed it
	case "SRANDOM": // pseudo-random generator from the system
		var p [4]byte
		cryptorand.Read(p[:])
		n := binary.NativeEndian.Uint32(p[:])
		vr.Kind, vr.Str = expand.String, strconv.FormatUint(uint64(n), 10)
	case "DIRSTACK":
		vr.Kind, vr.List = expand.Indexed, r.dirStack
	case "BASH_SOURCE":
		if stack := r.bashSourceStack(); len(stack) > 0 {
			vr.Kind, vr.List = expand.Indexed, stack
		}
	case "BASH_LINENO":
		if stack := r.bashLineNoStack(); len(stack) > 0 {
			vr.Kind, vr.List = expand.Indexed, stack
		}
	case "FUNCNAME":
		if stack := r.funcNameStack(); len(stack) > 0 {
			vr.Kind, vr.List = expand.Indexed, stack
		}
	}
	if vr.Kind != expand.Unknown {
		vr.Set = true
		return vr
	}
	if vr := r.writeEnv.Get(name); vr.Declared() {
		return vr
	}
	return expand.Variable{}
}

func positionalParamIndex(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	i, err := strconv.Atoi(name)
	if err != nil {
		return 0, false
	}
	return i, true
}

func (r *Runner) envGet(name string) string {
	return r.lookupVar(name).String()
}

func (r *Runner) delVar(name string) {
	if err := r.writeEnv.Set(name, expand.Variable{}); err != nil {
		r.errf("%s: %v\n", name, err)
		r.exit.code = 1
		return
	}
}

func (r *Runner) setVarString(name, value string) {
	r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
}

func (r *Runner) setVar(name string, vr expand.Variable) {
	if r.opts[optAllExport] {
		vr.Exported = true
	}
	if err := r.writeEnv.Set(name, vr); err != nil {
		r.errf("%s: %v\n", name, err)
		r.exit.code = 1
		return
	}
}

// applyVarAttrs transforms the variable's value based on its attributes
// (integer, lowercase, uppercase).
func (r *Runner) applyVarAttrs(vr *expand.Variable) {
	if !vr.IsSet() {
		return
	}
	if vr.Integer && vr.Kind == expand.String {
		vr.Str = strconv.Itoa(r.evalIntegerAttr(vr.Str))
	}
	if vr.Lower && vr.Kind == expand.String {
		vr.Str = strings.ToLower(vr.Str)
	}
	if vr.Upper && vr.Kind == expand.String {
		vr.Str = strings.ToUpper(vr.Str)
	}
}

// evalIntegerAttr evaluates a string as a shell arithmetic expression,
// used for the -i variable attribute.
func (r *Runner) evalIntegerAttr(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	p := syntax.NewParser()
	expr, err := p.Arithmetic(strings.NewReader(s))
	if err != nil {
		// Fall back to simple integer parsing on syntax error.
		n, _ := strconv.ParseInt(s, 10, 64)
		return int(n)
	}
	return r.arithm(expr)
}

func (r *Runner) setVarWithIndex(prev expand.Variable, name string, index syntax.ArithmExpr, vr expand.Variable) {
	prev.Set = true
	if name2, var2 := prev.Resolve(r.writeEnv); name2 != "" {
		name = name2
		prev = var2
	}

	if vr.Kind == expand.String && index == nil {
		// When assigning a string to an array, fall back to the
		// zero value for the index.
		switch prev.Kind {
		case expand.Indexed:
			index = &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "0"},
			}}
		case expand.Associative:
			index = &syntax.Word{Parts: []syntax.WordPart{
				&syntax.DblQuoted{},
			}}
		}
	}
	if index == nil {
		r.setVar(name, vr)
		return
	}

	// from the syntax package, we know that value must be a string if index
	// is non-nil; nested arrays are forbidden.
	valStr := vr.Str

	var list []string
	switch prev.Kind {
	case expand.String:
		list = append(list, prev.Str)
	case expand.Indexed:
		// TODO: only clone when inside a subshell and getting a var from outside for the first time
		list = slices.Clone(prev.List)
	case expand.Associative:
		// if the existing variable is already an AssocArray, try our
		// best to convert the key to a string
		w, ok := index.(*syntax.Word)
		if !ok {
			return
		}
		k := r.literal(w)

		// TODO: only clone when inside a subshell and getting a var from outside for the first time
		prev.Map = maps.Clone(prev.Map)
		if prev.Map == nil {
			prev.Map = make(map[string]string)
		}
		prev.Map[k] = valStr
		r.setVar(name, prev)
		return
	}
	k := r.arithm(index)
	for len(list) < k+1 {
		list = append(list, "")
	}
	list[k] = valStr
	prev.Kind = expand.Indexed
	prev.List = list
	r.setVar(name, prev)
}

func (r *Runner) setFunc(name string, body *syntax.Stmt) {
	if r.funcs == nil {
		r.funcs = make(map[string]*syntax.Stmt, 4)
	}
	r.funcs[name] = body
	r.setFuncSource(name, r.currentDefinitionSource())
	r.setFuncInternal(name, r.currentInternal())
}

type shadowWriteEnviron struct {
	parent     expand.WriteEnviron
	shadow     expand.Variable
	shadowName string
	shadowSet  bool
}

func (e *shadowWriteEnviron) Get(name string) expand.Variable {
	if e.shadowSet && name == e.shadowName {
		return e.shadow
	}
	return e.parent.Get(name)
}

func (e *shadowWriteEnviron) Set(name string, vr expand.Variable) error {
	if name == e.shadowName {
		e.shadow = vr
		e.shadowSet = true
		return nil
	}
	return e.parent.Set(name, vr)
}

func (e *shadowWriteEnviron) Each(fn func(name string, vr expand.Variable) bool) {
	seenShadow := false
	stopped := false
	e.parent.Each(func(name string, vr expand.Variable) bool {
		if e.shadowSet && name == e.shadowName {
			seenShadow = true
			if !fn(name, e.shadow) {
				stopped = true
				return false
			}
			return true
		}
		if !fn(name, vr) {
			stopped = true
			return false
		}
		return true
	})
	if e.shadowSet && !seenShadow && !stopped {
		fn(e.shadowName, e.shadow)
	}
}

type expandedArrayElem struct {
	kind   syntax.ArrayElemKind
	index  *syntax.Subscript
	fields []string
	value  string
}

func declArrayModeFromValueType(valType string) syntax.ArrayExprMode {
	switch valType {
	case "-a":
		return syntax.ArrayExprIndexed
	case "-A":
		return syntax.ArrayExprAssociative
	default:
		return syntax.ArrayExprInherit
	}
}

func arrayExprModeFromValueKind(kind expand.ValueKind) syntax.ArrayExprMode {
	switch kind {
	case expand.Indexed:
		return syntax.ArrayExprIndexed
	case expand.Associative:
		return syntax.ArrayExprAssociative
	default:
		return syntax.ArrayExprInherit
	}
}

func arrayValueKind(mode syntax.ArrayExprMode) expand.ValueKind {
	switch mode {
	case syntax.ArrayExprAssociative:
		return expand.Associative
	default:
		return expand.Indexed
	}
}

func resolveArrayExprMode(prev expand.Variable, as *syntax.Assign, valType string) syntax.ArrayExprMode {
	if as != nil && as.Array != nil && as.Array.Mode != syntax.ArrayExprInherit {
		return as.Array.Mode
	}
	if mode := declArrayModeFromValueType(valType); mode != syntax.ArrayExprInherit {
		return mode
	}
	if mode := arrayExprModeFromValueKind(prev.Kind); mode != syntax.ArrayExprInherit {
		return mode
	}
	return syntax.ArrayExprIndexed
}

func (r *Runner) resolvedCompoundArrayTarget(prev expand.Variable, ref *syntax.VarRef) (string, expand.Variable) {
	if ref == nil {
		return "", prev
	}
	name := ref.Name.Value
	resolvedRef, _, err := prev.ResolveRef(r.writeEnv, ref)
	if err == nil && resolvedRef != nil && resolvedRef.Index == nil {
		name = resolvedRef.Name.Value
		return name, r.lookupVar(name)
	}
	return name, prev
}

func (r *Runner) expandCompoundArrayElems(elems []*syntax.ArrayElem) []expandedArrayElem {
	expanded := make([]expandedArrayElem, 0, len(elems))
	for _, elem := range elems {
		item := expandedArrayElem{
			kind:  elem.Kind,
			index: elem.Index,
		}
		switch elem.Kind {
		case syntax.ArrayElemSequential:
			if elem.Value != nil {
				item.fields = r.fields(elem.Value)
			}
		default:
			if elem.Value != nil {
				item.value = r.literal(elem.Value)
			}
		}
		expanded = append(expanded, item)
		if r.exit.fatalExit || r.exit.exiting {
			break
		}
	}
	return expanded
}

func compoundArrayBase(prev expand.Variable, mode syntax.ArrayExprMode, appendAssign bool) expand.Variable {
	base := expand.Variable{
		Set:  true,
		Kind: arrayValueKind(mode),
	}
	if !appendAssign {
		if base.Kind == expand.Associative {
			base.Map = make(map[string]string)
		}
		return base
	}
	switch base.Kind {
	case expand.Indexed:
		switch prev.Kind {
		case expand.String:
			base.List = []string{prev.Str}
		case expand.Indexed:
			base.List = slices.Clone(prev.List)
		}
	case expand.Associative:
		if prev.Kind == expand.Associative {
			base.Map = maps.Clone(prev.Map)
		} else {
			base.Map = make(map[string]string)
		}
	}
	return base
}

func indexedAssign(list []string, index int, value string, appendValue bool) []string {
	for len(list) < index+1 {
		list = append(list, "")
	}
	if appendValue {
		list[index] += value
	} else {
		list[index] = value
	}
	return list
}

func (r *Runner) associativeArrayKey(index *syntax.Subscript) string {
	if index == nil {
		return ""
	}
	if word, ok := index.Expr.(*syntax.Word); ok {
		return r.literal(word)
	}
	var sb strings.Builder
	if err := syntax.NewPrinter().Print(&sb, index.Expr); err != nil {
		return ""
	}
	return sb.String()
}

func (r *Runner) assignArray(prev expand.Variable, as *syntax.Assign, valType string) expand.Variable {
	targetName, targetPrev := r.resolvedCompoundArrayTarget(prev, as.Ref)
	mode := resolveArrayExprMode(targetPrev, as, valType)
	elems := r.expandCompoundArrayElems(as.Array.Elems)
	vr := compoundArrayBase(targetPrev, mode, as.Append)
	origEnv := r.writeEnv
	shadowEnv := &shadowWriteEnviron{
		parent:     origEnv,
		shadowName: targetName,
		shadow:     vr,
		shadowSet:  targetName != "",
	}
	r.writeEnv = shadowEnv
	defer func() {
		r.writeEnv = origEnv
	}()

	if mode == syntax.ArrayExprAssociative {
		pendingKey := ""
		hasPendingKey := false
		flushPending := func() {
			if !hasPendingKey {
				return
			}
			if shadowEnv.shadow.Map == nil {
				shadowEnv.shadow.Map = make(map[string]string)
			}
			shadowEnv.shadow.Map[pendingKey] = ""
			hasPendingKey = false
			pendingKey = ""
		}
		for _, elem := range elems {
			switch elem.kind {
			case syntax.ArrayElemSequential:
				for _, field := range elem.fields {
					if !hasPendingKey {
						pendingKey = field
						hasPendingKey = true
						continue
					}
					if shadowEnv.shadow.Map == nil {
						shadowEnv.shadow.Map = make(map[string]string)
					}
					shadowEnv.shadow.Map[pendingKey] = field
					hasPendingKey = false
					pendingKey = ""
				}
			case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
				flushPending()
				key := r.associativeArrayKey(elem.index)
				if shadowEnv.shadow.Map == nil {
					shadowEnv.shadow.Map = make(map[string]string)
				}
				if elem.kind == syntax.ArrayElemKeyedAppend {
					shadowEnv.shadow.Map[key] += elem.value
				} else {
					shadowEnv.shadow.Map[key] = elem.value
				}
			}
			shadowEnv.shadow.Kind = expand.Associative
			shadowEnv.shadow.Set = true
			if r.exit.fatalExit || r.exit.exiting {
				break
			}
		}
		flushPending()
		return shadowEnv.shadow
	}

	nextIndex := len(shadowEnv.shadow.List)
	for _, elem := range elems {
		switch elem.kind {
		case syntax.ArrayElemSequential:
			for _, field := range elem.fields {
				shadowEnv.shadow.List = indexedAssign(shadowEnv.shadow.List, nextIndex, field, false)
				nextIndex++
			}
		case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
			index := r.arithm(elem.index.Expr)
			shadowEnv.shadow.List = indexedAssign(shadowEnv.shadow.List, index, elem.value, elem.kind == syntax.ArrayElemKeyedAppend)
			nextIndex = index + 1
		}
		shadowEnv.shadow.Kind = expand.Indexed
		shadowEnv.shadow.Set = true
		if r.exit.fatalExit || r.exit.exiting {
			break
		}
	}
	return shadowEnv.shadow
}

// TODO: make assignVal and [setVar] consistent with the [expand.WriteEnviron] interface

func (r *Runner) assignVal(prev expand.Variable, as *syntax.Assign, valType string) expand.Variable {
	prev.Set = true
	if as.Value != nil {
		s := r.literal(as.Value)
		if !as.Append {
			prev.Kind = expand.String
			if valType == "-n" {
				prev.Kind = expand.NameRef
			}
			prev.Str = s
			return prev
		}
		switch prev.Kind {
		case expand.String, expand.Unknown:
			prev.Kind = expand.String
			prev.Str += s
		case expand.Indexed:
			if len(prev.List) == 0 {
				prev.List = append(prev.List, "")
			}
			prev.List[0] += s
		case expand.Associative:
			// TODO
		}
		return prev
	}
	if as.Array == nil {
		// don't return the zero value, as that's an unset variable
		prev.Kind = expand.String
		if valType == "-n" {
			prev.Kind = expand.NameRef
		}
		prev.Str = ""
		return prev
	}
	return r.assignArray(prev, as, valType)
}
