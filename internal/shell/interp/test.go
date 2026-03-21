// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	resyntax "regexp/syntax"
	"runtime"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

type condEval struct {
	value string
	trace string
}

func condBoolString(ok bool) string {
	if ok {
		return "1"
	}
	return ""
}

func condTraceArg(trace *tracer, value string) string {
	if trace == nil {
		return ""
	}
	return trace.traceArg(value)
}

func condTracePattern(trace *tracer, value string) string {
	if trace == nil {
		return ""
	}
	return value
}

func condTraceUnary(op fmt.Stringer, operand string) string {
	return fmt.Sprintf("%s %s", op, condTraceOperand(operand))
}

func condTraceBinary(left string, op fmt.Stringer, right string) string {
	return fmt.Sprintf("%s %s %s", condTraceOperand(left), op, condTraceOperand(right))
}

func condTraceOperand(operand string) string {
	if operand == "" {
		return "''"
	}
	return operand
}

func isNumericTestOp(op syntax.BinTestOperator) bool {
	switch op {
	case syntax.TsEql, syntax.TsNeq, syntax.TsLeq, syntax.TsGeq, syntax.TsLss, syntax.TsGtr:
		return true
	default:
		return false
	}
}

func classicTestInt(s string) (int64, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n, err == nil
}

func numericTest(op syntax.BinTestOperator, x, y int64) bool {
	switch op {
	case syntax.TsEql:
		return x == y
	case syntax.TsNeq:
		return x != y
	case syntax.TsLeq:
		return x <= y
	case syntax.TsGeq:
		return x >= y
	case syntax.TsLss:
		return x < y
	case syntax.TsGtr:
		return x > y
	default:
		return false
	}
}

// non-empty string is true, empty string is false
func (r *Runner) bashTest(ctx context.Context, expr syntax.TestExpr, classic bool, cmdName string) string {
	switch x := expr.(type) {
	case *syntax.Word:
		if classic {
			// In the classic "test" mode, we already expanded and
			// split the list of words, so don't redo that work.
			return r.document(x)
		}
		return r.condLiteral(x)
	case *syntax.ParenTest:
		return r.bashTest(ctx, x.X, classic, cmdName)
	case *syntax.BinaryTest:
		switch x.Op {
		case syntax.TsReMatch:
			if classic {
				break
			}
			expandLeft := expand.Literal
			expandRight := expand.Regexp
			if !condTildeExpandsInDBrackets() {
				expandLeft = expand.LiteralNoTilde
				expandRight = expand.RegexpNoTilde
			}
			left, ok := r.testExpandWord(x.X.(*syntax.Word), expandLeft)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			rightWord := x.Y.(*syntax.Word)
			right, ok := r.testExpandWord(rightWord, expandRight)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			if r.regexMatch(left, right) {
				return "1"
			}
			return ""
		case syntax.TsMatchShort, syntax.TsMatch, syntax.TsNoMatch:
			str := r.literal(x.X.(*syntax.Word))
			yw := x.Y.(*syntax.Word)
			if classic { // test, [
				str = r.document(x.X.(*syntax.Word))
				lit := r.document(yw)
				if (str == lit) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			} else { // [[
				pattern := r.condPatternWord(yw)
				if match(pattern, str) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			}
			return ""
		}
		if classic && isNumericTestOp(x.Op) {
			left := r.bashTest(ctx, x.X, classic, cmdName)
			if !r.exit.ok() {
				return ""
			}
			right := r.bashTest(ctx, x.Y, classic, cmdName)
			if !r.exit.ok() {
				return ""
			}
			lx, ok := classicTestInt(left)
			if !ok {
				r.classicTestError(cmdName, "%s: integer expected", left)
				r.exit.code = 2
				return ""
			}
			rx, ok := classicTestInt(right)
			if !ok {
				r.classicTestError(cmdName, "%s: integer expected", right)
				r.exit.code = 2
				return ""
			}
			if numericTest(x.Op, lx, rx) {
				return "1"
			}
			return ""
		}
		if r.binTest(ctx, x.Op, r.bashTest(ctx, x.X, classic, cmdName), r.bashTest(ctx, x.Y, classic, cmdName)) {
			return "1"
		}
		return ""
	case *syntax.UnaryTest:
		switch x.Op {
		case syntax.TsVarSet:
			word, ok := x.X.(*syntax.Word)
			if !ok {
				return ""
			}
			if classic {
				if r.refIsSet(r.looseVarRefWithContext(r.document(word), syntax.VarRefVarSet)) {
					return "1"
				}
				return ""
			}
			if r.refIsSet(r.looseVarRefWordWithContext(word, syntax.VarRefVarSet)) {
				return "1"
			}
			return ""
		case syntax.TsRefVar:
			word, ok := x.X.(*syntax.Word)
			if !ok {
				return ""
			}
			if classic {
				if r.refIsNameRef(r.looseVarRef(r.document(word))) {
					return "1"
				}
				return ""
			}
			if r.refIsNameRef(r.looseVarRefWord(word)) {
				return "1"
			}
			return ""
		}
		if r.unTest(ctx, x.Op, r.bashTest(ctx, x.X, classic, cmdName), cmdName) {
			return "1"
		}
		return ""
	}
	return ""
}

func (r *Runner) classicTestError(cmdName, format string, args ...any) {
	if cmdName == "" {
		cmdName = "["
	}
	values := make([]any, 0, len(args)+1)
	values = append(values, cmdName)
	values = append(values, args...)
	r.errf("%s: "+format+"\n", values...)
}

// non-empty string is true, empty string is false
func (r *Runner) bashCond(ctx context.Context, expr syntax.CondExpr) string {
	return r.evalCond(ctx, expr, nil).value
}

func (r *Runner) evalCond(ctx context.Context, expr syntax.CondExpr, trace *tracer) condEval {
	switch x := expr.(type) {
	case *syntax.CondWord:
		value := r.condLiteral(x.Word)
		return condEval{value: value, trace: condTraceArg(trace, value)}
	case *syntax.CondPattern:
		value := r.condPattern(x.Pattern)
		return condEval{value: value, trace: condTracePattern(trace, value)}
	case *syntax.CondRegex:
		value := r.condLiteral(x.Word)
		return condEval{value: value, trace: condTraceArg(trace, value)}
	case *syntax.CondVarRef:
		value := printVarRef(x.Ref)
		if trace == nil {
			return condEval{value: value}
		}
		return condEval{value: value, trace: value}
	case *syntax.CondParen:
		eval := r.evalCond(ctx, x.X, trace)
		if trace != nil {
			eval.trace = "( " + eval.trace + " )"
		}
		return eval
	case *syntax.CondBinary:
		switch x.Op {
		case syntax.TsReMatch:
			expandLeft := expand.Literal
			expandRight := expand.Regexp
			if !condTildeExpandsInDBrackets() {
				expandLeft = expand.LiteralNoTilde
				expandRight = expand.RegexpNoTilde
			}
			left, ok := r.testExpandWord(x.X.(*syntax.CondWord).Word, expandLeft)
			if !ok {
				r.clearBASH_REMATCH()
				return condEval{}
			}
			rightWord := x.Y.(*syntax.CondRegex).Word
			right, ok := r.testExpandWord(rightWord, expandRight)
			if !ok {
				r.clearBASH_REMATCH()
				return condEval{}
			}
			return condEval{
				value: condBoolString(r.regexMatch(left, right)),
				trace: condTraceBinary(condTraceArg(trace, left), x.Op, condTraceArg(trace, right)),
			}
		case syntax.TsMatchShort, syntax.TsMatch, syntax.TsNoMatch:
			str := r.condLiteral(x.X.(*syntax.CondWord).Word)
			pattern := r.condPattern(x.Y.(*syntax.CondPattern).Pattern)
			return condEval{
				value: condBoolString(match(pattern, str) == (x.Op != syntax.TsNoMatch)),
				trace: condTraceBinary(condTraceArg(trace, str), x.Op, condTracePattern(trace, pattern)),
			}
		}
		left := r.evalCond(ctx, x.X, trace)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return left
		}
		right := r.evalCond(ctx, x.Y, trace)
		if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
			return condEval{
				trace: condTraceBinary(left.trace, x.Op, right.trace),
			}
		}
		value := condBoolString(r.binTest(ctx, x.Op, left.value, right.value))
		if isNumericTestOp(x.Op) {
			leftNum := int64(r.evalIntegerAttr(left.value))
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return condEval{
					trace: condTraceBinary(left.trace, x.Op, right.trace),
				}
			}
			rightNum := int64(r.evalIntegerAttr(right.value))
			if !r.exit.ok() || r.exit.fatalExit || r.exit.exiting {
				return condEval{
					trace: condTraceBinary(left.trace, x.Op, right.trace),
				}
			}
			value = condBoolString(numericTest(x.Op, leftNum, rightNum))
		}
		return condEval{
			value: value,
			trace: condTraceBinary(left.trace, x.Op, right.trace),
		}
	case *syntax.CondUnary:
		switch x.Op {
		case syntax.TsVarSet:
			switch operand := x.X.(type) {
			case *syntax.CondVarRef:
				if r.refIsSet(operand.Ref) {
					return condEval{
						value: "1",
						trace: condTraceUnary(x.Op, printVarRef(operand.Ref)),
					}
				}
			case *syntax.CondWord:
				if r.refIsSet(r.looseVarRefWordWithContext(operand.Word, syntax.VarRefVarSet)) {
					return condEval{
						value: "1",
						trace: condTraceUnary(x.Op, printSyntaxNode(operand.Word)),
					}
				}
			}
			return condEval{trace: condTraceUnary(x.Op, printSyntaxNode(x.X.(syntax.Node)))}
		case syntax.TsRefVar:
			switch operand := x.X.(type) {
			case *syntax.CondVarRef:
				if r.refIsNameRef(operand.Ref) {
					return condEval{
						value: "1",
						trace: condTraceUnary(x.Op, printVarRef(operand.Ref)),
					}
				}
			case *syntax.CondWord:
				if r.refIsNameRef(r.looseVarRefWord(operand.Word)) {
					return condEval{
						value: "1",
						trace: condTraceUnary(x.Op, printSyntaxNode(operand.Word)),
					}
				}
			}
			return condEval{trace: condTraceUnary(x.Op, printSyntaxNode(x.X.(syntax.Node)))}
		}
		operand := r.evalCond(ctx, x.X, trace)
		return condEval{
			value: condBoolString(r.unTest(ctx, x.Op, operand.value, "")),
			trace: condTraceUnary(x.Op, operand.trace),
		}
	}
	if trace == nil {
		return condEval{}
	}
	return condEval{trace: printSyntaxNode(expr.(syntax.Node))}
}

func (r *Runner) condExpandConfig() *expand.Config {
	if r.ecfg == nil {
		r.fillExpandConfig(context.Background())
	}
	cfg := *r.ecfg
	cfg.StartupHome = ""
	return &cfg
}

func (r *Runner) condPatternWord(word *syntax.Word) string {
	str, err := expand.PatternWord(r.condExpandConfig(), word)
	r.expandErr(err)
	return str
}

func (r *Runner) testExpandWord(word *syntax.Word, expandFunc func(*expand.Config, *syntax.Word) (string, error)) (string, bool) {
	str, err := expandFunc(r.condExpandConfig(), word)
	if err == nil {
		return str, true
	}
	fmt.Fprintln(r.stderr, err)
	if testExpandErrFatal(err) {
		r.exit.code = 1
		r.exit.exiting = true
	} else {
		r.exit.code = 1
	}
	return "", false
}

func (r *Runner) failInvalidRegex(expr, reason string) bool {
	r.clearBASH_REMATCH()
	r.exit.code = 2
	if r.legacyBashCompat {
		return false
	}
	fmt.Fprintf(r.stderr, "[[: invalid regular expression %s: %s\n", bashQuoteString(expr), reason)
	return false
}

func bashQuoteString(s string) string {
	return "`" + s + "'"
}

func (r *Runner) clearBASH_REMATCH() {
	r.setVar("BASH_REMATCH", expand.Variable{
		Set:  true,
		Kind: expand.Indexed,
		List: []string{},
	})
}

func testExpandErrFatal(err error) bool {
	if err == nil {
		return false
	}
	var (
		unboundVarErr  expand.UnboundVariableError
		unsetErr       expand.UnsetParameterError
		indirectErr    expand.InvalidIndirectExpansionError
		invalidNameErr expand.InvalidVariableNameError
	)
	switch {
	case errors.As(err, &unboundVarErr):
		return true
	case errors.As(err, &unsetErr):
		return true
	case errors.As(err, &indirectErr):
		return true
	case errors.As(err, &invalidNameErr):
		return true
	default:
		return false
	}
}

func (r *Runner) regexMatch(subject, expr string) bool {
	if bashRegexHasInvalidBareBrace(expr) {
		return r.failInvalidRegex(expr, "Invalid preceding regular expression")
	}
	translated := translateBashRegex(expr)
	re, err := regexp.Compile(translated)
	if err != nil {
		return r.failInvalidRegex(expr, bashRegexCompileErrorReason(err))
	}
	m := re.FindStringSubmatch(subject)
	if m == nil {
		r.clearBASH_REMATCH()
		return false
	}
	vr := expand.Variable{
		Set:  true,
		Kind: expand.Indexed,
		List: m,
	}
	r.setVar("BASH_REMATCH", vr)
	return true
}

func (r *Runner) binTest(ctx context.Context, op syntax.BinTestOperator, x, y string) bool {
	switch op {
	case syntax.TsReMatch:
		return r.regexMatch(x, y)
	case syntax.TsNewer:
		// -nt: True if file1 is newer than file2, or if file1 exists and file2 does not.
		// Only treat ErrNotExist as "file missing" - other errors (permission, policy) return false.
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil {
			return false // file1 error or doesn't exist
		}
		if err2 != nil {
			if errors.Is(err2, fs.ErrNotExist) {
				return true // file1 exists, file2 doesn't exist
			}
			return false // file2 has other error (permission, etc.)
		}
		return info1.ModTime().After(info2.ModTime())
	case syntax.TsOlder:
		// -ot: True if file1 is older than file2, or if file2 exists and file1 does not.
		// Only treat ErrNotExist as "file missing" - other errors (permission, policy) return false.
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err2 != nil {
			return false // file2 error or doesn't exist
		}
		if err1 != nil {
			if errors.Is(err1, fs.ErrNotExist) {
				return true // file2 exists, file1 doesn't exist
			}
			return false // file1 has other error (permission, etc.)
		}
		return info1.ModTime().Before(info2.ModTime())
	case syntax.TsDevIno:
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil || err2 != nil {
			return false
		}
		return sameFile(info1, info2)
	case syntax.TsEql:
		return atoi(x) == atoi(y)
	case syntax.TsNeq:
		return atoi(x) != atoi(y)
	case syntax.TsLeq:
		return atoi(x) <= atoi(y)
	case syntax.TsGeq:
		return atoi(x) >= atoi(y)
	case syntax.TsLss:
		return atoi(x) < atoi(y)
	case syntax.TsGtr:
		return atoi(x) > atoi(y)
	case syntax.AndTest:
		return x != "" && y != ""
	case syntax.OrTest:
		return x != "" || y != ""
	case syntax.TsBefore:
		return x < y
	case syntax.TsAfter:
		return x > y
	default:
		panic(fmt.Sprintf("unsupported binary test operator: %q", op))
	}
}

func bashRegexCompileErrorReason(err error) string {
	var syntaxErr *resyntax.Error
	if !errors.As(err, &syntaxErr) {
		return err.Error()
	}
	switch syntaxErr.Code {
	case resyntax.ErrUnexpectedParen, resyntax.ErrMissingParen:
		return "parentheses not balanced"
	case resyntax.ErrMissingBracket:
		return "brackets ([ ]) not balanced"
	case resyntax.ErrMissingRepeatArgument, resyntax.ErrInvalidRepeatOp:
		if runtime.GOOS == "darwin" {
			return "repetition-operator operand invalid"
		}
		return "Invalid preceding regular expression"
	case resyntax.ErrInvalidRepeatSize:
		return "invalid repetition count(s)"
	default:
		return err.Error()
	}
}

func translateBashRegex(expr string) string {
	var b strings.Builder
	escaped := false
	inClass := false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			b.WriteByte(ch)
			escaped = true
		case '[':
			if !inClass {
				inClass = true
			}
			b.WriteByte(ch)
		case ']':
			if inClass {
				inClass = false
			}
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func bashRegexHasInvalidBareBrace(expr string) bool {
	for i := 0; i < len(expr); i++ {
		if expr[i] != '{' {
			continue
		}
		if bashRegexBraceIsLiteral(expr, i) {
			continue
		}
		return true
	}
	return false
}

func bashRegexBraceIsLiteral(expr string, index int) bool {
	escaped := false
	inClass := false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if escaped {
			if i == index {
				return true
			}
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			if i == index {
				return false
			}
			escaped = true
		case '[':
			if i == index {
				return false
			}
			if !inClass {
				inClass = true
			}
		case ']':
			if i == index && inClass {
				return true
			}
			if inClass {
				inClass = false
			}
		case '{':
			if i != index {
				continue
			}
			if inClass {
				return true
			}
			j := i + 1
			start := j
			for j < len(expr) && expr[j] >= '0' && expr[j] <= '9' {
				j++
			}
			if j == start {
				if j < len(expr) && expr[j] == ',' {
					j++
					for j < len(expr) && expr[j] >= '0' && expr[j] <= '9' {
						j++
					}
				} else {
					return false
				}
			} else if j < len(expr) && expr[j] == ',' {
				j++
				for j < len(expr) && expr[j] >= '0' && expr[j] <= '9' {
					j++
				}
			}
			return j < len(expr) && expr[j] == '}'
		default:
			if i == index && inClass {
				return true
			}
		}
	}
	return false
}

func (r *Runner) statMode(ctx context.Context, name string, mode os.FileMode) bool {
	if name == "" {
		return false
	}
	info, err := r.stat(ctx, name)
	return err == nil && info.Mode()&mode != 0
}

// These are copied from x/sys/unix as we can't import it here.
const (
	access_R_OK = 0x4
	access_W_OK = 0x2
	access_X_OK = 0x1
)

func (r *Runner) unTest(ctx context.Context, op syntax.UnTestOperator, x, cmdName string) bool {
	switch op {
	case syntax.TsExists:
		if x == "" {
			return false
		}
		_, err := r.stat(ctx, x)
		return err == nil
	case syntax.TsRegFile:
		if x == "" {
			return false
		}
		info, err := r.stat(ctx, x)
		return err == nil && info.Mode().IsRegular()
	case syntax.TsDirect:
		return r.statMode(ctx, x, os.ModeDir)
	case syntax.TsCharSp:
		return r.statMode(ctx, x, os.ModeCharDevice)
	case syntax.TsBlckSp:
		info, err := r.stat(ctx, x)
		return err == nil && info.Mode()&os.ModeDevice != 0 &&
			info.Mode()&os.ModeCharDevice == 0
	case syntax.TsNmPipe:
		return r.statMode(ctx, x, os.ModeNamedPipe)
	case syntax.TsSocket:
		return r.statMode(ctx, x, os.ModeSocket)
	case syntax.TsSmbLink:
		info, err := r.lstat(ctx, x)
		return err == nil && info.Mode()&os.ModeSymlink != 0
	case syntax.TsSticky:
		return r.statMode(ctx, x, os.ModeSticky)
	case syntax.TsUIDSet:
		return r.statMode(ctx, x, os.ModeSetuid)
	case syntax.TsGIDSet:
		return r.statMode(ctx, x, os.ModeSetgid)
	// case syntax.TsGrpOwn:
	// case syntax.TsUsrOwn:
	// case syntax.TsModif:
	case syntax.TsRead:
		if x == "" {
			return false
		}
		return r.access(ctx, r.absPath(x), access_R_OK) == nil
	case syntax.TsWrite:
		if x == "" {
			return false
		}
		return r.access(ctx, r.absPath(x), access_W_OK) == nil
	case syntax.TsExec:
		if x == "" {
			return false
		}
		return r.access(ctx, r.absPath(x), access_X_OK) == nil
	case syntax.TsNoEmpty:
		if x == "" {
			return false
		}
		info, err := r.stat(ctx, x)
		return err == nil && info.Size() > 0
	case syntax.TsFdTerm:
		if cmdName == "" {
			fd := atoi(x)
			var f any
			switch fd {
			case 0:
				f = r.stdin
			case 1:
				f = r.stdout
			case 2:
				f = r.stderr
			}
			if f, ok := f.(interface{ Fd() uintptr }); ok {
				if statter, ok := f.(interface{ Stat() (os.FileInfo, error) }); ok {
					if info, err := statter.Stat(); err == nil {
						return info.Mode()&os.ModeCharDevice != 0
					}
				}
			}
			return false
		}
		fd, ok := classicTestInt(x)
		if !ok {
			r.classicTestError(cmdName, "%s: integer expected", x)
			r.exit.code = 2
			return false
		}
		var f any
		switch fd {
		case 0:
			f = r.stdin
		case 1:
			f = r.stdout
		case 2:
			f = r.stderr
		}
		if f, ok := f.(interface{ Fd() uintptr }); ok {
			if statter, ok := f.(interface{ Stat() (os.FileInfo, error) }); ok {
				if info, err := statter.Stat(); err == nil {
					return info.Mode()&os.ModeCharDevice != 0
				}
			}
		}
		return false
	case syntax.TsEmpStr:
		return x == ""
	case syntax.TsNempStr:
		return x != ""
	case syntax.TsOptSet:
		if opt := r.posixOptByName(x); opt != nil {
			return *opt
		}
		return false
	case syntax.TsVarSet:
		return r.refIsSet(r.looseVarRef(x))
	case syntax.TsRefVar:
		return r.refIsNameRef(r.looseVarRef(x))
	case syntax.TsNot:
		return x == ""
	case syntax.TsUsrOwn, syntax.TsGrpOwn:
		return r.unTestOwnOrGrp(ctx, op, x)
	default:
		panic(fmt.Sprintf("unhandled unary test op: %v", op))
	}
}

// sameFile reports whether info1 and info2 describe the same file.
// It first tries os.SameFile which works for real filesystems, then falls back
// to comparing NodeID for memory filesystems or DeviceID/Inode for filesystems
// that provide those via the FileIdentity interface.
func sameFile(info1, info2 fs.FileInfo) bool {
	if os.SameFile(info1, info2) {
		return true
	}
	// Try FileIdentity interface for sandbox filesystems
	type fileIdentity interface {
		FileIdentity() (deviceID, inode uint64)
	}
	if fi1, ok := info1.Sys().(fileIdentity); ok {
		if fi2, ok := info2.Sys().(fileIdentity); ok {
			dev1, ino1 := fi1.FileIdentity()
			dev2, ino2 := fi2.FileIdentity()
			return dev1 == dev2 && ino1 == ino2
		}
	}
	// Check for structs with NodeID field (used by MemoryFS)
	sys1, sys2 := info1.Sys(), info2.Sys()
	if id1, ok := getNodeID(sys1); ok {
		if id2, ok := getNodeID(sys2); ok {
			return id1 == id2 && id1 != 0
		}
	}
	return false
}

// getNodeID extracts a NodeID field from a struct using reflection.
func getNodeID(v any) (uint64, bool) {
	if v == nil {
		return 0, false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0, false
	}
	field := rv.FieldByName("NodeID")
	if !field.IsValid() || field.Kind() != reflect.Uint64 {
		return 0, false
	}
	return field.Uint(), true
}
