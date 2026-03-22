// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

func (r *Runner) cmd(ctx context.Context, cm syntax.Command) {
	if r.stop(ctx) {
		return
	}

	tracingEnabled := r.opts[optXTrace]
	trace := r.tracer()

	switch cm := cm.(type) {
	case *syntax.Block:
		r.cmdBlock(ctx, cm)
	case *syntax.Subshell:
		r.cmdSubshell(ctx, cm)
	case *syntax.CallExpr:
		r.cmdCall(ctx, cm, tracingEnabled, trace)
	case *syntax.BinaryCmd:
		r.cmdBinary(ctx, cm)
	case *syntax.IfClause:
		r.cmdIf(ctx, cm)
	case *syntax.WhileClause:
		r.cmdWhile(ctx, cm)
	case *syntax.ForClause:
		r.cmdFor(ctx, cm, trace)
	case *syntax.FuncDecl:
		r.cmdFuncDecl(cm)
	case *syntax.ArithmCmd:
		r.cmdArithm(ctx, cm, tracingEnabled, trace)
	case *syntax.LetClause:
		r.cmdLet(cm, tracingEnabled, trace)
	case *syntax.CaseClause:
		r.cmdCase(ctx, cm, trace)
	case *syntax.TestClause:
		r.cmdTest(ctx, cm, tracingEnabled, trace)
	case *syntax.DeclClause:
		r.cmdDecl(ctx, cm, tracingEnabled, trace)
	case *syntax.TimeClause:
		r.cmdTime(ctx, cm)
	default:
		panic(fmt.Sprintf("unhandled command node: %T", cm))
	}
}

func (r *Runner) cmdBlock(ctx context.Context, cm *syntax.Block) {
	r.stmts(ctx, cm.Stmts)
}

func (r *Runner) cmdSubshell(ctx context.Context, cm *syntax.Subshell) {
	r2 := r.subshell(false)
	r2.stmts(ctx, cm.Stmts)
	r2.exit.exiting = false // subshells don't exit the parent shell
	r.exit = r2.exit
	r.exit.errExitIgnored = false
}

func (r *Runner) cmdCall(ctx context.Context, cm *syntax.CallExpr, tracingEnabled bool, trace *tracer) {
	if callShouldRunDebugTrap(cm) && r.runCommandDebugTrap(ctx, cm) {
		return
	}

	args := cm.Args
	r.lastExpandExit = exitStatus{}
	fields, decl := r.resolveCallExprArgs(args)
	if decl != nil {
		if r.posixMode() && IsPOSIXSpecialBuiltin(decl.Variant.Value) {
			restores := r.runSpecialCallAssigns(cm.Assigns)
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				r.restoreCallAssigns(restores)
				return
			}
			r.cmd(ctx, decl)
			return
		}
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
				if strings.HasSuffix(err.Error(), ": readonly variable") {
					if r.posixMode() {
						r.exit.code = 127
						if !r.interactive {
							r.exit.exiting = true
						}
						return
					}
					if r.currentStmtLine != 0 {
						r.skipStmtLine = r.currentStmtLine
					}
					return
				}
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
			r.exit.errExitIgnored = false
		}
		if !r.exit.fatalExit && !r.exit.exiting && r.exit.err == nil {
			r.setSpecialUnderscore("")
		}
		return
	}

	assignOverlayMode := callAssignOverlayDiscard
	assignOverlayConsumesLocals := false
	assignOverlayCrossesFuncScope := false
	if r.posixSpecialBuiltinActive(fields[0]) {
		assignOverlayMode = callAssignOverlayCommit
	} else if fields[0] == "eval" || fields[0] == "source" || fields[0] == "." {
		assignOverlayConsumesLocals = true
	} else {
		if info, ok := r.funcInfo(fields[0]); ok && info.body != nil {
			assignOverlayConsumesLocals = true
			assignOverlayCrossesFuncScope = true
		} else {
			assignOverlayMode = callAssignOverlayNone
		}
	}

	var (
		assignOverlay *overlayEnviron
		restoreEnv    expand.WriteEnviron
		restores      []restoreVar
	)
	if len(cm.Assigns) > 0 && assignOverlayMode != callAssignOverlayNone {
		var ok bool
		assignOverlay, ok = r.runCallAssignOverlay(cm.Assigns, true, assignOverlayConsumesLocals, assignOverlayCrossesFuncScope)
		if !ok || r.exit.fatalExit || r.exit.exiting {
			return
		}
		restoreEnv = r.writeEnv
		r.writeEnv = assignOverlay
	} else {
		restores = r.runCallAssigns(cm.Assigns)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			r.restoreCallAssigns(restores)
			return
		}
	}

	r.setSpecialUnderscoreFromFields(fields)

	trace.call(fields[0], fields[1:]...)
	trace.newLineFlush()

	r.call(ctx, cm.Args[0].Pos(), fields)
	r.exit.errExitIgnored = false
	if assignOverlay != nil {
		if assignOverlayMode == callAssignOverlayCommit && !r.exit.fatalExit {
			r.commitCallAssignOverlay(assignOverlay)
		}
		if _, hasPath := assignOverlay.values[assignOverlay.normalize("PATH")]; hasPath {
			r.commandHashClear()
		}
		r.writeEnv = restoreEnv
		return
	}
	r.restoreCallAssigns(restores)
}

func (r *Runner) cmdBinary(ctx context.Context, cm *syntax.BinaryCmd) {
	switch cm.Op {
	case syntax.AndStmt, syntax.OrStmt:
		oldNoErrExit := r.noErrExit
		r.noErrExit = true
		r.stmt(ctx, cm.X)
		r.noErrExit = oldNoErrExit
		if r.stop(ctx) {
			return
		}
		if r.exit.ok() == (cm.Op == syntax.AndStmt) {
			r.stmt(ctx, cm.Y)
		}
	case syntax.Pipe, syntax.PipeAll:
		suppressPipelineDebug := r.pipelineDebugTrapSuppressed(cm)
		if !suppressPipelineDebug && r.runPipelineDebugTrap(ctx, cm) {
			return
		}
		restorePipelineDebug := func() {}
		if !suppressPipelineDebug {
			restorePipelineDebug = r.pushPipelineDebugState(cm)
		}
		defer restorePipelineDebug()

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
			r2.pipelineErrTrapDepth++
			defer func() {
				r2.pipelineErrTrapDepth--
				pw.Close()
			}()
			r2.stmt(ctx, cm.X)
			r2.exit.exiting = false // subshells don't exit the parent shell
		})
		r.pipelineErrTrapDepth++
		if stmt, ok := r.lastpipeStmt(cm.Y); ok {
			r.stmt(ctx, stmt)
		} else {
			r.stmt(ctx, cm.Y)
		}
		r.pipelineErrTrapDepth--
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
			r.exit.errExitIgnored = false
		}
		if r2.exit.fatalExit {
			r.exit.fatal(r2.exit.err) // surface fatal errors immediately
		}
		if r.pipelineErrTrapDepth == 0 && !r.exit.ok() && !r.noErrExit && !r.exit.errExitIgnored {
			r.maybeRunErrTrap(ctx, cm.Pos().Line())
		}
	}
}

func (r *Runner) cmdIf(ctx context.Context, cm *syntax.IfClause) {
	oldNoErrExit := r.noErrExit
	r.noErrExit = true
	r.stmts(ctx, cm.Cond)
	r.noErrExit = oldNoErrExit
	if r.stmtAborted() {
		return
	}

	if r.exit.ok() {
		r.stmts(ctx, cm.Then)
		return
	}
	r.exit.clear()
	if cm.Else != nil {
		r.cmd(ctx, cm.Else)
	}
}

func (r *Runner) cmdWhile(ctx context.Context, cm *syntax.WhileClause) {
	for !r.stop(ctx) {
		oldNoErrExit := r.noErrExit
		r.noErrExit = true
		r.loopDepth++
		for _, condStmt := range cm.Cond {
			r.stmt(ctx, condStmt)
			if r.breakEnclosing > 0 || r.contnEnclosing > 0 {
				break
			}
		}
		r.loopDepth--
		r.noErrExit = oldNoErrExit
		if r.stmtAborted() {
			return
		}
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			if r.contnEnclosing > 0 {
				return
			}
			r.exit.clear()
			continue
		}
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			r.exit.clear()
			return
		}

		stop := r.exit.ok() == cm.Until
		r.exit.clear()
		if stop || r.loopStmtsBroken(ctx, cm.Do) {
			return
		}
	}
}

func (r *Runner) cmdFor(ctx context.Context, cm *syntax.ForClause, trace *tracer) {
	switch y := cm.Loop.(type) {
	case *syntax.WordIter:
		name := y.Name.Value
		if !syntax.ValidName(name) {
			r.errf("`%s': not a valid identifier\n", name)
			r.exit.code = 1
			return
		}
		items := r.Params // for i; do ...

		inToken := y.InPos.IsValid()
		if inToken {
			items = r.fields(y.Items...) // for i in ...; do ...
			if r.stmtAborted() {
				return
			}
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
				return
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
				return
			}
			trace.refreshPrefixContext()
		}
	case *syntax.CStyleLoop:
		if y.Init != nil {
			if r.runDebugTrap(ctx, cm.Pos().Line()) {
				return
			}
			r.arithmEval(y.Init, true, false, r.sourceForNode(y.Init), y.Init.Pos().Offset(), y.Init.End().Offset())
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return
			}
		}
		for {
			if r.runDebugTrap(ctx, cm.Pos().Line()) {
				return
			}
			if y.Cond != nil && r.arithmEval(y.Cond, true, false, r.sourceForNode(y.Cond), y.Cond.Pos().Offset(), y.Cond.End().Offset()) == 0 {
				return
			}
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return
			}
			if r.loopStmtsBroken(ctx, cm.Do) {
				return
			}
			if y.Post != nil {
				if r.runDebugTrap(ctx, cm.Pos().Line()) {
					return
				}
				r.arithmEval(y.Post, true, false, r.sourceForNode(y.Post), y.Post.Pos().Offset(), y.Post.End().Offset())
				if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
					return
				}
			}
		}
	}
}

func (r *Runner) cmdFuncDecl(cm *syntax.FuncDecl) {
	if r.posixMode() && IsPOSIXSpecialBuiltin(cm.Name.Value) {
		r.errf("`%s': is a special builtin\n", cm.Name.Value)
		r.exit.code = 2
		if !r.interactive {
			r.exit.exiting = true
		}
		return
	}
	if !validFunctionName(cm.Name.Value) {
		r.errf("`%s': not a valid identifier\n", cm.Name.Value)
		r.exit.code = 1
		return
	}
	r.setFunc(cm.Name.Value, cm.Body)
}

func (r *Runner) cmdArithm(ctx context.Context, cm *syntax.ArithmCmd, tracingEnabled bool, trace *tracer) {
	if r.runCommandDebugTrap(ctx, cm) {
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
}

func (r *Runner) cmdLet(cm *syntax.LetClause, tracingEnabled bool, trace *tracer) {
	var val int
	for _, expr := range cm.Exprs {
		val = r.arithmLet(expr)

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
}

func (r *Runner) cmdCase(ctx context.Context, cm *syntax.CaseClause, trace *tracer) {
	if r.runCommandDebugTrap(ctx, cm) {
		return
	}
	trace.string("case ")
	trace.expr(cm.Word)
	trace.string(" in")
	trace.newLineFlush()

	str := r.literal(cm.Word)
	if r.stmtAborted() {
		return
	}

	fallthroughNext := false
	for _, ci := range cm.Items {
		matched := fallthroughNext
		if !matched {
			for _, word := range ci.Patterns {
				pat := r.pattern(word)
				if r.stmtAborted() {
					return
				}
				if r.patternMatch(pat, str) {
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
}

func (r *Runner) cmdTest(ctx context.Context, cm *syntax.TestClause, tracingEnabled bool, trace *tracer) {
	if r.runCommandDebugTrap(ctx, cm) {
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
}

func (r *Runner) cmdDecl(ctx context.Context, cm *syntax.DeclClause, tracingEnabled bool, trace *tracer) {
	if r.runCommandDebugTrap(ctx, cm) {
		return
	}
	newDeclCommand(r, cm, tracingEnabled, trace).run()
}

func (r *Runner) cmdTime(ctx context.Context, cm *syntax.TimeClause) {
	start := time.Now()
	if cm.Stmt != nil {
		r.stmt(ctx, cm.Stmt)
	}
	format := "%s\t%s\n"
	if cm.PosixFormat {
		format = "%s %s\n"
	} else {
		r.errf("\n")
	}
	realTime := time.Since(start)
	r.errf(format, "real", elapsedString(realTime, cm.PosixFormat))
	// TODO: can we do these?
	r.errf(format, "user", elapsedString(0, cm.PosixFormat))
	r.errf(format, "sys", elapsedString(0, cm.PosixFormat))
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
