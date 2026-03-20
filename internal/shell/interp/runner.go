// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
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
)

// newPipe creates a pipe using the default virtual pipe implementation.
func (r *Runner) newPipe() (StdinReader, io.WriteCloser) {
	return NewVirtualPipe()
}

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env:         expandEnv{r},
		StartupHome: r.startupHome,
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
			r2.opts[optVerbose] = false
			r2.stdout = w
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
			r2.stdout = endpoint.Writer
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
			r2.stdin = stdin
			r2.stdout = stdout
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
		arithSyntaxErr expand.ArithmSyntaxError
		arithDiagErr   *expand.ArithmDiagnosticError
	)
	fmt.Fprintln(r.stderr, errMsg)
	switch {
	case errors.As(err, &cmdArithErr):
		r.exit.code = 1
	case errors.As(err, &unboundVarErr):
		r.exit.code = 127
		r.exit.exiting = true
	case errors.As(err, &unsetErr):
		r.exit.code = 127
		r.exit.exiting = true
	case errors.As(err, &indirectErr):
		r.exit.code = 1
	case errors.As(err, &invalidNameErr):
		r.exit.code = 1
	case errors.As(err, &arithSyntaxErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case errors.As(err, &arithDiagErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr
	case errMsg == "bad substitution" || strings.Contains(errMsg, ": bad substitution"):
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
	return n
}

func (r *Runner) arithmCmdExpr(cm *syntax.ArithmCmd) int {
	return r.arithmEval(cm.X, true, cm.Source, cm.Left.Offset()+2, cm.Right.Offset())
}

type arithmCommandError struct {
	err error
}

func (e arithmCommandError) Error() string {
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

func (e expandEnv) Get(name string) expand.Variable {
	return e.r.lookupVar(name)
}

func (e expandEnv) Set(name string, vr expand.Variable) error {
	e.r.setVar(name, vr)
	return nil // TODO: return any errors
}

func (e expandEnv) SetVarRef(ref *syntax.VarRef, vr expand.Variable, appendValue bool) error {
	return e.r.setVarByRef(e.r.lookupVar(ref.Name.Value), ref, vr, appendValue)
}

func (e expandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
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
	return context.WithValue(ctx, handlerCtxKey{}, hc)
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
	// Some traps trigger on exit, so we do want those to run.
	if !r.handlingTrap && (r.exit.returning || r.exit.exiting) {
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
	r.exit = exitStatus{}
	if st.Background || st.Disown {
		r2 := r.subshell(true)
		st2 := *st
		st2.Background = false
		st2.Disown = false
		bg := bgProc{
			done: make(chan struct{}),
			exit: new(exitStatus),
		}
		r.bgProcs = append(r.bgProcs, bg)
		go func() {
			r2.Run(ctx, &st2)
			r2.exit.exiting = false // subshells don't exit the parent shell
			*bg.exit = r2.exit
			close(bg.done)
		}()
	} else {
		r.stmtSync(ctx, st)
	}
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
	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	procSubstStart := len(r.bgProcs)
	closers := make([]io.Closer, 0, len(st.Redirs))
	for _, rd := range st.Redirs {
		cls, err := r.redir(ctx, rd)
		if err != nil {
			r.exit.code = 1
			break
		}
		if cls != nil {
			closers = append(closers, cls)
		}
	}
	if r.exit.ok() && st.Cmd != nil {
		r.cmd(ctx, st.Cmd)
	}
	for i := len(closers) - 1; i >= 0; i-- {
		_ = closers[i].Close()
	}
	r.waitProcSubsts(procSubstStart)
	if st.Negated {
		if r.exit.ok() {
			r.exit.code = 1
		} else {
			r.exit.clear()
		}
	} else if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.AndStmt || b.Op == syntax.OrStmt) {
	} else if !r.exit.ok() && !r.noErrExit {
		r.trapCallback(ctx, r.callbackErr, "error")
		// If the "errexit" option is set and a command failed, exit the shell. Exceptions:
		//
		//   conditions (if <cond>, while <cond>, etc)
		//   part of && or || lists; excluded via "else" above
		//   preceded by !; excluded via "else" above
		if r.opts[optErrExit] {
			r.exit.exiting = true
		}
	}
	if !r.keepRedirs {
		r.stdin, r.stdout, r.stderr = oldIn, oldOut, oldErr
	}
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
		args := cm.Args
		if decl := prefixAssignDeclClause(args, cm.Assigns); decl != nil {
			if !r.expandAssignsForSideEffects(cm.Assigns) {
				return
			}
			r.cmd(ctx, decl)
			return
		}
		// Check for declaration builtins before expanding args to avoid
		// double expansion of command substitutions in decl arguments.
		if decl := callExprDeclClause(args); decl != nil {
			if !r.expandAssignsForSideEffects(cm.Assigns) {
				return
			}
			r.cmd(ctx, decl)
			return
		}
		r.lastExpandExit = exitStatus{}
		fields := r.fields(args...)
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
				vr.Upper = prev.Upper
				if prev.Integer && as.Append && vr.Kind == expand.String {
					// For -i with +=, do arithmetic addition instead of string concat.
					oldVal := r.evalIntegerAttr(prev.String())
					newVal := r.evalIntegerAttr(r.assignLiteral(as))
					vr.Str = strconv.Itoa(oldVal + newVal)
				} else {
					r.applyVarAttrs(&vr)
				}
				if err := r.setVarByRef(prev, as.Ref, vr, as.Append); err != nil {
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
			break
		}

		restores := r.runCallAssigns(cm.Assigns)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			r.restoreCallAssigns(restores)
			break
		}

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
			r2.stdout = pw
			if cm.Op == syntax.PipeAll {
				r2.stderr = pw
			} else {
				r2.stderr = r.stderr
			}
			r.stdin = pr
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
			}
			for y.Cond == nil || r.arithmCmd(y.Cond) != 0 {
				if !r.exit.ok() || r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
				if y.Post != nil {
					r.arithmCmd(y.Post)
				}
			}
		}
	case *syntax.FuncDecl:
		r.setFunc(cm.Name.Value, cm.Body)
	case *syntax.ArithmCmd:
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
		trace.string("case ")
		trace.expr(cm.Word)
		trace.string(" in")
		trace.newLineFlush()
		str := r.literal(cm.Word)
		for _, ci := range cm.Items {
			for _, word := range ci.Patterns {
				pat := r.pattern(word)
				if match(pat, str) {
					r.stmts(ctx, ci.Stmts)
					return
				}
			}
		}
	case *syntax.TestClause:
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
		local, global := false, false
		var modes []string
		valType := ""
		declQuery := "" // "-f" or "-p" for query mode
		allowPlusFlags := false
		builtinTraceFields := []string{cm.Variant.Value}
		var leadingTraceLines []string
		var trailingTraceLines []string
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
		processNamedOperand := func(ref *syntax.VarRef, as *syntax.Assign, isAssign bool) bool {
			name := ref.Name.Value
			if !syntax.ValidName(name) {
				if allowPlusFlags && !strings.ContainsAny(name, "[]") {
					r.errf("declare: invalid name %q\n", name)
				} else {
					r.errf("%s: `%s': not a valid identifier\n", cm.Variant.Value, name)
				}
				r.exit.code = 1
				return false
			}
			if tracingEnabled && !isAssign {
				builtinTraceFields = append(builtinTraceFields, name)
			}
			if declQuery == "-f" {
				// declare -f name: print function definition.
				// Bash silently returns exit 1 for missing functions.
				if body := r.funcs[name]; body != nil {
					r.outf("%s()\n", name)
					printer := syntax.NewPrinter()
					var buf bytes.Buffer
					printer.Print(&buf, body)
					r.outf("%s\n", buf.String())
				} else {
					r.exit.code = 1
				}
				return true
			}
			if declQuery == "-p" {
				// declare -p name: print variable with attributes.
				vr := r.lookupVar(name)
				if !vr.Declared() {
					r.errf("declare: %s: not found\n", name)
					r.exit.code = 1
					return true
				}
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
				return true
			}
			vr := r.lookupVar(name)
			arrayAssignTrace := ""
			if !isAssign {
				if valType == "-A" {
					vr.Kind = expand.Associative
				} else if valType == "+a" || valType == "+A" {
					// Remove array/assoc attribute, convert to string.
					if vr.Kind == expand.Indexed || vr.Kind == expand.Associative {
						vr.Kind = expand.String
						vr.Str = vr.String()
						vr.List = nil
						vr.Indices = nil
						vr.Map = nil
					}
				} else {
					vr.Kind = expand.KeepValue
				}
			} else {
				var ok bool
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
						newVal := r.evalIntegerAttr(r.assignLiteral(as))
						vr.Str = strconv.Itoa(oldVal + newVal)
					}
				}
			}
			// Apply attribute modes before transforming the value,
			// so that "declare -i foo=2+3" evaluates arithmetic.
			for _, mode := range modes {
				switch mode {
				case "-i":
					vr.Integer = true
				case "+i":
					vr.Integer = false
				case "-l":
					vr.Lower = true
					vr.Upper = false // -l and -u are mutually exclusive
				case "+l":
					vr.Lower = false
				case "-u":
					vr.Upper = true
					vr.Lower = false // -l and -u are mutually exclusive
				case "+u":
					vr.Upper = false
				}
			}
			if global {
				vr.Local = false
			} else if local {
				vr.Local = true
			}
			for _, mode := range modes {
				switch mode {
				case "-x":
					vr.Exported = true
				case "-r":
					vr.ReadOnly = true
				}
			}
			r.applyVarAttrs(&vr)
			if !isAssign {
				r.setVar(name, vr)
				return true
			}
			if vr.Kind == expand.NameRef && as.Ref != nil && as.Ref.Index == nil {
				r.setVar(name, vr)
				if tracingEnabled {
					builtinTraceFields = append(builtinTraceFields, traceAssignFieldRaw(as.Ref, vr, as.Append))
				}
				return true
			}
			if err := r.setVarByRef(r.lookupVar(as.Ref.Name.Value), as.Ref, vr, as.Append); err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return false
			}
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
		}
		var processOperand func(syntax.DeclOperand) bool
		processOperand = func(operand syntax.DeclOperand) bool {
			switch operand := operand.(type) {
			case *syntax.DeclFlag:
				name := r.literal(operand.Word)
				if allowPlusFlags || !strings.HasPrefix(name, "+") {
					fp := flagParser{remaining: []string{name}}
					for fp.more() {
						switch flag := fp.flag(); flag {
						case "-x", "-r", "-i", "-l", "-u":
							modes = append(modes, flag)
						case "+i", "+l", "+u":
							modes = append(modes, flag)
						case "-a", "-A", "-n":
							valType = flag
						case "+a", "+A":
							valType = flag
						case "-g":
							global = true
						case "-f", "-p":
							declQuery = flag
						default:
							r.errf("declare: invalid option %q\n", flag)
							r.exit.code = 2
							return false
						}
					}
					if tracingEnabled {
						builtinTraceFields = append(builtinTraceFields, name)
					}
					return true
				}
				return processNamedOperand(&syntax.VarRef{Name: &syntax.Lit{Value: name}}, nil, false)
			case *syntax.DeclName:
				return processNamedOperand(operand.Ref, nil, false)
			case *syntax.DeclAssign:
				return processNamedOperand(operand.Assign.Ref, operand.Assign, true)
			case *syntax.DeclDynamicWord:
				for _, field := range r.fields(operand.Word) {
					parsed, err := parseDeclOperandField(field)
					splitFields := []string{field}
					if strings.ContainsAny(field, "[]") && (err != nil || parsed == nil) {
						splitFields = splitDeclDynamicField(field)
					}
					for i, splitField := range splitFields {
						if i > 0 || len(splitFields) > 1 {
							parsed, err = parseDeclOperandField(splitField)
						}
						if err != nil {
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
						if !processOperand(parsed) {
							return false
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

func prefixAssignDeclClause(args []*syntax.Word, assigns []*syntax.Assign) *syntax.DeclClause {
	if len(assigns) == 0 || len(args) == 0 {
		return nil
	}
	variant := args[0].Lit()
	switch variant {
	case "declare", "local", "export", "readonly", "typeset", "nameref":
	default:
		return nil
	}
	operands := make([]syntax.DeclOperand, 0, len(args)-1)
	for _, arg := range args[1:] {
		if arg == nil {
			continue
		}
		operands = append(operands, &syntax.DeclDynamicWord{Word: arg})
	}
	return &syntax.DeclClause{
		Variant:  &syntax.Lit{Value: variant},
		Operands: operands,
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

func (r *Runner) trapCallback(ctx context.Context, callback, name string) {
	if callback == "" {
		return // nothing to do
	}
	if r.handlingTrap {
		return // don't recurse, as that could lead to cycles
	}
	r.handlingTrap = true

	oldExit := r.exit
	defer func() {
		r.exit = oldExit // traps on EXIT or ERR should not modify the result
		r.handlingTrap = false
	}()

	if err := r.runShellReader(ctx, strings.NewReader(callback), name+" trap", nil); err != nil {
		var status ExitStatus
		if !errors.As(err, &status) {
			r.errf(name+"trap: %v\n", err)
		}
	}
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
	if r != nil && r.opts[optExpandAliases] {
		base = append(base, syntax.ExpandAliases(r.aliasResolver))
	}
	return syntax.NewParser(base...)
}

type restoreVar struct {
	name string
	vr   expand.Variable
}

func (r *Runner) restoreCallAssigns(restores []restoreVar) {
	for _, restore := range restores {
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
			rendered := r.renderInlineArrayValue(as.Array)
			vr := expand.Variable{
				Set:      true,
				Kind:     expand.String,
				Str:      rendered,
				Exported: true,
			}
			if as.Append {
				vr.Str = prev.String() + vr.Str
			}
			resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
			if err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
			r.setVar(resolvedRef.Name.Value, vr)
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return restores
			}
			restores = append(restores, restoreVar{resolvedRef.Name.Value, resolvedPrev})
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
		if err := r.setVarByRef(prev, as.Ref, vr, as.Append); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
		restores = append(restores, restoreVar{resolvedRef.Name.Value, resolvedPrev})
	}
	return restores
}

func (r *Runner) renderInlineArrayValue(expr *syntax.ArrayExpr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := syntax.NewPrinter().Print(&buf, expr); err != nil {
		return ""
	}
	return buf.String()
}

func callExprDeclClause(args []*syntax.Word) *syntax.DeclClause {
	if len(args) == 0 {
		return nil
	}
	name := args[0].Lit()
	switch name {
	case "declare", "local", "export", "readonly", "typeset", "nameref":
	default:
		return nil
	}
	decl := &syntax.DeclClause{
		Variant: &syntax.Lit{
			ValuePos: args[0].Pos(),
			ValueEnd: args[0].End(),
			Value:    name,
		},
	}
	for _, arg := range args[1:] {
		decl.Operands = append(decl.Operands, &syntax.DeclDynamicWord{Word: arg})
	}
	return decl
}

func parseDeclOperandField(field string) (syntax.DeclOperand, error) {
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
	matcher, err := pattern.ExtendedPatternMatcher(pat, pattern.EntireString|pattern.ExtendedOperators)
	_ = err // TODO: report these errors
	return matcher != nil && matcher(name)
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

func (r *Runner) redir(ctx context.Context, rd *syntax.Redirect) (io.Closer, error) {
	if rd.Hdoc != nil {
		pr, err := r.hdocReader(rd)
		if err != nil {
			return nil, err
		}
		r.stdin = pr
		return pr, nil
	}

	orig := &r.stdout
	if rd.N != nil {
		switch rd.N.Value {
		case "0":
			// Note that the input redirects below always use stdin (0)
			// because we don't support anything else right now.
		case "1":
			// The default for the output redirects below.
		case "2":
			orig = &r.stderr
		default:
			panic(fmt.Sprintf("unsupported redirect fd: %v", rd.N.Value))
		}
	}
	arg := r.expandingRedirectWord(func() string {
		return r.literal(rd.Word)
	})
	switch rd.Op {
	case syntax.WordHdoc:
		pr, pw := r.newPipe()
		r.stdin = pr
		// We write to the pipe in a new goroutine,
		// as pipe writes may block once the buffer gets full.
		go func() {
			io.WriteString(pw, arg)
			io.WriteString(pw, "\n")
			pw.Close()
		}()
		return pr, nil
	case syntax.DplOut:
		switch arg {
		case "1":
			*orig = r.stdout
		case "2":
			*orig = r.stderr
		case "-":
			*orig = io.Discard // closing the output writer
		default:
			panic(fmt.Sprintf("unhandled %v arg: %q", rd.Op, arg))
		}
		return nil, nil
	case syntax.RdrIn, syntax.RdrOut, syntax.AppOut,
		syntax.RdrAll, syntax.AppAll:
		// done further below
	case syntax.DplIn:
		switch arg {
		case "-":
			r.stdin = nil // closing the input file
		default:
			panic(fmt.Sprintf("unhandled %v arg: %q", rd.Op, arg))
		}
		return nil, nil
	default:
		panic(fmt.Sprintf("unhandled redirect op: %v", rd.Op))
	}
	mode := os.O_RDONLY
	switch rd.Op {
	case syntax.AppOut, syntax.AppAll:
		mode = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case syntax.RdrOut, syntax.RdrAll:
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := r.open(ctx, arg, mode, 0o644, true)
	if err != nil {
		return nil, err
	}
	switch rd.Op {
	case syntax.RdrIn:
		r.stdin = stdinReader(f)
	case syntax.RdrOut, syntax.AppOut:
		*orig = f
	case syntax.RdrAll, syntax.AppAll:
		r.stdout = f
		r.stderr = f
	default:
		panic(fmt.Sprintf("unhandled redirect op: %v", rd.Op))
	}
	return f, nil
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
	if body := r.funcs[name]; body != nil {
		source := r.funcSource(name)
		isInternal := r.funcInternal(name)
		bashSource := source
		if isInternal {
			bashSource = ""
		}
		// stack them to support nested func calls
		oldParams := r.Params
		r.Params = args[1:]
		oldInFunc := r.inFunc
		r.inFunc = true
		restoreFrame := r.pushFrame(execFrame{
			kind:       frameKindFunction,
			label:      name,
			execFile:   source,
			bashSource: bashSource,
			callLine:   r.functionCallLine(pos),
			internal:   isInternal,
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

		r.stmt(ctx, body)

		r.currentChunkSource = prevChunkSource
		r.currentChunkSourceBase = prevChunkSourceBase
		r.writeEnv = origEnv
		restoreFrame()

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

func (r *Runner) stat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, true)
}

func (r *Runner) lstat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, false)
}
