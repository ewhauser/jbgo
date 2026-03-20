package shellstate

import (
	"context"
	"slices"
	"sync"
)

const (
	CompletionSpecDefaultKey = "__default__"
	CompletionSpecEmptyKey   = "__empty__"
)

type CompletionSpec struct {
	IsDefault   bool
	IsEmptyLine bool
	Wordlist    string
	HasWordlist bool
	Function    string
	HasFunction bool
	Command     string
	HasCommand  bool
	Filter      string
	HasFilter   bool
	Prefix      string
	HasPrefix   bool
	Suffix      string
	HasSuffix   bool
	Options     []string
	Actions     []string
}

func cloneCompletionSpec(spec *CompletionSpec) CompletionSpec {
	if spec == nil {
		return CompletionSpec{}
	}
	out := *spec
	out.Options = slices.Clone(spec.Options)
	out.Actions = slices.Clone(spec.Actions)
	return out
}

type CompletionState struct {
	mu    sync.RWMutex
	specs map[string]CompletionSpec
}

func NewCompletionState() *CompletionState {
	return &CompletionState{specs: make(map[string]CompletionSpec)}
}

func (s *CompletionState) Get(name string) (CompletionSpec, bool) {
	if s == nil {
		return CompletionSpec{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	spec, ok := s.specs[name]
	return cloneCompletionSpec(&spec), ok
}

func (s *CompletionState) Set(name string, spec *CompletionSpec) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs[name] = cloneCompletionSpec(spec)
}

func (s *CompletionState) Update(name string, fn func(*CompletionSpec)) CompletionSpec {
	if s == nil {
		var spec CompletionSpec
		if fn != nil {
			fn(&spec)
		}
		return spec
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.specs[name]
	spec := cloneCompletionSpec(&current)
	if fn != nil {
		fn(&spec)
	}
	s.specs[name] = cloneCompletionSpec(&spec)
	return cloneCompletionSpec(&spec)
}

func (s *CompletionState) Delete(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.specs, name)
}

func (s *CompletionState) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.specs)
}

func (s *CompletionState) Keys() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.specs))
	for key := range s.specs {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type completionStateKey struct{}

func WithCompletionState(ctx context.Context, state *CompletionState) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, completionStateKey{}, state)
}

func CompletionStateFromContext(ctx context.Context) *CompletionState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(completionStateKey{}).(*CompletionState)
	return state
}
