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
	"syscall"
	"time"

	"github.com/ewhauser/gbash/host"
	shellpattern "github.com/ewhauser/gbash/internal/shellpattern"
	"github.com/ewhauser/gbash/shell/analysis"
	"github.com/ewhauser/gbash/shell/expand"
	"github.com/ewhauser/gbash/shell/syntax"
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

// newPipe creates a pipe using the configured host pipe implementation.
func (r *Runner) newPipe() (StdinReader, io.WriteCloser) {
	reader, writer, err := newPipe(r.pipeFactory)
	if err != nil {
		pr, pw := NewVirtualPipe()
		return pr, pw
	}
	return reader, writer
}

func (r *Runner) currentVisibleLine(line uint) uint {
	if r == nil {
		return line
	}
	if r.trapLineOverride != 0 {
		return r.trapLineOverride
	}
	if line != 0 {
		return line
	}
	if r.currentStmtLine != 0 {
		return r.currentStmtLine
	}
	return 0
}

func (r *Runner) ExpandCurrentLine() uint {
	return r.currentVisibleLine(0)
}

func (r *Runner) ExpandReportError(err error) {
	r.expandErr(err)
}

func (r *Runner) ExpandCmdSubst(w io.Writer, cs *syntax.CmdSubst) error {
	ctx := r.ectx
	if cs.Backquotes && len(cs.Stmts) == 0 {
		return r.expandDeferredBackquote(ctx, w, cs)
	}
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
		r2.shareFDTableSnapshot(r.expandBaseFDs)
	}
	r2.opts[optVerbose] = false
	r2.setStandardFDs(standardFDUpdate{stdout: w, setStdout: true})
	r2.stmts(ctx, cs.Stmts)
	r2.exit.exiting = false // subshells don't exit the parent shell
	r.lastExpandExit = r2.exit
	r.lastExpandExit.errExitIgnored = false
	if r2.exit.fatalExit {
		return r2.exit.err // surface fatal errors immediately
	}
	return nil
}

func (r *Runner) expandDeferredBackquote(ctx context.Context, w io.Writer, cs *syntax.CmdSubst) error {
	src := r.sourceForNode(cs)
	if len(src) >= 2 && src[0] == '`' && src[len(src)-1] == '`' {
		src = src[1 : len(src)-1]
	}

	r2 := r.subshell(false)
	if r.expandBaseFDs != nil {
		r2.shareFDTableSnapshot(r.expandBaseFDs)
	}
	r2.opts[optVerbose] = false
	r2.setStandardFDs(standardFDUpdate{stdout: w, setStdout: true})
	err := r2.runShellReader(ctx, strings.NewReader(src), "`", nil)
	r.lastExpandExit = r2.exit
	r.lastExpandExit.errExitIgnored = false
	if err == nil {
		return nil
	}
	var parseErr syntax.ParseError
	if errors.As(err, &parseErr) {
		msg := nestedBackquoteParseErrorMessage(parseErr)
		if msg != "" {
			if !strings.HasSuffix(msg, "\n") {
				msg += "\n"
			}
			_, _ = io.WriteString(r.stderr, msg)
		}
		return nil
	}
	return err
}

func nestedBackquoteParseErrorMessage(parseErr syntax.ParseError) string {
	switch parseErr.Text {
	case "reached EOF without closing quote `\"`",
		"reached \"`\" without closing quote `\"`":
		return "unexpected EOF while looking for matching `\"'"
	case "reached EOF without closing quote `'`",
		"reached \"`\" without closing quote `'`":
		return "unexpected EOF while looking for matching `''"
	default:
		return stripNestedBackquoteParsePrefix(parseErr.BashError())
	}
}

func stripNestedBackquoteParsePrefix(msg string) string {
	lines := strings.Split(msg, "\n")
	for i, line := range lines {
		lines[i] = stripSingleNestedBackquoteLinePrefix(line)
	}
	return strings.TrimSuffix(strings.Join(lines, "\n"), "\n")
}

func stripSingleNestedBackquoteLinePrefix(line string) string {
	if !strings.HasPrefix(line, "line ") {
		return line
	}
	colon := strings.Index(line, ": ")
	if colon <= len("line ") {
		return line
	}
	for _, r := range line[len("line "):colon] {
		if r < '0' || r > '9' {
			return line
		}
	}
	return line[colon+2:]
}

func (r *Runner) ExpandProcSubst(ps *syntax.ProcSubst) (string, error) {
	ctx := r.ectx
	if len(ps.Stmts) == 0 { // nothing to do
		return "/dev/null", nil
	}
	if r.procSubstHandler != nil {
		return r.customProcSubst(ctx, ps)
	}
	return "", fmt.Errorf("process substitution unavailable")
}

func (r *Runner) ExpandReadDir(path string) ([]fs.DirEntry, error) {
	return r.readDirHandler(r.handlerCtx(r.ectx, handlerKindReadDir, todoPos), path)
}

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg.ResetRuntimeState()
	r.ecfg.Env = expandEnv{r}
	r.ecfg.Runtime = r
	r.ecfg.PlatformOS = r.platform.OS.String()
	r.ecfg.LangVariant = r.parserLangVariant()
	r.ecfg.TildeEnv = tildeExpandEnv{r}
	r.ecfg.StartupHome = r.startupHome
	r.ecfg.PreferStartupHomeForArgTilde = r.platform.OS == host.OSDarwin
	r.updateExpandOpts()
	r.ecfgInit = true
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
			r2.setStandardFDs(standardFDUpdate{stdout: endpoint.Writer, setStdout: true})
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
			stdin, release, err := procSubstStdin(endpoint.Reader, r.pipeFactory)
			if err != nil {
				cleanup()
				return nil, err
			}
			r2.setStandardFDs(standardFDUpdate{
				stdin:     stdin,
				stdout:    stdout,
				setStdin:  true,
				setStdout: true,
			})
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

func procSubstStdin(reader io.ReadCloser, pipeFactory func() (io.ReadCloser, io.WriteCloser, error)) (StdinReader, func(), error) {
	if reader == nil {
		return nil, nil, fmt.Errorf("process substitution reader is nil")
	}
	if sr, ok := reader.(StdinReader); ok {
		return sr, func() {
			_ = reader.Close()
		}, nil
	}
	sr := stdinReader(reader, pipeFactory)
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
	r.ecfg.ReadDirEnabled = !r.opts[optNoGlob]
	r.ecfg.GlobStar = r.opts[optGlobStar]
	r.ecfg.DotGlob = r.opts[optDotGlob]
	r.ecfg.NoCaseGlob = r.opts[optNoCaseGlob]
	r.ecfg.NullGlob = r.opts[optNullGlob]
	r.ecfg.FailGlob = r.opts[optFailGlob]
	r.ecfg.GlobSkipDots = r.opts[optGlobSkipDots]
	r.ecfg.NoUnset = r.opts[optNoUnset]
	r.ecfg.NoBraceExpand = !r.opts[optBraceExpand]
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
	fatalExpansionErr := r.commandString && !r.interactive
	errMsg := err.Error()
	fatalProcSubstArithErr := strings.Contains(errMsg, "arithmetic syntax error") &&
		(strings.Contains(errMsg, `error token is "<(`) ||
			strings.Contains(errMsg, `error token is ">(`))
	if r.commandString && !r.interactive && runtime.GOOS == "darwin" && errors.As(err, &divErr) {
		errMsg = strings.Replace(errMsg, `error token is "0 "`, `error token is " "`, 1)
	}
	var (
		unboundVarErr  expand.UnboundVariableError
		unsetErr       expand.UnsetParameterError
		indirectErr    expand.InvalidIndirectExpansionError
		invalidNameErr expand.InvalidVariableNameError
		failGlobErr    expand.FailGlobError
		arithSyntaxErr expand.ArithmSyntaxError
		arithDiagErr   *expand.ArithmDiagnosticError
	)
	if errors.As(err, &unsetErr) && unsetErr.Node != nil {
		if file := r.currentExecFile(); file != "" {
			errMsg = fmt.Sprintf("%s: line %d: %s", file, unsetErr.Node.Pos().Line(), errMsg)
		}
	}
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
		if r.inAssignment > 0 && r.inFunc {
			r.exit.returning = true
		}
	case errors.As(err, &failGlobErr):
		r.exit.code = 1
		if r.currentStmtLine != 0 {
			r.skipStmtLine = r.currentStmtLine
		}
	case errors.As(err, &divErr):
		r.exit.code = 1
		r.commandAborted = true
	case errors.As(err, &arithSyntaxErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr || fatalProcSubstArithErr
	case errors.As(err, &arithDiagErr):
		r.exit.code = 1
		r.exit.exiting = fatalExpansionErr || fatalProcSubstArithErr
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
		if fatalProcSubstArithErr {
			r.exit.code = 1
			r.exit.exiting = true
			return
		}
		return // other cases do not exit
	}
}

func (r *Runner) stmtAborted() bool {
	return r.exit.exiting || r.exit.fatalExit || r.commandAborted
}

func (r *Runner) arithm(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, false, false, "", 0, 0)
}

func (r *Runner) arithmLet(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, false, true, "", 0, 0)
}

func (r *Runner) arithmCmd(expr syntax.ArithmExpr) int {
	return r.arithmEval(expr, true, false, "", 0, 0)
}

func (r *Runner) arithmEval(expr syntax.ArithmExpr, command, let bool, source string, sourceStart, sourceEnd uint) int {
	var (
		n   int
		err error
	)
	if source != "" {
		n, err = expand.ArithmWithSource(&r.ecfg, expr, source, sourceStart, sourceEnd)
	} else if let {
		n, err = expand.ArithmLet(&r.ecfg, expr)
	} else {
		n, err = expand.Arithm(&r.ecfg, expr)
	}
	var syntaxErr expand.ArithmSyntaxError
	var diagErr *expand.ArithmDiagnosticError
	if command && (errors.As(err, &syntaxErr) || errors.As(err, &diagErr)) {
		err = arithmCommandError{err: err}
	}
	r.expandErr(err)
	var divErr *expand.ArithmDivByZeroError
	if (command || let) && errors.As(err, &divErr) {
		// In let/arithmetic-command contexts, division by zero is a
		// diagnostic that sets exit status 1 but must not abort control
		// flow.  This keeps if/while conditions working like bash:
		//   if let '42/0'; then ... else ... fi  → runs else
		r.commandAborted = false
	}
	if command && err != nil && r.exit.code == 0 {
		r.exit.code = 1
	}
	return n
}

func (r *Runner) arithmCmdExpr(cm *syntax.ArithmCmd) int {
	return r.arithmEval(cm.X, true, false, cm.Source, cm.Left.Offset()+2, cm.Right.Offset())
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
	strs, err := expand.Fields(&r.ecfg, words...)
	r.expandErr(err)
	return strs
}

func (r *Runner) literal(word *syntax.Word) string {
	str, err := expand.Literal(&r.ecfg, word)
	r.expandErr(err)
	return str
}

func condTildeExpandsInDBrackets() bool {
	return runtime.GOOS != "darwin"
}

func callShouldRunDebugTrap(cm *syntax.CallExpr) bool {
	if cm == nil || len(cm.Args) == 0 {
		return true
	}
	return cm.Args[0].Lit() != loopIterHelperCommand
}

func commandUsesTopLevelDebugTrap(cm syntax.Command) bool {
	switch cm := cm.(type) {
	case *syntax.CallExpr:
		return callShouldRunDebugTrap(cm)
	case *syntax.ArithmCmd, *syntax.CaseClause, *syntax.TestClause, *syntax.DeclClause:
		return true
	default:
		return false
	}
}

func isPipelineBinaryCmd(cm *syntax.BinaryCmd) bool {
	return cm != nil && (cm.Op == syntax.Pipe || cm.Op == syntax.PipeAll)
}

func (r *Runner) unwrapSyntheticPipelineStmt(st *syntax.Stmt) *syntax.Stmt {
	if st == nil {
		return nil
	}
	if inner, ok := r.syntheticPipelineStmts[st]; ok && inner != nil {
		return inner
	}
	return st
}

func (r *Runner) collectPipelineDebugStmts(st *syntax.Stmt, nodes *[]*syntax.BinaryCmd, segments *[]*syntax.Stmt) {
	st = r.unwrapSyntheticPipelineStmt(st)
	if st == nil {
		return
	}
	if cm, ok := st.Cmd.(*syntax.BinaryCmd); ok && isPipelineBinaryCmd(cm) {
		r.collectPipelineDebugInfo(cm, nodes, segments)
		return
	}
	*segments = append(*segments, st)
}

func (r *Runner) collectPipelineDebugInfo(cm *syntax.BinaryCmd, nodes *[]*syntax.BinaryCmd, segments *[]*syntax.Stmt) {
	if !isPipelineBinaryCmd(cm) {
		return
	}
	*nodes = append(*nodes, cm)
	r.collectPipelineDebugStmts(cm.X, nodes, segments)
	r.collectPipelineDebugStmts(cm.Y, nodes, segments)
}

func (r *Runner) pipelineDebugInfo(cm *syntax.BinaryCmd) ([]*syntax.BinaryCmd, []*syntax.Stmt) {
	var nodes []*syntax.BinaryCmd
	var segments []*syntax.Stmt
	r.collectPipelineDebugInfo(cm, &nodes, &segments)
	return nodes, segments
}

func debugLineForStmt(st *syntax.Stmt) uint {
	if st == nil {
		return 0
	}
	if line := debugLineForCommand(st.Cmd); line != 0 {
		return line
	}
	return st.Pos().Line()
}

func (r *Runner) pipelineDebugTrapSuppressed(cm *syntax.BinaryCmd) bool {
	return r != nil && r.pipelineDebugSkips != nil && r.pipelineDebugSkips[cm] > 0
}

func (r *Runner) commandDebugTrapSuppressed(cm syntax.Command) bool {
	return r != nil && cm != nil && r.pipelineSegmentDebugSkips != nil && r.pipelineSegmentDebugSkips[cm] > 0
}

func (r *Runner) pipelineDebugActive() bool {
	if r == nil || !r.debugTrapAllowed() {
		return false
	}
	return r.trapAction(trapIDDebug).kind == trapActionCommand
}

func (r *Runner) runCommandDebugTrap(ctx context.Context, cm syntax.Command) bool {
	if !commandUsesTopLevelDebugTrap(cm) || r.commandDebugTrapSuppressed(cm) {
		return false
	}
	return r.runDebugTrap(ctx, debugLineForCommand(cm))
}

func (r *Runner) runPipelineDebugTrap(ctx context.Context, cm *syntax.BinaryCmd) bool {
	if !r.pipelineDebugActive() {
		return false
	}
	if r.pipelineDebugTrapSuppressed(cm) {
		return false
	}
	_, segments := r.pipelineDebugInfo(cm)
	for _, st := range segments {
		if st == nil || !commandUsesTopLevelDebugTrap(st.Cmd) {
			continue
		}
		if r.runDebugTrap(ctx, debugLineForStmt(st)) {
			return true
		}
	}
	return false
}

func (r *Runner) pushPipelineDebugState(cm *syntax.BinaryCmd) func() {
	if !r.pipelineDebugActive() {
		return func() {}
	}
	nodes, segments := r.pipelineDebugInfo(cm)
	if len(nodes) == 0 && len(segments) == 0 {
		return func() {}
	}
	if r.pipelineDebugSkips == nil {
		r.pipelineDebugSkips = make(map[*syntax.BinaryCmd]int, len(nodes))
	}
	if r.pipelineSegmentDebugSkips == nil {
		r.pipelineSegmentDebugSkips = make(map[syntax.Command]int, len(segments))
	}
	for _, node := range nodes {
		r.pipelineDebugSkips[node]++
	}
	for _, st := range segments {
		if st == nil || !commandUsesTopLevelDebugTrap(st.Cmd) {
			continue
		}
		r.pipelineSegmentDebugSkips[st.Cmd]++
	}
	return func() {
		for _, node := range nodes {
			r.pipelineDebugSkips[node]--
			if r.pipelineDebugSkips[node] == 0 {
				delete(r.pipelineDebugSkips, node)
			}
		}
		for _, st := range segments {
			if st == nil || !commandUsesTopLevelDebugTrap(st.Cmd) {
				continue
			}
			r.pipelineSegmentDebugSkips[st.Cmd]--
			if r.pipelineSegmentDebugSkips[st.Cmd] == 0 {
				delete(r.pipelineSegmentDebugSkips, st.Cmd)
			}
		}
	}
}

func (r *Runner) condLiteral(word *syntax.Word) string {
	var (
		str string
		err error
	)
	if condTildeExpandsInDBrackets() {
		str, err = expand.Literal(r.condExpandConfig(), word)
	} else {
		str, err = expand.LiteralNoTilde(r.condExpandConfig(), word)
	}
	r.expandErr(err)
	return str
}

func (r *Runner) assignmentLiteral(word *syntax.Word) string {
	if !r.ecfgInit {
		r.fillExpandConfig(context.Background())
	}
	cfg := r.ecfg
	str, err := expand.AssignmentLiteral(&cfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) assignmentWordLiteral(word *syntax.Word) string {
	if !r.ecfgInit {
		r.fillExpandConfig(context.Background())
	}
	cfg := r.ecfg
	str, err := expand.AssignmentWordLiteral(&cfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) document(word *syntax.Word) string {
	str, err := expand.Document(&r.ecfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) pattern(pat *syntax.Pattern) string {
	str, err := expand.Pattern(&r.ecfg, pat)
	r.expandErr(err)
	return str
}

func (r *Runner) condPattern(pat *syntax.Pattern) string {
	var (
		str string
		err error
	)
	if condTildeExpandsInDBrackets() {
		str, err = expand.Pattern(r.condExpandConfig(), pat)
	} else {
		str, err = expand.PatternNoTilde(r.condExpandConfig(), pat)
	}
	r.expandErr(err)
	return str
}

func (r *Runner) patternWord(word *syntax.Word) string {
	str, err := expand.PatternWord(&r.ecfg, word)
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
	vr := e.r.lookupVar(name)
	e.r.analysisVariableRead(name, nil, vr)
	return vr
}

func (e expandEnv) Set(name string, vr expand.Variable) error {
	e.r.setVar(name, vr)
	return nil // TODO: return any errors
}

func (e expandEnv) SetVarRef(ref *syntax.VarRef, vr expand.Variable, appendValue bool) error {
	return e.r.setVarByRef(e.r.lookupVar(ref.Name.Value), ref, vr, appendValue, attrUpdate{})
}

func (e expandEnv) Each() expand.VarSeq {
	return e.r.writeEnv.Each()
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

func (e tildeExpandEnv) Each() expand.VarSeq {
	return e.r.writeEnv.Each()
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
	overlay := newScopedOverlayEnviron(r.writeEnv, r.platform.UsesCaseInsensitiveEnv())
	if kind == handlerKindExec {
		// When SHELLOPTS is exported, update the env overlay with the
		// current dynamic value so child processes inherit the active
		// options (e.g. xtrace enabled after the export).
		if shellOpts := r.writeEnv.Get("SHELLOPTS"); shellOpts.Exported {
			overlay.Set("SHELLOPTS", expand.Variable{
				Set:      true,
				Kind:     expand.String,
				Str:      r.shellOptsValue(),
				Exported: true,
				ReadOnly: true,
			})
		}
		// Same for BASHOPTS: propagate current shopt state to children.
		if bashOpts := r.writeEnv.Get("BASHOPTS"); bashOpts.Exported {
			overlay.Set("BASHOPTS", expand.Variable{
				Set:      true,
				Kind:     expand.String,
				Str:      r.bashOptsValue(),
				Exported: true,
				ReadOnly: true,
			})
		}
	}
	hc := HandlerContext{
		runner:             r,
		kind:               kind,
		Env:                overlay,
		Dir:                r.Dir,
		VisibleDir:         r.visibleDir(),
		ExecFile:           r.currentExecFile(),
		Internal:           r.currentInternal(),
		DisableCommandHash: commandHashDisabled(ctx),
		Pos:                pos,
		Stdout:             r.stdout,
		Stderr:             r.stderr,
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

func (r *Runner) clearStandardFDErrors() {
	if fd := r.getFD(1); fd != nil {
		fd.clearWriteError()
	}
	if fd := r.getFD(2); fd != nil {
		fd.clearWriteError()
	}
}

func (r *Runner) applyStandardFDErrors() {
	if !r.exit.ok() {
		return
	}
	for _, fdNum := range []int{1, 2} {
		fd := r.getFD(fdNum)
		if fd == nil {
			continue
		}
		if err := fd.writeError(); err != nil {
			if diag, ok := shellWriteErrorDiagnostic(err); ok {
				r.errf("%s\n", diag)
			}
			r.exit.code = 1
			r.exit.err = ExitStatus(1)
			r.exit.errExitIgnored = false
			return
		}
	}
}

func shellWriteErrorDiagnostic(err error) (string, bool) {
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr == nil || pathErr.Path == "" || pathErr.Err == nil {
		return "", false
	}
	text := pathErr.Err.Error()
	if text == "" {
		return "", false
	}
	return pathErr.Path + ": " + strings.ToUpper(text[:1]) + text[1:], true
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
	prevStmt := r.analysisStatementEnter(st)
	defer func() {
		r.analysisStatementExit(prevStmt, r.AnalysisStatus())
	}()
	line := st.Pos().Line()
	if r.stmtDepth == 0 {
		r.commandAborted = false
	}
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
	r.redirectErrLine = 0
	r.stmtDepth++
	hadPipeStatus := r.pipeStatusSet
	r.pipeStatusSet = false
	defer func() {
		r.stmtDepth--
		r.currentStmtLine = 0
	}()
	if st.Background || st.Disown {
		r2 := r.subshell(true)
		r2.suppressTopLevelErrTrap = true
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
		r.stmtSync(ctx, st, hadPipeStatus)
	}
	r.runPendingSignalTraps(ctx)
	r.lastStmtLine = line
	r.lastExit = r.exit
}

func (r *Runner) sourceForNode(node syntax.Node) string {
	if node == nil || r.currentChunkSource == "" {
		return ""
	}
	return r.sourceForOffsets(node.Pos().Offset(), node.End().Offset())
}

func (r *Runner) sourceForOffsets(startOffset, endOffset uint) string {
	if r.currentChunkSource == "" {
		return ""
	}
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

func (r *Runner) stmtSync(ctx context.Context, st *syntax.Stmt, hadPipeStatus bool) {
	if r.currentChunkSource == "" {
		r.printVerbose(st)
	}
	r.ensureFDTable()
	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	oldFDs := r.fds
	oldFDsShared := r.fdsShared
	r.fdsShared = true
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
	ranCmd := false
	closers := make([]io.Closer, 0, len(st.Redirs))
	keepClosedFDs := make(map[int]struct{}, len(st.Redirs))
	releasedNamedFDs := make([]string, 0, len(st.Redirs))
	for _, rd := range st.Redirs {
		result, err := r.redir(ctx, rd)
		if err != nil {
			r.redirectErrLine = rd.Pos().Line()
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
	// Sync r.stdin/stdout/stderr from the fd table so that redirections
	// like 2>&1 are visible to the command (the fd table was updated by
	// redir, but the convenience fields were not).
	if len(st.Redirs) > 0 {
		r.syncStandardFDs()
	}
	if r.exit.ok() && st.Cmd != nil {
		ranCmd = true
		if st.Negated {
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.cmd(ctx, st.Cmd)
			r.noErrExit = oldNoErrExit
		} else {
			r.cmd(ctx, st.Cmd)
		}
	}
	if !r.pipeStatusSet && stmtShouldMaterializePipeStatus(st, hadPipeStatus) {
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
	if !r.exit.ok() && r.noErrExit {
		r.exit.errExitIgnored = true
	}
	if st.Negated {
		if r.exit.ok() {
			r.exit.code = 1
		} else {
			r.exit.clear()
		}
	} else if r.pipelineErrTrapDepth > 0 {
		if !r.exit.ok() && !r.noErrExit && !r.exit.errExitIgnored && r.opts[optErrExit] {
			r.exit.exiting = true
		}
	} else if r.shouldRunStmtErrTrap(st, ranCmd) {
		r.maybeRunErrTrap(ctx, r.stmtErrTrapLine(st, ranCmd))
	}
	if !keepRedirs {
		r.stdin, r.stdout, r.stderr = oldIn, oldOut, oldErr
		r.fds = oldFDs
		r.fdsShared = oldFDsShared
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

func (r *Runner) shouldRunStmtErrTrap(st *syntax.Stmt, ranCmd bool) bool {
	if r.exit.ok() || r.noErrExit || r.exit.errExitIgnored || st == nil {
		return false
	}
	if r.suppressTopLevelErrTrap && r.stmtDepth == 1 {
		return false
	}
	if b, ok := st.Cmd.(*syntax.BinaryCmd); ok {
		switch b.Op {
		case syntax.AndStmt, syntax.OrStmt, syntax.Pipe, syntax.PipeAll:
			return false
		}
	}
	if !ranCmd {
		return true
	}
	switch st.Cmd.(type) {
	case *syntax.Block, *syntax.ForClause, *syntax.CaseClause:
		return r.skipStmtLine == st.Pos().Line()
	default:
		return true
	}
}

func (r *Runner) stmtErrTrapLine(st *syntax.Stmt, ranCmd bool) uint {
	if !ranCmd && len(st.Redirs) > 0 {
		if r.redirectErrLine != 0 {
			return r.redirectErrLine
		}
		return st.Redirs[0].Pos().Line()
	}
	return st.Pos().Line()
}

func stmtShouldMaterializePipeStatus(st *syntax.Stmt, hadPipeStatus bool) bool {
	if st == nil {
		return false
	}
	if st.Cmd == nil {
		return len(st.Redirs) > 0
	}
	switch st.Cmd.(type) {
	case *syntax.ForClause, *syntax.WhileClause, *syntax.IfClause, *syntax.CaseClause, *syntax.FuncDecl:
		return hadPipeStatus
	default:
		return true
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
	r.ensureOwnHiddenReadonlyArrayDecl()
	r.hiddenReadonlyArrayDecl[name] = kind
}

func (r *Runner) clearHiddenReadonlyArrayDecl(name string) {
	if r.hiddenReadonlyArrayDecl == nil || name == "" {
		return
	}
	r.hiddenReadonlyArrayDecl = cloneMapOnWrite(r.hiddenReadonlyArrayDecl, &r.hiddenReadonlyArrayDeclShared)
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
	base := append([]syntax.ParserOption{syntax.Variant(r.parserLangVariant())}, opts...)
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
	for len(restores) > 0 {
		last := len(restores) - 1
		restore := restores[last]
		restores = restores[:last]
		if restore.restoreSeconds {
			if err := r.writeEnv.Set(restore.name, restore.vr); err != nil {
				// If the variable became readonly during command execution
				// (e.g. SECONDS), silently skip the restore rather than
				// treating it as a fatal error.
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

type callAssignOverlayMode uint8

const (
	callAssignOverlayNone callAssignOverlayMode = iota
	callAssignOverlayDiscard
	callAssignOverlayCommit
)

func (r *Runner) runCallAssignOverlay(assigns []*syntax.Assign, forceExport, consumeLocals, crossesFuncScope bool) (*overlayEnviron, bool) {
	overlay := newScopedOverlayEnviron(r.writeEnv, r.platform.UsesCaseInsensitiveEnv())
	overlay.tempScopeConsumesLocals = consumeLocals
	overlay.tempScopeCrossesFuncScope = crossesFuncScope
	origEnv := r.writeEnv
	r.writeEnv = overlay
	defer func() {
		r.writeEnv = origEnv
	}()
	_ = r.runCallAssignsWithExport(assigns, forceExport)
	if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
		return nil, false
	}
	// Enable tempScope after bindings are populated so that Set during
	// binding populates the overlay rather than passing through.
	overlay.tempScope = true
	return overlay, true
}

func (r *Runner) commitCallAssignOverlay(overlay *overlayEnviron) {
	if overlay == nil {
		return
	}
	parent, ok := overlay.parent.(expand.WriteEnviron)
	if !ok {
		return
	}
	names := make([]string, 0, len(overlay.values))
	for _, vr := range overlay.values {
		names = append(names, vr.Name)
	}
	sort.Strings(names)
	for _, name := range names {
		vr, ok := overlay.values[overlay.normalize(name)]
		if !ok {
			continue
		}
		origEnv := r.writeEnv
		r.writeEnv = parent
		r.setVar(vr.Name, vr.Variable)
		r.writeEnv = origEnv
	}
	// Commit any temp bindings that were explicitly unset during
	// execution so the unset propagates to the parent scope.
	for normalized := range overlay.tempUnset {
		if _, inValues := overlay.values[normalized]; inValues {
			continue // still has a value, already committed above
		}
		origEnv := r.writeEnv
		r.writeEnv = parent
		r.delVar(normalized)
		r.writeEnv = origEnv
	}
}

func (r *Runner) runCallAssigns(assigns []*syntax.Assign) []restoreVar {
	return r.runCallAssignsWithExport(assigns, true)
}

func (r *Runner) runSpecialCallAssigns(assigns []*syntax.Assign) []restoreVar {
	return r.runCallAssignsWithExport(assigns, true)
}

func (r *Runner) runTempCallAssigns(assigns []*syntax.Assign) []restoreVar {
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
				Exported: true,
				Kind:     expand.String,
				Str:      r.renderInlineArrayValue(as.Array),
			}
			resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
			if err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
			restore := r.callAssignRestoreVar(resolvedRef.Name.Value, resolvedPrev)
			if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
				if strings.HasSuffix(err.Error(), ": readonly variable") {
					r.errf("%v\n", err)
					if len(restores) > 0 {
						r.exit.code = 1
						return restores
					}
					continue
				}
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
			restores = append(restores, restore)
			continue
		}

		vr, _, ok := r.assignVal(prev, as, "")
		if !ok || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
		vr.Exported = true
		resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		restore := r.callAssignRestoreVar(resolvedRef.Name.Value, resolvedPrev)
		if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
			if strings.HasSuffix(err.Error(), ": readonly variable") {
				r.errf("%v\n", err)
				if len(restores) > 0 {
					r.exit.code = 1
					return restores
				}
				continue
			}
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		restores = append(restores, restore)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
	}
	return restores
}
func (r *Runner) callAssignRestoreVar(name string, vr expand.Variable) restoreVar {
	restore := restoreVar{name: name, vr: vr}
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
	return restore
}

func (r *Runner) runCallAssignsWithExport(assigns []*syntax.Assign, forceExport bool) []restoreVar {
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
				Set:  true,
				Kind: expand.String,
				Str:  r.renderInlineArrayValue(as.Array),
			}
			resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
			if err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
			if forceExport {
				vr.Exported = true
			} else {
				vr.Exported = resolvedPrev.Exported
			}
			restore := r.callAssignRestoreVar(resolvedRef.Name.Value, resolvedPrev)
			if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
				r.errf("%v\n", err)
				r.exit.code = 1
				return restores
			}
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

		resolvedRef, resolvedPrev, err := prev.ResolveRef(r.writeEnv, as.Ref)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		if forceExport {
			vr.Exported = true
		} else {
			vr.Exported = resolvedPrev.Exported
		}
		restore := r.callAssignRestoreVar(resolvedRef.Name.Value, resolvedPrev)
		if err := r.setVarByRef(prev, as.Ref, vr, as.Append, attrUpdate{}); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 1
			return restores
		}
		restores = append(restores, restore)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return restores
		}
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
				b.WriteString(bashDeclPlainValue(r.parserLangVariant(), field))
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
			b.WriteString(bashDeclPlainValue(r.parserLangVariant(), r.assignmentLiteral(elem.Value)))
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

func validFunctionName(name string) bool {
	return strings.TrimSpace(name) != "" &&
		!strings.Contains(name, "$") &&
		!strings.Contains(name, "<(") &&
		!strings.Contains(name, ">(")
}

func (r *Runner) patternMatch(pat, name string) bool {
	mode := shellpattern.EntireString | shellpattern.ExtendedOperators
	if r.opts[optNoCaseMatch] {
		mode |= shellpattern.NoGlobCase
	}
	ok, err := shellpattern.Match(pat, name, mode)
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
	quotedHdoc := rd.HdocDelim != nil && !rd.HdocDelim.BodyExpands
	if rd.Op != syntax.DashHdoc {
		var hdoc string
		if quotedHdoc {
			hdoc = hdocLiteral(rd.Hdoc)
		} else {
			hdoc = r.document(rd.Hdoc)
		}
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
		if quotedHdoc {
			buf.WriteString(hdocLiteral(&syntax.Word{Parts: cur}))
		} else {
			buf.WriteString(r.document(&syntax.Word{Parts: cur}))
		}
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

// hdocLiteral extracts the verbatim content of a quoted heredoc Word
// without any backslash processing or expansion.
func hdocLiteral(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range word.Parts {
		if lit, ok := part.(*syntax.Lit); ok {
			sb.WriteString(lit.Value)
		}
	}
	return sb.String()
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
		fields, err := expand.RedirectFields(&r.ecfg, rd.Word)
		r.expandErr(err)
		r.inRedirectWord--
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
		fields, err := expand.RedirectFields(&r.ecfg, rd.Word)
		r.expandErr(err)
		r.inRedirectWord--
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
	case syntax.DplOut:
		r.inRedirectWord++
		fields, err := expand.DupFields(&r.ecfg, rd.Word)
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
	return f.fd.Write(p)
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
	r.ensureOwnNamedFDReleased()
	r.namedFDReleased[name] = true
}

func (r *Runner) clearNamedFDReleased(name string) {
	if name == "" || r.namedFDReleased == nil {
		return
	}
	r.namedFDReleased = cloneMapOnWrite(r.namedFDReleased, &r.namedFDReleasedShared)
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
	r.loopDepth++
	defer func() { r.loopDepth-- }()
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
	if r.posixSpecialBuiltinActive(name) {
		r.exit = r.builtin(ctx, pos, name, args[1:])
		return
	}
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
		r.analysisScopeEnter(analysis.Scope{
			Kind:     analysis.ScopeFunction,
			Name:     name,
			File:     source,
			CallLine: uint(r.functionCallLine(pos)),
		})
		defer r.analysisScopeExit(r.AnalysisStatus())
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
		scopeEnv := newScopedOverlayEnviron(r.writeEnv, r.platform.UsesCaseInsensitiveEnv())
		scopeEnv.funcScope = true
		r.writeEnv = scopeEnv
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
	if r.canDispatchBuiltin(name) {
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
