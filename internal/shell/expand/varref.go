package expand

import (
	"fmt"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func defaultAssociativeSubscript(key string) *syntax.Subscript {
	return &syntax.Subscript{
		Kind: syntax.SubscriptExpr,
		Mode: syntax.SubscriptAssociative,
		Expr: &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: key},
		}},
	}
}

func resolveSubscriptAuto(kind ValueKind, index *syntax.Subscript) *syntax.Subscript {
	if index == nil || index.AllElements() || index.Mode != syntax.SubscriptAuto {
		return index
	}
	dup := syntax.CloneSubscript(index)
	switch kind {
	case Associative:
		dup.Mode = syntax.SubscriptAssociative
	default:
		dup.Mode = syntax.SubscriptIndexed
	}
	return dup
}

type InvalidIdentifierError struct {
	Ref string
}

func (e InvalidIdentifierError) Error() string {
	return fmt.Sprintf("%q: not a valid identifier", e.Ref)
}

type CircularNameRefError struct {
	Name string
}

func (e CircularNameRefError) Error() string {
	return fmt.Sprintf("warning: %s: circular name reference", e.Name)
}

type RefResolutionStatus int

const (
	RefResolved RefResolutionStatus = iota
	RefTargetInvalid
	RefTargetEmpty
	RefTargetCircular
)

type RefResolution struct {
	Ref    *syntax.VarRef
	Var    Variable
	Target string
	Status RefResolutionStatus
}

// ResolveRef follows nameref variables and returns the effective variable
// reference plus the final variable value that it points to.
func (v Variable) ResolveRef(env Environ, ref *syntax.VarRef) (*syntax.VarRef, Variable, error) {
	result, err := v.ResolveRefState(env, ref)
	if err != nil {
		return nil, Variable{}, err
	}
	if result.Status == RefTargetCircular {
		return result.Ref, result.Var, CircularNameRefError{Name: result.Ref.Name.Value}
	}
	return result.Ref, result.Var, nil
}

// ResolveRefState follows nameref variables and reports how resolution ended.
func (v Variable) ResolveRefState(env Environ, ref *syntax.VarRef) (RefResolution, error) {
	resolved := syntax.CloneVarRef(ref)
	if resolved != nil && emptySubscript(resolved.Index) {
		return RefResolution{}, BadArraySubscriptError{Name: printNode(resolved)}
	}
	original := syntax.CloneVarRef(resolved)
	seen := make(map[string]struct{})
	for range maxNameRefDepth {
		if v.Kind != NameRef {
			if resolved != nil {
				resolved.Index = resolveSubscriptAuto(v.Kind, resolved.Index)
			}
			return RefResolution{Ref: resolved, Var: v, Status: RefResolved}, nil
		}
		raw := v.Str
		if raw == "" {
			return RefResolution{Ref: original, Var: Variable{Kind: String}, Status: RefTargetEmpty}, nil
		}
		if _, ok := seen[raw]; ok {
			return RefResolution{Ref: original, Var: Variable{Set: true, Kind: String}, Target: raw, Status: RefTargetCircular}, nil
		}
		seen[raw] = struct{}{}
		target, err := syntax.ParseVarRef(raw)
		if err != nil {
			if raw == "@" || raw == "*" {
				return RefResolution{Ref: original, Var: Variable{Kind: String}, Target: raw, Status: RefTargetInvalid}, nil
			}
			return RefResolution{Ref: original, Var: Variable{Set: true, Kind: String, Str: raw}, Target: raw, Status: RefTargetInvalid}, nil
		}
		if !syntax.ValidName(target.Name.Value) {
			if raw == "@" || raw == "*" {
				return RefResolution{Ref: original, Var: Variable{Kind: String}, Target: raw, Status: RefTargetInvalid}, nil
			}
			return RefResolution{Ref: original, Var: Variable{Set: true, Kind: String, Str: raw}, Target: raw, Status: RefTargetInvalid}, nil
		}
		if emptySubscript(target.Index) {
			return RefResolution{}, BadArraySubscriptError{Name: printNode(target)}
		}
		if resolved != nil && resolved.Index != nil {
			if target.Index != nil {
				if resolved.Index.AllElements() && target.Index.AllElements() {
					return RefResolution{
						Ref:    original,
						Var:    Variable{Kind: Indexed},
						Target: raw,
						Status: RefResolved,
					}, nil
				}
				return RefResolution{}, InvalidIdentifierError{Ref: raw}
			}
			target = &syntax.VarRef{
				Name:    target.Name,
				Index:   syntax.CloneSubscript(resolved.Index),
				Context: resolved.Context,
			}
		} else if resolved != nil {
			target.Context = resolved.Context
		}
		resolved = target
		v = env.Get(target.Name.Value)
	}
	return RefResolution{Ref: original, Var: Variable{Set: true, Kind: String}, Status: RefTargetCircular}, nil
}

func (cfg *Config) resolveVarRef(ref *syntax.VarRef) (*syntax.VarRef, Variable, error) {
	vr := cfg.Env.Get(ref.Name.Value)
	return vr.ResolveRef(cfg.Env, ref)
}

func (cfg *Config) varRef(ref *syntax.VarRef) (string, error) {
	ref, vr, err := cfg.resolveVarRef(ref)
	if err != nil {
		return "", err
	}
	return cfg.varInd(ref.Name.Value, vr, ref.Index)
}

func (cfg *Config) envSetRef(ref *syntax.VarRef, value string) error {
	if ref == nil {
		return nil
	}
	if resolvedRef, vr, err := cfg.resolveVarRef(ref); err == nil && resolvedRef != nil && resolvedRef.Index == nil && vr.Kind == Associative {
		ref = syntax.CloneVarRef(resolvedRef)
		ref.Index = defaultAssociativeSubscript("0")
	}
	if wenv, ok := cfg.Env.(VarRefWriter); ok {
		return wenv.SetVarRef(ref, Variable{Set: true, Kind: String, Str: value}, false)
	}
	if ref.Index != nil {
		return fmt.Errorf("environment cannot set indexed references")
	}
	return cfg.envSet(ref.Name.Value, value)
}
