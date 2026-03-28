package shellvariant

import (
	"path"
	"strings"

	"github.com/ewhauser/gbash/shell/syntax"
)

type ShellVariant string

const (
	Auto ShellVariant = "auto"
	Bash ShellVariant = "bash"
	SH   ShellVariant = "sh"
	Mksh ShellVariant = "mksh"
	Zsh  ShellVariant = "zsh"
	Bats ShellVariant = "bats"
)

func Normalize(variant ShellVariant) ShellVariant {
	switch strings.ToLower(strings.TrimSpace(string(variant))) {
	case "", string(Auto):
		return Auto
	case string(Bash):
		return Bash
	case string(SH):
		return SH
	case string(Mksh):
		return Mksh
	case string(Zsh):
		return Zsh
	case string(Bats):
		return Bats
	default:
		return ShellVariant(strings.ToLower(strings.TrimSpace(string(variant))))
	}
}

func (variant ShellVariant) Normalize() ShellVariant {
	return Normalize(variant)
}

func (variant ShellVariant) Valid() bool {
	switch Normalize(variant) {
	case Auto, Bash, SH, Mksh, Zsh, Bats:
		return true
	default:
		return false
	}
}

func (variant ShellVariant) Resolved() bool {
	switch Normalize(variant) {
	case Bash, SH, Mksh, Zsh, Bats:
		return true
	default:
		return false
	}
}

func (variant ShellVariant) UsesBashDiagnostics() bool {
	switch Normalize(variant) {
	case Bash, Bats:
		return true
	default:
		return false
	}
}

func (variant ShellVariant) UsesLegacyBashCompat() bool {
	return Normalize(variant) == Bash
}

func (variant ShellVariant) SyntaxLang() syntax.LangVariant {
	switch Normalize(variant) {
	case SH:
		return syntax.LangPOSIX
	case Mksh:
		return syntax.LangMirBSDKorn
	case Zsh:
		return syntax.LangZsh
	case Bats:
		return syntax.LangBats
	case Bash, Auto:
		return syntax.LangBash
	default:
		return syntax.LangBash
	}
}

func FromInterpreter(name string) ShellVariant {
	switch Normalize(ShellVariant(path.Base(strings.TrimSpace(name)))) {
	case Bash, SH, Mksh, Zsh, Bats:
		return Normalize(ShellVariant(path.Base(strings.TrimSpace(name))))
	default:
		return Auto
	}
}

func FromPath(name string) ShellVariant {
	base := strings.ToLower(path.Base(strings.TrimSpace(name)))
	switch {
	case strings.HasSuffix(base, ".bash"):
		return Bash
	case strings.HasSuffix(base, ".sh"):
		return SH
	case strings.HasSuffix(base, ".mksh"):
		return Mksh
	case strings.HasSuffix(base, ".zsh"):
		return Zsh
	case strings.HasSuffix(base, ".bats"):
		return Bats
	default:
		return Auto
	}
}
