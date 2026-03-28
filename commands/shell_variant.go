package commands

import "github.com/ewhauser/gbash/shellvariant"

type ShellVariant = shellvariant.ShellVariant

const (
	ShellVariantAuto = shellvariant.Auto
	ShellVariantBash = shellvariant.Bash
	ShellVariantSH   = shellvariant.SH
	ShellVariantMksh = shellvariant.Mksh
	ShellVariantZsh  = shellvariant.Zsh
	ShellVariantBats = shellvariant.Bats
)
