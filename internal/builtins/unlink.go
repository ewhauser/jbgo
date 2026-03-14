package builtins

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/policy"
)

type Unlink struct{}

func NewUnlink() *Unlink {
	return &Unlink{}
}

func (c *Unlink) Name() string {
	return "unlink"
}

func (c *Unlink) Run(ctx context.Context, inv *Invocation) error {
	file, action, err := parseUnlinkArgs(inv, inv.Args)
	if err != nil {
		return err
	}

	switch action {
	case unlinkActionHelp:
		_, err := io.WriteString(inv.Stdout, unlinkHelpText)
		return err
	case unlinkActionVersion:
		_, err := io.WriteString(inv.Stdout, unlinkVersionText)
		return err
	}

	info, abs, err := lstatPath(ctx, inv, file)
	if err != nil {
		if policy.IsDenied(err) {
			return err
		}
		return exitf(inv, 1, "unlink: cannot unlink %s: %s", quoteGNUOperand(file), unlinkErrorText(err))
	}
	if info.IsDir() {
		return exitf(inv, 1, "unlink: cannot unlink %s: Is a directory", quoteGNUOperand(file))
	}
	if err := inv.FS.Remove(ctx, abs, false); err != nil {
		if policy.IsDenied(err) {
			return err
		}
		return exitf(inv, 1, "unlink: cannot unlink %s: %s", quoteGNUOperand(file), unlinkErrorText(err))
	}
	return nil
}

type unlinkAction int

const (
	unlinkActionRun unlinkAction = iota
	unlinkActionHelp
	unlinkActionVersion
)

func parseUnlinkArgs(inv *Invocation, args []string) (string, unlinkAction, error) {
	operands := make([]string, 0, 1)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case arg == "-h":
			return "", unlinkActionHelp, nil
		case arg == "-V":
			return "", unlinkActionVersion, nil
		case strings.HasPrefix(arg, "--"):
			action, err := parseUnlinkLongOption(inv, arg)
			if err != nil {
				return "", unlinkActionRun, err
			}
			if action != unlinkActionRun {
				return "", action, nil
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			return "", unlinkActionRun, unlinkUnexpectedArgument(inv, arg)
		default:
			operands = append(operands, arg)
		}
	}

	switch len(operands) {
	case 0:
		return "", unlinkActionRun, unlinkMissingOperand(inv)
	case 1:
		return operands[0], unlinkActionRun, nil
	default:
		return "", unlinkActionRun, unlinkUnexpectedArgument(inv, operands[1])
	}
}

func parseUnlinkLongOption(inv *Invocation, raw string) (unlinkAction, error) {
	name := strings.TrimPrefix(raw, "--")
	value, hasValue := "", false
	if strings.Contains(name, "=") {
		var canonical string
		name, value, _ = strings.Cut(name, "=")
		hasValue = true
		if action, matchedName, ok := matchUnlinkLongOption(name); ok {
			canonical = matchedName
			if hasValue {
				return unlinkActionRun, unlinkUnexpectedValue(inv, canonical, value)
			}
			return action, nil
		}
		return unlinkActionRun, unlinkUnexpectedArgument(inv, raw)
	}

	action, _, ok := matchUnlinkLongOption(name)
	if !ok {
		return unlinkActionRun, unlinkUnexpectedArgument(inv, raw)
	}
	return action, nil
}

func matchUnlinkLongOption(name string) (unlinkAction, string, bool) {
	switch {
	case unlinkMatchesLongName("help", name):
		return unlinkActionHelp, "help", true
	case unlinkMatchesLongName("version", name):
		return unlinkActionVersion, "version", true
	default:
		return unlinkActionRun, "", false
	}
}

func unlinkMatchesLongName(option, prefix string) bool {
	return prefix != "" && strings.HasPrefix(option, prefix)
}

func unlinkMissingOperand(inv *Invocation) error {
	return exitf(inv, 1, "error: the following required arguments were not provided:\n  <FILE>\n\n%s", unlinkUsageText)
}

func unlinkUnexpectedArgument(inv *Invocation, arg string) error {
	var b strings.Builder
	b.WriteString("error: unexpected argument ")
	b.WriteString(quoteGNUOperand(arg))
	b.WriteString(" found\n\n")
	if strings.HasPrefix(arg, "-") {
		b.WriteString("  tip: to pass ")
		b.WriteString(quoteGNUOperand(arg))
		b.WriteString(" as a value, use '-- ")
		b.WriteString(arg)
		b.WriteString("'\n\n")
	}
	b.WriteString(unlinkUsageText)
	return exitf(inv, 1, "%s", b.String())
}

func unlinkUnexpectedValue(inv *Invocation, option, value string) error {
	return exitf(inv, 1, "error: unexpected value %s for '--%s' found; no more were expected\n\n%s", quoteGNUOperand(value), option, unlinkUsageText)
}

func unlinkErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return unlinkErrorText(pathErr.Err)
	}

	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, syscall.ENOTDIR):
		return "Not a directory"
	case errors.Is(err, syscall.EISDIR):
		return "Is a directory"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	default:
		lower := strings.ToLower(err.Error())
		switch {
		case strings.Contains(lower, "is a directory"):
			return "Is a directory"
		case strings.Contains(lower, "not a directory"):
			return "Not a directory"
		default:
			return err.Error()
		}
	}
}

const unlinkUsageText = `Usage: unlink FILE
       unlink OPTION

For more information, try '--help'.`

const unlinkHelpText = `Unlink the file at FILE.

Usage: unlink FILE
       unlink OPTION

Options:
  -h, --help     Print help
  -V, --version  Print version
`

const unlinkVersionText = "unlink (gbash) dev\n"

var _ Command = (*Unlink)(nil)
