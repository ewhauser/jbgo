// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"maps"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

const loopIterHelperCommand = "__jb_loop_iter"

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
	if parentWrite, ok := parent.(expand.WriteEnviron); ok {
		if optEnv, optVar, ok := visibleBindingWriteEnv(parentWrite, "OPTIND"); ok {
			if state := getoptsStateForEnv(optEnv); state != nil {
				oenv.optState = *state
			}
			if !background {
				if err := oenv.Set("OPTIND", optVar); err != nil {
					panic(fmt.Sprintf("copy OPTIND into overlay: %v", err))
				}
			}
		}
		if background {
			if secondsEnv, _, ok := visibleSecondsBinding(parentWrite); ok {
				if started, ok := secondsStartTimeForEnv(secondsEnv); ok {
					oenv.secondsStartTime = started
					oenv.secondsStartSet = true
				}
			}
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

	// optState tracks clustered getopts progress for the OPTIND binding visible
	// in this scope.
	optState getopts

	// secondsStartTime tracks the SECONDS baseline for this scope when that
	// binding is visible from here.
	secondsStartTime time.Time
	secondsStartSet  bool
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
	if !inOverlay && o.parent != nil {
		prev.Variable = o.parent.Get(name)
	}
	if o.funcScope && !vr.Local && !inOverlay {
		// Functions use dynamic scope: writes to non-local names should walk
		// outward until they reach the defining scope, including caller locals.
		return o.parent.(expand.WriteEnviron).Set(name, vr)
	}

	if o.values == nil {
		o.values = make(map[string]namedVariable)
	}
	if vr.Kind == expand.KeepValue {
		vr.Kind = prev.Kind
		vr.Str = prev.Str
		vr.List = prev.List
		vr.Indices = prev.Indices
		vr.Map = prev.Map
	} else if prev.ReadOnly {
		return fmt.Errorf("readonly variable")
	}
	if !vr.IsSet() { // unsetting
		// Same-scope unsets keep a local shadow so later lookups don't reveal
		// the next outer binding inside the same function scope.
		vr.Local = prev.Local || vr.Local
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
			continue
		}
		if !vr.Exported || (vr.Kind != expand.String && vr.Kind != expand.NameRef) {
			for i, kv := range list {
				if strings.HasPrefix(kv, name+"=") {
					list[i] = ""
				}
			}
			continue
		}
		value := vr.String()
		if vr.Kind == expand.NameRef {
			value = vr.Str
		}
		list = append(list, name+"="+value)
	}
	return list
}

func currentScopeVar(env expand.WriteEnviron, name string) (expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize(name)
		vr, ok := env.values[normalized]
		if !ok {
			return expand.Variable{}, false
		}
		return vr.Variable, true
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			return env.shadow, true
		}
		return currentScopeVar(env.parent, name)
	default:
		return expand.Variable{}, false
	}
}

func currentScopeVars(env expand.WriteEnviron, f func(name string, vr expand.Variable) bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		for _, vr := range env.values {
			if !f(vr.Name, vr.Variable) {
				return
			}
		}
	case *shadowWriteEnviron:
		if env.shadowSet && !f(env.shadowName, env.shadow) {
			return
		}
		currentScopeVars(env.parent, f)
	}
}

func visibleBindingWriteEnv(env expand.WriteEnviron, name string) (expand.WriteEnviron, expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize(name)
		vr, ok := env.values[normalized]
		if ok {
			return env, vr.Variable, true
		}
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok {
			return nil, expand.Variable{}, false
		}
		return visibleBindingWriteEnv(parent, name)
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			return env, env.shadow, true
		}
		return visibleBindingWriteEnv(env.parent, name)
	default:
		return nil, expand.Variable{}, false
	}
}

func getoptsStateForEnv(env expand.WriteEnviron) *getopts {
	switch env := env.(type) {
	case *overlayEnviron:
		return &env.optState
	case *shadowWriteEnviron:
		return &env.optState
	default:
		return nil
	}
}

func (r *Runner) currentGetoptsState() *getopts {
	if env, _, ok := visibleBindingWriteEnv(r.writeEnv, "OPTIND"); ok {
		if state := getoptsStateForEnv(env); state != nil {
			return state
		}
	}
	return &r.optState
}

func visibleSecondsBinding(env expand.WriteEnviron) (expand.WriteEnviron, expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize("SECONDS")
		if vr, ok := env.values[normalized]; ok {
			if vr.Variable.Declared() {
				return env, vr.Variable, true
			}
			return nil, expand.Variable{}, false
		}
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok {
			return nil, expand.Variable{}, false
		}
		return visibleSecondsBinding(parent)
	case *shadowWriteEnviron:
		if env.shadowSet && env.shadowName == "SECONDS" {
			if env.shadow.Declared() {
				return env, env.shadow, true
			}
			return nil, expand.Variable{}, false
		}
		return visibleSecondsBinding(env.parent)
	default:
		return nil, expand.Variable{}, false
	}
}

func secondsStartTimeForEnv(env expand.WriteEnviron) (time.Time, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		return env.secondsStartTime, env.secondsStartSet
	case *shadowWriteEnviron:
		return env.secondsStartTime, env.secondsStartSet
	default:
		return time.Time{}, false
	}
}

func setSecondsStartTimeForEnv(env expand.WriteEnviron, started time.Time) bool {
	switch env := env.(type) {
	case *overlayEnviron:
		env.secondsStartTime = started
		env.secondsStartSet = true
		return true
	case *shadowWriteEnviron:
		env.secondsStartTime = started
		env.secondsStartSet = true
		return true
	default:
		return false
	}
}

func deleteCurrentScopeVar(env expand.WriteEnviron, name string) bool {
	switch env := env.(type) {
	case *overlayEnviron:
		if env.values == nil {
			return false
		}
		normalized := env.normalize(name)
		if _, ok := env.values[normalized]; !ok {
			return false
		}
		delete(env.values, normalized)
		return true
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			env.shadow = expand.Variable{}
			env.shadowSet = false
			return true
		}
		return deleteCurrentScopeVar(env.parent, name)
	default:
		return false
	}
}

func localScopeEnv(env expand.WriteEnviron) expand.WriteEnviron {
	switch env := env.(type) {
	case *overlayEnviron:
		if env.funcScope {
			return env
		}
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok {
			return env
		}
		return localScopeEnv(parent)
	case *shadowWriteEnviron:
		return localScopeEnv(env.parent)
	default:
		return env
	}
}

func globalWriteEnv(env expand.WriteEnviron) expand.WriteEnviron {
	switch env := env.(type) {
	case *overlayEnviron:
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok {
			return env
		}
		return globalWriteEnv(parent)
	case *shadowWriteEnviron:
		return globalWriteEnv(env.parent)
	default:
		return env
	}
}

func (r *Runner) optionFlags() string {
	var flags strings.Builder
	for _, opt := range posixOptsTable {
		if opt.flag == ' ' {
			continue
		}
		enabled := r.posixOptByFlag(opt.flag)
		if enabled == nil || !*enabled {
			continue
		}
		flags.WriteByte(opt.flag)
	}
	if r.interactive {
		flags.WriteByte('i')
	}
	return flags.String()
}

func (r *Runner) printSetVar(name string, vr expand.Variable) {
	if !vr.IsSet() {
		return
	}
	switch vr.Kind {
	case expand.Indexed:
		r.outf("declare -a %s", name)
		r.out("=(")
		for i, index := range vr.IndexedIndices() {
			if i > 0 {
				r.out(" ")
			}
			val, _ := vr.IndexedGet(index)
			r.outf("[%d]=%s", index, bashDeclPrintValue(val))
		}
		r.out(")\n")
	case expand.Associative:
		r.outf("declare -A %s", name)
		r.out("=(")
		first := true
		for _, k := range expand.AssociativeKeys(vr.Map) {
			v := vr.Map[k]
			if !first {
				r.out(" ")
			}
			r.outf("[%s]=%s", bashDeclAssocKey(k), bashDeclPrintValue(v))
			first = false
		}
		if !first {
			r.out(" ")
		}
		r.out(")\n")
	default:
		r.outf("%s=%s\n", name, bashDeclPlainValue(vr.String()))
	}
}

func (r *Runner) printSetVars() {
	seen := make(map[string]expand.Variable)
	r.writeEnv.Each(func(name string, vr expand.Variable) bool {
		seen[name] = vr
		return true
	})
	names := make([]string, 0, len(seen))
	for name, vr := range seen {
		if !vr.Declared() || !vr.IsSet() {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		r.printSetVar(name, seen[name])
	}
}

func (r *Runner) lookupVar(name string) expand.Variable {
	if name == "" {
		panic("variable name must not be empty")
	}
	switch name {
	case "HOSTNAME", "OSTYPE":
		if r.writeEnv != nil {
			if oenv, ok := r.writeEnv.(*overlayEnviron); ok {
				if _, shadowed := oenv.values[oenv.normalize(name)]; shadowed {
					return oenv.Get(name)
				}
			}
			if vr := r.writeEnv.Get(name); vr.Declared() {
				return vr
			}
		}
	}
	var vr expand.Variable
	if i, ok := positionalParamIndex(name); ok {
		switch {
		case i == 0:
			vr.Kind = expand.String
			if r.filename != "" && r.filename != "stdin" {
				vr.Str = r.filename
			} else {
				vr.Str = "gbash"
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
	case "-":
		vr.Kind, vr.Str = expand.String, r.optionFlags()
	case "?":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.lastExit.code))
	case "$":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.pid)
	case "BASHPID":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.bashPID)
	case "PPID":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.ppid)
		vr.ReadOnly = true
	case "PIPESTATUS":
		if len(r.pipeStatuses) > 0 {
			vr.Kind, vr.List = expand.Indexed, append([]string(nil), r.pipeStatuses...)
		}
	case "RANDOM":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.nextRandom())
		// TODO: support setting RANDOM to seed it
	case "SRANDOM": // pseudo-random generator from the system
		var p [4]byte
		cryptorand.Read(p[:])
		n := binary.NativeEndian.Uint32(p[:])
		vr.Kind, vr.Str = expand.String, strconv.FormatUint(uint64(n), 10)
	case "SECONDS":
		started := r.startTime
		if env, _, ok := visibleSecondsBinding(r.writeEnv); ok {
			if scopedStart, ok := secondsStartTimeForEnv(env); ok {
				started = scopedStart
			}
		}
		seconds := 0
		if !started.IsZero() {
			seconds = int(time.Since(started) / time.Second)
		}
		vr.Kind, vr.Str = expand.String, strconv.Itoa(seconds)
	case "HOSTNAME":
		vr.Kind, vr.Str = expand.String, r.defaultHostname()
	case "OSTYPE":
		vr.Kind, vr.Str = expand.String, r.defaultOSType()
	case "SHELLOPTS":
		vr.Kind, vr.Str = expand.String, r.shellOptsValue()
		vr.ReadOnly = true
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
	case "BASH_VERSION":
		vr.Kind, vr.Str = expand.String, "5.2.0(1)-gbash"
		vr.ReadOnly = true
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

func (r *Runner) setOPTIND(value string) {
	vr := expand.Variable{Set: true, Kind: expand.String, Str: value}
	if prev := r.lookupVar("OPTIND"); prev.Exported || r.opts[optAllExport] {
		vr.Exported = true
	}
	if err := r.writeEnv.Set("OPTIND", vr); err != nil {
		r.errf("OPTIND: %v\n", err)
		r.exit.code = 1
	}
}

func (r *Runner) setExportedVarString(name, value string) {
	r.setVar(name, expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: value})
}

func (r *Runner) setSpecialUnderscore(value string) {
	r.setVarString("_", value)
}

func (r *Runner) setSpecialUnderscoreFromFields(fields []string) {
	if len(fields) == 0 || fields[0] == loopIterHelperCommand {
		return
	}
	r.setSpecialUnderscore(fields[len(fields)-1])
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
	r.afterSetVar(name, vr)
}

func (r *Runner) afterSetVar(name string, vr expand.Variable) {
	if name == "PATH" {
		r.commandHashClear()
	}
	if name == "OPTIND" {
		r.currentGetoptsState().reset()
	}
	if name == "SECONDS" && vr.IsSet() {
		seconds, err := strconv.ParseInt(strings.TrimSpace(vr.String()), 10, 64)
		if err != nil {
			seconds = 0
		}
		started := time.Now().Add(-time.Duration(seconds) * time.Second)
		if env, _, ok := visibleSecondsBinding(r.writeEnv); ok && setSecondsStartTimeForEnv(env, started) {
			return
		}
		r.startTime = started
	}
}

func (r *Runner) nextRandom() int {
	if r.random == 0 {
		started := r.startTime
		if started.IsZero() {
			started = r.origStart
		}
		r.random = randomSeed(r.bashPID, started)
	}
	r.random = r.random*1103515245 + 12345
	return int((r.random >> 16) & 0x7fff)
}

func (r *Runner) setPipeStatuses(statuses ...uint8) {
	values := make([]string, 0, len(statuses))
	for _, status := range statuses {
		values = append(values, strconv.Itoa(int(status)))
	}
	r.setPipeStatusValues(values)
}

func (r *Runner) setPipeStatusValues(values []string) {
	r.pipeStatuses = append(r.pipeStatuses[:0], values...)
	r.pipeStatusSet = true
}

func (r *Runner) pipeStatusValues() []string {
	return append([]string(nil), r.pipeStatuses...)
}

func (r *Runner) defaultHostname() string {
	if r.writeEnv != nil {
		if vr := r.writeEnv.Get("GBASH_UNAME_NODENAME"); vr.IsSet() {
			if value := strings.TrimSpace(vr.String()); value != "" {
				return value
			}
		}
	}
	return "gbash"
}

func (r *Runner) defaultOSType() string {
	switch runtime.GOOS {
	case "linux":
		return "linux-gnu"
	case "darwin":
		return "darwin"
	case "windows":
		return "msys"
	case "freebsd":
		return "freebsd"
	case "openbsd":
		return "openbsd"
	case "netbsd":
		return "netbsd"
	default:
		return runtime.GOOS
	}
}

func (r *Runner) shellOptsValue() string {
	names := []string{
		"braceexpand",
		"hashall",
		"interactive-comments",
	}
	for _, opt := range posixOptsTable {
		enabled := r.posixOptByName(opt.name)
		if enabled == nil || !*enabled {
			continue
		}
		names = append(names, opt.name)
	}
	slices.Sort(names)
	return strings.Join(names, ":")
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
	s = expandTrimArithSpace(s)
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

func expandTrimArithSpace(s string) string {
	return strings.Trim(s, " \t\n")
}

func (r *Runner) setFunc(name string, body *syntax.Stmt) {
	info, _ := r.funcInfo(name)
	info.body = body
	info.definitionSource = r.currentDefinitionSource()
	if source := r.sourceForNode(body); source != "" {
		info.bodySource = funcSourceSpan{text: source, base: body.Pos().Offset()}
		info.hasBodySource = true
	} else {
		info.bodySource = funcSourceSpan{}
		info.hasBodySource = false
	}
	info.internal = r.currentInternal()
	r.setFuncInfo(name, info)
}

type shadowWriteEnviron struct {
	parent     expand.WriteEnviron
	shadow     expand.Variable
	shadowName string
	shadowSet  bool

	optState getopts

	secondsStartTime time.Time
	secondsStartSet  bool
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

func subscriptModeFromArrayExprMode(mode syntax.ArrayExprMode) syntax.SubscriptMode {
	switch mode {
	case syntax.ArrayExprIndexed:
		return syntax.SubscriptIndexed
	case syntax.ArrayExprAssociative:
		return syntax.SubscriptAssociative
	default:
		return syntax.SubscriptAuto
	}
}

func cloneSubscript(index *syntax.Subscript) *syntax.Subscript {
	if index == nil {
		return nil
	}
	dup := *index
	return &dup
}

func stampSubscriptMode(index *syntax.Subscript, mode syntax.SubscriptMode) *syntax.Subscript {
	if index == nil || index.AllElements() || mode == syntax.SubscriptAuto || index.Mode == mode {
		return index
	}
	dup := cloneSubscript(index)
	dup.Mode = mode
	return dup
}

func stampVarRefSubscriptMode(ref *syntax.VarRef, mode syntax.SubscriptMode) {
	if ref == nil {
		return
	}
	ref.Index = stampSubscriptMode(ref.Index, mode)
}

func stampArrayExprSubscriptModes(array *syntax.ArrayExpr, mode syntax.SubscriptMode) {
	if array == nil {
		return
	}
	for _, elem := range array.Elems {
		if elem == nil || elem.Kind == syntax.ArrayElemSequential {
			continue
		}
		elem.Index = stampSubscriptMode(elem.Index, mode)
	}
}

func cloneArrayElemsWithSubscriptMode(elems []*syntax.ArrayElem, mode syntax.SubscriptMode) []*syntax.ArrayElem {
	if mode == syntax.SubscriptAuto || len(elems) == 0 {
		return elems
	}
	cloned := make([]*syntax.ArrayElem, len(elems))
	for i, elem := range elems {
		if elem == nil {
			continue
		}
		dup := *elem
		if elem.Kind != syntax.ArrayElemSequential {
			dup.Index = stampSubscriptMode(elem.Index, mode)
		}
		cloned[i] = &dup
	}
	return cloned
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
				item.value = r.assignmentLiteral(elem.Value)
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
	base := prev
	base.Set = true
	base.Kind = arrayValueKind(mode)
	base.Str = ""
	base.List = nil
	base.Indices = nil
	base.Map = nil
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
			base.Indices = slices.Clone(prev.Indices)
		}
	case expand.Associative:
		if prev.Kind == expand.Associative {
			base.Map = maps.Clone(prev.Map)
		} else if prev.Kind == expand.String && prev.IsSet() {
			base.Map = map[string]string{"0": prev.Str}
		} else {
			base.Map = make(map[string]string)
		}
	}
	return base
}

func (r *Runner) associativeArrayKey(index *syntax.Subscript) string {
	if index == nil {
		return ""
	}
	if !index.AllElements() && resolvedSubscriptMode(index) != syntax.SubscriptAssociative {
		panic("interp: associative array key requires associative subscript mode")
	}
	if word, ok := index.Expr.(*syntax.Word); ok {
		return r.literal(word)
	}
	var sb strings.Builder
	if err := syntax.NewPrinter(syntax.Minify(true)).Print(&sb, index.Expr); err != nil {
		return ""
	}
	return sb.String()
}

func associativeArrayHasExplicitKeys(elems []expandedArrayElem) bool {
	for _, elem := range elems {
		switch elem.kind {
		case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
			return true
		}
	}
	return false
}

func (r *Runner) assignArray(prev expand.Variable, as *syntax.Assign, valType string) (expand.Variable, string, bool) {
	targetName, targetPrev := r.resolvedCompoundArrayTarget(prev, as.Ref)
	mode := resolveArrayExprMode(targetPrev, as, valType)
	elems := r.expandCompoundArrayElems(cloneArrayElemsWithSubscriptMode(as.Array.Elems, subscriptModeFromArrayExprMode(mode)))
	trace := traceExpandedArrayAssign(as.Ref, as.Append, elems)
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
		hasExplicitKeys := associativeArrayHasExplicitKeys(elems)
		reportedBareField := false
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
				if hasExplicitKeys {
					if !reportedBareField && len(elem.fields) > 0 {
						r.errf("%s: %s: must use subscript when assigning associative array\n", as.Ref.Name.Value, elem.fields[0])
						r.exit.code = 1
						reportedBareField = true
					}
					continue
				}
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
					if as.Append {
						shadowEnv.shadow.Map[key] += elem.value
					} else {
						shadowEnv.shadow.Map[key] = targetPrev.Map[key] + elem.value
					}
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
		return shadowEnv.shadow, trace, true
	}

	nextIndex := shadowEnv.shadow.IndexedAppendIndex()
	for _, elem := range elems {
		switch elem.kind {
		case syntax.ArrayElemSequential:
			for _, field := range elem.fields {
				shadowEnv.shadow = shadowEnv.shadow.IndexedSet(nextIndex, field, false)
				nextIndex++
			}
		case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
			index := r.arithm(elem.index.Expr)
			if index < 0 {
				if resolved, ok := shadowEnv.shadow.IndexedResolve(index); ok {
					index = resolved
				}
			}
			shadowEnv.shadow = shadowEnv.shadow.IndexedSet(index, elem.value, elem.kind == syntax.ArrayElemKeyedAppend)
			nextIndex = index + 1
		}
		shadowEnv.shadow.Kind = expand.Indexed
		shadowEnv.shadow.Set = true
		if r.exit.fatalExit || r.exit.exiting {
			break
		}
	}
	return shadowEnv.shadow, trace, true
}

// TODO: make assignVal and [setVar] consistent with the [expand.WriteEnviron] interface

func (r *Runner) assignVal(prev expand.Variable, as *syntax.Assign, valType string) (expand.Variable, string, bool) {
	prev.Set = true
	if as.Value != nil {
		s := r.assignLiteral(as)
		if as.Ref != nil && as.Ref.Index != nil {
			return expand.Variable{Set: true, Kind: expand.String, Str: s}, "", true
		}
		if !as.Append {
			prev.Kind = expand.String
			if valType == "-n" {
				prev.Kind = expand.NameRef
			}
			prev.Str = s
			return prev, "", true
		}
		switch prev.Kind {
		case expand.String, expand.Unknown:
			prev.Kind = expand.String
			prev.Str += s
		case expand.Indexed:
			prev = prev.IndexedSet(0, s, true)
		case expand.Associative:
			prev.Kind = expand.Associative
			prev.Str = ""
			prev.List = nil
			prev.Indices = nil
			prev.Map = maps.Clone(prev.Map)
			if prev.Map == nil {
				prev.Map = make(map[string]string)
			}
			prev.Map["0"] += s
		}
		return prev, "", true
	}
	if as.Array == nil {
		// don't return the zero value, as that's an unset variable
		prev.Kind = expand.String
		if valType == "-n" {
			prev.Kind = expand.NameRef
		}
		prev.Str = ""
		return prev, "", true
	}
	if as.Ref != nil && as.Ref.Index != nil {
		r.errf("%s: cannot assign list to array member\n", printVarRef(as.Ref))
		r.exit.code = 1
		return prev, "", false
	}
	return r.assignArray(prev, as, valType)
}

func (r *Runner) assignLiteral(as *syntax.Assign) string {
	if as == nil || as.Value == nil {
		return ""
	}
	if as.LiteralizedValue() {
		raw, ok := declFieldScalarLiteral(as.Value)
		if !ok {
			panic("interp: literalized assignment missing scalar literal")
		}
		return raw
	}
	if r.ecfg == nil {
		r.fillExpandConfig(context.Background())
	}
	str, err := expand.AssignmentLiteral(r.ecfg, as.Value)
	r.expandErr(err)
	return str
}

func declFieldScalarLiteral(word *syntax.Word) (string, bool) {
	if word == nil || len(word.Parts) != 1 {
		return "", false
	}
	lit, ok := word.Parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}
	return lit.Value, true
}
