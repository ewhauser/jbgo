package commands

import (
	"context"
	"encoding/base32"
	"fmt"
	"strings"
)

type Base32 struct{}

func NewBase32() *Base32 {
	return &Base32{}
}

func (c *Base32) Name() string {
	return "base32"
}

func (c *Base32) Run(ctx context.Context, inv *Invocation) error {
	args := inv.Args
	decode := false
	ignoreGarbage := false
	wrap := 76

optionLoop:
	for len(args) > 0 {
		arg := args[0]
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}

		switch {
		case arg == "--":
			args = args[1:]
			break optionLoop
		case arg == "--help":
			_, _ = fmt.Fprintln(inv.Stdout, "usage: base32 [OPTION]... [FILE]")
			return nil
		case arg == "--version":
			_, _ = fmt.Fprintln(inv.Stdout, "base32 (jbgo)")
			return nil
		case arg == "--decode":
			decode = true
			args = args[1:]
		case arg == "--ignore-garbage":
			ignoreGarbage = true
			args = args[1:]
		case arg == "--wrap":
			if len(args) < 2 {
				return exitf(inv, 1, "base32: option requires an argument -- wrap")
			}
			value, err := parseBaseEncWrap(c.Name(), args[1], inv)
			if err != nil {
				return err
			}
			wrap = value
			args = args[2:]
		case strings.HasPrefix(arg, "--wrap="):
			value, err := parseBaseEncWrap(c.Name(), strings.TrimPrefix(arg, "--wrap="), inv)
			if err != nil {
				return err
			}
			wrap = value
			args = args[1:]
		default:
			consumed, err := parseBase32ShortOptions(arg, args, &decode, &ignoreGarbage, &wrap, inv)
			if err != nil {
				return err
			}
			args = args[consumed:]
		}
	}

	data, err := readSingleBaseEncInput(ctx, inv, c.Name(), args)
	if err != nil {
		return err
	}

	if decode {
		decoded, err := decodeBase32Data(data, ignoreGarbage)
		if err != nil {
			return exitf(inv, 1, "base32: invalid input")
		}
		if _, err := inv.Stdout.Write(decoded); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	encoded := base32.StdEncoding.EncodeToString(data)
	if err := writeBaseEncOutput(inv.Stdout, encoded, wrap); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func parseBase32ShortOptions(arg string, args []string, decode, ignoreGarbage *bool, wrap *int, inv *Invocation) (int, error) {
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'd':
			*decode = true
		case 'i':
			*ignoreGarbage = true
		case 'w':
			value := arg[i+1:]
			if value == "" {
				if len(args) < 2 {
					return 0, exitf(inv, 1, "base32: option requires an argument -- w")
				}
				parsed, err := parseBaseEncWrap("base32", args[1], inv)
				if err != nil {
					return 0, err
				}
				*wrap = parsed
				return 2, nil
			}
			parsed, err := parseBaseEncWrap("base32", value, inv)
			if err != nil {
				return 0, err
			}
			*wrap = parsed
			return 1, nil
		default:
			return 0, exitf(inv, 1, "base32: unsupported flag -%c", arg[i])
		}
	}
	return 1, nil
}

func decodeBase32Data(data []byte, ignoreGarbage bool) ([]byte, error) {
	normalized := normalizeBase32Input(string(data), ignoreGarbage)
	if rem := len(normalized) % 8; rem != 0 {
		normalized += strings.Repeat("=", 8-rem)
	}
	return base32.StdEncoding.DecodeString(normalized)
}

func normalizeBase32Input(input string, ignoreGarbage bool) string {
	var b strings.Builder
	for _, r := range input {
		switch {
		case r == ' ' || r == '\n' || r == '\r' || r == '\t':
			continue
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case (r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7') || r == '=':
			b.WriteRune(r)
		case ignoreGarbage:
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

var _ Command = (*Base32)(nil)
