// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ewhauser/gbash/host"
	"github.com/ewhauser/gbash/internal/shellvariantprofile"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

const loopIterHelperCommand = "__jb_loop_iter"

func newScopedOverlayEnviron(parent expand.Environ, caseInsensitive bool) *overlayEnviron {
	return &overlayEnviron{
		parent:          parent,
		caseInsensitive: caseInsensitive,
		envEpoch:        newOverlayEnvEpoch(parent, false),
	}
}

func newOverlayEnviron(parent expand.Environ, snapshot, caseInsensitive bool) *overlayEnviron {
	epoch := newOverlayEnvEpoch(parent, snapshot)
	oenv := &overlayEnviron{
		caseInsensitive: caseInsensitive,
		envEpoch:        epoch,
	}
	if !snapshot {
		oenv.parent = parent
	} else if parentWrite, ok := parent.(expand.WriteEnviron); ok {
		oenv.parent = forkWriteEnvironWithEpoch(parentWrite, epoch)
	} else {
		oenv.parent = parent
	}
	if parentWrite, ok := parent.(expand.WriteEnviron); ok {
		if optEnv, optVar, ok := visibleBindingWriteEnv(parentWrite, "OPTIND"); ok {
			if state := getoptsStateForEnv(optEnv); state != nil {
				oenv.optState = *state
			}
			if !snapshot {
				if err := oenv.Set("OPTIND", optVar); err != nil {
					panic(fmt.Sprintf("copy OPTIND into overlay: %v", err))
				}
			}
		}
		if snapshot {
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

func forkEnviron(parent expand.Environ) expand.Environ {
	if parentWrite, ok := parent.(expand.WriteEnviron); ok {
		return forkWriteEnviron(parentWrite)
	}
	return parent
}

func forkWriteEnviron(parent expand.WriteEnviron) expand.WriteEnviron {
	return forkWriteEnvironWithEpoch(parent, newEnvEpoch())
}

func forkEnvironWithEpoch(parent expand.Environ, epoch *envEpoch) expand.Environ {
	if parentWrite, ok := parent.(expand.WriteEnviron); ok {
		return forkWriteEnvironWithEpoch(parentWrite, epoch)
	}
	return parent
}

func forkWriteEnvironWithEpoch(parent expand.WriteEnviron, epoch *envEpoch) expand.WriteEnviron {
	switch parent := parent.(type) {
	case *overlayEnviron:
		clone := *parent
		clone.parent = forkEnvironWithEpoch(parent.parent, epoch)
		clone.valuesShared = shareMapForSubshell(parent.values, &parent.valuesShared)
		clone.tempUnsetShared = shareMapForSubshell(parent.tempUnset, &parent.tempUnsetShared)
		clone.envEpoch = epoch
		clone.visibleCache = nil
		clone.visibleCacheEpoch = 0
		clone.visibleCacheReady = false
		return &clone
	case *shadowWriteEnviron:
		clone := *parent
		clone.parent = forkWriteEnvironWithEpoch(parent.parent, epoch)
		return &clone
	default:
		oenv := &overlayEnviron{envEpoch: epoch}
		for name, vr := range parent.Each() {
			if err := oenv.Set(name, vr); err != nil {
				panic(fmt.Sprintf("copy %s into overlay: %v", name, err))
			}
		}
		if optEnv, _, ok := visibleBindingWriteEnv(parent, "OPTIND"); ok {
			if state := getoptsStateForEnv(optEnv); state != nil {
				oenv.optState = *state
			}
		}
		if secondsEnv, _, ok := visibleSecondsBinding(parent); ok {
			if started, ok := secondsStartTimeForEnv(secondsEnv); ok {
				oenv.secondsStartTime = started
				oenv.secondsStartSet = true
			}
		}
		return oenv
	}
}

// overlayEnviron is our main implementation of [expand.WriteEnviron].
type overlayEnviron struct {
	// parent is non-nil if [values] is an overlay over a parent environment
	// which we can safely reuse without data races, such as non-background subshells
	// or function calls.
	parent expand.Environ

	caseInsensitive bool

	// values maps normalized variable names, per [overlayEnviron.normalize].
	values map[string]namedVariable
	// valuesShared tracks whether values is shared with another environ and
	// must be cloned before mutation.
	valuesShared bool

	// envEpoch tracks visible-binding mutations across a shared overlay chain.
	envEpoch *envEpoch
	// visibleCache stores the flattened visible bindings for the current envEpoch.
	visibleCache []visibleBinding
	// visibleCacheEpoch is the envEpoch value used to build visibleCache.
	visibleCacheEpoch uint64
	// visibleCacheReady reports whether visibleCache contains a valid snapshot.
	visibleCacheReady bool

	// We need to know if the current scope is a function's scope, because
	// functions can modify global variables. When true, [parent] must not be nil.
	funcScope bool

	// tempScope marks call-prefix assignments that should behave like bash's
	// temporary variable scope during function calls and eval/source execution.
	tempScope bool

	// tempScopeConsumesLocals marks function-call temp scopes whose bindings
	// should be consumed when a local of the same name is declared.
	tempScopeConsumesLocals bool

	// tempScopeCrossesFuncScope allows current-frame temp binding lookup to
	// traverse a function-scope boundary to this temp scope. Function-call temp
	// scopes set this so locals in the function can consume the prefix binding;
	// eval/source temp scopes keep it false so later nested function calls do
	// not consume the caller's eval/source temp binding.
	tempScopeCrossesFuncScope bool

	// tempUnset tracks variable names whose temp bindings were explicitly
	// unset. Subsequent writes to these variables should pass through to
	// the parent rather than being recaptured in the temp scope.
	tempUnset map[string]bool
	// tempUnsetShared tracks whether tempUnset is shared with another environ
	// and must be cloned before mutation.
	tempUnsetShared bool

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

type visibleBinding struct {
	Name       string
	Normalized string
	Variable   expand.Variable
}

type envEpoch struct {
	value uint64
}

var immutableListEnvironType = reflect.TypeOf(expand.ListEnviron())

func newEnvEpoch() *envEpoch {
	return &envEpoch{value: 1}
}

func newOverlayEnvEpoch(parent expand.Environ, snapshot bool) *envEpoch {
	if snapshot {
		return newEnvEpoch()
	}
	if epoch := sharedEnvEpoch(parent); epoch != nil {
		return epoch
	}
	return newEnvEpoch()
}

func sharedEnvEpoch(env expand.Environ) *envEpoch {
	switch env := env.(type) {
	case *overlayEnviron:
		return env.ensureEnvEpoch()
	case *shadowWriteEnviron:
		return sharedEnvEpoch(env.parent)
	default:
		return nil
	}
}

func isKnownImmutableEnviron(env expand.Environ) bool {
	if env == nil {
		return true
	}
	typ := reflect.TypeOf(env)
	return typ == immutableListEnvironType
}

func cacheableParentEnviron(env expand.Environ) bool {
	switch env := env.(type) {
	case nil:
		return true
	case *overlayEnviron:
		return env.canCacheVisibleBindings()
	case *shadowWriteEnviron:
		return cacheableParentEnviron(env.parent)
	default:
		return isKnownImmutableEnviron(env)
	}
}

func (o *overlayEnviron) normalize(name string) string {
	return normalizeEnvName(name, o.caseInsensitive)
}

func normalizeEnvName(name string, caseInsensitive bool) string {
	if caseInsensitive {
		return strings.ToUpper(name)
	}
	return name
}

func (o *overlayEnviron) ensureEnvEpoch() *envEpoch {
	if o.envEpoch != nil {
		return o.envEpoch
	}
	if epoch := sharedEnvEpoch(o.parent); epoch != nil {
		o.envEpoch = epoch
		return epoch
	}
	o.envEpoch = newEnvEpoch()
	return o.envEpoch
}

func (o *overlayEnviron) envEpochValue() uint64 {
	return o.ensureEnvEpoch().value
}

func (o *overlayEnviron) bumpEnvEpoch() {
	o.ensureEnvEpoch().value++
}

func (o *overlayEnviron) canCacheVisibleBindings() bool {
	if o == nil {
		return false
	}
	return cacheableParentEnviron(o.parent)
}

func writeEnvEpoch(env expand.WriteEnviron) (uint64, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		if !env.canCacheVisibleBindings() {
			return 0, false
		}
		return env.envEpochValue(), true
	default:
		return 0, false
	}
}

func (r *Runner) currentWriteEnvCacheToken() (expand.WriteEnviron, uint64, bool) {
	if r == nil || r.writeEnv == nil {
		return nil, 0, false
	}
	epoch, ok := writeEnvEpoch(r.writeEnv)
	if !ok {
		return nil, 0, false
	}
	return r.writeEnv, epoch, true
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
	if o.tempScope && !vr.Local && !inOverlay {
		// Temp scope: variables not originally bound in this overlay pass
		// through to the parent so that assignments and unsets inside eval,
		// source, or function calls are not silently swallowed when the
		// overlay is discarded.
		return o.parent.(expand.WriteEnviron).Set(name, vr)
	}
	if o.tempScope && inOverlay && o.tempUnset[normalized] {
		// A temp binding was explicitly unset; pass writes through to the
		// parent so they land in the correct outer scope rather than being
		// recaptured by the temp overlay.
		return o.parent.(expand.WriteEnviron).Set(name, vr)
	}

	o.values = cloneMapOnWrite(o.values, &o.valuesShared)
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
		o.bumpEnvEpoch()
		return nil
	}
	// modifying the entire variable
	vr.Local = prev.Local || vr.Local
	o.values[normalized] = namedVariable{name, vr}
	o.bumpEnvEpoch()
	return nil
}

func (o *overlayEnviron) Each() expand.VarSeq {
	return func(yield func(string, expand.Variable) bool) {
		for _, binding := range o.visibleBindings() {
			if !yield(binding.Name, binding.Variable) {
				return
			}
		}
	}
}

func (o *overlayEnviron) visibleBindings() []visibleBinding {
	if !o.canCacheVisibleBindings() {
		return o.buildVisibleBindings()
	}
	epoch := o.envEpochValue()
	if o.visibleCacheReady && o.visibleCacheEpoch == epoch {
		return o.visibleCache
	}
	bindings := o.buildVisibleBindings()
	if bindings == nil {
		bindings = []visibleBinding{}
	}
	o.visibleCache = bindings
	o.visibleCacheEpoch = epoch
	o.visibleCacheReady = true
	return bindings
}

func (o *overlayEnviron) buildVisibleBindings() []visibleBinding {
	if len(o.values) == 0 {
		if parent, ok := o.parent.(*overlayEnviron); ok {
			return parent.visibleBindings()
		}
		if o.parent == nil {
			return nil
		}
	}

	capHint := len(o.values)
	if parent, ok := o.parent.(*overlayEnviron); ok {
		capHint += len(parent.visibleBindings())
	}
	bindings := make([]visibleBinding, 0, capHint)
	hidden := make([]bool, 0, capHint)
	indices := make(map[string]int, capHint)
	appendBinding := func(binding visibleBinding) {
		if prev, ok := indices[binding.Normalized]; ok {
			hidden[prev] = true
		}
		indices[binding.Normalized] = len(bindings)
		bindings = append(bindings, binding)
		hidden = append(hidden, false)
	}

	switch parent := o.parent.(type) {
	case *overlayEnviron:
		for _, binding := range parent.visibleBindings() {
			appendBinding(binding)
		}
	case nil:
	default:
		for name, vr := range parent.Each() {
			appendBinding(visibleBinding{
				Name:       name,
				Normalized: o.normalize(name),
				Variable:   vr,
			})
		}
	}
	for normalized, vr := range o.values {
		appendBinding(visibleBinding{
			Name:       vr.Name,
			Normalized: normalized,
			Variable:   vr.Variable,
		})
	}
	for _, removed := range hidden {
		if removed {
			compacted := bindings[:0]
			for i, binding := range bindings {
				if hidden[i] {
					continue
				}
				compacted = append(compacted, binding)
			}
			return compacted
		}
	}
	return bindings
}

func currentScopeVar(env expand.WriteEnviron, name string) (expand.Variable, bool) {
	_, vr, ok := currentScopeBinding(env, name)
	return vr, ok
}

func currentScopeBinding(env expand.WriteEnviron, name string) (expand.WriteEnviron, expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize(name)
		vr, ok := env.values[normalized]
		if ok {
			return env, vr.Variable, true
		}
		if parent, ok := env.parent.(expand.WriteEnviron); ok && (env.funcScope || env.tempScope) {
			return currentTempScopeBinding(parent, name)
		}
		return nil, expand.Variable{}, false
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			return env, env.shadow, true
		}
		return currentScopeBinding(env.parent, name)
	default:
		return nil, expand.Variable{}, false
	}
}

func currentTempScopeBinding(env expand.WriteEnviron, name string) (expand.WriteEnviron, expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize(name)
		if env.tempScope {
			if vr, ok := env.values[normalized]; ok {
				return env, vr.Variable, true
			}
		}
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok || (!env.funcScope && !env.tempScope) {
			return nil, expand.Variable{}, false
		}
		return currentTempScopeBinding(parent, name)
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			return env, env.shadow, true
		}
		return currentTempScopeBinding(env.parent, name)
	default:
		return nil, expand.Variable{}, false
	}
}

func currentScopeVars(env expand.WriteEnviron) expand.VarSeq {
	return func(yield func(string, expand.Variable) bool) {
		switch env := env.(type) {
		case *overlayEnviron:
			for _, vr := range env.values {
				if !yield(vr.Name, vr.Variable) {
					return
				}
			}
		case *shadowWriteEnviron:
			if env.shadowSet && !yield(env.shadowName, env.shadow) {
				return
			}
			for name, vr := range currentScopeVars(env.parent) {
				if !yield(name, vr) {
					return
				}
			}
		}
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
		env.values = cloneMapOnWrite(env.values, &env.valuesShared)
		delete(env.values, normalized)
		if env.tempScope {
			env.tempUnset = cloneMapOnWrite(env.tempUnset, &env.tempUnsetShared)
			if env.tempUnset == nil {
				env.tempUnset = make(map[string]bool)
			}
			env.tempUnset[normalized] = true
		}
		env.bumpEnvEpoch()
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

func envHasTempScope(env expand.WriteEnviron) bool {
	switch env := env.(type) {
	case *overlayEnviron:
		return env.tempScope
	case *shadowWriteEnviron:
		return envHasTempScope(env.parent)
	default:
		return false
	}
}

func envAllowsDynamicUnset(env expand.WriteEnviron) bool {
	switch env := env.(type) {
	case *overlayEnviron:
		return env.funcScope || env.tempScope
	case *shadowWriteEnviron:
		return envAllowsDynamicUnset(env.parent)
	default:
		return false
	}
}

func tempScopeConsumesLocals(env expand.WriteEnviron) bool {
	switch env := env.(type) {
	case *overlayEnviron:
		return env.tempScope && env.tempScopeConsumesLocals
	case *shadowWriteEnviron:
		return tempScopeConsumesLocals(env.parent)
	default:
		return false
	}
}

func currentFrameTempBinding(env expand.WriteEnviron, name string) (expand.WriteEnviron, expand.Variable, bool) {
	return currentFrameTempBindingWithState(env, name, false)
}

func currentFrameTempBindingWithState(env expand.WriteEnviron, name string, seenFrame bool) (expand.WriteEnviron, expand.Variable, bool) {
	switch env := env.(type) {
	case *overlayEnviron:
		normalized := env.normalize(name)
		if env.tempScope {
			if vr, ok := env.values[normalized]; ok {
				return env, vr.Variable, true
			}
			parent, ok := env.parent.(expand.WriteEnviron)
			if !ok {
				return nil, expand.Variable{}, false
			}
			return currentFrameTempBindingWithState(parent, name, true)
		}
		if env.funcScope {
			if seenFrame {
				return nil, expand.Variable{}, false
			}
			parent, ok := env.parent.(expand.WriteEnviron)
			if !ok {
				return nil, expand.Variable{}, false
			}
			if overlay, ok := parent.(*overlayEnviron); ok && overlay.tempScope && !overlay.tempScopeCrossesFuncScope {
				return nil, expand.Variable{}, false
			}
			return currentFrameTempBindingWithState(parent, name, true)
		}
		parent, ok := env.parent.(expand.WriteEnviron)
		if !ok {
			return nil, expand.Variable{}, false
		}
		return currentFrameTempBindingWithState(parent, name, seenFrame)
	case *shadowWriteEnviron:
		if env.shadowSet && name == env.shadowName {
			if envHasTempScope(env.parent) {
				return env, env.shadow, true
			}
			return nil, expand.Variable{}, false
		}
		return currentFrameTempBindingWithState(env.parent, name, seenFrame)
	default:
		return nil, expand.Variable{}, false
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

func (r *Runner) materializedSetVars() ([]namedVariable, bool) {
	cacheEnv, cacheEpoch, cacheable := r.currentWriteEnvCacheToken()
	if cacheable && r.setVarsCacheReady && r.setVarsCacheEnv == cacheEnv && r.setVarsCacheEpoch == cacheEpoch {
		return r.setVarsCache, r.setVarsCacheHasBashLineNo
	}

	var vars []namedVariable
	hasBashLineNo := false
	for name, vr := range r.writeEnv.Each() {
		if name == "BASH_LINENO" {
			hasBashLineNo = true
		}
		if !r.printSetVarVisible(name, vr) {
			continue
		}
		vars = append(vars, namedVariable{Name: name, Variable: vr})
	}
	slices.SortFunc(vars, func(a, b namedVariable) int {
		return strings.Compare(a.Name, b.Name)
	})

	if cacheable {
		r.setVarsCache = vars
		r.setVarsCacheEnv = cacheEnv
		r.setVarsCacheEpoch = cacheEpoch
		r.setVarsCacheHasBashLineNo = hasBashLineNo
		r.setVarsCacheReady = true
	}
	return vars, hasBashLineNo
}

func (r *Runner) printSetVar(name string, vr expand.Variable) {
	if !vr.IsSet() {
		return
	}
	switch vr.Kind {
	case expand.Indexed:
		r.outf("%s", name)
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
		r.outf("%s", name)
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
		r.outf("%s=%s\n", name, bashDeclPlainValue(r.parserLangVariant(), vr.String()))
	}
}

func (r *Runner) printSetVars() {
	vars, hasBashLineNo := r.materializedSetVars()
	if hasBashLineNo {
		for _, vr := range vars {
			r.printSetVar(vr.Name, vr.Variable)
		}
		return
	}

	synthetic, ok := r.setBuiltinSpecialVar("BASH_LINENO")
	if !ok || !r.printSetVarVisible("BASH_LINENO", synthetic) {
		for _, vr := range vars {
			r.printSetVar(vr.Name, vr.Variable)
		}
		return
	}

	inserted := false
	for _, vr := range vars {
		if !inserted && strings.Compare("BASH_LINENO", vr.Name) < 0 {
			r.printSetVar("BASH_LINENO", synthetic)
			inserted = true
		}
		r.printSetVar(vr.Name, vr.Variable)
	}
	if !inserted {
		r.printSetVar("BASH_LINENO", synthetic)
	}
}

func (r *Runner) printSetVarVisible(name string, vr expand.Variable) bool {
	if r.hiddenBashSpecialVar(name) {
		return false
	}
	return vr.Declared() && vr.IsSet()
}

func (r *Runner) setBuiltinSpecialVar(name string) (expand.Variable, bool) {
	switch name {
	case "BASH_LINENO":
		if !r.shellProfile().ExposesBashSpecialVar(name) {
			return expand.Variable{}, false
		}
		vr := r.lookupVar(name)
		if vr.IsSet() {
			return vr, true
		}
		return expand.Variable{Set: true, Kind: expand.Indexed, List: []string{}}, true
	default:
		return expand.Variable{}, false
	}
}

func (r *Runner) lookupVar(name string) expand.Variable {
	if name == "" {
		panic("variable name must not be empty")
	}
	profile := r.shellProfile()
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
			vr.Str = r.Arg0
			if vr.Str == "" {
				vr.Str = defaultVirtualShell
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
		if profile.ExposesBashSpecialVar(name) {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(r.bashPID)
		}
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
	case "BASHOPTS":
		if profile.ExposesBashSpecialVar(name) {
			vr.Kind, vr.Str = expand.String, r.bashOptsValue()
			vr.ReadOnly = true
		}
	case "DIRSTACK":
		vr.Kind, vr.List = expand.Indexed, r.dirStack
	case "BASH_SOURCE":
		if profile.ExposesBashSpecialVar(name) {
			if stack := r.bashSourceStack(); len(stack) > 0 {
				vr.Kind, vr.List = expand.Indexed, stack
			}
		}
	case "BASH_LINENO":
		if profile.ExposesBashSpecialVar(name) {
			if stack := r.bashLineNoStack(); len(stack) > 0 {
				vr.Kind, vr.List = expand.Indexed, stack
			}
		}
	case "FUNCNAME":
		if profile.ExposesBashSpecialVar(name) {
			if stack := r.funcNameStack(); len(stack) > 0 {
				vr.Kind, vr.List = expand.Indexed, stack
			}
		}
	case "BASH_VERSION":
		if profile.ExposesBashSpecialVar(name) {
			vr.Kind, vr.Str = expand.String, "5.2.0(1)-gbash"
			vr.ReadOnly = true
		}
	}
	if vr.Kind != expand.Unknown {
		vr.Set = true
		return vr
	}
	if r.hiddenBashSpecialVar(name) {
		return expand.Variable{}
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

func (r *Runner) hiddenBashSpecialVar(name string) bool {
	return shellvariantprofile.IsBashSpecialVar(name) && !r.shellProfile().ExposesBashSpecialVar(name)
}

type varMutation struct {
	ref         *syntax.VarRef
	previous    expand.Variable
	hasPrevious bool
	appendValue bool
}

func (r *Runner) setVarObserved(name string, vr expand.Variable, mutation *varMutation) error {
	if vr.IsSet() && r.opts[optAllExport] {
		vr.Exported = true
	}
	prev := expand.Variable{}
	if mutation != nil && mutation.hasPrevious {
		prev = mutation.previous
	} else if name != "" {
		prev = r.lookupVar(name)
	}
	if err := r.writeEnv.Set(name, vr); err != nil {
		r.errf("%s: %v\n", name, err)
		r.exit.code = 1
		return err
	}
	if vr.IsSet() {
		r.afterSetVar(name, vr)
		r.analysisVariableWrite(name, mutationRef(mutation), vr, prev, mutationAppend(mutation))
		return nil
	}
	r.analysisVariableUnset(name, mutationRef(mutation), prev)
	return nil
}

func mutationRef(mutation *varMutation) *syntax.VarRef {
	if mutation == nil {
		return nil
	}
	return mutation.ref
}

func mutationAppend(mutation *varMutation) bool {
	if mutation == nil {
		return false
	}
	return mutation.appendValue
}

func (r *Runner) delVar(name string) {
	_ = r.setVarObserved(name, expand.Variable{}, nil)
}

func (r *Runner) setVarString(name, value string) {
	r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
}

func (r *Runner) setOPTIND(value string) {
	vr := expand.Variable{Set: true, Kind: expand.String, Str: value}
	prev := r.lookupVar("OPTIND")
	if prev.Exported || r.opts[optAllExport] {
		vr.Exported = true
	}
	_ = r.setVarObserved("OPTIND", vr, &varMutation{previous: prev, hasPrevious: true})
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
	_ = r.setVarObserved(name, vr, nil)
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

func (r *Runner) hostOS() string {
	if value := strings.TrimSpace(r.platform.OS.String()); value != "" {
		return value
	}
	if r.writeEnv != nil {
		if vr := r.writeEnv.Get("GBASH_HOST_OS"); vr.IsSet() {
			if value := strings.TrimSpace(vr.String()); value != "" {
				return value
			}
		}
	}
	return host.OSLinux.String()
}

func (r *Runner) requireExecutableBit() bool {
	if r.platform.OS == "" {
		platform := r.platform
		platform.OS = host.OS(r.hostOS())
		return platform.RequiresExecutableBit()
	}
	return r.platform.RequiresExecutableBit()
}

func (r *Runner) defaultOSType() string {
	if value := strings.TrimSpace(r.platform.OSType); value != "" {
		return value
	}
	if r.writeEnv != nil {
		if vr := r.writeEnv.Get("GBASH_OSTYPE"); vr.IsSet() {
			if value := strings.TrimSpace(vr.String()); value != "" {
				return value
			}
		}
	}
	return host.OS(r.hostOS()).PlatformDefaults().OSType
}

func (r *Runner) shellOptsValue() string {
	names := []string{
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

func (r *Runner) bashOptsValue() string {
	var names []string
	for i, opt := range &bashOptsTable {
		if r.opts[len(posixOptsTable)+i] {
			names = append(names, opt.name)
		}
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

func (e *shadowWriteEnviron) Each() expand.VarSeq {
	return func(yield func(string, expand.Variable) bool) {
		seenShadow := false
		for name, vr := range e.parent.Each() {
			if e.shadowSet && name == e.shadowName {
				if seenShadow {
					continue
				}
				seenShadow = true
				if !yield(name, e.shadow) {
					return
				}
				continue
			}
			if !yield(name, vr) {
				return
			}
		}
		if e.shadowSet && !seenShadow {
			if !yield(e.shadowName, e.shadow) {
				return
			}
		}
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
				cfg := r.ecfg
				cfg.PreferStartupHomeForAssignmentTilde = r.platform.OS == host.OSDarwin
				str, err := expand.AssignmentLiteral(&cfg, elem.Value)
				r.expandErr(err)
				item.value = str
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
	trace := traceExpandedArrayAssign(r.parserLangVariant(), as.Ref, as.Append, elems)
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
	if !r.ecfgInit {
		r.fillExpandConfig(context.Background())
	}
	r.inAssignment++
	defer func() {
		r.inAssignment--
	}()
	str, err := expand.AssignmentLiteral(&r.ecfg, as.Value)
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
