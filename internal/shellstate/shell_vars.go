package shellstate

import (
	"context"
	"maps"
	"sync"
)

type ShellVarUpdate struct {
	Value string
	Unset bool
}

type ShellVarAssignments struct {
	mu      sync.Mutex
	updates map[string]ShellVarUpdate
}

func NewShellVarAssignments() *ShellVarAssignments {
	return &ShellVarAssignments{updates: make(map[string]ShellVarUpdate)}
}

func (s *ShellVarAssignments) Set(name, value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates[name] = ShellVarUpdate{Value: value}
}

func (s *ShellVarAssignments) Unset(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates[name] = ShellVarUpdate{Unset: true}
}

func (s *ShellVarAssignments) Snapshot() map[string]ShellVarUpdate {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.updates)
}

type shellVarAssignmentsKey struct{}

func WithShellVarAssignments(ctx context.Context, state *ShellVarAssignments) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if state == nil {
		return ctx
	}
	return context.WithValue(ctx, shellVarAssignmentsKey{}, state)
}

func ShellVarAssignmentsFromContext(ctx context.Context) *ShellVarAssignments {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(shellVarAssignmentsKey{}).(*ShellVarAssignments)
	return state
}
