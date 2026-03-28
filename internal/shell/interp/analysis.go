package interp

import (
	"errors"
	"maps"
	"slices"

	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
)

type analysisState struct {
	observer analysis.Observer
	run      analysis.RunMetadata
	file     analysis.FileMetadata
	stmt     *syntax.Stmt
	cmd      syntax.Command
	scopes   []analysis.Scope
	mute     int
}

type analysisStatusErr struct {
	err    error
	status analysis.Status
}

type analysisContextSnapshot struct {
	run         analysis.RunMetadata
	file        analysis.FileMetadata
	stmt        *syntax.Stmt
	cmd         syntax.Command
	scopes      []analysis.Scope
	options     analysis.Options
	controlFlow analysis.ControlFlow
	chunkSource string
	chunkBase   uint
}

func (e analysisStatusErr) Error() string {
	return e.err.Error()
}

func (e analysisStatusErr) Unwrap() error {
	return e.err
}

func (e analysisStatusErr) analysisStatus() analysis.Status {
	return e.status
}

func newAnalysisState(observer analysis.Observer, run analysis.RunMetadata) *analysisState {
	if observer == nil {
		return nil
	}
	return &analysisState{
		observer: observer,
		run:      run,
	}
}

func (c analysisContextSnapshot) Run() analysis.RunMetadata {
	return c.run
}

func (c analysisContextSnapshot) File() analysis.FileMetadata {
	return c.file
}

func (c analysisContextSnapshot) Statement() *syntax.Stmt {
	return c.stmt
}

func (c analysisContextSnapshot) Command() syntax.Command {
	return c.cmd
}

func (c analysisContextSnapshot) Scopes() []analysis.Scope {
	return append([]analysis.Scope(nil), c.scopes...)
}

func (c analysisContextSnapshot) Options() analysis.Options {
	return c.options
}

func (c analysisContextSnapshot) ControlFlow() analysis.ControlFlow {
	return c.controlFlow
}

func (c analysisContextSnapshot) Source(node syntax.Node) string {
	if node == nil || c.chunkSource == "" {
		return ""
	}
	startOffset := node.Pos().Offset()
	endOffset := node.End().Offset()
	if endOffset < startOffset || startOffset < c.chunkBase {
		return ""
	}
	start := int(startOffset - c.chunkBase)
	end := int(endOffset - c.chunkBase)
	if start < 0 || end < start || end > len(c.chunkSource) {
		return ""
	}
	return c.chunkSource[start:end]
}

func (r *Runner) resetAnalysisState() {
	if r == nil || r.analysis == nil {
		return
	}
	r.analysis.file = analysis.FileMetadata{}
	r.analysis.stmt = nil
	r.analysis.cmd = nil
	r.analysis.scopes = nil
}

func (r *Runner) cloneAnalysisState() *analysisState {
	if r == nil || r.analysis == nil {
		return nil
	}
	clone := *r.analysis
	clone.scopes = append([]analysis.Scope(nil), r.analysis.scopes...)
	return &clone
}

func (a *analysisState) pushMute() {
	if a != nil {
		a.mute++
	}
}

func (a *analysisState) popMute() {
	if a != nil && a.mute > 0 {
		a.mute--
	}
}

func (r *Runner) analysisEnabled() bool {
	return r != nil && r.analysis != nil && r.analysis.observer != nil
}

func (r *Runner) analysisMuted() bool {
	return !r.analysisEnabled() || r.analysis.mute > 0
}

func (r *Runner) analysisSnapshot() analysisContextSnapshot {
	scopes := append([]analysis.Scope(nil), r.analysis.scopes...)
	return analysisContextSnapshot{
		run:         r.analysis.run,
		file:        r.analysis.file,
		stmt:        r.analysis.stmt,
		cmd:         r.analysis.cmd,
		scopes:      scopes,
		options:     r.analysisOptions(),
		controlFlow: r.analysisControlFlow(scopes),
		chunkSource: r.currentChunkSource,
		chunkBase:   r.currentChunkSourceBase,
	}
}

func (r *Runner) analysisOptions() analysis.Options {
	if r == nil {
		return analysis.Options{}
	}
	return analysis.Options{
		Errexit:  r.opts[optErrExit],
		Pipefail: r.opts[optPipeFail],
		Lastpipe: r.opts[optLastPipe],
		Nounset:  r.opts[optNoUnset],
		Posix:    r.opts[optPosix],
	}
}

func (r *Runner) analysisControlFlow(scopes []analysis.Scope) analysis.ControlFlow {
	pipelineDepth := 0
	for _, scope := range scopes {
		if scope.Kind == analysis.ScopePipeline {
			pipelineDepth++
		}
	}
	return analysis.ControlFlow{
		StatementDepth:    r.stmtDepth,
		EvalDepth:         r.evalDepth,
		InFunction:        r.inFunc,
		InSource:          r.inSource,
		InSubshell:        r.inSubshell,
		PipelineDepth:     pipelineDepth,
		ErrExitSuppressed: r.noErrExit,
	}
}

func analysisStatusFromExit(exit exitStatus) analysis.Status {
	return analysis.Status{
		Code:           int(exit.code),
		Returning:      exit.returning,
		Exiting:        exit.exiting,
		Fatal:          exit.fatalExit,
		ErrExitIgnored: exit.errExitIgnored,
	}
}

// WithAnalysisStatus attaches analysis-only status metadata to an error without
// changing its runtime semantics. The wrapped error is still returned to the
// caller and remains visible via errors.As/errors.Is.
func WithAnalysisStatus(err error, status analysis.Status) error {
	if err == nil {
		return nil
	}
	return analysisStatusErr{
		err:    err,
		status: status,
	}
}

func (r *Runner) AnalysisStatus() analysis.Status {
	if r == nil {
		return analysis.Status{}
	}
	return analysisStatusFromExit(r.exit)
}

func cloneAnalysisVariable(v expand.Variable) expand.Variable {
	v.List = slices.Clone(v.List)
	v.Indices = slices.Clone(v.Indices)
	v.Map = maps.Clone(v.Map)
	return v
}

func (r *Runner) analysisStatusForError(err error) analysis.Status {
	status := r.AnalysisStatus()
	if err == nil || status.Code != 0 {
		return status
	}
	var withStatus interface{ analysisStatus() analysis.Status }
	if errors.As(err, &withStatus) {
		status = withStatus.analysisStatus()
		if status.Code != 0 {
			return status
		}
	}
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		status.Code = 2
		return status
	}
	var exitStatus ExitStatus
	if errors.As(err, &exitStatus) {
		status.Code = int(exitStatus)
		return status
	}
	status.Code = 1
	return status
}

func (r *Runner) analysisEmit(event analysis.Event) {
	if r.analysisMuted() || event == nil {
		return
	}
	ctx := r.analysisSnapshot()
	defer func() {
		_ = recover()
	}()
	r.analysis.observer.Observe(ctx, event)
}

func (r *Runner) AnalysisRunStart() {
	r.analysisEmit(analysis.RunStart{})
}

func (r *Runner) AnalysisRunFinish(status analysis.Status) {
	r.analysisEmit(analysis.RunFinish{Status: status})
}

func (r *Runner) analysisSetFile(file analysis.FileMetadata) analysis.FileMetadata {
	if !r.analysisEnabled() {
		return analysis.FileMetadata{}
	}
	prev := r.analysis.file
	r.analysis.file = file
	return prev
}

func (r *Runner) analysisRestoreFile(file analysis.FileMetadata) {
	if r.analysisEnabled() {
		r.analysis.file = file
	}
}

func (r *Runner) analysisFileStart(file analysis.FileMetadata) analysis.FileMetadata {
	prev := r.analysisSetFile(file)
	r.analysisEmit(analysis.FileStart{})
	return prev
}

func (r *Runner) analysisFileFinish(status analysis.Status) {
	r.analysisEmit(analysis.FileFinish{Status: status})
}

func (r *Runner) analysisStatementEnter(st *syntax.Stmt) *syntax.Stmt {
	if !r.analysisEnabled() {
		return nil
	}
	prev := r.analysis.stmt
	r.analysis.stmt = st
	r.analysisEmit(analysis.StatementEnter{})
	return prev
}

func (r *Runner) analysisStatementExit(prev *syntax.Stmt, status analysis.Status) {
	if !r.analysisEnabled() {
		return
	}
	r.analysisEmit(analysis.StatementExit{Status: status})
	r.analysis.stmt = prev
}

func (r *Runner) analysisCommandEnter(cmd syntax.Command) syntax.Command {
	if !r.analysisEnabled() {
		return nil
	}
	prev := r.analysis.cmd
	r.analysis.cmd = cmd
	r.analysisEmit(analysis.CommandEnter{})
	return prev
}

func (r *Runner) analysisCommandExit(prev syntax.Command, status analysis.Status) {
	if !r.analysisEnabled() {
		return
	}
	r.analysisEmit(analysis.CommandExit{Status: status})
	r.analysis.cmd = prev
}

func (r *Runner) analysisScopeEnter(scope analysis.Scope) {
	if !r.analysisEnabled() {
		return
	}
	r.analysis.scopes = append(r.analysis.scopes, scope)
	r.analysisEmit(analysis.ScopeEnter{Scope: scope})
}

func (r *Runner) analysisScopeExit(status analysis.Status) {
	if !r.analysisEnabled() || len(r.analysis.scopes) == 0 {
		return
	}
	scope := r.analysis.scopes[len(r.analysis.scopes)-1]
	r.analysisEmit(analysis.ScopeExit{Scope: scope, Status: status})
	r.analysis.scopes = r.analysis.scopes[:len(r.analysis.scopes)-1]
}

func (r *Runner) analysisWithScope(scope analysis.Scope, fn func()) {
	if !r.analysisEnabled() {
		fn()
		return
	}
	r.analysisScopeEnter(scope)
	defer r.analysisScopeExit(r.AnalysisStatus())
	fn()
}

func (r *Runner) analysisVariableRead(name string, ref *syntax.VarRef, vr expand.Variable) {
	if r.analysisMuted() {
		return
	}
	r.analysisEmit(analysis.VariableRead{
		Name:     name,
		Ref:      ref,
		Variable: cloneAnalysisVariable(vr),
	})
}

func (r *Runner) analysisVariableWrite(name string, ref *syntax.VarRef, vr, prev expand.Variable, appendValue bool) {
	if r.analysisMuted() {
		return
	}
	r.analysisEmit(analysis.VariableWrite{
		Name:     name,
		Ref:      ref,
		Variable: cloneAnalysisVariable(vr),
		Previous: cloneAnalysisVariable(prev),
		Append:   appendValue,
	})
}

func (r *Runner) analysisVariableUnset(name string, ref *syntax.VarRef, prev expand.Variable) {
	if r.analysisMuted() {
		return
	}
	r.analysisEmit(analysis.VariableUnset{
		Name:     name,
		Ref:      ref,
		Previous: cloneAnalysisVariable(prev),
	})
}

func (r *Runner) analysisOptionChanges(namespace analysis.OptionNamespace, before, after analysis.Options) {
	if r.analysisMuted() {
		return
	}
	if before.Errexit != after.Errexit {
		r.analysisEmit(analysis.OptionChange{Namespace: namespace, Name: "errexit", Old: before.Errexit, New: after.Errexit})
	}
	if before.Pipefail != after.Pipefail {
		r.analysisEmit(analysis.OptionChange{Namespace: namespace, Name: "pipefail", Old: before.Pipefail, New: after.Pipefail})
	}
	if before.Lastpipe != after.Lastpipe {
		r.analysisEmit(analysis.OptionChange{Namespace: namespace, Name: "lastpipe", Old: before.Lastpipe, New: after.Lastpipe})
	}
	if before.Nounset != after.Nounset {
		r.analysisEmit(analysis.OptionChange{Namespace: namespace, Name: "nounset", Old: before.Nounset, New: after.Nounset})
	}
	if before.Posix != after.Posix {
		r.analysisEmit(analysis.OptionChange{Namespace: namespace, Name: "posix", Old: before.Posix, New: after.Posix})
	}
}
