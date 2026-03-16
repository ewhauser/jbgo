package builtins

import (
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
)

type Dircolors struct{}

type dircolorsOutputFormat int

const (
	dircolorsOutputShell dircolorsOutputFormat = iota
	dircolorsOutputCShell
	dircolorsOutputDisplay
	dircolorsOutputUnknown
)

func NewDircolors() *Dircolors {
	return &Dircolors{}
}

func (c *Dircolors) Name() string {
	return "dircolors"
}

func (c *Dircolors) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Dircolors) Spec() CommandSpec {
	return CommandSpec{
		Name:  "dircolors",
		About: "Output commands to set the LS_COLORS environment variable.",
		Usage: "dircolors [OPTION]... [FILE]",
		AfterHelp: "Determine format and output the commands to set LS_COLORS.\n" +
			"Output is Bourne shell code by default, unless the SHELL environment\n" +
			"variable ends in csh or tcsh.",
		Options: []OptionSpec{
			{Name: "bourne-shell", Short: 'b', Long: "sh", Aliases: []string{"bourne-shell"}, Help: "output Bourne shell code to set LS_COLORS"},
			{Name: "c-shell", Short: 'c', Long: "csh", Aliases: []string{"c-shell"}, Help: "output C shell code to set LS_COLORS"},
			{Name: "print-database", Short: 'p', Long: "print-database", Help: "output defaults"},
			{Name: "print-ls-colors", Long: "print-ls-colors", Help: "output fully escaped colors for display"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Dircolors) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if hasAny(matches, "bourne-shell", "c-shell") && hasAny(matches, "print-database", "print-ls-colors") {
		return exitf(inv, 1, "dircolors: options to output shell code and options to print other output are mutually exclusive")
	}
	if matches.Has("print-database") && matches.Has("print-ls-colors") {
		return exitf(inv, 1, "dircolors: options --print-database and --print-ls-colors are mutually exclusive")
	}

	files := matches.Args("file")
	if matches.Has("print-database") {
		if len(files) > 0 {
			return exitf(inv, 1, "dircolors: extra operand %s", quoteGNUOperand(files[0]))
		}
		_, err := io.WriteString(inv.Stdout, dircolorsGenerateConfig())
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}
	if len(files) > 1 {
		return exitf(inv, 1, "dircolors: extra operand %s", quoteGNUOperand(files[1]))
	}

	format := dircolorsSelectFormat(inv.Env, matches)
	if format == dircolorsOutputUnknown {
		return exitf(inv, 1, "dircolors: no SHELL environment variable, and no shell type option given")
	}

	if len(files) == 0 {
		text := dircolorsGenerateLSColors(format, ":")
		if _, err := io.WriteString(inv.Stdout, text+"\n"); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	var (
		lines []string
		fp    = files[0]
	)
	if fp == "-" {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		lines = textLines(data)
	} else {
		info, _, exists, err := statMaybe(ctx, inv, fp)
		if err != nil {
			return err
		}
		if exists && info.IsDir() {
			return exitf(inv, 2, "dircolors: expected file, got directory %s", quoteGNUOperand(fp))
		}
		data, _, err := readAllFile(ctx, inv, fp)
		if err != nil {
			return exitf(inv, 1, "dircolors: %s: %v", fp, err)
		}
		lines = textLines(data)
	}

	result, err := dircolorsParse(lines, format, fp, inv.Env)
	if err != nil {
		return exitf(inv, 1, "dircolors: %s", err.Error())
	}
	if _, err := io.WriteString(inv.Stdout, result+"\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func hasAny(matches *ParsedCommand, names ...string) bool {
	return slices.ContainsFunc(names, matches.Has)
}

func dircolorsSelectFormat(env map[string]string, matches *ParsedCommand) dircolorsOutputFormat {
	format := dircolorsOutputUnknown
	for _, option := range matches.OptionOrder() {
		switch option {
		case "bourne-shell":
			format = dircolorsOutputShell
		case "c-shell":
			format = dircolorsOutputCShell
		}
	}
	if format != dircolorsOutputUnknown {
		return format
	}
	if matches.Has("print-ls-colors") {
		return dircolorsOutputDisplay
	}

	shell := env["SHELL"]
	if shell == "" {
		return dircolorsOutputUnknown
	}
	switch path.Base(shell) {
	case "csh", "tcsh":
		return dircolorsOutputCShell
	default:
		return dircolorsOutputShell
	}
}

func dircolorsFormatAffixes(format dircolorsOutputFormat) (prefix, suffix string) {
	switch format {
	case dircolorsOutputShell:
		return "LS_COLORS='", "';\nexport LS_COLORS"
	case dircolorsOutputCShell:
		return "setenv LS_COLORS '", "'"
	case dircolorsOutputDisplay:
		return "", ""
	default:
		return "", ""
	}
}

func dircolorsGenerateTypeOutput(format dircolorsOutputFormat) string {
	parts := make([]string, 0, len(dircolorsFileTypes))
	for _, entry := range dircolorsFileTypes {
		if format == dircolorsOutputDisplay {
			parts = append(parts, fmt.Sprintf("\x1b[%sm%s\t%s\x1b[0m", entry.Code, entry.Key, entry.Code))
			continue
		}
		parts = append(parts, entry.Key+"="+entry.Code)
	}
	if format == dircolorsOutputDisplay {
		return strings.Join(parts, "\n")
	}
	return strings.Join(parts, ":")
}

func dircolorsGenerateLSColors(format dircolorsOutputFormat, sep string) string {
	if format == dircolorsOutputDisplay {
		lines := []string{dircolorsGenerateTypeOutput(format)}
		for _, entry := range dircolorsFileColors {
			prefix := "*"
			if strings.HasPrefix(entry.Pattern, "*") {
				prefix = ""
			}
			lines = append(lines, fmt.Sprintf("\x1b[%sm%s%s\t%s\x1b[0m", entry.Code, prefix, entry.Pattern, entry.Code))
		}
		return strings.Join(lines, "\n")
	}

	parts := make([]string, 0, len(dircolorsFileColors))
	for _, entry := range dircolorsFileColors {
		name := entry.Pattern
		if !strings.HasPrefix(name, "*") {
			name = "*" + name
		}
		parts = append(parts, name+"="+entry.Code)
	}
	prefix, suffix := dircolorsFormatAffixes(format)
	return prefix + dircolorsGenerateTypeOutput(format) + ":" + strings.Join(parts, sep) + ":" + suffix
}

func dircolorsGenerateConfig() string {
	var b strings.Builder
	b.WriteString("# Configuration file for dircolors, a utility to help you set the\n")
	b.WriteString("# LS_COLORS environment variable used by GNU ls with the --color option.\n")
	b.WriteString("# The keywords COLOR, OPTIONS, and EIGHTBIT (honored by the\n")
	b.WriteString("# slackware version of dircolors) are recognized but ignored.\n")
	b.WriteString("# Global config options can be specified before TERM or COLORTERM entries\n")
	b.WriteString("# Below are TERM or COLORTERM entries, which can be glob patterns, which\n")
	b.WriteString("# restrict following config to systems with matching environment variables.\n")
	b.WriteString("COLORTERM ?*\n")
	for _, term := range dircolorsTerms {
		b.WriteString("TERM ")
		b.WriteString(term)
		b.WriteByte('\n')
	}
	b.WriteString("# Below are the color init strings for the basic file types.\n")
	b.WriteString("# One can use codes for 256 or more colors supported by modern terminals.\n")
	b.WriteString("# The default color codes use the capabilities of an 8 color terminal\n")
	b.WriteString("# with some additional attributes as per the following codes:\n")
	b.WriteString("# Attribute codes:\n")
	b.WriteString("# 00=none 01=bold 04=underscore 05=blink 07=reverse 08=concealed\n")
	b.WriteString("# Text color codes:\n")
	b.WriteString("# 30=black 31=red 32=green 33=yellow 34=blue 35=magenta 36=cyan 37=white\n")
	b.WriteString("# Background color codes:\n")
	b.WriteString("# 40=black 41=red 42=green 43=yellow 44=blue 45=magenta 46=cyan 47=white\n")
	b.WriteString("#NORMAL 00 # no color code at all\n")
	b.WriteString("#FILE 00 # regular file: use no color at all\n")
	for _, entry := range dircolorsFileTypes {
		b.WriteString(entry.Name)
		b.WriteByte(' ')
		b.WriteString(entry.Code)
		b.WriteByte('\n')
	}
	b.WriteString("# List any file extensions like '.gz' or '.tar' that you would like ls\n")
	b.WriteString("# to color below. Put the extension, a space, and the color init string.\n")
	for _, entry := range dircolorsFileColors {
		b.WriteString(entry.Pattern)
		b.WriteByte(' ')
		b.WriteString(entry.Code)
		b.WriteByte('\n')
	}
	b.WriteString("# Subsequent TERM or COLORTERM entries, can be used to add / override\n")
	b.WriteString("# config specific to those matching environment variables.")
	return b.String()
}

func dircolorsParse(lines []string, format dircolorsOutputFormat, fp string, env map[string]string) (string, error) {
	prefix, suffix := dircolorsFormatAffixes(format)
	var b strings.Builder
	b.WriteString(prefix)

	term := env["TERM"]
	colorterm := env["COLORTERM"]
	state := dircolorsParseStateGlobal
	sawColortermMatch := false

	for i, raw := range lines {
		line := dircolorsPurify(raw)
		if line == "" {
			continue
		}
		line = dircolorsEscape(line)
		key, val := dircolorsSplitTwo(line)
		if val == "" {
			return "", fmt.Errorf("%s:%d: invalid line; missing second token", fp, i+1)
		}
		lower := strings.ToLower(key)

		switch lower {
		case "term":
			if dircolorsFnmatch(term, val) {
				state = dircolorsParseStateMatched
			} else if state == dircolorsParseStateGlobal {
				state = dircolorsParseStatePass
			}
		case "colorterm":
			matches := false
			if val == "?*" {
				matches = colorterm != ""
			} else {
				matches = dircolorsFnmatch(colorterm, val)
			}
			if matches {
				state = dircolorsParseStateMatched
				sawColortermMatch = true
			} else if !sawColortermMatch && state == dircolorsParseStateGlobal {
				state = dircolorsParseStatePass
			}
		default:
			if state == dircolorsParseStateMatched {
				state = dircolorsParseStateContinue
			}
			if state != dircolorsParseStatePass {
				if err := dircolorsAppendEntry(&b, format, key, lower, val); err != nil {
					return "", err
				}
			}
		}
	}

	result := b.String()
	if format == dircolorsOutputDisplay && strings.HasSuffix(result, "\n") {
		result = strings.TrimSuffix(result, "\n")
	}
	return result + suffix, nil
}

type dircolorsParseState int

const (
	dircolorsParseStateGlobal dircolorsParseState = iota
	dircolorsParseStateMatched
	dircolorsParseStateContinue
	dircolorsParseStatePass
)

func dircolorsAppendEntry(b *strings.Builder, format dircolorsOutputFormat, key, lower, val string) error {
	if strings.HasPrefix(key, ".") || strings.HasPrefix(key, "*") {
		entry := key
		if strings.HasPrefix(key, ".") {
			entry = "*" + key
		}
		if format == dircolorsOutputDisplay {
			b.WriteString("\x1b[")
			b.WriteString(val)
			b.WriteString("m")
			b.WriteString(entry)
			b.WriteByte('\t')
			b.WriteString(val)
			b.WriteString("\x1b[0m\n")
		} else {
			b.WriteString(entry)
			b.WriteByte('=')
			b.WriteString(val)
			b.WriteByte(':')
		}
		return nil
	}

	switch lower {
	case "options", "color", "eightbit":
		return nil
	}

	mapped, ok := dircolorsAttributeCodes[lower]
	if !ok {
		return fmt.Errorf("unrecognized keyword %s", quoteGNUOperand(key))
	}
	if format == dircolorsOutputDisplay {
		b.WriteString("\x1b[")
		b.WriteString(val)
		b.WriteString("m")
		b.WriteString(mapped)
		b.WriteByte('\t')
		b.WriteString(val)
		b.WriteString("\x1b[0m\n")
		return nil
	}
	b.WriteString(mapped)
	b.WriteByte('=')
	b.WriteString(val)
	b.WriteByte(':')
	return nil
}

func dircolorsPurify(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != '#' {
			continue
		}
		if i == 0 {
			return ""
		}
		r, size := utf8LastRuneInString(s[:i])
		if isWhitespaceRune(r) {
			return strings.TrimSpace(s[:i-size])
		}
	}
	return strings.TrimSpace(s)
}

func dircolorsSplitTwo(s string) (key, value string) {
	for i, r := range s {
		if !isWhitespaceRune(r) {
			continue
		}
		key = s[:i]
		value = strings.TrimLeftFunc(s[i:], isWhitespaceRune)
		return key, value
	}
	return "", ""
}

func dircolorsEscape(s string) string {
	var b strings.Builder
	prev := rune(' ')
	for _, r := range s {
		switch {
		case r == '\'':
			b.WriteString("'\\''")
		case r == ':' && prev != '\\':
			b.WriteString("\\:")
		default:
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}

func dircolorsFnmatch(value, pattern string) bool {
	pattern = strings.ReplaceAll(pattern, "[!", "[^")
	ok, err := path.Match(pattern, value)
	return err == nil && ok
}

func utf8LastRuneInString(s string) (last rune, size int) {
	for _, rr := range s {
		last = rr
		size = len(string(rr))
	}
	return last, size
}

func isWhitespaceRune(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

var _ Command = (*Dircolors)(nil)
var _ SpecProvider = (*Dircolors)(nil)
var _ ParsedRunner = (*Dircolors)(nil)
