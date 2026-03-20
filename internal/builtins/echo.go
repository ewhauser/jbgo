package builtins

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ewhauser/gbash/internal/printfutil"
)

type Echo struct{}

type echoOptions struct {
	trailingNewline bool
	escape          bool
	posixlyCorrect  bool
}

func NewEcho() *Echo {
	return &Echo{}
}

func (c *Echo) Name() string {
	return "echo"
}

func (c *Echo) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Echo) Spec() CommandSpec {
	return CommandSpec{
		Name:  "echo",
		About: "Echo the STRING(s) to standard output.",
		Usage: "echo [SHORT-OPTION]... [STRING]...\n  or:  echo LONG-OPTION",
		Options: []OptionSpec{
			{Name: "no-newline", Short: 'n', Help: "do not output the trailing newline"},
			{Name: "enable-backslash-escape", Short: 'e', Help: "enable interpretation of backslash escapes"},
			{Name: "disable-backslash-escape", Short: 'E', Help: "disable interpretation of backslash escapes (default)"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "string", ValueName: "STRING", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions: true,
		},
		HelpRenderer:    renderStaticHelp(echoHelpText),
		VersionRenderer: renderStaticVersion(echoVersionText),
	}
}

func (c *Echo) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil {
		return nil
	}
	args, mode := normalizeEchoArgs(inv)
	clone := *inv
	clone.Args = args
	if mode == "help" {
		clone.Args = []string{"--help"}
	}
	if mode == "version" {
		clone.Args = []string{"--version"}
	}
	return &clone
}

func (c *Echo) RunParsed(_ context.Context, inv *Invocation, matches *ParsedCommand) error {
	args, opts := parseEchoMatches(inv, matches)
	if matches.Has("help") {
		return renderStaticHelp(echoHelpText)(inv.Stdout, c.Spec())
	}
	if matches.Has("version") {
		return renderStaticVersion(echoVersionText)(inv.Stdout, c.Spec())
	}

	stopped, err := writeEchoOutput(inv.Stdout, args, opts, echoUsesCLocale(inv))
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if stopped || !opts.trailingNewline {
		return nil
	}
	if _, err := io.WriteString(inv.Stdout, "\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func normalizeEchoArgs(inv *Invocation) (args []string, mode string) {
	if inv == nil {
		return nil, ""
	}
	args = append([]string(nil), inv.Args...)
	posixlyCorrect := false
	if inv.Env != nil {
		_, posixlyCorrect = inv.Env["POSIXLY_CORRECT"]
	}
	if !posixlyCorrect && len(args) == 1 {
		switch args[0] {
		case "--help":
			return nil, "help"
		case "--version":
			return nil, "version"
		}
	}

	normalized := make([]string, 0, len(args)+1)
	index := 0
	for index < len(args) && echoIsOption(args[index]) {
		for i := 1; i < len(args[index]); i++ {
			switch args[index][i] {
			case 'e':
				normalized = append(normalized, "-e")
			case 'E':
				normalized = append(normalized, "-E")
			case 'n':
				normalized = append(normalized, "-n")
			}
		}
		index++
	}
	normalized = append(normalized, "--")
	normalized = append(normalized, args[index:]...)
	return normalized, ""
}

func parseEchoMatches(inv *Invocation, matches *ParsedCommand) (args []string, opts echoOptions) {
	if matches != nil {
		args = matches.Args("string")
	}
	if inv != nil && inv.Env != nil {
		_, opts.posixlyCorrect = inv.Env["POSIXLY_CORRECT"]
	}
	opts.trailingNewline = true
	if matches == nil {
		return args, opts
	}
	for _, name := range matches.OptionOrder() {
		switch name {
		case "no-newline":
			opts.trailingNewline = false
		case "enable-backslash-escape":
			opts.escape = true
		case "disable-backslash-escape":
			opts.escape = false
		}
	}
	return args, opts
}

func echoIsOption(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'e', 'E', 'n':
		default:
			return false
		}
	}
	return true
}

func writeEchoOutput(w io.Writer, args []string, opts echoOptions, cLocale bool) (stopped bool, err error) {
	for i, arg := range args {
		if i > 0 {
			if _, err := io.WriteString(w, " "); err != nil {
				return false, err
			}
		}

		if !opts.escape {
			if _, err := io.WriteString(w, arg); err != nil {
				return false, err
			}
			continue
		}

		decoded, stop := decodeEchoEscapes(arg, cLocale)
		if len(decoded) > 0 {
			if _, err := w.Write(decoded); err != nil {
				return false, err
			}
		}
		if stop {
			return true, nil
		}
	}
	return false, nil
}

func decodeEchoEscapes(value string, cLocale bool) (decoded []byte, stop bool) {
	decoded = make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			decoded = append(decoded, value[i])
			continue
		}

		i++
		switch value[i] {
		case 'a':
			decoded = append(decoded, '\a')
		case 'b':
			decoded = append(decoded, '\b')
		case 'c':
			return decoded, true
		case 'e':
			decoded = append(decoded, '\x1b')
		case 'f':
			decoded = append(decoded, '\f')
		case 'n':
			decoded = append(decoded, '\n')
		case 'r':
			decoded = append(decoded, '\r')
		case 't':
			decoded = append(decoded, '\t')
		case 'v':
			decoded = append(decoded, '\v')
		case '\\':
			decoded = append(decoded, '\\')
		case 'x':
			hex, advance, ok := decodeEchoHex(value, i, 2)
			if !ok {
				decoded = append(decoded, '\\', 'x')
				continue
			}
			decoded = append(decoded, byte(hex))
			i = advance
		case 'u':
			hex, advance, ok := decodeEchoHex(value, i, 4)
			if !ok {
				decoded = append(decoded, '\\', 'u')
				continue
			}
			decoded = appendEchoUnicode(decoded, hex, cLocale)
			i = advance
		case 'U':
			hex, advance, ok := decodeEchoHex(value, i, 8)
			if !ok {
				decoded = append(decoded, '\\', 'U')
				continue
			}
			decoded = appendEchoUnicode(decoded, hex, cLocale)
			i = advance
		case '0':
			octal, advance := decodeEchoOctal(value, i)
			decoded = append(decoded, byte(octal))
			i = advance
		case '1', '2', '3', '4', '5', '6', '7':
			decoded = append(decoded, '\\', value[i])
		default:
			decoded = append(decoded, '\\', value[i])
		}
	}
	return decoded, false
}

func echoUsesCLocale(inv *Invocation) bool {
	if inv == nil || len(inv.Env) == 0 {
		return false
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		switch strings.TrimSpace(inv.Env[name]) {
		case "":
			continue
		case "C", "POSIX":
			return true
		default:
			return false
		}
	}
	return false
}

func decodeEchoHex(value string, index, maxDigits int) (decoded uint32, advance int, ok bool) {
	advance = index
	for count := 0; count < maxDigits && advance+1 < len(value) && printfutil.IsHexDigit(value[advance+1]); count++ {
		advance++
		decoded = decoded*16 + uint32(echoHexValue(value[advance]))
	}
	return decoded, advance, advance != index
}

func appendEchoUnicode(dst []byte, value uint32, cLocale bool) []byte {
	if value <= 0x7f {
		return append(dst, byte(value))
	}
	if cLocale {
		return appendEchoCanonicalUnicode(dst, value)
	}
	return appendEchoUTF8(dst, value)
}

func appendEchoCanonicalUnicode(dst []byte, value uint32) []byte {
	if value > 0x7fffffff {
		return dst
	}
	if value <= 0xffff {
		return append(dst, fmt.Sprintf("\\u%04X", value)...)
	}
	return append(dst, fmt.Sprintf("\\U%08X", value)...)
}

func appendEchoUTF8(dst []byte, value uint32) []byte {
	switch {
	case value <= 0x7f:
		return append(dst, byte(value))
	case value <= 0x7ff:
		return append(dst,
			0xc0|byte(value>>6),
			0x80|byte(value&0x3f),
		)
	case value <= 0xffff:
		return append(dst,
			0xe0|byte(value>>12),
			0x80|byte((value>>6)&0x3f),
			0x80|byte(value&0x3f),
		)
	case value <= 0x1fffff:
		return append(dst,
			0xf0|byte(value>>18),
			0x80|byte((value>>12)&0x3f),
			0x80|byte((value>>6)&0x3f),
			0x80|byte(value&0x3f),
		)
	case value <= 0x3ffffff:
		return append(dst,
			0xf8|byte(value>>24),
			0x80|byte((value>>18)&0x3f),
			0x80|byte((value>>12)&0x3f),
			0x80|byte((value>>6)&0x3f),
			0x80|byte(value&0x3f),
		)
	case value <= 0x7fffffff:
		return append(dst,
			0xfc|byte(value>>30),
			0x80|byte((value>>24)&0x3f),
			0x80|byte((value>>18)&0x3f),
			0x80|byte((value>>12)&0x3f),
			0x80|byte((value>>6)&0x3f),
			0x80|byte(value&0x3f),
		)
	default:
		return dst
	}
}

func decodeEchoOctal(value string, index int) (decoded, advance int) {
	advance = index
	if value[index] == '0' {
		if index+1 >= len(value) || !echoIsOctalDigit(value[index+1]) {
			return 0, advance
		}
		advance++
		decoded = int(value[advance] - '0')
	} else {
		decoded = int(value[index] - '0')
	}
	for count := 0; count < 2 && advance+1 < len(value) && echoIsOctalDigit(value[advance+1]); count++ {
		advance++
		decoded = decoded*8 + int(value[advance]-'0')
	}
	return decoded, advance
}

func echoIsOctalDigit(ch byte) bool {
	return ch >= '0' && ch <= '7'
}

func echoHexValue(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'f':
		return 10 + int(ch-'a')
	default:
		return 10 + int(ch-'A')
	}
}

const echoHelpText = `Usage: echo [SHORT-OPTION]... [STRING]...
  or:  echo LONG-OPTION
Echo the STRING(s) to standard output.

  -n     do not output the trailing newline
  -e     enable interpretation of backslash escapes
  -E     disable interpretation of backslash escapes (default)
      --help        display this help and exit
      --version     output version information and exit

If -e is in effect, the following sequences are recognized:
  \\      backslash
  \a      alert (BEL)
  \b      backspace
  \c      produce no further output
  \e      escape
  \f      form feed
  \n      new line
  \r      carriage return
  \t      horizontal tab
  \v      vertical tab
  \0NNN   byte with octal value NNN (1 to 3 digits)
  \xHH    byte with hexadecimal value HH (1 to 2 digits)
  \uHHHH  Unicode code point (1 to 4 hex digits)
  \UHHHHHHHH  Unicode code point (1 to 8 hex digits)
`

const echoVersionText = "echo (gbash) dev\n"

var _ Command = (*Echo)(nil)
var _ SpecProvider = (*Echo)(nil)
var _ ParsedRunner = (*Echo)(nil)
var _ ParseInvocationNormalizer = (*Echo)(nil)
