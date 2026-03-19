// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"unicode"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// non-empty string is true, empty string is false
func (r *Runner) bashTest(ctx context.Context, expr syntax.TestExpr, classic bool) string {
	switch x := expr.(type) {
	case *syntax.Word:
		if classic {
			// In the classic "test" mode, we already expanded and
			// split the list of words, so don't redo that work.
			return r.document(x)
		}
		return r.literal(x)
	case *syntax.ParenTest:
		return r.bashTest(ctx, x.X, classic)
	case *syntax.BinaryTest:
		switch x.Op {
		case syntax.TsReMatch:
			if classic {
				break
			}
			left, ok := r.testExpandWord(x.X.(*syntax.Word), expand.Literal)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			right, ok := r.testExpandWord(x.Y.(*syntax.Word), expand.Regexp)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			if r.binTest(ctx, x.Op, left, right) {
				return "1"
			}
			return ""
		case syntax.TsMatchShort, syntax.TsMatch, syntax.TsNoMatch:
			str := r.literal(x.X.(*syntax.Word))
			yw := x.Y.(*syntax.Word)
			if classic { // test, [
				lit := r.literal(yw)
				if (str == lit) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			} else { // [[
				pattern := r.patternWord(yw)
				if match(pattern, str) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			}
			return ""
		}
		if r.binTest(ctx, x.Op, r.bashTest(ctx, x.X, classic), r.bashTest(ctx, x.Y, classic)) {
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
				if r.refIsSet(r.looseVarRef(r.document(word))) {
					return "1"
				}
				return ""
			}
			if r.refIsSet(r.looseVarRefWord(word)) {
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
		if r.unTest(ctx, x.Op, r.bashTest(ctx, x.X, classic)) {
			return "1"
		}
		return ""
	}
	return ""
}

// non-empty string is true, empty string is false
func (r *Runner) bashCond(ctx context.Context, expr syntax.CondExpr) string {
	switch x := expr.(type) {
	case *syntax.CondWord:
		return r.literal(x.Word)
	case *syntax.CondPattern:
		return r.pattern(x.Pattern)
	case *syntax.CondRegex:
		return r.literal(x.Word)
	case *syntax.CondVarRef:
		return printVarRef(x.Ref)
	case *syntax.CondParen:
		return r.bashCond(ctx, x.X)
	case *syntax.CondBinary:
		switch x.Op {
		case syntax.TsReMatch:
			left, ok := r.testExpandWord(x.X.(*syntax.CondWord).Word, expand.Literal)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			right, ok := r.testExpandWord(x.Y.(*syntax.CondRegex).Word, expand.Regexp)
			if !ok {
				r.clearBASH_REMATCH()
				return ""
			}
			if r.binTest(ctx, x.Op, left, right) {
				return "1"
			}
			return ""
		case syntax.TsMatchShort, syntax.TsMatch, syntax.TsNoMatch:
			str := r.literal(x.X.(*syntax.CondWord).Word)
			pattern := r.pattern(x.Y.(*syntax.CondPattern).Pattern)
			if match(pattern, str) == (x.Op != syntax.TsNoMatch) {
				return "1"
			}
			return ""
		}
		if r.binTest(ctx, x.Op, r.bashCond(ctx, x.X), r.bashCond(ctx, x.Y)) {
			return "1"
		}
		return ""
	case *syntax.CondUnary:
		switch x.Op {
		case syntax.TsVarSet:
			switch operand := x.X.(type) {
			case *syntax.CondVarRef:
				if r.refIsSet(operand.Ref) {
					return "1"
				}
			case *syntax.CondWord:
				if r.refIsSet(r.looseVarRefWord(operand.Word)) {
					return "1"
				}
			}
			return ""
		case syntax.TsRefVar:
			switch operand := x.X.(type) {
			case *syntax.CondVarRef:
				if r.refIsNameRef(operand.Ref) {
					return "1"
				}
			case *syntax.CondWord:
				if r.refIsNameRef(r.looseVarRefWord(operand.Word)) {
					return "1"
				}
			}
			return ""
		}
		if r.unTest(ctx, x.Op, r.bashCond(ctx, x.X)) {
			return "1"
		}
		return ""
	}
	return ""
}

func (r *Runner) testExpandWord(word *syntax.Word, expandFunc func(*expand.Config, *syntax.Word) (string, error)) (string, bool) {
	str, err := expandFunc(r.ecfg, word)
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
	errMsg := err.Error()
	switch {
	case errors.As(err, &expand.UnsetParameterError{}):
		return true
	case errMsg == "invalid indirect expansion":
		return true
	default:
		return false
	}
}

func (r *Runner) binTest(ctx context.Context, op syntax.BinTestOperator, x, y string) bool {
	switch op {
	case syntax.TsReMatch:
		if bashRegexHasInvalidBareBraces(y) {
			r.exit.code = 2
			return false
		}
		re, err := regexp.Compile(y)
		if err != nil {
			r.exit.code = 2
			return false
		}
		m := re.FindStringSubmatch(x)
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
	case syntax.TsNewer:
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil || err2 != nil {
			return false
		}
		return info1.ModTime().After(info2.ModTime())
	case syntax.TsOlder:
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil || err2 != nil {
			return false
		}
		return info1.ModTime().Before(info2.ModTime())
	case syntax.TsDevIno:
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil || err2 != nil {
			return false
		}
		return os.SameFile(info1, info2)
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

func bashRegexHasInvalidBareBraces(expr string) bool {
	escaped := false
	inClass := false
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			if inClass {
				inClass = false
			}
		case '{':
			if inClass {
				continue
			}
			j := i + 1
			start := j
			for j < len(expr) && unicode.IsDigit(rune(expr[j])) {
				j++
			}
			if j == start {
				if j < len(expr) && expr[j] == ',' {
					j++
					for j < len(expr) && unicode.IsDigit(rune(expr[j])) {
						j++
					}
				} else {
					return true
				}
			} else if j < len(expr) && expr[j] == ',' {
				j++
				for j < len(expr) && unicode.IsDigit(rune(expr[j])) {
					j++
				}
			}
			if j >= len(expr) || expr[j] != '}' {
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

func (r *Runner) unTest(ctx context.Context, op syntax.UnTestOperator, x string) bool {
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
