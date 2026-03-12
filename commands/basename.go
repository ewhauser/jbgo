package commands

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
)

type Basename struct{}

func NewBasename() *Basename {
	return &Basename{}
}

func (c *Basename) Name() string {
	return "basename"
}

func (c *Basename) Run(_ context.Context, inv *Invocation) error {
	args := append([]string(nil), inv.Args...)
	multiple := false
	suffix := ""
	terminator := "\n"

	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			args = args[1:]
			goto operands
		case arg == "--multiple":
			multiple = true
			args = args[1:]
		case arg == "--zero":
			terminator = "\x00"
			args = args[1:]
		case arg == "--help":
			_, _ = io.WriteString(inv.Stdout, basenameHelpText)
			return nil
		case arg == "--version":
			_, _ = io.WriteString(inv.Stdout, basenameVersionText)
			return nil
		case arg == "--suffix":
			if len(args) < 2 {
				return exitf(inv, 1, "basename: option requires an argument -- suffix")
			}
			suffix = args[1]
			multiple = true
			args = args[2:]
		case strings.HasPrefix(arg, "--suffix="):
			suffix = strings.TrimPrefix(arg, "--suffix=")
			multiple = true
			args = args[1:]
		case arg == "-":
			goto operands
		case strings.HasPrefix(arg, "-"):
			rest := arg[1:]
			if rest == "" {
				goto operands
			}
			args = args[1:]
			for rest != "" {
				switch rest[0] {
				case 'a':
					multiple = true
					rest = rest[1:]
				case 'z':
					terminator = "\x00"
					rest = rest[1:]
				case 's':
					multiple = true
					if len(rest) > 1 {
						suffix = rest[1:]
						rest = ""
						break
					}
					if len(args) == 0 {
						return exitf(inv, 1, "basename: option requires an argument -- s")
					}
					suffix = args[0]
					args = args[1:]
					rest = ""
				default:
					return exitf(inv, 1, "basename: unsupported flag -%c", rest[0])
				}
			}
		default:
			goto operands
		}
	}

operands:
	if len(args) == 0 {
		return exitf(inv, 1, "basename: missing operand\nTry 'basename --help' for more information.")
	}
	if !multiple && len(args) > 2 {
		return exitf(inv, 1, "basename: extra operand %s\nTry 'basename --help' for more information.", quoteGNUOperand(args[2]))
	}
	if !multiple && len(args) == 2 && suffix == "" {
		suffix = args[1]
		args = args[:1]
	}

	for _, operand := range args {
		base := basename(operand)
		if shouldStripBasenameSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
		if _, err := fmt.Fprint(inv.Stdout, base, terminator); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func basename(name string) string {
	if name == "" {
		return ""
	}
	cleaned := strings.TrimRight(name, "/")
	if cleaned == "" {
		return "/"
	}
	return path.Base(cleaned)
}

func shouldStripBasenameSuffix(base, suffix string) bool {
	return suffix != "" && base != suffix && strings.HasSuffix(base, suffix)
}

func quoteGNUOperand(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

const basenameHelpText = `Usage: basename NAME [SUFFIX]
  or:  basename OPTION... NAME...
Print NAME with any leading directory components removed.
`

const basenameVersionText = "basename (gbash) dev\n"

var _ Command = (*Basename)(nil)
