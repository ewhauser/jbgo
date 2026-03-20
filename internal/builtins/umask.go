package builtins

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type Umask struct{}

func NewUmask() *Umask {
	return &Umask{}
}

func (c *Umask) Name() string {
	return "umask"
}

const umaskEnvKey = "GBASH_UMASK"

func (c *Umask) Run(_ context.Context, inv *Invocation) error {
	args := inv.Args

	if len(args) == 0 {
		return printUmaskOctal(inv)
	}

	arg := args[0]
	switch arg {
	case "-S":
		return printUmaskSymbolic(inv)
	case "-p":
		return printUmaskShell(inv)
	}
	if strings.HasPrefix(arg, "-") && arg != "-" {
		return exitf(inv, 2, "umask: %s: invalid option\numask: usage: umask [-p] [-S] [mode]", umaskInvalidOption(arg))
	}
	if looksLikeUmaskOctal(arg) {
		value, ok := parseUmaskOctal(arg)
		if !ok {
			return exitf(inv, 1, "umask: %s: octal number out of range", arg)
		}
		inv.Env[umaskEnvKey] = fmt.Sprintf("%04o", value)
		return nil
	}

	value, err := applyUmaskSymbolic(umaskValue(inv), arg)
	if err != nil {
		return exitf(inv, 1, "%s", err.Error())
	}
	inv.Env[umaskEnvKey] = fmt.Sprintf("%04o", value)
	return nil
}

func umaskValue(inv *Invocation) uint32 {
	if inv == nil {
		return 0o022
	}
	raw := strings.TrimSpace(inv.Env[umaskEnvKey])
	if raw == "" {
		return 0o022
	}
	value, err := strconv.ParseUint(raw, 8, 32)
	if err != nil || value > 0o777 {
		return 0o022
	}
	return uint32(value)
}

func printUmaskOctal(inv *Invocation) error {
	_, err := fmt.Fprintf(inv.Stdout, "%04o\n", umaskValue(inv))
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func printUmaskShell(inv *Invocation) error {
	_, err := fmt.Fprintf(inv.Stdout, "umask %04o\n", umaskValue(inv))
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func printUmaskSymbolic(inv *Invocation) error {
	mask := umaskAllowed(umaskValue(inv))
	_, err := fmt.Fprintf(inv.Stdout, "u=%s,g=%s,o=%s\n",
		umaskPermissionString((mask>>6)&0o7),
		umaskPermissionString((mask>>3)&0o7),
		umaskPermissionString(mask&0o7),
	)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func umaskPermissionString(bits uint32) string {
	var b strings.Builder
	if bits&0o4 != 0 {
		b.WriteByte('r')
	}
	if bits&0o2 != 0 {
		b.WriteByte('w')
	}
	if bits&0o1 != 0 {
		b.WriteByte('x')
	}
	return b.String()
}

func umaskInvalidOption(arg string) string {
	if len(arg) >= 2 {
		return arg[:2]
	}
	return arg
}

func looksLikeUmaskOctal(arg string) bool {
	if arg == "" {
		return false
	}
	ch := arg[0]
	return ch >= '0' && ch <= '9'
}

func parseUmaskOctal(arg string) (uint32, bool) {
	for _, ch := range arg {
		if ch < '0' || ch > '7' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(arg, 8, 32)
	if err != nil || value > 0o777 {
		return 0, false
	}
	return uint32(value), true
}

func applyUmaskSymbolic(mask uint32, spec string) (uint32, error) {
	base := umaskAllowed(mask)
	working := base
	if spec == "" {
		return 0, umaskInvalidModeOperator(0)
	}

	for idx := 0; idx < len(spec); {
		whoMask, next := parseUmaskWho(spec, idx)
		if whoMask == 0 {
			whoMask = 0o777
		}
		idx = next

		sawAction := false
		for idx < len(spec) && spec[idx] != ',' {
			op := spec[idx]
			if op != '+' && op != '-' && op != '=' {
				return 0, umaskInvalidModeOperator(op)
			}
			sawAction = true
			idx++

			start := idx
			for idx < len(spec) && spec[idx] != ',' && spec[idx] != '+' && spec[idx] != '-' && spec[idx] != '=' {
				idx++
			}

			permMask, err := umaskPermMask(base, whoMask, spec[start:idx])
			if err != nil {
				return 0, err
			}
			switch op {
			case '+':
				working |= permMask
			case '-':
				working &^= permMask
			case '=':
				working &^= whoMask
				working |= permMask
			}
		}
		if !sawAction {
			return 0, umaskInvalidModeOperator(umaskModeByte(spec, idx))
		}
		if idx == len(spec) {
			break
		}
		idx++
		if idx == len(spec) {
			return 0, umaskInvalidModeOperator(0)
		}
	}

	return umaskFromAllowed(working), nil
}

func parseUmaskWho(spec string, start int) (uint32, int) {
	mask := uint32(0)
	idx := start
	for idx < len(spec) {
		switch spec[idx] {
		case 'u':
			mask |= 0o700
		case 'g':
			mask |= 0o070
		case 'o':
			mask |= 0o007
		case 'a':
			mask |= 0o777
		default:
			return mask, idx
		}
		idx++
	}
	return mask, idx
}

func umaskPermMask(baseAllowed, whoMask uint32, perms string) (uint32, error) {
	mask := uint32(0)
	for i := 0; i < len(perms); i++ {
		switch perms[i] {
		case 'r':
			mask |= 0o444 & whoMask
		case 'w':
			mask |= 0o222 & whoMask
		case 'x':
			mask |= 0o111 & whoMask
		case 'X':
			if baseAllowed&0o111 != 0 {
				mask |= 0o111 & whoMask
			}
		case 's', 't':
			continue
		case 'u', 'g', 'o':
			if len(perms) != 1 {
				return 0, umaskInvalidModeCharacter(perms[i])
			}
			mask |= umaskCopyMask(baseAllowed, perms[i], whoMask)
		default:
			return 0, umaskInvalidModeCharacter(perms[i])
		}
	}
	return mask, nil
}

func umaskCopyMask(baseAllowed uint32, source byte, whoMask uint32) uint32 {
	var src uint32
	switch source {
	case 'u':
		src = (baseAllowed & 0o700) >> 6
	case 'g':
		src = (baseAllowed & 0o070) >> 3
	case 'o':
		src = baseAllowed & 0o007
	}

	out := uint32(0)
	if whoMask&0o700 != 0 {
		out |= (src << 6) & 0o700
	}
	if whoMask&0o070 != 0 {
		out |= (src << 3) & 0o070
	}
	if whoMask&0o007 != 0 {
		out |= src & 0o007
	}
	return out
}

func umaskAllowed(mask uint32) uint32 {
	return (^mask) & 0o777
}

func umaskFromAllowed(allowed uint32) uint32 {
	return (^allowed) & 0o777
}

func umaskInvalidModeOperator(ch byte) error {
	return fmt.Errorf("umask: `%c': invalid symbolic mode operator", ch)
}

func umaskInvalidModeCharacter(ch byte) error {
	return fmt.Errorf("umask: `%c': invalid symbolic mode character", ch)
}

func umaskModeByte(spec string, idx int) byte {
	if idx < 0 || idx >= len(spec) {
		return 0
	}
	return spec[idx]
}

var _ Command = (*Umask)(nil)
