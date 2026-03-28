package shell

import (
	"fmt"
	"strings"

	"github.com/ewhauser/gbash/shellvariant"
)

func resolveExecutionVariant(exec *Execution) error {
	if exec == nil {
		return nil
	}
	if exec.activeShellVariant.Resolved() && exec.activeLangVariant != 0 {
		return nil
	}
	variant, err := effectiveExecutionVariant(exec)
	if err != nil {
		return err
	}
	exec.activeShellVariant = variant
	exec.activeLangVariant = variant.SyntaxLang()
	return nil
}

func effectiveExecutionVariant(exec *Execution) (shellvariant.ShellVariant, error) {
	requested, err := normalizeRequestedShellVariant(shellvariant.Auto)
	if err != nil {
		return "", err
	}
	if exec != nil {
		requested, err = normalizeRequestedShellVariant(exec.ShellVariant)
		if err != nil {
			return "", err
		}
	}
	if requested != shellvariant.Auto {
		return requested, nil
	}
	if exec != nil {
		if variant := shellvariant.FromInterpreter(exec.Interpreter); variant.Resolved() {
			return variant, nil
		}
		if variant := shellVariantFromScript(exec); variant.Resolved() {
			return variant, nil
		}
	}
	return shellvariant.Bash, nil
}

func normalizeRequestedShellVariant(variant shellvariant.ShellVariant) (shellvariant.ShellVariant, error) {
	normalized := shellvariant.Normalize(variant)
	if !normalized.Valid() {
		return "", fmt.Errorf("invalid shell variant %q", variant)
	}
	return normalized, nil
}

func shellVariantFromScript(exec *Execution) shellvariant.ShellVariant {
	if exec == nil {
		return shellvariant.Auto
	}
	if variant := shellVariantFromScriptText(exec.Script); variant.Resolved() {
		return variant
	}
	if variant := nonPosixPathShellVariant(exec.ScriptPath); variant.Resolved() {
		return variant
	}
	return shellvariant.Auto
}

func nonPosixPathShellVariant(name string) shellvariant.ShellVariant {
	switch variant := shellvariant.FromPath(name); variant {
	case shellvariant.Bash, shellvariant.Mksh, shellvariant.Zsh, shellvariant.Bats:
		return variant
	default:
		return shellvariant.Auto
	}
}

func shellVariantFromScriptText(script string) shellvariant.ShellVariant {
	if !strings.HasPrefix(script, "#!") {
		return shellvariant.Auto
	}
	line := script
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	_, name, _, ok := parseShebangInterpreter(strings.TrimSpace(strings.TrimPrefix(line, "#!")))
	if !ok {
		return shellvariant.Auto
	}
	return shellvariant.FromInterpreter(name)
}

func executionShellVariant(exec *Execution) shellvariant.ShellVariant {
	if exec == nil {
		return shellvariant.Bash
	}
	if exec.activeShellVariant.Resolved() {
		return exec.activeShellVariant
	}
	variant, err := effectiveExecutionVariant(exec)
	if err != nil {
		return shellvariant.Bash
	}
	return variant
}

func defaultScriptInterpreter(exec *Execution) string {
	switch executionShellVariant(exec) {
	case shellvariant.SH:
		return "sh"
	case shellvariant.Mksh:
		return "mksh"
	case shellvariant.Zsh:
		return "zsh"
	case shellvariant.Bats:
		return "bats"
	default:
		return "bash"
	}
}
