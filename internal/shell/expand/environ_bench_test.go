package expand

import (
	"fmt"
	"testing"
)

// benchEnviron is a simple Environ backed by a flat slice of name/variable pairs,
// similar to what the runner uses for layered scopes.
type benchEnviron struct {
	vars []benchVar
}

type benchVar struct {
	name string
	vr   Variable
}

func (e *benchEnviron) Get(name string) Variable {
	for i := len(e.vars) - 1; i >= 0; i-- {
		if e.vars[i].name == name {
			return e.vars[i].vr
		}
	}
	return Variable{}
}

func (e *benchEnviron) Each() VarSeq {
	return func(yield func(string, Variable) bool) {
		for _, v := range e.vars {
			if !yield(v.name, v.vr) {
				return
			}
		}
	}
}

// benchShadowEnviron wraps an environ with a shadow variable,
// mirroring shadowWriteEnviron.Each in interp/vars.go.
type benchShadowEnviron struct {
	parent     *benchEnviron
	shadowName string
	shadow     Variable
}

func (e *benchShadowEnviron) Each() VarSeq {
	return func(yield func(string, Variable) bool) {
		seenShadow := false
		for name, vr := range e.parent.Each() {
			if name == e.shadowName {
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
		if !seenShadow {
			yield(e.shadowName, e.shadow)
		}
	}
}

func makeBenchEnviron(n int) *benchEnviron {
	env := &benchEnviron{vars: make([]benchVar, n)}
	for i := range n {
		env.vars[i] = benchVar{
			name: fmt.Sprintf("VAR_%d", i),
			vr:   Variable{Set: true, Kind: String, Str: fmt.Sprintf("value_%d", i)},
		}
	}
	return env
}

func BenchmarkEnvironEach(b *testing.B) {
	for _, size := range []int{10, 50, 200} {
		env := makeBenchEnviron(size)
		for i := range env.vars {
			if i%2 == 0 {
				env.vars[i].vr.Exported = true
			}
		}
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			for range b.N {
				var count int
				for _, vr := range env.Each() {
					if vr.Exported {
						count++
					}
				}
				_ = count
			}
		})
	}
}

func BenchmarkShadowEach(b *testing.B) {
	for _, size := range []int{10, 50, 200} {
		env := makeBenchEnviron(size)
		shadow := &benchShadowEnviron{
			parent:     env,
			shadowName: "SHADOW_VAR",
			shadow:     Variable{Set: true, Kind: String, Str: "shadowed"},
		}
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			for range b.N {
				var count int
				for range shadow.Each() {
					count++
				}
				_ = count
			}
		})
	}
}

func BenchmarkEarlyExit(b *testing.B) {
	for _, size := range []int{10, 50, 200} {
		env := makeBenchEnviron(size)
		target := fmt.Sprintf("VAR_%d", size/2)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			for range b.N {
				var found Variable
				for name, vr := range env.Each() {
					if name == target {
						found = vr
						break
					}
				}
				_ = found
			}
		})
	}
}

func BenchmarkCollectAll(b *testing.B) {
	for _, size := range []int{10, 50, 200} {
		env := makeBenchEnviron(size)
		b.Run(fmt.Sprintf("n=%d", size), func(b *testing.B) {
			for range b.N {
				seen := make(map[string]Variable, size)
				for name, vr := range env.Each() {
					seen[name] = vr
				}
				_ = seen
			}
		})
	}
}
