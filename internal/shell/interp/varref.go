package interp

import (
	"bytes"
	"fmt"
	"maps"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func parseVarRef(src string) (*syntax.VarRef, error) {
	p := syntax.NewParser()
	return p.VarRef(strings.NewReader(src))
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

func (r *Runner) resolveVarRef(ref *syntax.VarRef) (*syntax.VarRef, expand.Variable, error) {
	vr := r.lookupVar(ref.Name.Value)
	return vr.ResolveRef(r.writeEnv, ref)
}

func (r *Runner) strictVarRef(src string) (*syntax.VarRef, error) {
	return r.strictVarRefWithContext(src, syntax.VarRefDefault)
}

func (r *Runner) strictVarRefWithContext(src string, context syntax.VarRefContext) (*syntax.VarRef, error) {
	ref, err := parseVarRef(src)
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
		return emptyAssociativeSubscript()
	default:
		return nil
	}
}

func (r *Runner) setVarByRef(prev expand.Variable, ref *syntax.VarRef, vr expand.Variable, appendValue bool) error {
	ref, prev, err := prev.ResolveRef(r.writeEnv, ref)
	if err != nil {
		return err
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
		key := r.arithm(index.Expr)
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

func (r *Runner) unsetVarByRef(ref *syntax.VarRef) error {
	ref, prev, err := r.resolveVarRef(ref)
	if err != nil {
		return err
	}
	if ref == nil || ref.Index == nil {
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
	case expand.String:
		if r.arithm(ref.Index.Expr) == 0 {
			r.delVar(name)
		}
	}
	return nil
}
