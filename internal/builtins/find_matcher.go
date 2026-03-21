package builtins

import (
	"context"
	"math"
	"path"
	"regexp"
	"strings"
	"time"
)

func resolveFindExpr(ctx context.Context, inv *Invocation, expr findExpr) error {
	switch e := expr.(type) {
	case nil:
		return nil
	case *findNewerExpr:
		info, _, exists, err := statMaybe(ctx, inv, e.refPath)
		if err != nil {
			return err
		}
		e.referenceReady = true
		e.referenceFound = exists
		if exists {
			e.resolvedTime = info.ModTime() //nolint:nilaway // info is non-nil when exists is true
		}
		return nil
	case *findNotExpr:
		return resolveFindExpr(ctx, inv, e.expr)
	case *findAndExpr:
		if err := resolveFindExpr(ctx, inv, e.left); err != nil {
			return err
		}
		return resolveFindExpr(ctx, inv, e.right)
	case *findOrExpr:
		if err := resolveFindExpr(ctx, inv, e.left); err != nil {
			return err
		}
		return resolveFindExpr(ctx, inv, e.right)
	default:
		return nil
	}
}

func evaluateFindExpr(expr findExpr, ctx *findEvalContext) findEvalResult {
	switch e := expr.(type) {
	case nil:
		return findEvalResult{matches: true}
	case *findNameExpr:
		return findEvalResult{matches: findGlobMatch(ctx.name, e.pattern, e.ignoreCase)}
	case *findPathExpr:
		return findEvalResult{matches: findGlobMatch(ctx.displayPath, e.pattern, e.ignoreCase)}
	case *findRegexExpr:
		return findEvalResult{matches: e.regex.MatchString(ctx.displayPath)}
	case *findTypeExpr:
		if e.fileType == 'f' {
			return findEvalResult{matches: !ctx.isDir}
		}
		if e.fileType == 'd' {
			return findEvalResult{matches: ctx.isDir}
		}
		return findEvalResult{}
	case *findEmptyExpr:
		return findEvalResult{matches: ctx.isEmpty}
	case *findMTimeExpr:
		ageDays := time.Since(ctx.mtime).Hours() / 24
		switch e.comparison {
		case findCompareMore:
			return findEvalResult{matches: ageDays > float64(e.days)}
		case findCompareLess:
			return findEvalResult{matches: ageDays < float64(e.days)}
		default:
			return findEvalResult{matches: int(math.Floor(ageDays)) == e.days}
		}
	case *findNewerExpr:
		return findEvalResult{matches: e.referenceReady && e.referenceFound && ctx.mtime.After(e.resolvedTime)}
	case *findSizeExpr:
		return findEvalResult{matches: findSizeMatch(ctx.size, e)}
	case *findPermExpr:
		fileMode := ctx.mode.Perm()
		targetMode := e.mode.Perm()
		switch e.matchType {
		case findPermAll:
			return findEvalResult{matches: fileMode&targetMode == targetMode}
		case findPermAny:
			return findEvalResult{matches: fileMode&targetMode != 0}
		default:
			return findEvalResult{matches: fileMode == targetMode}
		}
	case *findPruneExpr:
		return findEvalResult{matches: true, pruned: true}
	case *findPrintExpr:
		return findEvalResult{matches: true, printed: true}
	case *findPrint0Expr:
		return findEvalResult{matches: true, printed: true}
	case *findNotExpr:
		inner := evaluateFindExpr(e.expr, ctx)
		return findEvalResult{matches: !inner.matches, pruned: inner.pruned}
	case *findAndExpr:
		left := evaluateFindExpr(e.left, ctx)
		if !left.matches {
			return findEvalResult{matches: false, pruned: left.pruned}
		}
		right := evaluateFindExpr(e.right, ctx)
		return findEvalResult{
			matches: right.matches,
			pruned:  left.pruned || right.pruned,
			printed: left.printed || right.printed,
		}
	case *findOrExpr:
		left := evaluateFindExpr(e.left, ctx)
		if left.matches {
			return left
		}
		right := evaluateFindExpr(e.right, ctx)
		return findEvalResult{
			matches: right.matches,
			pruned:  left.pruned || right.pruned,
			printed: right.printed,
		}
	default:
		return findEvalResult{}
	}
}

func findExprNeedsEmptyCheck(expr findExpr) bool {
	switch e := expr.(type) {
	case nil:
		return false
	case *findEmptyExpr:
		return true
	case *findNotExpr:
		return findExprNeedsEmptyCheck(e.expr)
	case *findAndExpr:
		return findExprNeedsEmptyCheck(e.left) || findExprNeedsEmptyCheck(e.right)
	case *findOrExpr:
		return findExprNeedsEmptyCheck(e.left) || findExprNeedsEmptyCheck(e.right)
	default:
		return false
	}
}

func findGlobMatch(value, pattern string, ignoreCase bool) bool {
	if strings.Contains(value, "/") || strings.Contains(pattern, "/") {
		return findPathGlobMatch(value, pattern, ignoreCase)
	}

	targetValue := value
	targetPattern := pattern
	if ignoreCase {
		targetValue = strings.ToLower(targetValue)
		targetPattern = strings.ToLower(targetPattern)
	}
	matched, err := path.Match(targetPattern, targetValue)
	return err == nil && matched
}

func findPathGlobMatch(value, pattern string, ignoreCase bool) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")

	expr := b.String()
	if ignoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func findSizeMatch(size int64, expr *findSizeExpr) bool {
	targetBytes := expr.value
	switch expr.unit {
	case 'c':
		targetBytes = expr.value
	case 'k':
		targetBytes = expr.value * 1024
	case 'M':
		targetBytes = expr.value * 1024 * 1024
	case 'G':
		targetBytes = expr.value * 1024 * 1024 * 1024
	case 'b':
		targetBytes = expr.value * 512
	}

	switch expr.comparison {
	case findCompareMore:
		return size > targetBytes
	case findCompareLess:
		return size < targetBytes
	default:
		if expr.unit == 'b' {
			blocks := (size + 511) / 512
			return blocks == expr.value
		}
		return size == targetBytes
	}
}
