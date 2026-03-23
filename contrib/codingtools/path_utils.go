package codingtools

import (
	"context"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
	"golang.org/x/text/unicode/norm"
)

const narrowNoBreakSpace = "\u202F"

func normalizeUnicodeSpaces(str string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\u00A0', r == '\u202F', r == '\u205F', r == '\u3000':
			return ' '
		case r >= '\u2000' && r <= '\u200A':
			return ' '
		default:
			return r
		}
	}, str)
}

func normalizeAtPrefix(filePath string) string {
	if strings.HasPrefix(filePath, "@") {
		return filePath[1:]
	}
	return filePath
}

func expandPath(filePath, homeDir string) string {
	normalized := normalizeUnicodeSpaces(normalizeAtPrefix(filePath))
	switch {
	case normalized == "~":
		return homeDir
	case strings.HasPrefix(normalized, "~/"):
		return homeDir + normalized[1:]
	default:
		return normalized
	}
}

func resolveToCwd(filePath, cwd, homeDir string) string {
	expanded := expandPath(filePath, homeDir)
	if strings.HasPrefix(expanded, "/") {
		return gbfs.Clean(expanded)
	}
	return gbfs.Resolve(cwd, expanded)
}

func tryMacOSScreenshotPath(filePath string) string {
	return strings.NewReplacer(" AM.", narrowNoBreakSpace+"AM.", " PM.", narrowNoBreakSpace+"PM.").Replace(filePath)
}

func tryNFDVariant(filePath string) string {
	return norm.NFD.String(filePath)
}

func tryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "\u2019")
}

func pathExists(ctx context.Context, fsys gbfs.FileSystem, filePath string) bool {
	if fsys == nil {
		return false
	}
	_, err := fsys.Stat(ctx, filePath)
	return err == nil
}

func resolveReadPath(ctx context.Context, fsys gbfs.FileSystem, filePath, cwd, homeDir string) string {
	resolved := resolveToCwd(filePath, cwd, homeDir)
	if pathExists(ctx, fsys, resolved) {
		return resolved
	}

	amPmVariant := tryMacOSScreenshotPath(resolved)
	if amPmVariant != resolved && pathExists(ctx, fsys, amPmVariant) {
		return amPmVariant
	}

	nfdVariant := tryNFDVariant(resolved)
	if nfdVariant != resolved && pathExists(ctx, fsys, nfdVariant) {
		return nfdVariant
	}

	curlyVariant := tryCurlyQuoteVariant(resolved)
	if curlyVariant != resolved && pathExists(ctx, fsys, curlyVariant) {
		return curlyVariant
	}

	nfdCurlyVariant := tryCurlyQuoteVariant(nfdVariant)
	if nfdCurlyVariant != resolved && pathExists(ctx, fsys, nfdCurlyVariant) {
		return nfdCurlyVariant
	}

	return resolved
}
