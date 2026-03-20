package expand

import (
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func parseVarRef(src string) (*syntax.VarRef, error) {
	p := syntax.NewParser()
	return p.VarRef(strings.NewReader(src))
}

func cloneSubscript(index *syntax.Subscript) *syntax.Subscript {
	return syntax.CloneSubscript(index)
}

func cloneVarRef(ref *syntax.VarRef) *syntax.VarRef {
	return syntax.CloneVarRef(ref)
}

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
	dup := cloneSubscript(index)
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

// ResolveRef follows nameref variables and returns the effective variable
// reference plus the final variable value that it points to.
func (v Variable) ResolveRef(env Environ, ref *syntax.VarRef) (*syntax.VarRef, Variable, error) {
	resolved := cloneVarRef(ref)
	if resolved != nil && emptySubscript(resolved.Index) {
		return nil, Variable{}, BadArraySubscriptError{Name: printNode(resolved)}
	}
	for range maxNameRefDepth {
		if v.Kind != NameRef {
			if resolved != nil {
				resolved.Index = resolveSubscriptAuto(v.Kind, resolved.Index)
			}
			return resolved, v, nil
		}
		target, err := parseVarRef(v.Str)
		if err != nil {
			return nil, Variable{}, err
		}
		if emptySubscript(target.Index) {
			return nil, Variable{}, BadArraySubscriptError{Name: printNode(target)}
		}
		if resolved != nil && resolved.Index != nil {
			if target.Index != nil {
				return nil, Variable{}, InvalidIdentifierError{Ref: v.Str}
			}
			target = &syntax.VarRef{
				Name:    target.Name,
				Index:   cloneSubscript(resolved.Index),
				Context: resolved.Context,
			}
		} else if resolved != nil {
			target.Context = resolved.Context
		}
		resolved = target
		v = env.Get(target.Name.Value)
	}
	return nil, Variable{}, fmt.Errorf("nameref depth exceeded")
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
		ref = cloneVarRef(resolvedRef)
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
