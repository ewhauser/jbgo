package commands

import (
	"context"
	"fmt"
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
	args := inv.Args
	multiple := false
	suffix := ""

	for len(args) > 0 {
		switch {
		case args[0] == "-a" || args[0] == "--multiple":
			multiple = true
			args = args[1:]
		case args[0] == "-s":
			if len(args) < 2 {
				return exitf(inv, 1, "basename: option requires an argument -- s")
			}
			suffix = args[1]
			multiple = true
			args = args[2:]
		case strings.HasPrefix(args[0], "--suffix="):
			suffix = strings.TrimPrefix(args[0], "--suffix=")
			multiple = true
			args = args[1:]
		case args[0] == "--":
			args = args[1:]
			goto operands
		case strings.HasPrefix(args[0], "-"):
			return exitf(inv, 1, "basename: unsupported flag %s", args[0])
		default:
			goto operands
		}
	}

operands:
	if len(args) == 0 {
		return exitf(inv, 1, "basename: missing operand")
	}
	if !multiple && len(args) > 2 {
		return exitf(inv, 1, "basename: extra operand %q", args[2])
	}
	if !multiple && len(args) == 2 && suffix == "" {
		suffix = args[1]
		args = args[:1]
	}

	for _, operand := range args {
		base := basename(operand)
		if suffix != "" && strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
		if _, err := fmt.Fprintln(inv.Stdout, base); err != nil {
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

var _ Command = (*Basename)(nil)
