package analysis

import (
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

// Observer receives read-only semantic events from shell execution.
type Observer interface {
	Observe(Context, Event)
}

// ObserverFunc adapts a function into an [Observer].
type ObserverFunc func(Context, Event)

// Observe implements [Observer].
func (fn ObserverFunc) Observe(ctx Context, event Event) {
	if fn != nil {
		fn(ctx, event)
	}
}

// Context is an immutable snapshot of the current execution state.
type Context interface {
	Run() RunMetadata
	File() FileMetadata
	Statement() *syntax.Stmt
	Command() syntax.Command
	Scopes() []Scope
	Options() Options
	ControlFlow() ControlFlow
	Source(syntax.Node) string
}

// RunMetadata describes the current top-level shell execution.
type RunMetadata struct {
	Name          string
	ScriptPath    string
	Interactive   bool
	CommandString bool
}

// FileMetadata describes the current parsed chunk being executed.
type FileMetadata struct {
	Name         string
	TopLevelPath string
	ChunkIndex   int
	ChunkLine    uint
	ChunkOffset  uint
}

// Status describes the shell status at one semantic boundary.
type Status struct {
	Code           int
	Returning      bool
	Exiting        bool
	Fatal          bool
	ErrExitIgnored bool
}

// ScopeKind identifies a semantic shell scope boundary.
type ScopeKind string

const (
	ScopeFunction ScopeKind = "function"
	ScopeSource   ScopeKind = "source"
	ScopeEval     ScopeKind = "eval"
	ScopeSubshell ScopeKind = "subshell"
	ScopePipeline ScopeKind = "pipeline"
)

// Scope describes one active semantic scope.
type Scope struct {
	Kind     ScopeKind
	Name     string
	File     string
	CallLine uint
}

// Options is the analysis-visible shell option snapshot.
type Options struct {
	Errexit  bool
	Pipefail bool
	Lastpipe bool
	Nounset  bool
	Posix    bool
}

// ControlFlow captures control-flow-sensitive execution state.
type ControlFlow struct {
	StatementDepth    int
	EvalDepth         int
	InFunction        bool
	InSource          bool
	InSubshell        bool
	PipelineDepth     int
	ErrExitSuppressed bool
}

// OptionNamespace identifies which shell option namespace changed.
type OptionNamespace string

const (
	OptionNamespaceSet   OptionNamespace = "set"
	OptionNamespaceShopt OptionNamespace = "shopt"
)

// Event is implemented by every semantic event value emitted by the observer
// API.
type Event interface {
	isAnalysisEvent()
}

// RunStart fires when a top-level shell execution begins.
type RunStart struct{}

// RunFinish fires when a top-level shell execution finishes.
type RunFinish struct {
	Status Status
}

// FileStart fires when execution begins for one parsed chunk.
type FileStart struct{}

// FileFinish fires when execution finishes for one parsed chunk.
type FileFinish struct {
	Status Status
}

// StatementEnter fires before a statement executes.
type StatementEnter struct{}

// StatementExit fires after a statement finishes.
type StatementExit struct {
	Status Status
}

// CommandEnter fires before a concrete command node executes.
type CommandEnter struct{}

// CommandExit fires after a concrete command node finishes.
type CommandExit struct {
	Status Status
}

// ScopeEnter fires when a semantic scope begins.
type ScopeEnter struct {
	Scope Scope
}

// ScopeExit fires when a semantic scope ends.
type ScopeExit struct {
	Scope  Scope
	Status Status
}

// VariableRead fires for shell-visible variable reads during execution.
type VariableRead struct {
	Name     string
	Ref      *syntax.VarRef
	Variable expand.Variable
}

// VariableWrite fires when a shell variable is written.
type VariableWrite struct {
	Name     string
	Ref      *syntax.VarRef
	Variable expand.Variable
	Previous expand.Variable
	Append   bool
}

// VariableUnset fires when a shell variable binding is removed.
type VariableUnset struct {
	Name     string
	Ref      *syntax.VarRef
	Previous expand.Variable
}

// OptionChange fires when one analysis-visible shell option changes.
type OptionChange struct {
	Namespace OptionNamespace
	Name      string
	Old       bool
	New       bool
}

func (RunStart) isAnalysisEvent()       {}
func (RunFinish) isAnalysisEvent()      {}
func (FileStart) isAnalysisEvent()      {}
func (FileFinish) isAnalysisEvent()     {}
func (StatementEnter) isAnalysisEvent() {}
func (StatementExit) isAnalysisEvent()  {}
func (CommandEnter) isAnalysisEvent()   {}
func (CommandExit) isAnalysisEvent()    {}
func (ScopeEnter) isAnalysisEvent()     {}
func (ScopeExit) isAnalysisEvent()      {}
func (VariableRead) isAnalysisEvent()   {}
func (VariableWrite) isAnalysisEvent()  {}
func (VariableUnset) isAnalysisEvent()  {}
func (OptionChange) isAnalysisEvent()   {}
