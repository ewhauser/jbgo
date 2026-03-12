package commands

import (
	"context"
	"io"
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

func (c *Echo) Run(_ context.Context, inv *Invocation) error {
	args, opts, mode := parseEchoArgs(inv)
	switch mode {
	case "help":
		_, _ = io.WriteString(inv.Stdout, echoHelpText)
		return nil
	case "version":
		_, _ = io.WriteString(inv.Stdout, echoVersionText)
		return nil
	}

	stopped, err := writeEchoOutput(inv.Stdout, args, opts)
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

func parseEchoArgs(inv *Invocation) (args []string, opts echoOptions, mode string) {
	if inv != nil {
		args = append([]string(nil), inv.Args...)
	}
	if inv != nil && inv.Env != nil {
		_, opts.posixlyCorrect = inv.Env["POSIXLY_CORRECT"]
	}
	opts.trailingNewline = true

	allowOptions := !opts.posixlyCorrect || (len(args) > 0 && args[0] == "-n")
	if allowOptions && len(args) == 1 {
		switch args[0] {
		case "--help":
			return nil, opts, "help"
		case "--version":
			return nil, opts, "version"
		}
	}
	if !allowOptions {
		return args, opts, ""
	}

	for len(args) > 0 && echoIsOption(args[0]) {
		for i := 1; i < len(args[0]); i++ {
			switch args[0][i] {
			case 'e':
				opts.escape = true
			case 'E':
				opts.escape = false
			case 'n':
				opts.trailingNewline = false
			}
		}
		args = args[1:]
	}

	return args, opts, ""
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

func writeEchoOutput(w io.Writer, args []string, opts echoOptions) (stopped bool, err error) {
	for i, arg := range args {
		if i > 0 {
			if _, err := io.WriteString(w, " "); err != nil {
				return false, err
			}
		}

		if !opts.escape && !opts.posixlyCorrect {
			if _, err := io.WriteString(w, arg); err != nil {
				return false, err
			}
			continue
		}

		decoded, stop := decodeEchoEscapes(arg)
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

func decodeEchoEscapes(value string) (decoded []byte, stop bool) {
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
			decoded = append(decoded, 0x1b)
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
			if i+1 >= len(value) || !isHexDigit(value[i+1]) {
				decoded = append(decoded, '\\', 'x')
				continue
			}
			i++
			hex := echoHexValue(value[i])
			if i+1 < len(value) && isHexDigit(value[i+1]) {
				i++
				hex = hex*16 + echoHexValue(value[i])
			}
			decoded = append(decoded, byte(hex))
		case '0':
			octal, advance := decodeEchoOctal(value, i)
			decoded = append(decoded, byte(octal))
			i = advance
		case '1', '2', '3', '4', '5', '6', '7':
			octal, advance := decodeEchoOctal(value, i)
			decoded = append(decoded, byte(octal))
			i = advance
		default:
			decoded = append(decoded, '\\', value[i])
		}
	}
	return decoded, false
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
`

const echoVersionText = "echo (gbash) dev\n"

var _ Command = (*Echo)(nil)
