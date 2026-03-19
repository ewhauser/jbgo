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
	"iter"
	"math"
	"os"
	"slices"
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
		Env: expandEnv{r},
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
	errMsg := err.Error()
	fmt.Fprintln(r.stderr, errMsg)
	switch {
	case errors.As(err, &expand.UnsetParameterError{}):
	case errMsg == "invalid indirect expansion":
		// TODO: These errors are treated as fatal by bash.
		// Make the error type reflect that.
	default:
		return // other cases do not exit
	}
	r.exit.code = 127
	r.exit.exiting = true
}

func (r *Runner) arithm(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, false)
}

func (r *Runner) arithmCmd(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, true)
}

func (r *Runner) arithmEval(expr syntax.ArithmExpr, command bool) int {
	n, err := expand.Arithm(r.ecfg, expr)
	var syntaxErr expand.ArithmSyntaxError
	if command && errors.As(err, &syntaxErr) {
		err = arithmCommandError{err: err}
	}
	r.expandErr(err)
	return n
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

func (r *Runner) pattern(word *syntax.Word) string {
	str, err := expand.Pattern(r.ecfg, word)
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

func (r *Runner) stmtSync(ctx context.Context, st *syntax.Stmt) {
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
		// Use a new slice, to not modify the slice in the alias map.
		args := cm.Args
		for i := 0; i < len(args); {
			if !r.opts[optExpandAliases] {
				break
			}
			als, ok := r.alias[args[i].Lit()]
			if !ok {
				break
			}
			args = slices.Replace(args, i, i+1, als.args...)
			if !als.blank {
				break
			}
			i += len(als.args)
		}
		if decl := prefixAssignDeclClause(args, cm.Assigns); decl != nil {
			r.expandAssignsForSideEffects(cm.Assigns)
			if r.exit.fatalExit || r.exit.exiting {
				return
			}
			r.cmd(ctx, decl)
			return
		}
		// Check for declaration builtins before expanding args to avoid
		// double expansion of command substitutions in decl arguments.
		if decl := callExprDeclClause(args); decl != nil {
			r.expandAssignsForSideEffects(cm.Assigns)
			if r.exit.fatalExit || r.exit.exiting {
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

				vr := r.assignVal(prev, as, "")
				// Preserve and apply variable attributes from the previous declaration.
				vr.Integer = prev.Integer
				vr.Lower = prev.Lower
				vr.Upper = prev.Upper
				if prev.Integer && as.Append && vr.Kind == expand.String {
					// For -i with +=, do arithmetic addition instead of string concat.
					oldVal := r.evalIntegerAttr(prev.String())
					newVal := r.evalIntegerAttr(r.literal(as.Value))
					vr.Str = strconv.Itoa(oldVal + newVal)
				} else {
					r.applyVarAttrs(&vr)
				}
				if err := r.setVarByRef(prev, as.Ref, vr); err != nil {
					r.errf("%v\n", err)
					r.exit.code = 1
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
		if !r.exit.ok() {
			break
		}

		trace.call(fields[0], fields[1:]...)
		trace.newLineFlush()

		r.call(ctx, cm.Args[0].Pos(), fields)
		for _, restore := range restores {
			r.setVar(restore.name, restore.vr)
		}
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
		r.exit.oneIf(r.arithmCmd(cm.X) == 0)
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
		r.exit.oneIf(val == 0)
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
		if r.bashTest(ctx, cm.X, false) == "" && r.exit.ok() {
			// to preserve exit status code 2 for regex errors, etc
			r.exit.code = 1
		}
	case *syntax.DeclClause:
		local, global := false, false
		var modes []string
		valType := ""
		declQuery := "" // "-f" or "-p" for query mode
		allowPlusFlags := false
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
	assignLoop:
		for as := range r.flattenAssigns(cm.Args) {
			name := as.Ref.Name.Value
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
						return
					}
					continue assignLoop
				}
			}
			if !syntax.ValidName(name) {
				if allowPlusFlags {
					r.errf("declare: invalid name %q\n", name)
				} else {
					r.errf("%s: `%s': not a valid identifier\n", cm.Variant.Value, name)
				}
				r.exit.code = 1
				return
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
				continue
			}
			if declQuery == "-p" {
				// declare -p name: print variable with attributes.
				vr := r.lookupVar(name)
				if !vr.Declared() {
					r.errf("declare: %s: not found\n", name)
					r.exit.code = 1
					continue
				}
				flags := vr.Flags()
				if flags == "" {
					flags = "-"
				}
				switch vr.Kind {
				case expand.Indexed:
					r.outf("declare -%s %s=(", flags, name)
					for i, v := range vr.List {
						if i > 0 {
							r.out(" ")
						}
						r.outf("[%d]=%q", i, v)
					}
					r.out(")\n")
				case expand.Associative:
					r.outf("declare -%s %s=(", flags, name)
					first := true
					for k, v := range vr.Map {
						if !first {
							r.out(" ")
						}
						r.outf("[%s]=%q", k, v)
						first = false
					}
					r.out(")\n")
				default:
					r.outf("declare -%s %s=%q\n", flags, name, vr.Str)
				}
				continue
			}
			vr := r.lookupVar(name)
			if as.Naked {
				if valType == "-A" {
					vr.Kind = expand.Associative
				} else if valType == "+a" || valType == "+A" {
					// Remove array/assoc attribute, convert to string.
					if vr.Kind == expand.Indexed || vr.Kind == expand.Associative {
						vr.Kind = expand.String
						vr.Str = vr.String()
						vr.List = nil
						vr.Map = nil
					}
				} else {
					vr.Kind = expand.KeepValue
				}
			} else {
				// When -a/-A is in effect and the value looks like a compound
				// assignment "(elem ...)", reparse it as an array.
				if (valType == "-a" || valType == "-A") && as.Array == nil && as.Value != nil {
					if reparsed := r.reparseCompoundAssign(as); reparsed != nil {
						as = reparsed
					}
				}
				if valType == "+a" || valType == "+A" {
					// +a/+A with a value: treat as string assignment.
					vr = r.assignVal(vr, as, "")
				} else {
					vr = r.assignVal(vr, as, valType)
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
						newVal := r.evalIntegerAttr(r.literal(as.Value))
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
			if as.Naked {
				r.setVar(name, vr)
			} else {
				if err := r.setVarByRef(r.lookupVar(as.Ref.Name.Value), as.Ref, vr); err != nil {
					r.errf("%v\n", err)
					r.exit.code = 1
					return
				}
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
	declArgs := make([]*syntax.Assign, 0, len(args)-1)
	for _, arg := range args[1:] {
		if arg == nil {
			continue
		}
		declArgs = append(declArgs, &syntax.Assign{
			Naked: true,
			Value: arg,
		})
	}
	return &syntax.DeclClause{
		Variant: &syntax.Lit{Value: variant},
		Args:    declArgs,
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

	p := syntax.NewParser()
	// TODO: do this parsing when "trap" is called?
	file, err := p.Parse(strings.NewReader(callback), name+" trap")
	if err != nil {
		r.errf(name+"trap: %v\n", err)
		// ignore errors in the callback
		return
	}
	oldExit := r.exit
	r.stmts(ctx, file.Stmts)
	r.exit = oldExit // traps on EXIT or ERR should not modify the result

	r.handlingTrap = false
}

func (r *Runner) flattenAssigns(args []*syntax.Assign) iter.Seq[*syntax.Assign] {
	return func(yield func(*syntax.Assign) bool) {
		for _, as := range args {
			// Convert "declare $x" into "declare value".
			// Don't use syntax.Parser here, as we only want the basic
			// splitting by '='.
			if as.Ref != nil {
				if !yield(as) {
					return
				}
				continue
			}
			for _, field := range r.fields(as.Value) {
				as := &syntax.Assign{}
				name, val, ok := strings.Cut(field, "=")
				as.Ref = &syntax.VarRef{Name: &syntax.Lit{Value: name}}
				if !ok {
					as.Naked = true
				} else {
					as.Value = &syntax.Word{Parts: []syntax.WordPart{
						&syntax.Lit{Value: val},
					}}
				}
				if !yield(as) {
					return
				}
			}
		}
	}
}

type restoreVar struct {
	name string
	vr   expand.Variable
}

// expandAssignsForSideEffects expands assignment values to trigger side effects
// (like command substitutions) without persisting the assignments. This is used
// for prefix assignments before declaration builtins, where bash runs command
// substitutions but does not actually set the variables.
func (r *Runner) expandAssignsForSideEffects(assigns []*syntax.Assign) {
	for _, as := range assigns {
		// Just expand the value to trigger side effects; don't set the variable.
		r.assignVal(r.lookupVar(as.Ref.Name.Value), as, "")
	}
}

func (r *Runner) runCallAssigns(assigns []*syntax.Assign) []restoreVar {
	var restores []restoreVar
	for _, as := range assigns {
		name := as.Ref.Name.Value
		prev := r.lookupVar(name)

		vr := r.assignVal(prev, as, "")
		// Inline command vars are always exported.
		vr.Exported = true

		restores = append(restores, restoreVar{name, prev})
		if err := r.setVarByRef(prev, as.Ref, vr); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
	}
	return restores
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
		decl.Args = append(decl.Args, &syntax.Assign{
			Naked: true,
			Value: arg,
		})
	}
	return decl
}

// reparseCompoundAssign checks if an assign's value looks like a compound
// assignment "(elem ...)" and reparses it so that as.Array is populated.
// This is needed for "declare -a 'var=(1 2 3)'" where the value came from
// a dynamically expanded string. Returns nil if the value is not a compound
// assignment.
func (r *Runner) reparseCompoundAssign(as *syntax.Assign) *syntax.Assign {
	val := r.literal(as.Value)
	if !strings.HasPrefix(val, "(") || !strings.HasSuffix(val, ")") {
		return nil
	}
	// Parse "name=(elems)" to get a proper Assign with Array populated.
	src := as.Ref.Name.Value + "=" + val
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader(src), "")
	if err != nil || len(file.Stmts) != 1 {
		return nil
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) != 1 {
		return nil
	}
	reparsed := call.Assigns[0]
	if reparsed.Array == nil {
		return nil
	}
	// Preserve the append flag from the original assign.
	reparsed.Append = as.Append
	return reparsed
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

		r.stmt(ctx, body)

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
