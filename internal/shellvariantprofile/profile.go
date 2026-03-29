package shellvariantprofile

import (
	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

type Profile struct {
	Variant              shellvariant.ShellVariant
	SyntaxLang           syntax.LangVariant
	UsesBashDiagnostics  bool
	LegacyBashCompat     bool
	DefaultPosixMode     bool
	DefaultBraceExpand   bool
	ExposesBashNamespace bool
}

func Resolve(variant shellvariant.ShellVariant) Profile {
	resolved := shellvariant.Normalize(variant)
	if !resolved.Resolved() {
		resolved = shellvariant.Bash
	}

	profile := Profile{
		Variant:            resolved,
		DefaultBraceExpand: true,
	}

	switch resolved {
	case shellvariant.SH:
		profile.SyntaxLang = syntax.LangPOSIX
		profile.DefaultPosixMode = true
		profile.DefaultBraceExpand = false
	case shellvariant.Mksh:
		profile.SyntaxLang = syntax.LangMirBSDKorn
	case shellvariant.Zsh:
		profile.SyntaxLang = syntax.LangZsh
	case shellvariant.Bats:
		profile.SyntaxLang = syntax.LangBats
		profile.UsesBashDiagnostics = true
		profile.ExposesBashNamespace = true
	case shellvariant.Bash:
		profile.SyntaxLang = syntax.LangBash
		profile.UsesBashDiagnostics = true
		profile.ExposesBashNamespace = true
	default:
		profile.SyntaxLang = syntax.LangBash
	}

	return profile
}

func (p Profile) SupportsBuiltin(name string) bool {
	switch name {
	case "caller", "compgen", "complete", "compopt", "mapfile", "readarray", "shopt":
		return p.Variant == shellvariant.Bash || p.Variant == shellvariant.Bats
	case "dirs", "pushd", "popd":
		return p.Variant == shellvariant.Bash || p.Variant == shellvariant.Zsh
	default:
		return true
	}
}

func (p Profile) ExposesBashSpecialVar(name string) bool {
	switch name {
	case "BASH_VERSION", "BASHOPTS", "BASH_EXECUTION_STRING", "BASH_SOURCE", "BASH_LINENO", "BASH_REMATCH", "BASHPID", "FUNCNAME":
		return p.ExposesBashNamespace
	default:
		return false
	}
}
