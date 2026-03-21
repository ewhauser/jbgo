// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/pattern"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

const (
	// shellReplyPS3Var, or PS3, is a special variable in Bash used by the select command,
	// while the shell is awaiting for input. the default value is [shellDefaultPS3]
	shellReplyPS3Var = "PS3"
	// shellDefaultPS3, or #?, is PS3's default value
	shellDefaultPS3 = "#? "
	// shellReplyVar, or REPLY, is a special variable in Bash that is used to store the result of
	// the select command or of the read command, when no variable name is specified
	shellReplyVar = "REPLY"
	// Bash uses 128 + SIGALRM for read timeouts.
	readBuiltinTimeoutExitCode = 128 + 14
)

// newPipe creates a pipe using the default virtual pipe implementation.
func (r *Runner) newPipe() (StdinReader, io.WriteCloser) {
	return NewVirtualPipe()
}

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env:         expandEnv{r},
		TildeEnv:    tildeExpandEnv{r},
		StartupHome: r.startupHome,
		CurrentLine: func() uint {
			if r.trapLineOverride != 0 {
				return r.trapLineOverride
			}
			if r.currentStmtLine != 0 {
				return r.currentStmtLine
			}
			return 0
		},
		ReportError: func(err error) {
			r.expandErr(err)
		},
		CmdSubst: func(w io.Writer, cs *syntax.CmdSubst) error {
			switch len(cs.Stmts) {
			case 0: // nothing to do
				return nil
			case 1: // $(<file)
				word := catShortcutArg(cs.Stmts[0])
				if word == nil {
					break
				}
				path := r.literal(word)
				f, err := r.open(ctx, path, os.O_RDONLY, 0, true)
				if err != nil {
					return err
				}
				_, err = io.Copy(w, f)
				f.Close()
				return err
			}
			r2 := r.subshell(false)
			if r.expandBaseFDs != nil {
				r2.fds = cloneFDTable(r.expandBaseFDs)
				r2.syncStandardFDs()
			}
			r2.opts[optVerbose] = false
			r2.setStdoutWriter(w)
			r2.stmts(ctx, cs.Stmts)
			r2.exit.exiting = false // subshells don't exit the parent shell
			r.lastExpandExit = r2.exit
			if r2.exit.fatalExit {
				return r2.exit.err // surface fatal errors immediately
			}
			return nil
		},
		ProcSubst: func(ps *syntax.ProcSubst) (string, error) {
			if len(ps.Stmts) == 0 { // nothing to do
				return "/dev/null", nil
			}
			if r.procSubstHandler != nil {
				return r.customProcSubst(ctx, ps)
			}
			return "", fmt.Errorf("process substitution unavailable")
		},
	}
	r.updateExpandOpts()
}

func (r *Runner) expandingRedirectWord(fn func() string) string {
	r.inRedirectWord++
	defer func() {
		r.inRedirectWord--
	}()
	return fn()
}

func (r *Runner) customProcSubst(ctx context.Context, ps *syntax.ProcSubst) (string, error) {
	endpoint, err := r.procSubstHandler(r.handlerCtx(ctx, handlerKindProcSubst, ps.Pos()), ps)
	if err != nil {
		return "", err
	}
	if endpoint == nil {
		return "", fmt.Errorf("process substitution handler returned nil endpoint")
	}
	if endpoint.Path == "" {
		return "", fmt.Errorf("process substitution handler returned empty path")
	}

	return r.runProcSubst(ctx, ps, endpoint.Path, func(r2 *Runner) (func(), error) {
		stdout := r.origStdout
		cleanup := func() {
			if endpoint.Cleanup == nil {
				return
			}
			if err := endpoint.Cleanup(); err != nil {
				r.errf("process substitution cleanup: %v\n", err)
			}
		}
		switch ps.Op {
		case syntax.CmdIn:
			if endpoint.Writer == nil {
				return nil, fmt.Errorf("process substitution writer is nil")
			}
			r2.setStdoutWriter(endpoint.Writer)
			return func() {
				if err := endpoint.Writer.Close(); err != nil {
					r.errf("closing process substitution writer: %v\n", err)
				}
				cleanup()
			}, nil
		case syntax.CmdOut:
			if endpoint.Reader == nil {
				return nil, fmt.Errorf("process substitution reader is nil")
			}
			stdin, release, err := procSubstStdin(endpoint.Reader)
			if err != nil {
				cleanup()
				return nil, err
			}
			r2.setStdinReader(stdin)
			r2.setStdoutWriter(stdout)
			return func() {
				release()
				cleanup()
			}, nil
		default:
			panic(fmt.Sprintf("unsupported process substitution operator: %q", ps.Op))
		}
	})
}

func (r *Runner) runProcSubst(ctx context.Context, ps *syntax.ProcSubst, path string, configure func(r2 *Runner) (func(), error)) (string, error) {
	r2 := r.subshell(true)
	// TODO: note that `man bash` mentions that `wait` only waits for the last
	// process substitution as long as it is $!; the logic here would mean we wait for all of them.
	bg := bgProc{
		done:          make(chan struct{}),
		runner:        r2,
		exit:          new(exitStatus),
		procSubst:     true,
		waitAtStmtEnd: r.inRedirectWord > 0 && ps.Op == syntax.CmdOut,
	}
	r.bgProcs = append(r.bgProcs, bg)
	go func() {
		defer func() {
			*bg.exit = r2.exit
			close(bg.done)
		}()

		release, err := configure(r2)
		if err != nil {
			r.errf("%v\n", err)
			return
		}
		if release != nil {
			defer release()
		}
		r2.stmts(ctx, ps.Stmts)
		r2.exit.exiting = false // subshells don't exit the parent shell
	}()
	return path, nil
}

func procSubstStdin(reader io.ReadCloser) (StdinReader, func(), error) {
	if reader == nil {
		return nil, nil, fmt.Errorf("process substitution reader is nil")
	}
	if sr, ok := reader.(StdinReader); ok {
		return sr, func() {
			_ = reader.Close()
		}, nil
	}
	sr := stdinReader(reader)
	return sr, func() {
		if closer, ok := sr.(io.Closer); ok {
			_ = closer.Close()
		}
		_ = reader.Close()
	}, nil
}

// catShortcutArg checks if a statement is of the form "$(<file)". The redirect
// word is returned if there's a match, and nil otherwise.
func catShortcutArg(stmt *syntax.Stmt) *syntax.Word {
	if stmt.Cmd != nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return nil
	}
	if len(stmt.Redirs) != 1 {
		return nil
	}
	redir := stmt.Redirs[0]
	if redir.Op != syntax.RdrIn {
		return nil
	}
	return redir.Word
}

func (r *Runner) updateExpandOpts() {
	if r.opts[optNoGlob] {
		r.ecfg.ReadDir = nil
	} else {
		r.ecfg.ReadDir = func(s string) ([]fs.DirEntry, error) {
			return r.readDirHandler(r.handlerCtx(r.ectx, handlerKindReadDir, todoPos), s)
		}
	}
	r.ecfg.GlobStar = r.opts[optGlobStar]
	r.ecfg.DotGlob = r.opts[optDotGlob]
	r.ecfg.NoCaseGlob = r.opts[optNoCaseGlob]
	r.ecfg.NullGlob = r.opts[optNullGlob]
	r.ecfg.FailGlob = r.opts[optFailGlob]
	r.ecfg.GlobSkipDots = r.opts[optGlobSkipDots]
	r.ecfg.NoUnset = r.opts[optNoUnset]
	r.ecfg.ExtGlob = r.opts[optExtGlob]
}

func (r *Runner) expandErr(err error) {
	if err == nil {
		return
	}
	var (
		cmdArithErr arithmCommandError
		divErr      *expand.ArithmDivByZeroError
	)
	if r.currentChunkSource != "" && !errors.As(err, &cmdArithErr) && errors.As(err, &divErr) {
		err = expand.WithArithmSource(
			err,
			r.currentChunkSource,
			r.currentChunkSourceBase,
			r.currentChunkSourceBase+uint(len(r.currentChunkSource)),
		)
	}
	errMsg := err.Error()
	fatalExpansionErr := r.commandString && !r.interactive
	var (
		unboundVarErr  expand.UnboundVariableError
		unsetErr       expand.UnsetParameterError
		indirectErr    expand.InvalidIndirectExpansionError
		invalidNameErr expand.InvalidVariableNameError
		failGlobErr    expand.FailGlobError
		arithSyntaxErr expand.ArithmSyntaxError
		arithDiagErr   *expand.ArithmDiagnosticError
	)
	if r.commandString && !r.interactive &&
		(errors.As(err, &arithSyntaxErr) || errors.As(err, &arithDiagErr)) {
		if name := r.lookupVar("0").String(); name != "" && name != "gosh" {
			errMsg = name + ": " + errMsg
		}
	}
	fmt.Fprintln(r.stderr, errMsg)
	switch {
	case errors.As(err, &cmdArithErr):
		r.exit.code = 1
	case errors.As(err, &unboundVarErr):
		r.exit.code = 127
		if r.inSubshell {
			r.exit.code = 1
		}
		if r.opts[optErrExit] {
			r.exit.code = 1
		}
		if r.interactive {
			if r.currentStmtLine != 0 {
				r.skipStmtLine = r.currentStmtLine
			}
		} else {
			r.exit.exiting = true
		}
	case errors.As(err, &unsetErr):
		r.exit.code = 127
		if r.inSubshell && unsetErr.Message == "unbound variable" {
			r.exit.code = 1
		}
		if r.opts[optErrExit] {
			r.exit.code = 1
		}
		if r.interactive {
			if r.currentStmtLine != 0 {
				r.skipStmtLine = r.currentStmtLine
			}
		} else {
			r.exit.exiting = true
		}
	case errors.As(err, &indirectErr):
		r.exit.code = 1
	case errors.As(err, &invalidNameErr):
		r.exit.code = 1
	case errors.As(err, &failGlobErr):
		r.exit.code = 1
		if r.currentStmtLine != 0 {
			r.skipStmtLine = r.currentStmtLine
		}
	case errors.As(err, &arithSyntaxErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case errors.As(err, &arithDiagErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case errMsg == "bad substitution" ||
		strings.HasPrefix(errMsg, "bad substitution:") ||
		strings.Contains(errMsg, ": bad substitution"):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case strings.Contains(errMsg, "substring expression < 0"):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case errMsg == "invalid indirect expansion":
		r.exit.code = 1
	default:
		return // other cases do not exit
	}
}

func (r *Runner) arithm(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, false, "", 0, 0)
}

func (r *Runner) arithmCmd(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, true, "", 0, 0)
}

func (r *Runner) arithmEval(expr syntax.ArithmExpr, command bool, source string, sourceStart, sourceEnd uint) int {
	var (
		n   int
		err error
	)
	if source != "" {
		n, err = expand.ArithmWithSource(r.ecfg, expr, source, sourceStart, sourceEnd)
	} else {
		n, err = expand.Arithm(r.ecfg, expr)
	}
	var syntaxErr expand.ArithmSyntaxError
	var diagErr *expand.ArithmDiagnosticError
	if command && (errors.As(err, &syntaxErr) || errors.As(err, &diagErr)) {
		err = arithmCommandError{err: err}
	}
	r.expandErr(err)
	if command && err != nil && r.exit.code == 0 {
		r.exit.code = 1
	}
	return n
}

func (r *Runner) arithmCmdExpr(cm *syntax.ArithmCmd) int {
	return r.arithmEval(cm.X, true, cm.Source, cm.Left.Offset()+2, cm.Right.Offset())
}

type arithmCommandError struct {
	err error
}

func (e arithmCommandError) Error() string {
	var diagErr *expand.ArithmDiagnosticError
	if runtime.GOOS == "darwin" && errors.As(e.err, &diagErr) && diagErr.Message == "syntax error in expression" {
		return e.err.Error()
	}
	return fmt.Sprintf("((: %s", e.err)
}

func (e arithmCommandError) Unwrap() error {
	return e.err
}

func (r *Runner) fields(words ...*syntax.Word) []string {
	strs, err := expand.Fields(r.ecfg, words...)
	r.expandErr(err)
	return strs
}

func (r *Runner) literal(word *syntax.Word) string {
	str, err := expand.Literal(r.ecfg, word)
	r.expandErr(err)
	return str
}

func condTildeExpandsInDBrackets() bool {
	return runtime.GOOS != "darwin"
}

func (r *Runner) condLiteral(word *syntax.Word) string {
	var (
		str string
		err error
	)
	if condTildeExpandsInDBrackets() {
		str, err = expand.Literal(r.ecfg, word)
	} else {
		str, err = expand.LiteralNoTilde(r.ecfg, word)
	}
	r.expandErr(err)
	return str
}

func (r *Runner) assignmentLiteral(word *syntax.Word) string {
	if r.ecfg == nil {
		r.fillExpandConfig(context.Background())
	}
	cfg := *r.ecfg
	str, err := expand.AssignmentLiteral(&cfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) assignmentWordLiteral(word *syntax.Word) string {
	if r.ecfg == nil {
		r.fillExpandConfig(context.Background())
	}
	cfg := *r.ecfg
	str, err := expand.AssignmentWordLiteral(&cfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) document(word *syntax.Word) string {
	str, err := expand.Document(r.ecfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) pattern(pat *syntax.Pattern) string {
	str, err := expand.Pattern(r.ecfg, pat)
	r.expandErr(err)
	return str
}

func (r *Runner) condPattern(pat *syntax.Pattern) string {
	var (
		str string
		err error
	)
	if condTildeExpandsInDBrackets() {
		str, err = expand.Pattern(r.ecfg, pat)
	} else {
		str, err = expand.PatternNoTilde(r.ecfg, pat)
	}
	r.expandErr(err)
	return str
}

func (r *Runner) patternWord(word *syntax.Word) string {
	str, err := expand.PatternWord(r.ecfg, word)
	r.expandErr(err)
	return str
}

// expandEnviron exposes [Runner]'s variables to the expand package.
type expandEnv struct {
	r *Runner
}

var _ expand.WriteEnviron = expandEnv{}
var _ expand.Environ = tildeExpandEnv{}

func (e expandEnv) Get(name string) expand.Variable {
	return e.r.lookupVar(name)
}

func (e expandEnv) Set(name string, vr expand.Variable) error {
	e.r.setVar(name, vr)
	return nil // TODO: return any errors
}

func (e expandEnv) SetVarRef(ref *syntax.VarRef, vr expand.Variable, appendValue bool) error {
	return e.r.setVarByRef(e.r.lookupVar(ref.Name.Value), ref, vr, appendValue, attrUpdate{})
}

func (e expandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
}

// tildeExpandEnv keeps named-user tilde expansion inside sandbox-owned data.
type tildeExpandEnv struct {
	r *Runner
}

func (e tildeExpandEnv) Get(name string) expand.Variable {
	if !strings.HasPrefix(name, "HOME ") {
		return e.r.lookupVar(name)
	}
	if vr := e.r.lookupVar(name); vr.IsSet() {
		return vr
	}
	user := strings.TrimSpace(strings.TrimPrefix(name, "HOME "))
	if user == "" {
		return expand.Variable{}
	}
	if home, ok := e.passwdHome(user); ok {
		return expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: home}
	}
	if home, ok := defaultNamedUserHome(user); ok {
		return expand.Variable{Set: true, Exported: true, Kind: expand.String, Str: home}
	}
	return expand.Variable{}
}

func (e tildeExpandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
}

func (e tildeExpandEnv) passwdHome(user string) (string, bool) {
	ctx := e.r.ectx
	if ctx == nil {
		ctx = context.Background()
	}
	f, err := e.r.open(ctx, "/etc/passwd", os.O_RDONLY, 0, false)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] != user {
			continue
		}
		home := strings.TrimSpace(fields[5])
		if home == "" {
			return "", false
		}
		return home, true
	}
	return "", false
}

func defaultNamedUserHome(user string) (string, bool) {
	if user != "root" {
		return "", false
	}
	if runtime.GOOS == "darwin" {
		return "/var/root", true
	}
	return "/root", true
}

var todoPos syntax.Pos // for handlerCtx callers where we don't yet have a position

func (r *Runner) handlerCtx(ctx context.Context, kind handlerKind, pos syntax.Pos) context.Context {
	hc := HandlerContext{
		runner:   r,
		kind:     kind,
		Env:      &overlayEnviron{parent: r.writeEnv},
		Dir:      r.Dir,
		ExecFile: r.currentExecFile(),
		Internal: r.currentInternal(),
		Pos:      pos,
		Stdout:   r.stdout,
		Stderr:   r.stderr,
	}
	if r.stdin != nil { // do not leave hc.Stdin as a typed nil
		hc.Stdin = r.stdin
	}
	return context.WithValue(ctx, handlerCtxKey{}, &hc)
}

func (r *Runner) out(s string) {
	io.WriteString(r.stdout, s)
}

func (r *Runner) outf(format string, a ...any) {
	fmt.Fprintf(r.stdout, format, a...)
}

func (r *Runner) errf(format string, a ...any) {
	fmt.Fprintf(r.stderr, format, a...)
}

func (r *Runner) stop(ctx context.Context) bool {
	if r.exit.returning || r.exit.exiting {
		return true
	}
	if err := ctx.Err(); err != nil {
		r.exit.fatal(err)
		return true
	}
	if r.opts[optNoExec] {
		return true
	}
	return false
}

func (r *Runner) stmt(ctx context.Context, st *syntax.Stmt) {
	if r.stop(ctx) {
		return
	}
	line := st.Pos().Line()
	if r.skipStmtLine != 0 {
		switch {
		case line == r.skipStmtLine:
			return
		case line > r.skipStmtLine:
			r.skipStmtLine = 0
		}
	}
	r.exit = exitStatus{}
	r.currentStmtLine = line
	r.pipeStatusSet = false
	defer func() {
		r.currentStmtLine = 0
	}()
	if st.Background || st.Disown {
		r2 := r.subshell(true)
		st2 := *st
		st2.Background = false
		st2.Disown = false
		bg := bgProc{
			done:   make(chan struct{}),
			exit:   new(exitStatus),
			runner: r2,
		}
		r.bgProcs = append(r.bgProcs, bg)
		go func() {
			r2.Run(ctx, &st2)
			r2.exit.exiting = false // subshells don't exit the parent shell
			*bg.exit = r2.exit
			close(bg.done)
		}()
		r.setPipeStatuses(0)
	} else {
		r.stmtSync(ctx, st)
	}
	r.runPendingSignalTraps(ctx)
	r.lastExit = r.exit
}

func (r *Runner) sourceForNode(node syntax.Node) string {
	if node == nil || r.currentChunkSource == "" {
		return ""
	}
	startOffset := node.Pos().Offset()
	endOffset := node.End().Offset()
	if endOffset < startOffset || startOffset < r.currentChunkSourceBase {
		return ""
	}
	start := int(startOffset - r.currentChunkSourceBase)
	end := int(endOffset - r.currentChunkSourceBase)
	if start < 0 || end < start || end > len(r.currentChunkSource) {
		return ""
	}
	return r.currentChunkSource[start:end]
}

func (r *Runner) verboseStmtSource(st *syntax.Stmt) string {
	src := r.sourceForNode(st)
	if src != "" {
		end := int(st.End().Offset() - r.currentChunkSourceBase)
		if end >= 0 && end < len(r.currentChunkSource) && r.currentChunkSource[end] == '\n' {
			src += "\n"
		}
		return src
	}
	return printSyntaxNode(st)
}

func (r *Runner) printVerbose(st *syntax.Stmt) {
	if !r.opts[optVerbose] {
		return
	}
	src := r.verboseStmtSource(st)
	if src == "" {
		return
	}
	if !strings.HasSuffix(src, "\n") {
		src += "\n"
	}
	io.WriteString(r.stderr, src)
}

func (r *Runner) stmtSync(ctx context.Context, st *syntax.Stmt) {
	if r.currentChunkSource == "" {
		r.printVerbose(st)
	}
	r.ensureFDTable()
	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	oldFDs := cloneFDTable(r.fds)
	oldTraceOutput := r.traceOutput
	oldExpandBaseFDs := r.expandBaseFDs
	oldNoErrExit := r.noErrExit
	if st.Negated {
		r.noErrExit = true
	}
	defer func() {
		r.noErrExit = oldNoErrExit
		r.traceOutput = oldTraceOutput
		r.expandBaseFDs = oldExpandBaseFDs
	}()
	if len(st.Redirs) > 0 && usesPreRedirectExpandFDs(st.Cmd) {
		if r.traceOutput == nil {
			r.traceOutput = oldErr
		}
		r.expandBaseFDs = oldFDs
	}
	r.pushFDSnapshot(oldFDs)
	procSubstStart := len(r.bgProcs)
	closers := make([]io.Closer, 0, len(st.Redirs))
	keepClosedFDs := make(map[int]struct{}, len(st.Redirs))
	releasedNamedFDs := make([]string, 0, len(st.Redirs))
	for _, rd := range st.Redirs {
		result, err := r.redir(ctx, rd)
		if err != nil {
			r.exit.code = 1
			break
		}
		if result.closer != nil {
			closers = append(closers, result.closer)
		}
		for _, fd := range result.keepClosed {
			keepClosedFDs[fd] = struct{}{}
		}
		releasedNamedFDs = append(releasedNamedFDs, result.releasedNamedFDs...)
	}
	if r.exit.ok() && st.Cmd != nil {
		r.cmd(ctx, st.Cmd)
	}
	if !r.pipeStatusSet {
		r.setPipeStatuses(r.exit.code)
	}
	keepRedirs := r.keepRedirs
	r.keepRedirs = false
	if keepRedirs {
		for _, name := range releasedNamedFDs {
			r.markNamedFDReleased(name)
		}
	}
	if !keepRedirs {
		for i := len(closers) - 1; i >= 0; i-- {
			_ = closers[i].Close()
		}
	}
	r.waitProcSubsts(procSubstStart)
	if st.Negated {
		if r.exit.ok() {
			r.exit.code = 1
		} else {
			r.exit.clear()
		}
	} else if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.AndStmt || b.Op == syntax.OrStmt) {
	} else {
		r.maybeRunErrTrap(ctx, st.Pos().Line())
	}
	if !keepRedirs {
		r.stdin, r.stdout, r.stderr = oldIn, oldOut, oldErr
		r.fds = oldFDs
		for fd := range keepClosedFDs {
			r.setFD(fd, nil)
		}
		r.popFDSnapshot()
		r.syncStandardFDs()
	} else {
		r.popFDSnapshot()
		r.closeUnusedSnapshotFDs(oldFDs)
		r.syncStandardFDs()
	}
}

func usesPreRedirectExpandFDs(cmd syntax.Command) bool {
	switch cmd.(type) {
	case *syntax.CallExpr, *syntax.DeclClause:
		return true
	default:
		return false
	}
}

type redirResult struct {
	closer           io.Closer
	keepClosed       []int
	releasedNamedFDs []string
}

type redirectDupSpec struct {
	sourceFD int
	move     bool
}

func (r *Runner) waitProcSubsts(start int) {
	if start < 0 || start >= len(r.bgProcs) {
		return
	}
	for i := start; i < len(r.bgProcs); i++ {
		bg := r.bgProcs[i]
		if !bg.procSubst || !bg.waitAtStmtEnd {
			continue
		}
		<-bg.done
	}
}

func (r *Runner) cmd(ctx context.Context, cm syntax.Command) {
	if r.stop(ctx) {
		return
	}

	tracingEnabled := r.opts[optXTrace]
	trace := r.tracer()

	switch cm := cm.(type) {
	case *syntax.Block:
		r.stmts(ctx, cm.Stmts)
	case *syntax.Subshell:
		r2 := r.subshell(false)
		r2.stmts(ctx, cm.Stmts)
		r2.exit.exiting = false // subshells don't exit the parent shell
		r.exit = r2.exit
	case *syntax.CallExpr:
		if r.runDebugTrap(ctx, debugLineForCommand(cm)) {
			return
		}
		args := cm.Args
		r.lastExpandExit = exitStatus{}
		fields, decl := r.resolveCallExprArgs(args)
		if decl != nil {
			if !r.expandAssignsForSideEffects(cm.Assigns) {
				return
			}
			r.cmd(ctx, decl)
			return
		}
		if len(fields) == 0 {
			for _, as := range cm.Assigns {
				prev := r.lookupVar(as.Ref.Name.Value)
				// Here we have a naked "foo=bar", so if we inherited a local var from a parent
				// function we want to signal that we are modifying the parent var rather than
				// creating a new local var via "local foo=bar".
				// TODO: there is likely a better way to do this.
				prev.Local = false

				vr, _, ok := r.assignVal(prev, as, "")
				if !ok || r.exit.fatalExit || r.exit.exiting {
					return
				}
				// Preserve and apply variable attributes from the previous declaration.
				vr.Integer = prev.Integer
				vr.Lower = prev.Lower
				vr.Trace = prev.Trace
				vr.Upper = prev.Upper
				if prev.Integer && as.Append && vr.Kind == expand.String {
					// For -i with +=, do arithmetic addition instead of string concat.
					oldVal := r.evalIntegerAttr(prev.String())
					newVal := r.evalIntegerAttr(r.assignLiteral(as))
					vr.Str = strconv.Itoa(oldVal + newVal)
				} else {
					r.applyVarAttrs(&vr)
				}
				if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
					r.errf("%v\n", err)
					r.exit.code = 1
					var strictErr strictIndexedSubscriptError
					if errors.As(err, &strictErr) {
						r.exit.exiting = true
						return
					}
					continue
				}

				if !tracingEnabled {
					continue
				}

				// Strangely enough, it seems like Bash prints original
				// source for arrays, but the expanded value otherwise.
				// TODO: add test cases for x[i]=y and x+=y.
				if as.Array != nil {
					trace.expr(as)
				} else if as.Value != nil {
					val, err := syntax.Quote(vr.String(), syntax.LangBash)
					if err != nil { // should never happen
						panic(err)
					}
					trace.stringf("%s=%s", printVarRef(as.Ref), val)
				}
				trace.newLineFlush()
			}
			// If interpreting the last expansion like $(foo) failed,
			// and the expansion and assignments otherwise succeeded,
			// we need to surface that last exit code.
			if r.exit.ok() {
				r.exit = r.lastExpandExit
			}
			if !r.exit.fatalExit && !r.exit.exiting && r.exit.err == nil {
				r.setSpecialUnderscore("")
			}
			break
		}

		restores := r.runCallAssigns(cm.Assigns)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			r.restoreCallAssigns(restores)
			break
		}

		r.setSpecialUnderscoreFromFields(fields)

		trace.call(fields[0], fields[1:]...)
		trace.newLineFlush()

		r.call(ctx, cm.Args[0].Pos(), fields)
		r.restoreCallAssigns(restores)
	case *syntax.BinaryCmd:
		switch cm.Op {
		case syntax.AndStmt, syntax.OrStmt:
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.stmt(ctx, cm.X)
			r.noErrExit = oldNoErrExit
			if r.exit.ok() == (cm.Op == syntax.AndStmt) {
				r.stmt(ctx, cm.Y)
			}
		case syntax.Pipe, syntax.PipeAll:
			pr, pw := r.newPipe()
			r2 := r.subshell(true)
			r2.setStdoutWriter(pw)
			if cm.Op == syntax.PipeAll {
				r2.setStderrWriter(pw)
			} else {
				r2.setStderrWriter(r.stderr)
			}
			r.setStdinReader(pr)
			var wg sync.WaitGroup
			wg.Go(func() {
				r2.stmt(ctx, cm.X)
				r2.exit.exiting = false // subshells don't exit the parent shell
				pw.Close()
			})
			if stmt, ok := r.lastpipeStmt(cm.Y); ok {
				r.stmt(ctx, stmt)
			} else {
				r.stmt(ctx, cm.Y)
			}
			pr.Close()
			wg.Wait()
			leftStatuses := r2.pipeStatusValues()
			if len(leftStatuses) == 0 {
				leftStatuses = []string{strconv.Itoa(int(r2.exit.code))}
			}
			rightStatuses := r.pipeStatusValues()
			if len(rightStatuses) == 0 {
				rightStatuses = []string{strconv.Itoa(int(r.exit.code))}
			}
			combined := append(append([]string(nil), leftStatuses...), rightStatuses...)
			r.setPipeStatusValues(combined)
			if r.opts[optPipeFail] && !r2.exit.ok() && r.exit.ok() {
				r.exit = r2.exit
			}
			if r2.exit.fatalExit {
				r.exit.fatal(r2.exit.err) // surface fatal errors immediately
			}
		}
	case *syntax.IfClause:
		oldNoErrExit := r.noErrExit
		r.noErrExit = true
		r.stmts(ctx, cm.Cond)
		r.noErrExit = oldNoErrExit

		if r.exit.ok() {
			r.stmts(ctx, cm.Then)
			break
		}
		r.exit.clear()
		if cm.Else != nil {
			r.cmd(ctx, cm.Else)
		}
	case *syntax.WhileClause:
		for !r.stop(ctx) {
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.stmts(ctx, cm.Cond)
			r.noErrExit = oldNoErrExit

			stop := r.exit.ok() == cm.Until
			r.exit.clear()
			if stop || r.loopStmtsBroken(ctx, cm.Do) {
				break
			}
		}
	case *syntax.ForClause:
		switch y := cm.Loop.(type) {
		case *syntax.WordIter:
			name := y.Name.Value
			items := r.Params // for i; do ...

			inToken := y.InPos.IsValid()
			if inToken {
				items = r.fields(y.Items...) // for i in ...; do ...
			}

			if cm.Select {
				ps3 := shellDefaultPS3
				if e := r.envGet(shellReplyPS3Var); e != "" {
					ps3 = e
				}

				prompt := func() []byte {
					// display menu
					for i, word := range items {
						r.errf("%d) %v\n", i+1, word)
					}
					r.errf("%s", ps3)

					line, err := r.readLine(ctx, true)
					if err != nil {
						r.exit.code = 1
						return nil
					}
					return line
				}

			retry:
				choice := prompt()
				if len(choice) == 0 {
					goto retry // no reply; try again
				}

				reply := string(choice)
				r.setVarString(shellReplyVar, reply)

				c, _ := strconv.Atoi(reply)
				if c > 0 && c <= len(items) {
					r.setVarString(name, items[c-1])
				}

				// execute commands until break or return is encountered
				if r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
			}

			for _, field := range items {
				if r.runDebugTrap(ctx, cm.Pos().Line()) {
					return
				}
				r.setVarString(name, field)
				trace.stringf("for %s in", y.Name.Value)
				if inToken {
					for _, item := range y.Items {
						trace.string(" ")
						trace.expr(item)
					}
				} else {
					trace.string(` "$@"`)
				}
				trace.newLineFlush()
				if r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
				trace.refreshPrefixContext()
			}
		case *syntax.CStyleLoop:
			if y.Init != nil {
				r.arithmCmd(y.Init)
				if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
					break
				}
			}
			for {
				if r.runDebugTrap(ctx, cm.Pos().Line()) {
					return
				}
				if y.Cond != nil && r.arithmCmd(y.Cond) == 0 {
					break
				}
				if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
					break
				}
				if r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
				if y.Post != nil {
					if r.runDebugTrap(ctx, cm.Pos().Line()) {
						return
					}
					r.arithmCmd(y.Post)
					if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
						break
					}
				}
			}
		}
	case *syntax.FuncDecl:
		r.setFunc(cm.Name.Value, cm.Body)
	case *syntax.ArithmCmd:
		if r.runDebugTrap(ctx, debugLineForCommand(cm)) {
			return
		}
		if tracingEnabled {
			if src := r.sourceForNode(cm); src != "" {
				if strings.HasPrefix(src, "((") && strings.HasSuffix(src, "))") {
					trace.string("(( ")
					trace.string(src[2 : len(src)-2])
					trace.string(" ))")
				} else {
					trace.string(src)
				}
			} else {
				trace.expr(cm)
			}
			trace.newLineFlush()
		}
		val := r.arithmCmdExpr(cm)
		if r.exit.ok() {
			r.exit.oneIf(val == 0)
		}
	case *syntax.LetClause:
		var val int
		for _, expr := range cm.Exprs {
			val = r.arithm(expr)

			if !tracingEnabled {
				continue
			}

			switch expr := expr.(type) {
			case *syntax.Word:
				qs, err := syntax.Quote(r.literal(expr), syntax.LangBash)
				if err != nil {
					return
				}
				trace.stringf("let %v", qs)
			case *syntax.BinaryArithm, *syntax.UnaryArithm:
				trace.expr(cm)
			case *syntax.ParenArithm:
				// TODO
			}
		}

		trace.newLineFlush()
		if r.exit.ok() {
			r.exit.oneIf(val == 0)
		}
	case *syntax.CaseClause:
		if r.runDebugTrap(ctx, cm.Pos().Line()) {
			return
		}
		trace.string("case ")
		trace.expr(cm.Word)
		trace.string(" in")
		trace.newLineFlush()
		str := r.literal(cm.Word)
		fallthroughNext := false
		for _, ci := range cm.Items {
			matched := fallthroughNext
			if !matched {
				for _, word := range ci.Patterns {
					pat := r.pattern(word)
					if match(pat, str) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
			r.stmts(ctx, ci.Stmts)
			if r.stop(ctx) || r.breakEnclosing > 0 || r.contnEnclosing > 0 {
				return
			}
			switch ci.Op {
			case syntax.Fallthrough:
				fallthroughNext = true
			case syntax.Resume, syntax.ResumeKorn:
				fallthroughNext = false
			default:
				return
			}
		}
	case *syntax.TestClause:
		if r.runDebugTrap(ctx, debugLineForCommand(cm)) {
			return
		}
		cond := r.evalCond(ctx, cm.X, trace)
		if tracingEnabled {
			trace.string("[[ ")
			trace.string(cond.trace)
			trace.string(" ]]")
			trace.newLineFlush()
		}
		if cond.value == "" && r.exit.ok() {
			// to preserve exit status code 2 for regex errors, etc
			r.exit.code = 1
		}
	case *syntax.DeclClause:
		if r.runDebugTrap(ctx, debugLineForCommand(cm)) {
			return
		}
		local, global := false, false
		var modes []string
		valType := ""
		declQuery := "" // "-f", "-F", or "-p" for query mode
		underscore := cm.Variant.Value
		allowPlusFlags := false
		builtinTraceFields := []string{cm.Variant.Value}
		var leadingTraceLines []string
		var trailingTraceLines []string
		declName := cm.Variant.Value
		switch cm.Variant.Value {
		case "declare":
			// When used in a function, "declare" acts as "local"
			// unless the "-g" option is used.
			local = r.inFunc
			allowPlusFlags = true
		case "local":
			if !r.inFunc {
				r.errf("local: can only be used in a function\n")
				r.exit.code = 1
				return
			}
			local = true
		case "export":
			modes = append(modes, "-x")
		case "readonly":
			modes = append(modes, "-r")
		case "nameref":
			valType = "-n"
		case "typeset":
			allowPlusFlags = true
		}
		declErrf := func(format string, a ...any) {
			if r.evalDepth > 0 {
				r.errf("eval: ")
			}
			r.errf(format, a...)
		}
		printFunction := func(name string, body *syntax.Stmt) {
			r.outf("%s()\n", name)
			printer := syntax.NewPrinter()
			var buf bytes.Buffer
			printer.Print(&buf, body)
			r.outf("%s\n", buf.String())
		}
		printDeclaredVar := func(name string, vr expand.Variable) {
			declVR := vr
			if r.hideReadonlyArrayDeclKind(name, declVR) {
				declVR.Kind = expand.Unknown
			}
			flags := declVR.Flags()
			if flags == "" {
				flags = "--"
			} else {
				flags = "-" + flags
			}
			switch vr.Kind {
			case expand.Indexed:
				r.outf("declare %s %s", flags, name)
				if !vr.IsSet() {
					r.out("\n")
					return
				}
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
				r.outf("declare %s %s", flags, name)
				if !vr.IsSet() {
					r.out("\n")
					return
				}
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
				r.outf("declare %s %s", flags, name)
				if !vr.IsSet() {
					r.out("\n")
					return
				}
				r.outf("=%s\n", bashDeclPrintValue(vr.Str))
			}
		}
		printPlainVar := func(name string, vr expand.Variable) {
			switch vr.Kind {
			case expand.Indexed, expand.Associative:
				printDeclaredVar(name, vr)
			default:
				if !vr.IsSet() {
					r.outf("%s\n", name)
					return
				}
				r.outf("%s=%s\n", name, bashDeclPlainValue(vr.Str))
			}
		}
		listedVars := func(currentOnly bool) map[string]expand.Variable {
			seen := make(map[string]expand.Variable)
			if currentOnly {
				currentScopeVars(localScopeEnv(r.writeEnv), func(name string, vr expand.Variable) bool {
					seen[name] = vr
					return true
				})
				return seen
			}
			r.writeEnv.Each(func(name string, vr expand.Variable) bool {
				seen[name] = vr
				return true
			})
			return seen
		}
		matchesVarFilter := func(name string, vr expand.Variable) bool {
			if !vr.Declared() {
				return false
			}
			if name == "BASH_EXECUTION_STRING" && len(modes) > 0 {
				return false
			}
			switch valType {
			case "-a":
				if vr.Kind != expand.Indexed {
					return false
				}
			case "-A":
				if vr.Kind != expand.Associative {
					return false
				}
			case "-n":
				if vr.Kind != expand.NameRef {
					return false
				}
			}
			if declQuery == "" || (declQuery == "-p" && declName == "local") {
				switch declName {
				case "readonly":
					return vr.ReadOnly
				case "export":
					return vr.Exported
				case "local":
					return vr.Local
				case "nameref":
					return vr.Kind == expand.NameRef
				}
			}
			for _, mode := range modes {
				switch mode {
				case "-r":
					if !vr.ReadOnly {
						return false
					}
				case "-x":
					if !vr.Exported {
						return false
					}
				case "-i":
					if !vr.Integer {
						return false
					}
				case "-l":
					if !vr.Lower {
						return false
					}
				case "-t":
					if !vr.Trace {
						return false
					}
				case "-u":
					if !vr.Upper {
						return false
					}
				}
			}
			return true
		}
		printListedVars := func(currentOnly, plain bool) {
			seen := listedVars(currentOnly)
			names := make([]string, 0, len(seen))
			for name, vr := range seen {
				if !matchesVarFilter(name, vr) {
					continue
				}
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				vr := seen[name]
				if plain {
					printPlainVar(name, vr)
				} else {
					printDeclaredVar(name, vr)
				}
			}
		}
		arrayConversionError := func(name string, vr expand.Variable) string {
			switch {
			case valType == "-A" && vr.Kind == expand.Indexed:
				return fmt.Sprintf("%s: cannot convert indexed to associative array", name)
			case valType == "-a" && vr.Kind == expand.Associative:
				return fmt.Sprintf("%s: cannot convert associative to indexed array", name)
			default:
				return ""
			}
		}
		runWithWriteEnv := func(env expand.WriteEnviron, fn func() bool) bool {
			if env == r.writeEnv {
				return fn()
			}
			origEnv := r.writeEnv
			r.writeEnv = env
			defer func() {
				r.writeEnv = origEnv
			}()
			return fn()
		}
		declEvalEnv := newOverlayEnviron(r.writeEnv, true)
		declLookupVar := func(env expand.WriteEnviron, name string) expand.Variable {
			if !local || global {
				return env.Get(name)
			}
			if vr, ok := currentScopeVar(localScopeEnv(env), name); ok {
				if !vr.Local {
					return expand.Variable{}
				}
				return vr
			}
			return expand.Variable{}
		}
		onlyFlagOperands := true
		processNamedOperand := func(ref *syntax.VarRef, as *syntax.Assign, isAssign bool, raw string) bool {
			name := ref.Name.Value
			if declQuery != "-f" && declQuery != "-F" && !syntax.ValidName(name) {
				if allowPlusFlags && strings.HasPrefix(raw, "+") && !strings.ContainsAny(name, "[]") {
					r.errf("%s: invalid name %q\n", declName, name)
				} else {
					r.errf("%s: `%s': not a valid identifier\n", declName, name)
				}
				r.exit.code = 1
				return false
			}
			onlyFlagOperands = false
			if isAssign {
				switch {
				case as != nil && as.Array != nil:
					underscore = name
				case raw != "":
					underscore = raw
				default:
					underscore = name
				}
			} else {
				underscore = name
			}
			if tracingEnabled && !isAssign {
				builtinTraceFields = append(builtinTraceFields, name)
			}
			if raw == "" {
				raw = name
			}
			if declQuery == "-f" && functionTraceOnlyModes(modes) {
				info, ok := r.funcInfo(name)
				if !ok || info.body == nil {
					r.exit.code = 1
					return true
				}
				for _, mode := range modes {
					switch mode {
					case "-t":
						info.trace = true
					case "+t":
						info.trace = false
					}
				}
				r.setFuncInfo(name, info)
				return true
			}
			if declQuery == "-f" {
				// declare -f name: print function definition.
				// Bash silently returns exit 1 for missing functions.
				if body := r.funcBody(name); body != nil {
					printFunction(name, body)
				} else {
					r.exit.code = 1
				}
				return true
			}
			if declQuery == "-F" {
				if body := r.funcBody(name); body != nil {
					if r.opts[optExtDebug] {
						source := r.funcSource(name)
						if source == "" {
							source = "main"
						}
						r.outf("%s %d %s\n", name, body.Pos().Line(), source)
					} else {
						r.outf("%s\n", name)
					}
				} else {
					r.exit.code = 1
				}
				return true
			}
			if declQuery == "-p" {
				if declName == "readonly" || declName == "export" {
					return true
				}
				var vr expand.Variable
				if declName == "local" {
					var ok bool
					vr, ok = currentScopeVar(localScopeEnv(r.writeEnv), name)
					if !ok || !vr.Local {
						vr = expand.Variable{}
					}
				} else {
					vr = r.lookupVar(name)
				}
				if !vr.Declared() {
					declErrf("%s: %s: not found\n", declName, raw)
					r.exit.code = 1
					return true
				}
				printDeclaredVar(name, vr)
				return true
			}
			if body := r.funcBody(name); body != nil && as == nil && valType == "" && functionTraceOnlyModes(modes) {
				info, _ := r.funcInfo(name)
				for _, mode := range modes {
					switch mode {
					case "-t":
						info.trace = true
					case "+t":
						info.trace = false
					}
				}
				r.setFuncInfo(name, info)
				return true
			}
			targetEnv := r.writeEnv
			if global && r.inFunc {
				targetEnv = globalWriteEnv(r.writeEnv)
			} else if !local {
				if ownerEnv, _, ok := visibleBindingWriteEnv(r.writeEnv, name); ok {
					targetEnv = ownerEnv
				}
			}
			return runWithWriteEnv(targetEnv, func() bool {
				vr := declLookupVar(targetEnv, name)
				declaredBefore := vr.Declared()
				if msg := arrayConversionError(name, vr); msg != "" {
					if r.evalDepth > 0 {
						declErrf("%s\n", msg)
					} else {
						declErrf("%s: %s\n", declName, msg)
					}
					r.exit.code = 1
					return true
				}
				clearReadonly := false
				for _, mode := range modes {
					if mode == "+r" {
						clearReadonly = true
						break
					}
				}
				if clearReadonly && vr.ReadOnly {
					switch declName {
					case "local":
						declErrf("local: %s: readonly variable\n", name)
					case "declare", "typeset":
						declErrf("%s: %s: readonly variable\n", declName, name)
					default:
						declErrf("%s: readonly variable\n", name)
					}
					r.exit.code = 1
					return true
				}
				arrayAssignTrace := ""
				if !isAssign {
					switch valType {
					case "-A":
						vr.Kind = expand.Associative
					case "-a":
						vr.Kind = expand.Indexed
					case "-n":
						switch vr.Kind {
						case expand.Indexed, expand.Associative:
							vr.Kind = expand.NameRef
							vr.Str = vr.String()
							vr.List = nil
							vr.Indices = nil
							vr.Map = nil
						case expand.NameRef, expand.String:
							vr.Kind = expand.NameRef
						default:
							vr.Kind = expand.NameRef
							vr.Str = ""
						}
					case "+n":
						if vr.Kind == expand.NameRef {
							vr.Kind = expand.String
							vr.List = nil
							vr.Indices = nil
							vr.Map = nil
						} else {
							vr.Kind = expand.KeepValue
						}
					case "+a", "+A":
						// Remove array/assoc attribute, convert to string.
						if vr.Kind == expand.Indexed || vr.Kind == expand.Associative {
							vr.Kind = expand.String
							vr.Str = vr.String()
							vr.List = nil
							vr.Indices = nil
							vr.Map = nil
						}
					default:
						if !vr.Declared() {
							vr.Kind = expand.String
						} else {
							vr.Kind = expand.KeepValue
						}
					}
				} else {
					var ok bool
					runWithWriteEnv(declEvalEnv, func() bool {
						if valType == "+a" || valType == "+A" {
							// +a/+A with a value: treat as string assignment.
							vr, arrayAssignTrace, ok = r.assignVal(vr, as, "")
						} else {
							vr, arrayAssignTrace, ok = r.assignVal(vr, as, valType)
							if valType == "-a" && as.Value != nil && as.Array == nil && as.Ref != nil && as.Ref.Index == nil {
								vr.Kind = expand.Indexed
								vr.List = []string{vr.Str}
								vr.Str = ""
								vr.Map = nil
							}
						}
						return ok
					})
					if !ok || r.exit.fatalExit || r.exit.exiting {
						return false
					}
					// For integer append in declare context, redo as arithmetic addition.
					if as.Append && as.Value != nil && vr.Kind == expand.String {
						isInt := vr.Integer
						if !isInt {
							for _, mode := range modes {
								if mode == "-i" {
									isInt = true
								} else if mode == "+i" {
									isInt = false
								}
							}
						}
						if isInt {
							oldVal := r.evalIntegerAttr(r.lookupVar(name).String())
							newVal := 0
							runWithWriteEnv(declEvalEnv, func() bool {
								newVal = r.evalIntegerAttr(r.assignLiteral(as))
								return true
							})
							vr.Str = strconv.Itoa(oldVal + newVal)
						}
					}
				}
				updates := attrUpdate{}
				// Apply attribute modes before transforming the value,
				// so that "declare -i foo=2+3" evaluates arithmetic.
				for _, mode := range modes {
					switch mode {
					case "-i":
						updates.hasInteger = true
						updates.integer = true
						vr.Integer = true
					case "+i":
						updates.hasInteger = true
						updates.integer = false
						vr.Integer = false
					case "-l":
						updates.hasLower = true
						updates.lower = true
						updates.hasUpper = true
						updates.upper = false
						vr.Lower = true
						vr.Upper = false // -l and -u are mutually exclusive
					case "+l":
						updates.hasLower = true
						updates.lower = false
						vr.Lower = false
					case "-t":
						updates.hasTrace = true
						updates.trace = true
						vr.Trace = true
					case "+t":
						updates.hasTrace = true
						updates.trace = false
						vr.Trace = false
					case "-u":
						updates.hasUpper = true
						updates.upper = true
						updates.hasLower = true
						updates.lower = false
						vr.Upper = true
						vr.Lower = false // -l and -u are mutually exclusive
					case "+u":
						updates.hasUpper = true
						updates.upper = false
						vr.Upper = false
					}
				}
				if global {
					updates.hasLocal = true
					updates.local = false
					vr.Local = false
				} else if local {
					updates.hasLocal = true
					updates.local = true
					vr.Local = true
				}
				for _, mode := range modes {
					switch mode {
					case "-x":
						updates.hasExported = true
						updates.exported = true
						vr.Exported = true
					case "+x":
						updates.hasExported = true
						updates.exported = false
						vr.Exported = false
					case "-r":
						updates.hasReadOnly = true
						updates.readOnly = true
						vr.ReadOnly = true
					case "+r":
						updates.hasReadOnly = true
						updates.readOnly = false
						vr.ReadOnly = false
					}
				}
				if info, ok := r.funcInfo(name); ok && info.body != nil {
					switch {
					case vr.Trace:
						info.trace = true
						r.setFuncInfo(name, info)
					case updates.hasTrace && !updates.trace:
						info.trace = false
						r.setFuncInfo(name, info)
					}
				}
				r.applyVarAttrs(&vr)
				var nameRefErr error
				if vr.Kind == expand.NameRef {
					nameRefErr = validateNameRefTarget(vr.Str)
				}
				if !isAssign {
					r.setVar(name, vr)
					if declName == "readonly" && !declaredBefore && !vr.IsSet() &&
						(vr.Kind == expand.Indexed || vr.Kind == expand.Associative) {
						r.setHiddenReadonlyArrayDecl(name, vr.Kind)
					} else {
						r.clearHiddenReadonlyArrayDecl(name)
					}
					if nameRefErr != nil {
						r.errf("%s: %v\n", cm.Variant.Value, nameRefErr)
						r.exit.code = 1
					}
					return true
				}
				if vr.Kind == expand.NameRef && as.Ref != nil && as.Ref.Index == nil {
					r.setVar(name, vr)
					r.clearHiddenReadonlyArrayDecl(name)
					if nameRefErr != nil {
						r.errf("%s: %v\n", cm.Variant.Value, nameRefErr)
						r.exit.code = 1
					}
					if tracingEnabled {
						builtinTraceFields = append(builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
					}
					return true
				}
				if err := r.setVarByRef(r.lookupVar(as.Ref.Name.Value), as.Ref, vr, as.Append, updates); err != nil {
					if declName == "local" && strings.HasSuffix(err.Error(), ": readonly variable") {
						declErrf("local: %v\n", err)
					} else {
						r.errf("%v\n", err)
					}
					r.exit.code = 1
					return false
				}
				r.clearHiddenReadonlyArrayDecl(name)
				if tracingEnabled {
					switch {
					case cm.Variant.Value == "readonly":
						builtinTraceFields = append(builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
						if as.Array != nil {
							trailingTraceLines = append(trailingTraceLines, arrayAssignTrace)
						} else if as.Value != nil {
							trailingTraceLines = append(trailingTraceLines, r.traceAssignString(as.Ref, vr, as.Append))
						}
					case (cm.Variant.Value == "declare" || cm.Variant.Value == "typeset") && as.Array != nil:
						leadingTraceLines = append(leadingTraceLines, arrayAssignTrace)
						builtinTraceFields = append(builtinTraceFields, name)
					default:
						builtinTraceFields = append(builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
					}
				}
				return true
			})
		}
		var processOperand func(syntax.DeclOperand) bool
		processOperand = func(operand syntax.DeclOperand) bool {
			switch operand := operand.(type) {
			case *syntax.DeclFlag:
				name := r.literal(operand.Word)
				underscore = name
				if allowPlusFlags || !strings.HasPrefix(name, "+") {
					fp := flagParser{remaining: []string{name}}
					for fp.more() {
						switch flag := fp.flag(); flag {
						case "-x", "-r", "-i", "-l", "-t", "-u":
							modes = append(modes, flag)
						case "+x", "+r", "+i", "+l", "+t", "+u":
							modes = append(modes, flag)
						case "-a", "-A":
							valType = flag
						case "-n":
							if declName == "export" {
								modes = append(modes, "+x")
							} else {
								valType = flag
							}
						case "+a", "+A", "+n":
							valType = flag
						case "-g":
							global = true
						case "-f", "-F", "-p":
							declQuery = flag
						default:
							r.errf("%s: %s: invalid option\n", declName, flag)
							if usage := declUsage(declName); usage != "" {
								r.errf("%s", usage)
							}
							r.exit.code = 2
							return false
						}
					}
					if tracingEnabled {
						builtinTraceFields = append(builtinTraceFields, name)
					}
					return true
				}
				return processNamedOperand(&syntax.VarRef{Name: &syntax.Lit{Value: name}}, nil, false, name)
			case *syntax.DeclName:
				return processNamedOperand(operand.Ref, nil, false, declOperandString(operand))
			case *syntax.DeclAssign:
				if operand.Assign.Array != nil && operand.Assign.Ref != nil {
					underscore = operand.Assign.Ref.Name.Value
				} else {
					underscore = declOperandString(operand)
				}
				return processNamedOperand(operand.Assign.Ref, operand.Assign, true, declOperandString(operand))
			case *syntax.DeclDynamicWord:
				var fields []string
				runWithWriteEnv(declEvalEnv, func() bool {
					fields = r.fields(operand.Word)
					if len(fields) == 0 {
						fields = []string{r.literal(operand.Word)}
					}
					return true
				})
				for _, field := range fields {
					parsed, err := parseDeclOperandField(cm.Variant.Value, field)
					splitFields := []string{field}
					if strings.ContainsAny(field, "[]") && (err != nil || parsed == nil) {
						splitFields = splitDeclDynamicField(field)
					}
					for i, splitField := range splitFields {
						if i > 0 || len(splitFields) > 1 {
							parsed, err = parseDeclOperandField(cm.Variant.Value, splitField)
						}
						if err != nil {
							onlyFlagOperands = false
							if strings.ContainsAny(splitField, "[]") {
								parsed = nil
								err = nil
							} else {
								r.errf("%s: %v\n", cm.Variant.Value, err)
								r.exit.code = 1
								continue
							}
						}
						if parsed == nil {
							onlyFlagOperands = false
							r.errf("%s: `%s': not a valid identifier\n", cm.Variant.Value, splitField)
							r.exit.code = 1
							continue
						}
						subMode := subscriptModeFromArrayExprMode(declArrayModeFromValueType(valType))
						switch parsed := parsed.(type) {
						case *syntax.DeclName:
							stampVarRefSubscriptMode(parsed.Ref, subMode)
						case *syntax.DeclAssign:
							stampVarRefSubscriptMode(parsed.Assign.Ref, subMode)
							stampArrayExprSubscriptModes(parsed.Assign.Array, subMode)
						}
						if as, ok := parsed.(*syntax.DeclAssign); ok && as.Assign.Array != nil {
							if mode := declArrayModeFromValueType(valType); mode != syntax.ArrayExprInherit {
								as.Assign.Array.Mode = mode
							}
						}
						if as, ok := parsed.(*syntax.DeclAssign); ok && as.Assign.Array != nil &&
							as.Assign.Array.Mode == syntax.ArrayExprInherit {
							// Bash only keeps runtime-parsed compound assignments structural
							// when an explicit array attribute is active.
							parsed = declStringifiedArrayAssign(as.Assign)
						}
						if dyn, ok := parsed.(*syntax.DeclDynamicWord); ok {
							parsed = &syntax.DeclName{
								Ref: &syntax.VarRef{Name: &syntax.Lit{Value: r.literal(dyn.Word)}},
							}
						}
						switch parsed := parsed.(type) {
						case *syntax.DeclName:
							if !processNamedOperand(parsed.Ref, nil, false, splitField) {
								return false
							}
						case *syntax.DeclAssign:
							if !processNamedOperand(parsed.Assign.Ref, parsed.Assign, true, splitField) {
								return false
							}
						default:
							if !processOperand(parsed) {
								return false
							}
						}
						if r.exit.fatalExit || r.exit.exiting {
							return false
						}
					}
				}
				return true
			default:
				panic(fmt.Sprintf("unexpected declaration operand: %T", operand))
			}
		}
		for _, operand := range cm.Operands {
			if !processOperand(operand) {
				return
			}
		}
		if onlyFlagOperands {
			switch declQuery {
			case "-f":
				names := make([]string, 0, len(r.funcs))
				for name := range r.funcs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					printFunction(name, r.funcBody(name))
				}
			case "-F":
				names := make([]string, 0, len(r.funcs))
				for name := range r.funcs {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					r.outf("declare -f %s\n", name)
				}
			case "-p":
				printListedVars(declName == "local", false)
			case "":
				switch declName {
				case "declare", "typeset":
					printListedVars(false, true)
				case "local":
					printListedVars(true, false)
				case "readonly", "export", "nameref":
					printListedVars(false, false)
				}
			}
		}
		r.setSpecialUnderscore(underscore)
		if tracingEnabled {
			for _, line := range leadingTraceLines {
				trace.string(line)
				trace.newLineFlush()
			}
			trace.call(builtinTraceFields[0], builtinTraceFields[1:]...)
			trace.newLineFlush()
			for _, line := range trailingTraceLines {
				trace.string(line)
				trace.newLineFlush()
			}
		}
	case *syntax.TimeClause:
		start := time.Now()
		if cm.Stmt != nil {
			r.stmt(ctx, cm.Stmt)
		}
		format := "%s\t%s\n"
		if cm.PosixFormat {
			format = "%s %s\n"
		} else {
			r.outf("\n")
		}
		realTime := time.Since(start)
		r.outf(format, "real", elapsedString(realTime, cm.PosixFormat))
		// TODO: can we do these?
		r.outf(format, "user", elapsedString(0, cm.PosixFormat))
		r.outf(format, "sys", elapsedString(0, cm.PosixFormat))
	default:
		panic(fmt.Sprintf("unhandled command node: %T", cm))
	}
}

func (r *Runner) printDeclaredVar(name string, vr expand.Variable) {
	flags := vr.Flags()
	if flags == "" {
		flags = "-"
	}
	switch vr.Kind {
	case expand.Indexed:
		r.outf("declare -%s %s=(", flags, name)
		for i, index := range vr.IndexedIndices() {
			if i > 0 {
				r.out(" ")
			}
			val, _ := vr.IndexedGet(index)
			r.outf("[%d]=%q", index, val)
		}
		r.out(")\n")
	case expand.Associative:
		r.outf("declare -%s %s=(", flags, name)
		first := true
		for _, k := range expand.AssociativeKeys(vr.Map) {
			v := vr.Map[k]
			if !first {
				r.out(" ")
			}
			r.outf("[%s]=%q", k, v)
			first = false
		}
		if !first {
			r.out(" ")
		}
		r.out(")\n")
	default:
		r.outf("declare -%s %s=%q\n", flags, name, vr.Str)
	}
}

func declUsage(name string) string {
	switch name {
	case "declare":
		return "declare: usage: declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]\n"
	case "typeset":
		return "typeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n"
	default:
		return ""
	}
}

func functionTraceOnlyModes(modes []string) bool {
	if len(modes) == 0 {
		return false
	}
	for _, mode := range modes {
		if mode != "-t" && mode != "+t" {
			return false
		}
	}
	return true
}

func declaredVarMatches(vr expand.Variable, valType string, modes []string) bool {
	if !vr.Declared() {
		return false
	}
	switch valType {
	case "-a":
		if vr.Kind != expand.Indexed {
			return false
		}
	case "-A":
		if vr.Kind != expand.Associative {
			return false
		}
	case "-n":
		if vr.Kind != expand.NameRef {
			return false
		}
	}
	for _, mode := range modes {
		switch mode {
		case "-i":
			if !vr.Integer {
				return false
			}
		case "-l":
			if !vr.Lower {
				return false
			}
		case "-t":
			if !vr.Trace {
				return false
			}
		case "-r":
			if !vr.ReadOnly {
				return false
			}
		case "-u":
			if !vr.Upper {
				return false
			}
		case "-x":
			if !vr.Exported {
				return false
			}
		}
	}
	return true
}

func (r *Runner) printDeclaredVars(valType string, modes []string) {
	names := make([]string, 0, 16)
	seen := make(map[string]struct{})
	r.writeEnv.Each(func(name string, _ expand.Variable) bool {
		if _, ok := seen[name]; ok {
			return true
		}
		seen[name] = struct{}{}
		if declaredVarMatches(r.lookupVar(name), valType, modes) {
			names = append(names, name)
		}
		return true
	})
	sort.Strings(names)
	for _, name := range names {
		r.printDeclaredVar(name, r.lookupVar(name))
	}
}
func (r *Runner) lastpipeStmt(stmt *syntax.Stmt) (*syntax.Stmt, bool) {
	if !r.opts[optLastPipe] || r.interactive || stmt == nil {
		return nil, false
	}
	inner, ok := r.syntheticPipelineStmts[stmt]
	if !ok || inner == nil {
		return nil, false
	}
	return inner, true
}

func (r *Runner) setHiddenReadonlyArrayDecl(name string, kind expand.ValueKind) {
	if name == "" {
		return
	}
	if kind != expand.Indexed && kind != expand.Associative {
		r.clearHiddenReadonlyArrayDecl(name)
		return
	}
	if r.hiddenReadonlyArrayDecl == nil {
		r.hiddenReadonlyArrayDecl = make(map[string]expand.ValueKind)
	}
	r.hiddenReadonlyArrayDecl[name] = kind
}

func (r *Runner) clearHiddenReadonlyArrayDecl(name string) {
	if r.hiddenReadonlyArrayDecl == nil || name == "" {
		return
	}
	delete(r.hiddenReadonlyArrayDecl, name)
}

func (r *Runner) hideReadonlyArrayDeclKind(name string, vr expand.Variable) bool {
	if r.hiddenReadonlyArrayDecl == nil || vr.IsSet() || !vr.ReadOnly {
		return false
	}
	kind, ok := r.hiddenReadonlyArrayDecl[name]
	return ok && kind == vr.Kind
}

func (r *Runner) aliasResolver(name string) (syntax.AliasSpec, bool) {
	if r == nil || !r.opts[optExpandAliases] {
		return syntax.AliasSpec{}, false
	}
	als, ok := r.alias[name]
	if !ok {
		return syntax.AliasSpec{}, false
	}
	return syntax.AliasSpec{Value: als.value}, true
}

func (r *Runner) newParser(opts ...syntax.ParserOption) *syntax.Parser {
	base := append([]syntax.ParserOption{}, opts...)
	if r != nil && r.legacyBashCompat {
		base = append(base, syntax.LegacyBashCompat(true))
	}
	if r != nil {
		base = append(base, syntax.ParseExtGlob(r.opts[optExtGlob]))
	}
	if r != nil && r.opts[optExpandAliases] {
		base = append(base, syntax.ExpandAliases(r.aliasResolver))
	}
	return syntax.NewParser(base...)
}

type restoreVar struct {
	name             string
	vr               expand.Variable
	secondsEnv       expand.WriteEnviron
	secondsStartTime time.Time
	restoreSeconds   bool
}

func (r *Runner) restoreCallAssigns(restores []restoreVar) {
	for i := len(restores) - 1; i >= 0; i-- {
		restore := restores[i]
		if restore.restoreSeconds {
			if err := r.writeEnv.Set(restore.name, restore.vr); err != nil {
				r.errf("%s: %v\n", restore.name, err)
				r.exit.code = 1
				continue
			}
			if restore.secondsEnv != nil && setSecondsStartTimeForEnv(restore.secondsEnv, restore.secondsStartTime) {
				continue
			}
			r.startTime = restore.secondsStartTime
			continue
		}
		r.setVar(restore.name, restore.vr)
	}
}

// expandAssignsForSideEffects expands assignment values to trigger side effects
// (like command substitutions) without persisting the assignments. This is used
// for prefix assignments before declaration builtins, where bash runs command
// substitutions but does not actually set the variables.
func (r *Runner) expandAssignsForSideEffects(assigns []*syntax.Assign) bool {
	for _, as := range assigns {
		// Just expand the value to trigger side effects; don't set the variable.
		if _, _, ok := r.assignVal(r.lookupVar(as.Ref.Name.Value), as, ""); !ok || r.exit.fatalExit || r.exit.exiting {
			return false
		}
		if !r.exit.ok() {
			return false
		}
	}
	return true
}

func (r *Runner) runCallAssigns(assigns []*syntax.Assign) []restoreVar {
	var restores []restoreVar
	for _, as := range assigns {
		name := as.Ref.Name.Value
		prev := r.lookupVar(name)
		if as.Ref.Index != nil {
			r.errf("`%s': not a valid identifier\n", printVarRef(as.Ref))
			continue
		}
		if as.Array != nil {
			vr := expand.Variable{
				Set:      true,
				Kind:     expand.String,
				Str:      r.renderInlineArrayValue(as.Array),
				Exported: true,
			}
			resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
			if err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
			restore := restoreVar{name: resolvedRef.Name.Value, vr: resolvedPrev}
			if restore.name == "SECONDS" {
				restore.vr = expand.Variable{}
				if secondsEnv, secondsVR, ok := visibleSecondsBinding(r.writeEnv); ok {
					restore.secondsEnv = secondsEnv
					restore.vr = secondsVR
				}
				restore.secondsStartTime = r.startTime
				if restore.secondsEnv != nil {
					if started, ok := secondsStartTimeForEnv(restore.secondsEnv); ok {
						restore.secondsStartTime = started
					}
				}
				restore.restoreSeconds = true
			}
			r.setVar(resolvedRef.Name.Value, vr)
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return restores
			}
			restores = append(restores, restore)
			continue
		}

		vr, _, ok := r.assignVal(prev, as, "")
		if !ok || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
		// Inline command vars are always exported.
		vr.Exported = true

		resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		restore := restoreVar{name: resolvedRef.Name.Value, vr: resolvedPrev}
		if restore.name == "SECONDS" {
			restore.vr = expand.Variable{}
			if secondsEnv, secondsVR, ok := visibleSecondsBinding(r.writeEnv); ok {
				restore.secondsEnv = secondsEnv
				restore.vr = secondsVR
			}
			restore.secondsStartTime = r.startTime
			if restore.secondsEnv != nil {
				if started, ok := secondsStartTimeForEnv(restore.secondsEnv); ok {
					restore.secondsStartTime = started
				}
			}
			restore.restoreSeconds = true
		}
		if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
		restores = append(restores, restore)
	}
	return restores
}

func (r *Runner) renderInlineArrayValue(expr *syntax.ArrayExpr) string {
	if expr == nil {
		return ""
	}
	var b strings.Builder
	b.WriteByte('(')
	first := true
	for _, elem := range expr.Elems {
		switch elem.Kind {
		case syntax.ArrayElemSequential:
			for _, field := range r.fields(elem.Value) {
				if !first {
					b.WriteByte(' ')
				}
				b.WriteString(bashDeclPlainValue(field))
				first = false
			}
		case syntax.ArrayElemKeyed, syntax.ArrayElemKeyedAppend:
			if !first {
				b.WriteByte(' ')
			}
			key := r.inlineArrayIndexValue(elem.Index)
			b.WriteByte('[')
			b.WriteString(bashDeclAssocKey(key))
			if elem.Kind == syntax.ArrayElemKeyedAppend {
				b.WriteString("]+=")
			} else {
				b.WriteString("]=")
			}
			b.WriteString(bashDeclPlainValue(r.assignmentLiteral(elem.Value)))
			first = false
		}
	}
	b.WriteByte(')')
	return b.String()
}

func (r *Runner) inlineArrayIndexValue(index *syntax.Subscript) string {
	if index == nil || index.Expr == nil {
		return ""
	}
	if word, ok := index.Expr.(*syntax.Word); ok {
		return r.assignmentWordLiteral(word)
	}
	if raw := index.RawText(); raw != "" {
		return raw
	}
	var sb strings.Builder
	if err := syntax.NewPrinter(syntax.Minify(true)).Print(&sb, index.Expr); err == nil {
		return sb.String()
	}
	return ""
}

func bashDeclDoubleQuote(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		switch c := value[i]; c {
		case '"', '\\', '$', '`':
			b.WriteByte('\\')
		}
		b.WriteByte(value[i])
	}
	b.WriteByte('"')
	return b.String()
}

func bashDeclPrintValue(value string) string {
	if needsTraceANSIQuote(value) {
		return traceANSIQuote(value)
	}
	return bashDeclDoubleQuote(value)
}

func bashDeclPlainValue(value string) string {
	if needsTraceANSIQuote(value) {
		return traceANSIQuote(value)
	}
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err != nil {
		return bashDeclDoubleQuote(value)
	}
	return quoted
}

func bashDeclAssocKey(key string) string {
	if key != "" && !needsTraceANSIQuote(key) && !strings.ContainsAny(key, "\\]\"'\n\r") {
		return key
	}
	return bashDeclPrintValue(key)
}

func declOperandString(operand syntax.DeclOperand) string {
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, operand); err != nil {
		return ""
	}
	return buf.String()
}

func declClauseFromFields(name string, fields []string) *syntax.DeclClause {
	decl := &syntax.DeclClause{
		Variant: &syntax.Lit{Value: name},
	}
	for _, field := range fields {
		if operand, err := parseDeclOperandField(name, field); err == nil {
			switch operand.(type) {
			case *syntax.DeclFlag, *syntax.DeclName, *syntax.DeclAssign:
				decl.Operands = append(decl.Operands, operand)
				continue
			}
		}
		decl.Operands = append(decl.Operands, &syntax.DeclDynamicWord{
			Word: &syntax.Word{
				Parts: []syntax.WordPart{
					&syntax.DblQuoted{Parts: []syntax.WordPart{&syntax.Lit{Value: field}}},
				},
			},
		})
	}
	return decl
}

func isDeclVariantName(name string) bool {
	switch name {
	case "declare", "local", "export", "readonly", "typeset", "nameref":
		return true
	default:
		return false
	}
}

func declClauseFromCallWords(variant string, variantWord *syntax.Word, operands []*syntax.Word) *syntax.DeclClause {
	decl := declClauseFromFields(variant, nil)
	if variantWord != nil {
		decl.Variant.ValuePos = variantWord.Pos()
		decl.Variant.ValueEnd = variantWord.End()
	}
	for _, arg := range operands {
		if arg == nil {
			continue
		}
		decl.Operands = append(decl.Operands, declOperandFromCallWord(arg))
	}
	return decl
}

func declOperandFromCallWord(word *syntax.Word) syntax.DeclOperand {
	if word == nil {
		return nil
	}
	lit := word.Lit()
	if lit == "" {
		return &syntax.DeclDynamicWord{Word: word}
	}
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	op, err := p.DeclOperand(strings.NewReader(lit))
	if err != nil || op == nil {
		return &syntax.DeclDynamicWord{Word: word}
	}
	return op
}

func expandedDeclVariant(fields []string) (variant string, matched bool, needMore bool) {
	sawWrapperPrefix := false
	lastWasWrapper := false
	for i := 0; i < len(fields); i++ {
		name := fields[i]
		if isDeclVariantName(name) {
			return name, true, false
		}
		switch name {
		case "builtin":
			sawWrapperPrefix = true
			lastWasWrapper = true
			continue
		case "command":
			sawWrapperPrefix = true
			lastWasWrapper = true
			continue
		case "--":
			if !lastWasWrapper {
				return "", false, false
			}
			lastWasWrapper = false
			continue
		default:
			return "", false, false
		}
	}
	return "", false, sawWrapperPrefix
}

func (r *Runner) resolveCallExprArgs(args []*syntax.Word) ([]string, *syntax.DeclClause) {
	if len(args) == 0 {
		return nil, nil
	}
	fields := make([]string, 0, len(args))
	leading := make([]string, 0, len(args))
	canDetectDecl := true
	for i, arg := range args {
		expanded := r.fields(arg)
		if !r.exit.ok() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil {
			return nil, nil
		}
		fields = append(fields, expanded...)
		if !canDetectDecl {
			continue
		}
		if len(expanded) != 1 {
			canDetectDecl = false
			continue
		}
		leading = append(leading, expanded[0])
		variant, matched, needMore := expandedDeclVariant(leading)
		if matched {
			return nil, declClauseFromCallWords(variant, arg, args[i+1:])
		}
		if !needMore {
			canDetectDecl = false
		}
	}
	return fields, nil
}

func parseDeclOperandField(variant, field string) (syntax.DeclOperand, error) {
	if variant == "export" || variant == "local" {
		if eqIndex := strings.IndexByte(field, '='); eqIndex > 0 {
			name := field[:eqIndex]
			if strings.HasSuffix(name, "+") {
				name = name[:len(name)-1]
			}
			if !syntax.ValidName(name) && !strings.ContainsAny(name, "[]") && !strings.Contains(name, "{") {
				return &syntax.DeclDynamicWord{
					Word: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: field}}},
				}, nil
			}
		}
	}
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	return p.DeclOperandField(strings.NewReader(field))
}

func splitDeclDynamicField(field string) []string {
	p := syntax.NewParser(syntax.Variant(syntax.LangBash))
	var words []string
	err := p.Words(strings.NewReader(field), func(word *syntax.Word) bool {
		var buf bytes.Buffer
		if err := syntax.NewPrinter().Print(&buf, word); err != nil {
			panic(err)
		}
		words = append(words, buf.String())
		return true
	})
	if err != nil || len(words) == 0 {
		return []string{field}
	}
	return words
}

func declStringifiedArrayAssign(as *syntax.Assign) *syntax.DeclAssign {
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, as); err != nil {
		panic(err)
	}
	rendered := buf.String()
	i := strings.IndexByte(rendered, '=')
	if i < 0 {
		panic(fmt.Sprintf("assignment printed without '=': %q", rendered))
	}
	return &syntax.DeclAssign{Assign: &syntax.Assign{
		Append: as.Append,
		Ref:    as.Ref,
		Value: &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{
			Value: rendered[i+1:],
		}}},
	}}
}

func match(pat, name string) bool {
	ok, err := pattern.Match(pat, name, pattern.EntireString|pattern.ExtendedOperators)
	_ = err // TODO: report these errors
	return ok
}

func elapsedString(d time.Duration, posix bool) string {
	if posix {
		return fmt.Sprintf("%.2f", d.Seconds())
	}
	mins := int(d.Minutes())
	sec := math.Mod(d.Seconds(), 60.0)
	return fmt.Sprintf("%dm%.3fs", mins, sec)
}

func (r *Runner) stmts(ctx context.Context, stmts []*syntax.Stmt) {
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
	}
}

func (r *Runner) hdocReader(rd *syntax.Redirect) (StdinReader, error) {
	pr, pw := r.newPipe()
	// We write to the pipe in a new goroutine,
	// as pipe writes may block once the buffer gets full.
	// We still construct and buffer the entire heredoc first,
	// as doing it concurrently would lead to different semantics and be racy.
	if rd.Op != syntax.DashHdoc {
		hdoc := r.document(rd.Hdoc)
		go func() {
			io.WriteString(pw, hdoc)
			pw.Close()
		}()
		return pr, nil
	}
	var buf bytes.Buffer
	var cur []syntax.WordPart
	flushLine := func() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(r.document(&syntax.Word{Parts: cur}))
		cur = cur[:0]
	}
	for _, wp := range rd.Hdoc.Parts {
		lit, ok := wp.(*syntax.Lit)
		if !ok {
			cur = append(cur, wp)
			continue
		}
		first := true
		for part := range strings.SplitSeq(lit.Value, "\n") {
			if !first {
				flushLine()
				cur = cur[:0]
			}
			first = false
			part = strings.TrimLeft(part, "\t")
			cur = append(cur, &syntax.Lit{Value: part})
		}
	}
	flushLine()
	go func() {
		pw.Write(buf.Bytes())
		pw.Close()
	}()
	return pr, nil
}

func (r *Runner) redir(ctx context.Context, rd *syntax.Redirect) (redirResult, error) {
	var result redirResult
	r.ensureFDTable()
	arg := ""
	wordText := ""
	if rd.Word != nil {
		wordText = printSyntaxNode(rd.Word)
	}
	switch rd.Op {
	case syntax.RdrIn, syntax.RdrOut, syntax.RdrClob, syntax.AppOut, syntax.AppClob, syntax.RdrInOut,
		syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
		r.inRedirectWord++
		fields, err := expand.RedirectFields(r.ecfg, rd.Word)
		r.inRedirectWord--
		r.expandErr(err)
		if err != nil {
			return result, err
		}
		if len(fields) != 1 {
			if wordText != "" {
				r.errf("%s: ambiguous redirect\n", wordText)
			} else {
				r.errf("ambiguous redirect\n")
			}
			return result, errors.New("ambiguous redirect")
		}
		arg = fields[0]
	case syntax.DplIn:
		r.inRedirectWord++
		fields, err := expand.Fields(r.ecfg, rd.Word)
		r.inRedirectWord--
		r.expandErr(err)
		if err != nil {
			return result, err
		}
		if len(fields) != 1 {
			if wordText != "" {
				r.errf("%s: ambiguous redirect\n", wordText)
			} else {
				r.errf("ambiguous redirect\n")
			}
			return result, errors.New("ambiguous redirect")
		}
		arg = fields[0]
	default:
		arg = r.expandingRedirectWord(func() string {
			return r.literal(rd.Word)
		})
	}

	targetFD, allocated, err := r.redirectTargetFD(rd, arg)
	if err != nil {
		return result, err
	}

	switch rd.Op {
	case syntax.Hdoc, syntax.DashHdoc:
		pr, err := r.hdocReader(rd)
		if err != nil {
			return result, err
		}
		r.setFD(targetFD, newOwnedShellInputFD(pr))
		result.closer = pr
		return result, nil
	case syntax.WordHdoc:
		pr, pw := r.newPipe()
		r.setFD(targetFD, newOwnedShellInputFD(pr))
		// We write to the pipe in a new goroutine,
		// as pipe writes may block once the buffer gets full.
		go func() {
			io.WriteString(pw, arg)
			io.WriteString(pw, "\n")
			pw.Close()
		}()
		result.closer = pr
		return result, nil
	case syntax.DplOut:
		switch arg {
		case "-":
			if targetFD >= 0 && targetFD <= 2 {
				r.setFD(targetFD, newShellOutputFD(io.Discard))
			} else {
				r.setFD(targetFD, nil)
			}
			if rd.N != nil {
				if name, ok := redirectNamedFD(rd.N.Value); ok {
					result.releasedNamedFDs = append(result.releasedNamedFDs, name)
				}
			}
		default:
			dup, err := parseRedirectDupSpec(arg)
			if err != nil {
				// Bash treats >&word with a non-numeric word as "redirect stdout and
				// stderr to file". That path is unrelated to the read work, so leave it
				// on the existing file-opening implementation for fd 1.
				if targetFD != 1 || allocated {
					diag := redirectBadFDText(rd, arg, wordText)
					r.errf("%s: Bad file descriptor\n", diag)
					return result, errors.New("bad file descriptor")
				}
				fd, closer, err := r.openRedirectTarget(ctx, arg, syntax.RdrAll)
				if err != nil {
					return result, err
				}
				r.setFD(1, fd)
				r.setFD(2, fd)
				result.closer = closer
				return result, nil
			}
			if targetFD == dup.sourceFD {
				return result, nil
			}
			src := r.getFD(dup.sourceFD)
			if src == nil || src.writer == nil {
				diag := redirectBadFDText(rd, arg, wordText)
				r.errf("%s: Bad file descriptor\n", diag)
				return result, errors.New("bad file descriptor")
			}
			if targetFD != dup.sourceFD {
				r.setFD(targetFD, src)
			}
			if dup.move && targetFD != dup.sourceFD {
				r.setFD(dup.sourceFD, nil)
				if !r.keepRedirs {
					result.keepClosed = append(result.keepClosed, dup.sourceFD)
				}
			}
		}
		return result, nil
	case syntax.DplIn:
		switch arg {
		case "-":
			r.setFD(targetFD, nil)
			if rd.N != nil {
				if name, ok := redirectNamedFD(rd.N.Value); ok {
					result.releasedNamedFDs = append(result.releasedNamedFDs, name)
				}
			}
		default:
			dup, err := parseRedirectDupSpec(arg)
			if err != nil {
				diag := redirectBadFDText(rd, arg, wordText)
				r.errf("%s: Bad file descriptor\n", diag)
				return result, errors.New("bad file descriptor")
			}
			if targetFD == dup.sourceFD {
				return result, nil
			}
			src := r.getFD(dup.sourceFD)
			if src == nil {
				diag := redirectBadFDText(rd, arg, wordText)
				r.errf("%s: Bad file descriptor\n", diag)
				return result, errors.New("bad file descriptor")
			}
			if targetFD != dup.sourceFD {
				r.setFD(targetFD, src)
			}
			if dup.move && targetFD != dup.sourceFD {
				r.setFD(dup.sourceFD, nil)
				if !r.keepRedirs {
					result.keepClosed = append(result.keepClosed, dup.sourceFD)
				}
			}
		}
		return result, nil
	case syntax.RdrIn, syntax.RdrOut, syntax.RdrClob, syntax.AppOut, syntax.AppClob, syntax.RdrInOut,
		syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
		// done further below
	default:
		panic(fmt.Sprintf("unhandled redirect op: %v", rd.Op))
	}
	fd, closer, err := r.openRedirectTarget(ctx, arg, rd.Op)
	if err != nil {
		return result, err
	}
	switch rd.Op {
	case syntax.RdrIn:
		r.setFD(targetFD, fd)
	case syntax.RdrOut, syntax.RdrClob, syntax.AppOut, syntax.AppClob, syntax.RdrInOut:
		r.setFD(targetFD, fd)
	case syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob:
		r.setFD(1, fd)
		r.setFD(2, fd)
	default:
		panic(fmt.Sprintf("unhandled redirect op: %v", rd.Op))
	}
	result.closer = closer
	return result, nil
}

type directoryReadCloser struct {
	path string
}

func (d *directoryReadCloser) Read([]byte) (int, error) {
	return 0, &os.PathError{Op: "read", Path: d.path, Err: syscall.EISDIR}
}

func (d *directoryReadCloser) Close() error {
	return nil
}

func parseRedirectFDNumber(s string) (int, error) {
	fd, err := strconv.Atoi(s)
	if err != nil || fd < 0 {
		return 0, errors.New("bad file descriptor")
	}
	return fd, nil
}

func parseRedirectDupSpec(s string) (redirectDupSpec, error) {
	spec := redirectDupSpec{}
	if strings.HasSuffix(s, "-") && len(s) > 1 {
		fd, err := parseRedirectFDNumber(s[:len(s)-1])
		if err != nil {
			return spec, err
		}
		spec.sourceFD = fd
		spec.move = true
		return spec, nil
	}
	fd, err := parseRedirectFDNumber(s)
	if err != nil {
		return spec, err
	}
	spec.sourceFD = fd
	return spec, nil
}

func redirectBadFDText(rd *syntax.Redirect, arg, wordText string) string {
	if rd != nil && redirectWordIsLiteral(rd.Word, arg) {
		return arg
	}
	if wordText != "" {
		return wordText
	}
	return arg
}

func redirectWordIsLiteral(word *syntax.Word, lit string) bool {
	if word == nil || len(word.Parts) != 1 {
		return false
	}
	part, ok := word.Parts[0].(*syntax.Lit)
	if !ok {
		return false
	}
	return part.Value == lit
}

func redirectOpSpec(op syntax.RedirOperator) (mode int, readable, writable, truncates, allOutputs, ignoreNoClobber bool) {
	mode = os.O_RDONLY
	switch op {
	case syntax.RdrIn:
		readable = true
	case syntax.RdrInOut:
		readable = true
		writable = true
		mode = os.O_RDWR | os.O_CREATE
	case syntax.AppOut, syntax.AppClob:
		writable = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		ignoreNoClobber = true
	case syntax.AppAll, syntax.AppAllClob:
		writable = true
		allOutputs = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		ignoreNoClobber = true
	case syntax.RdrClob:
		writable = true
		truncates = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		ignoreNoClobber = true
	case syntax.RdrAllClob:
		writable = true
		truncates = true
		allOutputs = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		ignoreNoClobber = true
	case syntax.RdrOut:
		writable = true
		truncates = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case syntax.RdrAll:
		writable = true
		truncates = true
		allOutputs = true
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	return
}

func shellStdDeviceFD(path string) (int, bool) {
	switch path {
	case "/dev/stdin":
		return 0, true
	case "/dev/stdout":
		return 1, true
	case "/dev/stderr":
		return 2, true
	default:
		return 0, false
	}
}

type shellStdDeviceFile struct {
	path string
	fd   *shellFD
}

func (f *shellStdDeviceFile) Read(p []byte) (int, error) {
	if f == nil || f.fd == nil || f.fd.reader == nil {
		return 0, &os.PathError{Op: "read", Path: f.path, Err: syscall.EBADF}
	}
	return f.fd.Read(p)
}

func (f *shellStdDeviceFile) Write(p []byte) (int, error) {
	if f == nil || f.fd == nil || f.fd.writer == nil {
		return 0, &os.PathError{Op: "write", Path: f.path, Err: syscall.EBADF}
	}
	return f.fd.writer.Write(p)
}

func (f *shellStdDeviceFile) Close() error { return nil }

func (r *Runner) openRedirectTarget(ctx context.Context, arg string, op syntax.RedirOperator) (*shellFD, io.Closer, error) {
	mode, readable, writable, truncates, _, ignoreNoClobber := redirectOpSpec(op)
	if arg == "" {
		err := &os.PathError{Op: "open", Path: arg, Err: syscall.ENOENT}
		r.errf(": No such file or directory\n")
		return nil, nil, err
	}
	if truncates && r.opts[optNoClobber] && !ignoreNoClobber {
		if _, ok := shellStdDeviceFD(absPath(r.Dir, arg)); !ok {
			info, statErr := r.stat(ctx, arg)
			switch {
			case statErr == nil && info.Mode().IsRegular():
				err := errors.New("cannot overwrite existing file")
				r.errf("%s: cannot overwrite existing file\n", arg)
				return nil, nil, err
			case statErr == nil:
				// noclobber still allows non-regular files like /dev/null.
			case errors.Is(statErr, fs.ErrNotExist), errors.Is(statErr, syscall.ENOENT):
			default:
				// Let open surface the original error if the stat failure matters.
			}
		}
	}
	f, err := r.open(ctx, arg, mode, 0o666, false)
	if err != nil {
		if op == syntax.RdrIn && (errors.Is(err, syscall.EISDIR) || strings.Contains(err.Error(), "Is a directory")) {
			fd := newOwnedShellInputFD(&directoryReadCloser{path: arg})
			return fd, fd, nil
		}
		r.errf("%v\n", err)
		return nil, nil, err
	}
	fd := newShellReadWriteFD(f, readable, writable)
	return fd, fd, nil
}

func redirectDefaultFD(op syntax.RedirOperator) int {
	switch op {
	case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return 0
	default:
		return 1
	}
}

func redirectNamedFD(value string) (string, bool) {
	if len(value) < 3 || value[0] != '{' || value[len(value)-1] != '}' {
		return "", false
	}
	return value[1 : len(value)-1], true
}

func (r *Runner) markNamedFDReleased(name string) {
	if name == "" {
		return
	}
	if r.namedFDReleased == nil {
		r.namedFDReleased = make(map[string]bool)
	}
	r.namedFDReleased[name] = true
}

func (r *Runner) clearNamedFDReleased(name string) {
	if name == "" || r.namedFDReleased == nil {
		return
	}
	delete(r.namedFDReleased, name)
}

func (r *Runner) redirectTargetFD(rd *syntax.Redirect, arg string) (fd int, allocated bool, err error) {
	if rd.N == nil {
		return redirectDefaultFD(rd.Op), false, nil
	}
	if name, ok := redirectNamedFD(rd.N.Value); ok {
		if (rd.Op == syntax.DplIn || rd.Op == syntax.DplOut) && arg == "-" {
			fd, err := r.lookupNamedFD(name)
			return fd, false, err
		}
		start := shellNamedFDStart
		if prev, err := r.lookupNamedFD(name); err == nil && prev >= shellNamedFDStart {
			start = prev + 1
			if r.namedFDReleased != nil && r.namedFDReleased[name] {
				start = prev
			}
		}
		fd = r.allocateFDFrom(start)
		r.setVarString(name, strconv.Itoa(fd))
		r.clearNamedFDReleased(name)
		return fd, true, nil
	}
	fd, err = parseRedirectFDNumber(rd.N.Value)
	return fd, false, err
}

func (r *Runner) loopStmtsBroken(ctx context.Context, stmts []*syntax.Stmt) bool {
	oldInLoop := r.inLoop
	r.inLoop = true
	defer func() { r.inLoop = oldInLoop }()
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			return r.contnEnclosing > 0
		}
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			return true
		}
	}
	return false
}

func (r *Runner) call(ctx context.Context, pos syntax.Pos, args []string) {
	if r.stop(ctx) {
		return
	}
	if r.callHandler != nil {
		var err error
		args, err = r.callHandler(r.handlerCtx(ctx, handlerKindCall, pos), args)
		if err != nil {
			// handler's custom fatal error
			r.exit.fatal(err)
			return
		}
	}
	name := args[0]
	if info, ok := r.funcInfo(name); ok && info.body != nil {
		source := info.definitionSource
		isInternal := info.internal
		bashSource := source
		if isInternal {
			bashSource = ""
		}
		allowDebug := r.opts[optFuncTrace] || r.opts[optExtDebug] || info.trace
		allowReturn := r.opts[optFuncTrace] || info.trace
		allowErr := r.opts[optErrTrace]
		// stack them to support nested func calls
		oldParams := r.Params
		r.Params = args[1:]
		oldInFunc := r.inFunc
		r.inFunc = true
		restoreFrame := r.pushFrame(execFrame{
			kind:        frameKindFunction,
			label:       name,
			execFile:    source,
			bashSource:  bashSource,
			callLine:    r.functionCallLine(pos),
			internal:    isInternal,
			allowErr:    allowErr,
			allowDebug:  allowDebug,
			allowReturn: allowReturn,
		})

		// Functions run in a nested scope.
		// Note that [Runner.exec] below does something similar.
		origEnv := r.writeEnv
		r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true}
		prevChunkSource := r.currentChunkSource
		prevChunkSourceBase := r.currentChunkSourceBase
		if bodySource, ok := r.funcBodySource(name); ok {
			r.currentChunkSource = bodySource.text
			r.currentChunkSourceBase = bodySource.base
		}

		r.stmt(ctx, info.body)

		r.currentChunkSource = prevChunkSource
		r.currentChunkSourceBase = prevChunkSourceBase
		r.writeEnv = origEnv
		restoreFrame()
		if allowReturn && !r.exit.fatalExit {
			r.maybeRunReturnTrap(ctx, pos.Line(), r.exit.code)
		}

		r.Params = oldParams
		r.inFunc = oldInFunc
		r.exit.returning = false
		return
	}
	if IsBuiltin(name) {
		r.exit = r.builtin(ctx, pos, name, args[1:])
		return
	}
	r.exec(ctx, pos, args)
}

func (r *Runner) exec(ctx context.Context, pos syntax.Pos, args []string) {
	r.exit.fromHandlerError(r.execHandler(r.handlerCtx(ctx, handlerKindExec, pos), args))
}

func (r *Runner) open(ctx context.Context, path string, flags int, mode os.FileMode, printErr bool) (io.ReadWriteCloser, error) {
	if special, handled, err := r.openShellStdDevice(path, flags); handled {
		if err != nil && printErr {
			r.errf("%v\n", err)
		}
		return special, err
	}
	mode = r.applyShellUmask(flags, mode)
	f, err := r.openHandler(r.handlerCtx(ctx, handlerKindOpen, todoPos), path, flags, mode)
	var pathErr *os.PathError
	switch {
	case err == nil:
		return f, nil
	case errors.As(err, &pathErr):
		if printErr {
			r.errf("%v\n", err)
		}
	default: // handler's custom fatal error
		r.exit.fatal(err)
	}
	return nil, err
}

func (r *Runner) openShellStdDevice(path string, flags int) (io.ReadWriteCloser, bool, error) {
	fdNum, ok := shellStdDeviceFD(absPath(r.Dir, path))
	if !ok {
		return nil, false, nil
	}
	fd := r.getFD(fdNum)
	if fd == nil {
		return nil, true, &os.PathError{Op: "open", Path: path, Err: syscall.EBADF}
	}
	if flags&(os.O_WRONLY|os.O_RDWR) != 0 && fd.writer == nil {
		return nil, true, &os.PathError{Op: "open", Path: path, Err: syscall.EBADF}
	}
	if flags&(os.O_WRONLY|os.O_RDWR) != os.O_WRONLY && fd.reader == nil && flags&os.O_RDONLY == 0 {
		if flags&(os.O_WRONLY|os.O_RDWR) == os.O_RDWR {
			return nil, true, &os.PathError{Op: "open", Path: path, Err: syscall.EBADF}
		}
	}
	return &shellStdDeviceFile{path: path, fd: fd}, true, nil
}

func (r *Runner) applyShellUmask(flags int, mode os.FileMode) os.FileMode {
	if flags&os.O_CREATE == 0 {
		return mode
	}
	return mode &^ (r.shellUmask() & 0o777)
}

func (r *Runner) shellUmask() os.FileMode {
	if r == nil || r.writeEnv == nil {
		return 0o022
	}
	raw := strings.TrimSpace(r.writeEnv.Get("GBASH_UMASK").String())
	if raw == "" {
		return 0o022
	}
	value, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || value > 0o777 {
		return 0o022
	}
	return os.FileMode(value)
}

func (r *Runner) stat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, true)
}

func (r *Runner) lstat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, false)
}
