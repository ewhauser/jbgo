package interp

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func validateNameRefTarget(src string) error {
	if src == "" {
		return nil
	}
	ref, err := syntax.ParseVarRef(src)
	if err != nil || ref == nil || !syntax.ValidName(ref.Name.Value) {
		return fmt.Errorf("`%s': invalid variable name for name reference", src)
	}
	return nil
}

func literalSubscript(kind syntax.SubscriptKind, mode syntax.SubscriptMode, lit string) *syntax.Subscript {
	return &syntax.Subscript{
		Kind: kind,
		Mode: mode,
		Expr: &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: lit},
		}},
	}
}

func emptyAssociativeSubscript() *syntax.Subscript {
	return &syntax.Subscript{
		Kind: syntax.SubscriptExpr,
		Mode: syntax.SubscriptAssociative,
		Expr: &syntax.Word{Parts: []syntax.WordPart{
			&syntax.DblQuoted{},
		}},
	}
}

func resolvedSubscriptMode(sub *syntax.Subscript) syntax.SubscriptMode {
	if sub == nil || sub.AllElements() {
		return syntax.SubscriptAuto
	}
	switch sub.Mode {
	case syntax.SubscriptIndexed, syntax.SubscriptAssociative:
		return sub.Mode
	default:
		panic("interp: unresolved subscript mode")
	}
}

func subscriptLiteralKey(sub *syntax.Subscript) (string, bool) {
	if sub == nil {
		return "", false
	}
	switch sub.Kind {
	case syntax.SubscriptAt:
		return "@", true
	case syntax.SubscriptStar:
		return "*", true
	default:
		word, ok := subscriptWord(sub)
		if !ok {
			return "", false
		}
		return word.Lit(), true
	}
}

func subscriptWord(sub *syntax.Subscript) (*syntax.Word, bool) {
	if sub == nil {
		return nil, false
	}
	word, ok := sub.Expr.(*syntax.Word)
	return word, ok
}

func printVarRef(ref *syntax.VarRef) string {
	if ref == nil {
		return ""
	}
	if raw := ref.RawText(); raw != "" {
		return raw
	}
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	if err := printer.Print(&buf, ref); err != nil {
		return ref.Name.Value
	}
	return buf.String()
}

func badArraySubscriptRef(ref *syntax.VarRef, fallback string) string {
	if ref != nil && ref.Index != nil {
		return printVarRef(ref)
	}
	return fallback
}

type strictIndexedSubscriptError struct {
	err error
}

func (e strictIndexedSubscriptError) Error() string {
	return e.err.Error()
}

func (e strictIndexedSubscriptError) Unwrap() error {
	return e.err
}

func trailingOperandExpectedToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "<(") || strings.HasPrefix(raw, ">(") {
		return raw
	}
	for i := len(raw) - 1; i >= 0; i-- {
		switch raw[i] {
		case '+', '-', '*', '/', '%', '&', '|', '^', '!', '?', ':':
			return raw[i:]
		}
	}
	return ""
}

func parseStrictIndexedSubscript(raw string) (syntax.ArithmExpr, error) {
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		token := strings.TrimRight(raw[i:], " \t\r\n")
		return nil, fmt.Errorf("%s: arithmetic syntax error: invalid arithmetic operator (error token is %q)", strings.TrimSpace(raw), token)
	}
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	expr, err := p.Arithmetic(strings.NewReader(raw))
	if err == nil {
		return expr, nil
	}
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		if token := trailingOperandExpectedToken(raw); token != "" {
			return nil, fmt.Errorf("%s: arithmetic syntax error: operand expected (error token is %q)", strings.TrimSpace(raw), token)
		}
		if i := strings.IndexByte(raw, '#'); i >= 0 {
			token := strings.TrimRight(raw[i:], " \t\r\n")
			return nil, fmt.Errorf("%s: arithmetic syntax error: invalid arithmetic operator (error token is %q)", strings.TrimSpace(raw), token)
		}
	}
	return nil, err
}

func (r *Runner) strictIndexedSubscript(index *syntax.Subscript) (int, error) {
	expr := index.Expr
	if raw := index.RawText(); raw != "" &&
		!strings.Contains(raw, "$(") && !strings.Contains(raw, "`") {
		// Re-parse the raw text for stricter validation, but skip
		// re-parsing when command substitutions are present because
		// the original Expr already has alias-expanded content that
		// would be lost by re-parsing without an alias resolver.
		parsed, err := parseStrictIndexedSubscript(raw)
		if err != nil {
			return 0, strictIndexedSubscriptError{err: err}
		}
		expr = parsed
	}
	n, err := expand.Arithm(r.ecfg, expr)
	if err != nil {
		return 0, strictIndexedSubscriptError{err: err}
	}
	return n, nil
}

func (r *Runner) resolveVarRef(ref *syntax.VarRef) (*syntax.VarRef, expand.Variable, error) {
	vr := r.lookupVar(ref.Name.Value)
	return vr.ResolveRef(r.writeEnv, ref)
}

func (r *Runner) strictVarRef(src string) (*syntax.VarRef, error) {
	return r.strictVarRefWithContext(src, syntax.VarRefDefault)
}

func (r *Runner) strictVarRefWithContext(src string, context syntax.VarRefContext) (*syntax.VarRef, error) {
	ref, err := syntax.ParseVarRef(src)
	if ref != nil {
		ref.Context = context
	}
	return ref, err
}

func (r *Runner) looseVarRef(src string) *syntax.VarRef {
	return r.looseVarRefWithContext(src, syntax.VarRefDefault)
}

func (r *Runner) looseVarRefWithContext(src string, context syntax.VarRefContext) *syntax.VarRef {
	ref, err := r.strictVarRefWithContext(src, context)
	if err == nil {
		return ref
	}
	return &syntax.VarRef{Name: &syntax.Lit{Value: src}, Context: context}
}

func (r *Runner) looseVarRefWord(word *syntax.Word) *syntax.VarRef {
	return r.looseVarRefWordWithContext(word, syntax.VarRefDefault)
}

func (r *Runner) looseVarRefWordWithContext(word *syntax.Word, context syntax.VarRefContext) *syntax.VarRef {
	var buf bytes.Buffer
	printer := syntax.NewPrinter()
	if err := printer.Print(&buf, word); err == nil {
		if ref, err := r.strictVarRefWithContext(buf.String(), context); err == nil {
			return ref
		}
	}
	return &syntax.VarRef{Name: &syntax.Lit{Value: r.literal(word)}, Context: context}
}

func (r *Runner) refIsSet(ref *syntax.VarRef) bool {
	ref, vr, err := r.resolveVarRef(ref)
	if err != nil {
		return false
	}
	if ref == nil || ref.Index == nil {
		return vr.IsSet()
	}
	if ref.Index.AllElements() {
		if vr.Kind == expand.Associative && ref.Context == syntax.VarRefVarSet {
			key, ok := subscriptLiteralKey(ref.Index)
			if !ok {
				return false
			}
			_, ok = vr.Map[key]
			return ok
		}
		switch vr.Kind {
		case expand.Indexed:
			return vr.IndexedCount() > 0
		case expand.Associative:
			return len(vr.Map) > 0
		default:
			return vr.IsSet()
		}
	}
	switch resolvedSubscriptMode(ref.Index) {
	case syntax.SubscriptIndexed:
		switch vr.Kind {
		case expand.String:
			return vr.IsSet() && r.arithm(ref.Index.Expr) == 0
		case expand.Indexed:
			index := r.arithm(ref.Index.Expr)
			if index < 0 {
				resolved, ok := vr.IndexedResolve(index)
				if !ok {
					r.expandErr(expand.BadArraySubscriptError{Name: ref.Name.Value})
					return false
				}
				index = resolved
			}
			_, ok := vr.IndexedGet(index)
			return ok
		default:
			return false
		}
	case syntax.SubscriptAssociative:
		if vr.Kind != expand.Associative {
			return false
		}
		_, ok := vr.Map[r.associativeArrayKey(ref.Index)]
		return ok
	default:
		return false
	}
}

func (r *Runner) refIsNameRef(ref *syntax.VarRef) bool {
	return ref != nil && ref.Index == nil && r.lookupVar(ref.Name.Value).Kind == expand.NameRef
}

func arrayDefaultIndex(kind expand.ValueKind) *syntax.Subscript {
	switch kind {
	case expand.Indexed:
		return literalSubscript(syntax.SubscriptExpr, syntax.SubscriptIndexed, "0")
	case expand.Associative:
		return literalSubscript(syntax.SubscriptExpr, syntax.SubscriptAssociative, "0")
	default:
		return nil
	}
}

type attrUpdate struct {
	hasLocal    bool
	local       bool
	hasExported bool
	exported    bool
	hasReadOnly bool
	readOnly    bool
	hasInteger  bool
	integer     bool
	hasLower    bool
	lower       bool
	hasTrace    bool
	trace       bool
	hasUpper    bool
	upper       bool
}

func (u attrUpdate) mergeOntoTarget(vr *expand.Variable, target expand.Variable) {
	if vr == nil {
		return
	}
	if u.hasLocal {
		vr.Local = u.local
	}
	vr.Exported = target.Exported
	if u.hasExported {
		vr.Exported = u.exported
	}
	vr.ReadOnly = target.ReadOnly
	if u.hasReadOnly {
		vr.ReadOnly = u.readOnly
	}
	vr.Integer = target.Integer
	if u.hasInteger {
		vr.Integer = u.integer
	}
	vr.Lower = target.Lower
	if u.hasLower {
		vr.Lower = u.lower
	}
	vr.Trace = target.Trace
	if u.hasTrace {
		vr.Trace = u.trace
	}
	vr.Upper = target.Upper
	if u.hasUpper {
		vr.Upper = u.upper
	}
}

func (r *Runner) setVarByRef(prev expand.Variable, ref *syntax.VarRef, vr expand.Variable, appendValue bool, updates attrUpdate) error {
	origName := ref.Name.Value
	origKind := prev.Kind
	result, err := prev.ResolveRefState(r.writeEnv, ref)
	if err != nil {
		var invalid expand.InvalidIdentifierError
		if errors.As(err, &invalid) {
			return fmt.Errorf("`%s': not a valid identifier", invalid.Ref)
		}
		return err
	}
	switch result.Status {
	case expand.RefTargetInvalid:
		if ref.Index != nil || vr.Kind != expand.String || appendValue {
			return fmt.Errorf("`%s': not a valid identifier", result.Target)
		}
		r.setVar(origName, vr)
		return nil
	case expand.RefTargetEmpty:
		if ref.Index == nil && vr.Kind == expand.String && !appendValue {
			next := r.lookupVar(origName)
			next.Set = true
			next.Kind = expand.NameRef
			next.Str = vr.Str
			next.List = nil
			next.Indices = nil
			next.Map = nil
			r.setVar(origName, next)
			return nil
		}
		if ref.Index == nil {
			next := r.lookupVar(origName)
			r.errf("warning: %s: removing nameref attribute\n", origName)
			vr.Local = next.Local
			vr.Exported = next.Exported
			vr.ReadOnly = next.ReadOnly
			vr.Integer = next.Integer
			vr.Lower = next.Lower
			vr.Trace = next.Trace
			vr.Upper = next.Upper
			r.setVar(origName, vr)
			return nil
		}
		return fmt.Errorf("`%s': not a valid identifier", printVarRef(ref))
	case expand.RefTargetCircular:
		return expand.CircularNameRefError{Name: ref.Name.Value}
	}
	ref = result.Ref
	prev = result.Var
	if origKind == expand.NameRef {
		updates.mergeOntoTarget(&vr, prev)
	}
	if origKind == expand.NameRef && ref.Index != nil && ref.Index.AllElements() {
		return fmt.Errorf("`%s': not a valid identifier", printVarRef(ref))
	}
	if prev.ReadOnly {
		if vr.Kind == expand.String && ref.Index == nil && !appendValue {
			return fmt.Errorf("%s: readonly variable", ref.Name.Value)
		}
		return fmt.Errorf("%s: readonly variable", origName)
	}
	prev.Set = true
	name := ref.Name.Value
	index := ref.Index

	if vr.Kind == expand.String && index == nil {
		index = arrayDefaultIndex(prev.Kind)
	}
	if index == nil {
		r.setVar(name, vr)
		return nil
	}

	valStr := vr.Str

	switch {
	case index.AllElements():
		if prev.Kind == expand.Associative {
			key, ok := subscriptLiteralKey(index)
			if !ok {
				return fmt.Errorf("bad array subscript")
			}
			prev.Kind = expand.Associative
			prev.Str = ""
			prev.List = nil
			prev.Indices = nil
			prev.Map = maps.Clone(prev.Map)
			if prev.Map == nil {
				prev.Map = make(map[string]string)
			}
			if appendValue {
				prev.Map[key] += valStr
			} else {
				prev.Map[key] = valStr
			}
			r.setVar(name, prev)
			return nil
		}
		return fmt.Errorf("bad array subscript")
	case resolvedSubscriptMode(index) == syntax.SubscriptAssociative:
		key := r.associativeArrayKey(index)
		prev.Kind = expand.Associative
		prev.Str = ""
		prev.List = nil
		prev.Indices = nil
		prev.Map = maps.Clone(prev.Map)
		if prev.Map == nil {
			prev.Map = make(map[string]string)
		}
		if appendValue {
			prev.Map[key] += valStr
		} else {
			prev.Map[key] = valStr
		}
		r.setVar(name, prev)
		return nil
	case resolvedSubscriptMode(index) == syntax.SubscriptIndexed:
		key, err := r.strictIndexedSubscript(index)
		if err != nil {
			return err
		}
		current := r.lookupVar(name)
		ref, current, err = current.ResolveRef(r.writeEnv, ref)
		if err != nil {
			return err
		}
		name = ref.Name.Value
		prev = current
		if key < 0 {
			if prev.Kind == expand.Indexed {
				resolved, ok := prev.IndexedResolve(key)
				if !ok {
					return fmt.Errorf("%s: bad array subscript", badArraySubscriptRef(ref, name))
				}
				key = resolved
			} else {
				return fmt.Errorf("%s: bad array subscript", badArraySubscriptRef(ref, name))
			}
		}
		switch prev.Kind {
		case expand.String:
			next := expand.Variable{Kind: expand.Indexed}
			if prev.IsSet() {
				next.Set = true
				next.List = []string{prev.Str}
			}
			next = next.IndexedSet(key, valStr, appendValue)
			r.setVar(name, next)
			return nil
		case expand.Indexed:
			prev = prev.IndexedSet(key, valStr, appendValue)
			r.setVar(name, prev)
			return nil
		default:
			prev.Kind = expand.Indexed
			prev.Str = ""
			prev.List = nil
			prev.Map = nil
			prev.Indices = nil
			prev = prev.IndexedSet(key, valStr, appendValue)
			r.setVar(name, prev)
			return nil
		}
	default:
		return nil
	}
}

func (r *Runner) unsetVarByRef(ref *syntax.VarRef, strictType bool) error {
	ref, prev, err := r.resolveVarRef(ref)
	if err != nil {
		return err
	}
	if prev.ReadOnly {
		return fmt.Errorf("%s: cannot unset: readonly variable", ref.Name.Value)
	}
	if ref == nil || ref.Index == nil {
		if ownerEnv, ownerVar, ok := visibleBindingWriteEnv(r.writeEnv, ref.Name.Value); ok {
			if (envHasTempScope(ownerEnv) && (!ownerVar.Local || (ownerEnv != r.writeEnv && envAllowsDynamicUnset(r.writeEnv)))) ||
				(ownerVar.Local && ownerEnv != r.writeEnv && envAllowsDynamicUnset(r.writeEnv)) {
				if deleteCurrentScopeVar(ownerEnv, ref.Name.Value) {
					return nil
				}
			}
		}
		r.delVar(ref.Name.Value)
		return nil
	}
	name := ref.Name.Value
	switch prev.Kind {
	case expand.Indexed:
		index := r.arithm(ref.Index.Expr)
		if index < 0 {
			resolved, ok := prev.IndexedResolve(index)
			if !ok {
				return fmt.Errorf("[%d]: bad array subscript", index)
			}
			index = resolved
		}
		prev = prev.IndexedUnset(index)
		r.setVar(name, prev)
	case expand.Associative:
		key := r.associativeArrayKey(ref.Index)
		prev.Map = maps.Clone(prev.Map)
		delete(prev.Map, key)
		prev.Set = len(prev.Map) > 0
		r.setVar(name, prev)
	case expand.Unknown, expand.String:
		if r.arithm(ref.Index.Expr) == 0 {
			r.delVar(name)
			return nil
		}
		if strictType {
			return fmt.Errorf("%s: not an array variable", ref.Name.Value)
		}
	}
	return nil
}
