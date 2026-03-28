package interp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func normalizeShellVariant(variant shellvariant.ShellVariant) shellvariant.ShellVariant {
	switch normalized := shellvariant.Normalize(variant); {
	case normalized.Resolved():
		return normalized
	default:
		return shellvariant.Bash
	}
}

func (r *Runner) applyShellVariant(variant shellvariant.ShellVariant) {
	if r == nil {
		return
	}
	r.shellVariant = normalizeShellVariant(variant)
	r.langVariant = r.shellVariant.SyntaxLang()
	if r.shellVariant == shellvariant.SH {
		r.opts[optPosix] = true
		r.opts[optBraceExpand] = false
	}
}

func (r *Runner) parserLangVariant() syntax.LangVariant {
	if r == nil || r.langVariant == 0 || r.langVariant == syntax.LangAuto {
		return syntax.LangBash
	}
	return r.langVariant
}

func (r *Runner) shellVariantName() shellvariant.ShellVariant {
	if r == nil {
		return shellvariant.Bash
	}
	return normalizeShellVariant(r.shellVariant)
}

func (r *Runner) quoteForVariant(value string) (string, error) {
	return syntax.Quote(value, r.parserLangVariant())
}

func (r *Runner) parserForVariant(opts ...syntax.ParserOption) *syntax.Parser {
	base := append([]syntax.ParserOption{syntax.Variant(r.parserLangVariant())}, opts...)
	return syntax.NewParser(base...)
}

func (r *Runner) parseVarRef(src string) (*syntax.VarRef, error) {
	return r.parserForVariant().VarRef(strings.NewReader(src))
}

func formatParseError(err error, variant shellvariant.ShellVariant) string {
	var parseErr syntax.ParseError
	if err == nil || !errors.As(err, &parseErr) {
		return fmt.Sprint(err)
	}
	if shellvariant.Normalize(variant).UsesBashDiagnostics() {
		return parseErr.BashError()
	}
	return parseErr.Error()
}
