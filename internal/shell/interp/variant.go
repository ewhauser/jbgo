package interp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/internal/shellvariantprofile"
	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func normalizeShellVariant(variant shellvariant.ShellVariant) shellvariant.ShellVariant {
	return shellvariantprofile.Resolve(variant).Variant
}

func normalizeLangVariant(lang syntax.LangVariant) syntax.LangVariant {
	if lang == 0 || lang == syntax.LangAuto {
		return syntax.LangBash
	}
	return lang
}

func (r *Runner) applyShellVariant(variant shellvariant.ShellVariant) {
	if r == nil {
		return
	}
	profile := shellvariantprofile.Resolve(variant)
	r.shellVariant = profile.Variant
	r.langVariant = profile.SyntaxLang
	r.opts[optPosix] = profile.DefaultPosixMode
	r.opts[optBraceExpand] = profile.DefaultBraceExpand
}

func (r *Runner) parserLangVariant() syntax.LangVariant {
	if r == nil {
		return syntax.LangBash
	}
	if r.langVariant != 0 && r.langVariant != syntax.LangAuto {
		return r.langVariant
	}
	return r.shellProfile().SyntaxLang
}

func (r *Runner) shellVariantName() shellvariant.ShellVariant {
	if r == nil {
		return shellvariant.Bash
	}
	return normalizeShellVariant(r.shellVariant)
}

func (r *Runner) shellProfile() shellvariantprofile.Profile {
	if r == nil {
		return shellvariantprofile.Resolve(shellvariant.Bash)
	}
	return shellvariantprofile.Resolve(r.shellVariant)
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
	if shellvariantprofile.Resolve(variant).UsesBashDiagnostics {
		return parseErr.BashError()
	}
	return parseErr.Error()
}
