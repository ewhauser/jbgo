package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

type Tty struct{}

func NewTty() *Tty {
	return &Tty{}
}

func (c *Tty) Name() string {
	return "tty"
}

func (c *Tty) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Tty) Spec() CommandSpec {
	return CommandSpec{
		Name:    c.Name(),
		About:   "Print the file name of the terminal connected to standard input.",
		Usage:   "tty [OPTION]...",
		Options: ttyOptionSpecs(),
		Parse: ParseConfig{
			InferLongOptions:      true,
			GroupShortOptions:     true,
			LongOptionValueEquals: true,
		},
		HelpRenderer:    renderStaticHelp(ttyHelpText),
		VersionRenderer: renderStaticVersion(ttyVersionText),
	}
}

func (c *Tty) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return ttyWriteText(inv, ttyHelpText)
	}
	if matches.Has("version") {
		return ttyWriteText(inv, ttyVersionText)
	}

	if positionals := matches.Positionals(); len(positionals) > 0 {
		return ttyUnexpectedArgument(inv, positionals[0])
	}

	ttyPath, isTTY := ttyTerminalPath(inv)
	if matches.Has("silent") {
		if isTTY {
			return nil
		}
		return &ExitError{Code: 1}
	}

	if isTTY {
		return ttyWriteLine(inv, ttyPath)
	}

	if err := ttyWriteLine(inv, "not a tty"); err != nil {
		return err
	}
	return &ExitError{Code: 1}
}

func (c *Tty) NormalizeParseError(inv *Invocation, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	if inv != nil && inv.Stderr != nil {
		_, _ = io.WriteString(inv.Stderr, ttyNormalizeParseError(err.Error()))
	}
	return &ExitError{Code: 2}
}

func ttyOptionSpecs() []OptionSpec {
	return []OptionSpec{
		{Name: "silent", Short: 's', Long: "silent", Aliases: []string{"quiet"}, Help: "print nothing, only return an exit status"},
		{Name: "help", Short: 'h', Long: "help", Help: "Print help"},
		{Name: "version", Short: 'V', Long: "version", Help: "Print version"},
	}
}

func ttyWriteText(inv *Invocation, text string) error {
	if inv == nil || inv.Stdout == nil {
		return nil
	}
	if _, err := io.WriteString(inv.Stdout, text); err != nil {
		return &ExitError{Code: 3, Err: err}
	}
	if flusher, ok := inv.Stdout.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return &ExitError{Code: 3, Err: err}
		}
	}
	return nil
}

func ttyWriteLine(inv *Invocation, text string) error {
	return ttyWriteText(inv, text+"\n")
}

func ttyUnexpectedArgument(inv *Invocation, arg string) error {
	if inv != nil && inv.Stderr != nil {
		_, _ = io.WriteString(inv.Stderr, ttyUnexpectedArgumentMessage(arg))
	}
	return &ExitError{Code: 2}
}

func ttyNormalizeParseError(message string) string {
	trimmed := strings.TrimSpace(strings.TrimSuffix(message, "\nTry 'tty --help' for more information."))

	switch {
	case strings.HasPrefix(trimmed, "tty: unrecognized option '"):
		if arg, ok := ttyTrimQuoted(trimmed, "tty: unrecognized option '", "'"); ok {
			return ttyUnexpectedArgumentMessage(arg)
		}
	case strings.HasPrefix(trimmed, "tty: invalid option -- '"):
		if arg, ok := ttyTrimQuoted(trimmed, "tty: invalid option -- '", "'"); ok {
			return ttyUnexpectedArgumentMessage("-" + arg)
		}
	case strings.HasPrefix(trimmed, "tty: option '") && strings.HasSuffix(trimmed, "' doesn't allow an argument"):
		if arg, ok := ttyTrimQuoted(trimmed, "tty: option '", "' doesn't allow an argument"); ok {
			return ttyUnexpectedArgumentMessage(arg)
		}
	}

	return fmt.Sprintf("error: %s\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n", strings.TrimPrefix(trimmed, "tty: "))
}

func ttyTrimQuoted(message, prefix, suffix string) (string, bool) {
	value, ok := strings.CutPrefix(message, prefix)
	if !ok {
		return "", false
	}
	value, ok = strings.CutSuffix(value, suffix)
	return value, ok
}

func ttyUnexpectedArgumentMessage(arg string) string {
	return fmt.Sprintf("error: unexpected argument %s found\n\nUsage: tty [OPTION]...\n\nFor more information, try '--help'.\n", ttyQuote(arg))
}

func ttyQuote(value string) string {
	return "'" + strings.ReplaceAll(value, `'`, `'\''`) + "'"
}

func ttyTerminalPath(inv *Invocation) (string, bool) {
	if inv == nil || inv.Stdin == nil {
		return ttyEnvPath(inv)
	}

	if meta, ok := inv.Stdin.(RedirectMetadata); ok {
		if ttyPath, ok := ttyRecognizedPath(meta.RedirectPath()); ok {
			return ttyPath, true
		}
	}
	return ttyEnvPath(inv)
}

func ttyEnvPath(inv *Invocation) (string, bool) {
	if inv == nil || inv.Env == nil {
		return "", false
	}

	ttyValue := strings.TrimSpace(inv.Env["TTY"])
	if ttyValue == "" {
		return "", false
	}
	if !strings.HasPrefix(ttyValue, "/") {
		ttyValue = "/dev/" + strings.TrimLeft(ttyValue, "/")
	}
	return ttyRecognizedPath(ttyValue)
}

// The sandbox filesystem does not model character-device metadata, so virtual
// tty detection relies on canonical terminal path patterns used by redirections
// like `/dev/pts/0` and `/dev/tty1`.
func ttyRecognizedPath(name string) (string, bool) {
	cleaned := path.Clean(strings.TrimSpace(name))
	switch {
	case cleaned == "/dev/tty", cleaned == "/dev/console":
		return cleaned, true
	case path.Dir(cleaned) == "/dev" && strings.HasPrefix(path.Base(cleaned), "tty"):
		return cleaned, true
	case path.Dir(cleaned) == "/dev/pts":
		base := path.Base(cleaned)
		if base != "" && base != "." && base != ".." {
			return cleaned, true
		}
	}
	return "", false
}

const ttyHelpText = `Print the file name of the terminal connected to standard input.

Usage: tty [OPTION]...

Options:
  -s, --silent   print nothing, only return an exit status [aliases: --quiet]
  -h, --help     Print help
  -V, --version  Print version
`

const ttyVersionText = "tty (uutils coreutils) 0.7.0\n"

var _ Command = (*Tty)(nil)
var _ SpecProvider = (*Tty)(nil)
var _ ParsedRunner = (*Tty)(nil)
var _ ParseErrorNormalizer = (*Tty)(nil)
