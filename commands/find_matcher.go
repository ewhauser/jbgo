package commands

import (
	"context"
	"math"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/ewhauser/jbgo/policy"
)

func resolveFindExpr(ctx context.Context, inv *Invocation, expr findExpr) error {
	switch e := expr.(type) {
	case nil:
		return nil
	case *findNewerExpr:
		info, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, e.refPath)
		if err != nil {
			return err
		}
		e.referenceReady = true
		e.referenceFound = exists
		if exists {
			e.resolvedTime = info.ModTime()
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

func evaluateFindExpr(expr findExpr, ctx *findEvalContext) bool {
	switch e := expr.(type) {
	case nil:
		return true
	case *findNameExpr:
		return findGlobMatch(ctx.name, e.pattern, e.ignoreCase)
	case *findPathExpr:
		return findGlobMatch(ctx.displayPath, e.pattern, e.ignoreCase)
	case *findRegexExpr:
		return e.regex.MatchString(ctx.displayPath)
	case *findTypeExpr:
		if e.fileType == 'f' {
			return !ctx.isDir
		}
		if e.fileType == 'd' {
			return ctx.isDir
		}
		return false
	case *findEmptyExpr:
		return ctx.isEmpty
	case *findMTimeExpr:
		ageDays := time.Since(ctx.mtime).Hours() / 24
		switch e.comparison {
		case findCompareMore:
			return ageDays > float64(e.days)
		case findCompareLess:
			return ageDays < float64(e.days)
		default:
			return int(math.Floor(ageDays)) == e.days
		}
	case *findNewerExpr:
		return e.referenceReady && e.referenceFound && ctx.mtime.After(e.resolvedTime)
	case *findSizeExpr:
		return findSizeMatch(ctx.size, e)
	case *findNotExpr:
		return !evaluateFindExpr(e.expr, ctx)
	case *findAndExpr:
		return evaluateFindExpr(e.left, ctx) && evaluateFindExpr(e.right, ctx)
	case *findOrExpr:
		return evaluateFindExpr(e.left, ctx) || evaluateFindExpr(e.right, ctx)
	case *findTrueExpr:
		return true
	default:
		return false
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
