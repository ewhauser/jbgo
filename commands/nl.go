package commands

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type NL struct{}

type nlOptions struct {
	headerStyle      nlNumberingStyle
	bodyStyle        nlNumberingStyle
	footerStyle      nlNumberingStyle
	sectionDelimiter []byte
	startLineNumber  int64
	lineIncrement    int64
	joinBlankLines   uint64
	numberWidth      int
	numberFormat     nlNumberFormat
	renumber         bool
	numberSeparator  []byte
	showHelp         bool
	showVersion      bool
}

type nlStats struct {
	lineNumber            int64
	lineNumberValid       bool
	consecutiveEmptyLines uint64
}

type nlNumberingMode int

const (
	nlNumberingAll nlNumberingMode = iota
	nlNumberingNonEmpty
	nlNumberingNone
	nlNumberingRegex
)

type nlNumberingStyle struct {
	mode  nlNumberingMode
	regex *regexp.Regexp
}

type nlNumberFormat int

const (
	nlNumberFormatLeft nlNumberFormat = iota
	nlNumberFormatRight
	nlNumberFormatRightZero
)

type nlSectionKind int

const (
	nlSectionHeader nlSectionKind = iota
	nlSectionBody
	nlSectionFooter
)

func NewNL() *NL {
	return &NL{}
}

func (c *NL) Name() string {
	return "nl"
}

func (c *NL) Run(ctx context.Context, inv *Invocation) error {
	opts, names, err := parseNLArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		return writeNLHelp(inv)
	}
	if opts.showVersion {
		_, err := io.WriteString(inv.Stdout, nlVersionText)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}
	if len(names) == 0 {
		names = []string{"-"}
	}

	stats := nlStats{
		lineNumber:      opts.startLineNumber,
		lineNumberValid: true,
	}

	var (
		stdinData   []byte
		stdinLoaded bool
		sawDir      bool
	)
	for _, name := range names {
		data, isDir, err := readNLInput(ctx, inv, name, &stdinData, &stdinLoaded)
		if err != nil {
			return nlInputError(inv, name, err)
		}
		if isDir {
			sawDir = true
			if _, writeErr := fmt.Fprintf(inv.Stderr, "nl: %s: Is a directory\n", name); writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			continue
		}
		if err := runNL(inv, data, &stats, &opts); err != nil {
			return err
		}
	}

	if sawDir {
		return &ExitError{Code: 1}
	}
	return nil
}

func parseNLArgs(inv *Invocation) (nlOptions, []string, error) {
	opts := nlOptions{
		headerStyle:      nlNumberingStyle{mode: nlNumberingNone},
		bodyStyle:        nlNumberingStyle{mode: nlNumberingNonEmpty},
		footerStyle:      nlNumberingStyle{mode: nlNumberingNone},
		sectionDelimiter: []byte("\\:"),
		startLineNumber:  1,
		lineIncrement:    1,
		joinBlankLines:   1,
		numberWidth:      6,
		numberFormat:     nlNumberFormatRight,
		renumber:         true,
		numberSeparator:  []byte("\t"),
	}

	args := inv.Args
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			return opts, args[1:], nil
		case arg == "-" || !strings.HasPrefix(arg, "-"):
			return opts, args, nil
		case strings.HasPrefix(arg, "--"):
			consumed, err := parseNLLongOption(inv, args, &opts)
			if err != nil {
				return nlOptions{}, nil, err
			}
			args = args[consumed:]
		default:
			consumed, err := parseNLShortOptions(inv, args, &opts)
			if err != nil {
				return nlOptions{}, nil, err
			}
			args = args[consumed:]
		}
	}

	return opts, nil, nil
}

func parseNLLongOption(inv *Invocation, args []string, opts *nlOptions) (int, error) {
	nameValue := args[0][2:]
	name, value, hasValue := strings.Cut(nameValue, "=")

	match, err := matchNLLongOption(inv, name)
	if err != nil {
		return 0, err
	}

	switch match {
	case "help":
		opts.showHelp = true
		return 1, nil
	case "version":
		opts.showVersion = true
		return 1, nil
	case "no-renumber":
		opts.renumber = false
		return 1, nil
	case "body-numbering":
		return parseNLLongStringValue(inv, args, hasValue, value, "body-numbering", func(v string) error {
			style, err := parseNLNumberingStyle(inv, v)
			if err != nil {
				return err
			}
			opts.bodyStyle = style
			return nil
		})
	case "section-delimiter":
		return parseNLLongStringValue(inv, args, hasValue, value, "section-delimiter", func(v string) error {
			opts.sectionDelimiter = normalizeNLSectionDelimiter(v)
			return nil
		})
	case "footer-numbering":
		return parseNLLongStringValue(inv, args, hasValue, value, "footer-numbering", func(v string) error {
			style, err := parseNLNumberingStyle(inv, v)
			if err != nil {
				return err
			}
			opts.footerStyle = style
			return nil
		})
	case "header-numbering":
		return parseNLLongStringValue(inv, args, hasValue, value, "header-numbering", func(v string) error {
			style, err := parseNLNumberingStyle(inv, v)
			if err != nil {
				return err
			}
			opts.headerStyle = style
			return nil
		})
	case "line-increment":
		return parseNLLongStringValue(inv, args, hasValue, value, "line-increment", func(v string) error {
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nlInvalidValue(inv, "line increment", v)
			}
			opts.lineIncrement = parsed
			return nil
		})
	case "join-blank-lines":
		return parseNLLongStringValue(inv, args, hasValue, value, "join-blank-lines", func(v string) error {
			parsed, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return nlInvalidValue(inv, "join blank lines", v)
			}
			opts.joinBlankLines = parsed
			return nil
		})
	case "number-format":
		return parseNLLongStringValue(inv, args, hasValue, value, "number-format", func(v string) error {
			format, err := parseNLNumberFormat(inv, v)
			if err != nil {
				return err
			}
			opts.numberFormat = format
			return nil
		})
	case "number-separator":
		return parseNLLongStringValue(inv, args, hasValue, value, "number-separator", func(v string) error {
			opts.numberSeparator = []byte(v)
			return nil
		})
	case "starting-line-number":
		return parseNLLongStringValue(inv, args, hasValue, value, "starting-line-number", func(v string) error {
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nlInvalidValue(inv, "starting line number", v)
			}
			opts.startLineNumber = parsed
			return nil
		})
	case "number-width":
		return parseNLLongStringValue(inv, args, hasValue, value, "number-width", func(v string) error {
			width, err := parseNLWidth(inv, v)
			if err != nil {
				return err
			}
			opts.numberWidth = width
			return nil
		})
	default:
		return 0, nlOptionf(inv, "nl: unrecognized option '%s'", args[0])
	}
}

func parseNLLongStringValue(inv *Invocation, args []string, hasValue bool, value, name string, set func(string) error) (int, error) {
	if hasValue {
		return 1, set(value)
	}
	if len(args) < 2 {
		return 0, nlUsageError(inv, "nl: option '--%s' requires an argument", name)
	}
	return 2, set(args[1])
}

func parseNLShortOptions(inv *Invocation, args []string, opts *nlOptions) (int, error) {
	arg := args[0]
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'p':
			opts.renumber = false
		case 'b', 'd', 'f', 'h', 'i', 'l', 'n', 's', 'v', 'w':
			value := arg[i+1:]
			consumed := 1
			if value == "" {
				if len(args) < 2 {
					return 0, nlUsageError(inv, "nl: option requires an argument -- '%c'", arg[i])
				}
				value = args[1]
				consumed = 2
			}
			if err := applyNLShortOption(inv, opts, rune(arg[i]), value); err != nil {
				return 0, err
			}
			return consumed, nil
		default:
			return 0, nlOptionf(inv, "nl: invalid option -- '%c'", arg[i])
		}
	}
	return 1, nil
}

func applyNLShortOption(inv *Invocation, opts *nlOptions, flag rune, value string) error {
	switch flag {
	case 'b':
		style, err := parseNLNumberingStyle(inv, value)
		if err != nil {
			return err
		}
		opts.bodyStyle = style
	case 'd':
		opts.sectionDelimiter = normalizeNLSectionDelimiter(value)
	case 'f':
		style, err := parseNLNumberingStyle(inv, value)
		if err != nil {
			return err
		}
		opts.footerStyle = style
	case 'h':
		style, err := parseNLNumberingStyle(inv, value)
		if err != nil {
			return err
		}
		opts.headerStyle = style
	case 'i':
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nlInvalidValue(inv, "line increment", value)
		}
		opts.lineIncrement = parsed
	case 'l':
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return nlInvalidValue(inv, "join blank lines", value)
		}
		opts.joinBlankLines = parsed
	case 'n':
		format, err := parseNLNumberFormat(inv, value)
		if err != nil {
			return err
		}
		opts.numberFormat = format
	case 's':
		opts.numberSeparator = []byte(value)
	case 'v':
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nlInvalidValue(inv, "starting line number", value)
		}
		opts.startLineNumber = parsed
	case 'w':
		width, err := parseNLWidth(inv, value)
		if err != nil {
			return err
		}
		opts.numberWidth = width
	default:
		return nlOptionf(inv, "nl: invalid option -- '%c'", flag)
	}
	return nil
}

func matchNLLongOption(inv *Invocation, name string) (string, error) {
	candidates := []string{
		"body-numbering",
		"footer-numbering",
		"header-numbering",
		"help",
		"join-blank-lines",
		"line-increment",
		"no-renumber",
		"number-format",
		"number-separator",
		"number-width",
		"section-delimiter",
		"starting-line-number",
		"version",
	}
	for _, candidate := range candidates {
		if candidate == name {
			return candidate, nil
		}
	}
	var matches []string
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, name) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return "", nlOptionf(inv, "nl: unrecognized option '%s'", "--"+name)
	case 1:
		return matches[0], nil
	default:
		return "", nlOptionf(inv, "nl: option '%s' is ambiguous", "--"+name)
	}
}

func parseNLNumberingStyle(inv *Invocation, value string) (nlNumberingStyle, error) {
	switch value {
	case "a":
		return nlNumberingStyle{mode: nlNumberingAll}, nil
	case "t":
		return nlNumberingStyle{mode: nlNumberingNonEmpty}, nil
	case "n":
		return nlNumberingStyle{mode: nlNumberingNone}, nil
	default:
		if strings.HasPrefix(value, "p") {
			re, err := regexp.Compile(value[1:])
			if err != nil {
				return nlNumberingStyle{}, exitf(inv, 1, "nl: invalid regular expression")
			}
			return nlNumberingStyle{mode: nlNumberingRegex, regex: re}, nil
		}
		return nlNumberingStyle{}, exitf(inv, 1, "nl: invalid numbering style: '%s'", value)
	}
}

func parseNLNumberFormat(inv *Invocation, value string) (nlNumberFormat, error) {
	switch value {
	case "ln":
		return nlNumberFormatLeft, nil
	case "rn":
		return nlNumberFormatRight, nil
	case "rz":
		return nlNumberFormatRightZero, nil
	default:
		return 0, nlInvalidValue(inv, "number format", value)
	}
}

func parseNLWidth(inv *Invocation, value string) (int, error) {
	width, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, nlInvalidValue(inv, "line number field width", value)
	}
	if width == 0 {
		return 0, exitf(inv, 1, "nl: Invalid line number field width: '%s': Numerical result out of range", value)
	}
	if width > uint64(^uint(0)>>1) {
		return 0, nlInvalidValue(inv, "line number field width", value)
	}
	return int(width), nil
}

func normalizeNLSectionDelimiter(value string) []byte {
	delimiter := []byte(value)
	if len(delimiter) == 1 {
		return append(delimiter, ':')
	}
	return delimiter
}

func readNLInput(ctx context.Context, inv *Invocation, name string, stdinData *[]byte, stdinLoaded *bool) (data []byte, isDir bool, err error) {
	if name == "-" {
		if !*stdinLoaded {
			data, err = readAllStdin(inv)
			if err != nil {
				return nil, false, err
			}
			*stdinData = data
			*stdinLoaded = true
		}
		return *stdinData, false, nil
	}

	abs, err := allowPath(ctx, inv, "", name)
	if err != nil {
		return nil, false, err
	}
	info, err := inv.FS.Stat(ctx, abs)
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() {
		return nil, true, nil
	}

	file, err := inv.FS.Open(ctx, abs)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()

	data, err = io.ReadAll(file)
	if err != nil {
		return nil, false, err
	}
	return data, false, nil
}

func runNL(inv *Invocation, data []byte, stats *nlStats, opts *nlOptions) error {
	currentStyle := opts.bodyStyle
	for _, rawLine := range splitLines(data) {
		line := bytes.TrimSuffix(rawLine, []byte{'\n'})
		if len(line) == 0 {
			stats.consecutiveEmptyLines++
		} else {
			stats.consecutiveEmptyLines = 0
		}

		if section, ok := parseNLSectionDelimiter(line, opts.sectionDelimiter); ok {
			switch section {
			case nlSectionHeader:
				currentStyle = opts.headerStyle
			case nlSectionBody:
				currentStyle = opts.bodyStyle
			case nlSectionFooter:
				currentStyle = opts.footerStyle
			}
			if opts.renumber {
				stats.lineNumber = opts.startLineNumber
				stats.lineNumberValid = true
			}
			if _, err := inv.Stdout.Write([]byte{'\n'}); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			continue
		}

		numberLine := shouldNLNumberLine(currentStyle, line, opts.joinBlankLines, stats.consecutiveEmptyLines)
		if numberLine {
			if !stats.lineNumberValid {
				return exitf(inv, 1, "nl: line number overflow")
			}
			if err := writeNLNumber(inv.Stdout, opts.numberFormat, stats.lineNumber, opts.numberWidth); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if _, err := inv.Stdout.Write(opts.numberSeparator); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			next, ok := nlCheckedAdd(stats.lineNumber, opts.lineIncrement)
			stats.lineNumber = next
			stats.lineNumberValid = ok
		} else {
			prefix := bytes.Repeat([]byte(" "), opts.numberWidth+1)
			if _, err := inv.Stdout.Write(prefix); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if _, err := inv.Stdout.Write(line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if _, err := inv.Stdout.Write([]byte{'\n'}); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func parseNLSectionDelimiter(line, pattern []byte) (nlSectionKind, bool) {
	if len(line) == 0 || len(pattern) == 0 || len(line)%len(pattern) != 0 {
		return 0, false
	}
	count := len(line) / len(pattern)
	if count < 1 || count > 3 {
		return 0, false
	}
	for i := 0; i < len(line); i += len(pattern) {
		if !bytes.Equal(line[i:i+len(pattern)], pattern) {
			return 0, false
		}
	}
	switch count {
	case 1:
		return nlSectionFooter, true
	case 2:
		return nlSectionBody, true
	case 3:
		return nlSectionHeader, true
	default:
		return 0, false
	}
}

func shouldNLNumberLine(style nlNumberingStyle, line []byte, joinBlankLines, consecutiveEmptyLines uint64) bool {
	switch style.mode {
	case nlNumberingAll:
		if len(line) == 0 && joinBlankLines > 0 && consecutiveEmptyLines%joinBlankLines != 0 {
			return false
		}
		return true
	case nlNumberingNonEmpty:
		return len(line) > 0
	case nlNumberingNone:
		return false
	case nlNumberingRegex:
		return style.regex != nil && style.regex.Match(line)
	default:
		return false
	}
}

func writeNLNumber(w io.Writer, format nlNumberFormat, number int64, width int) error {
	switch format {
	case nlNumberFormatLeft:
		text := strconv.FormatInt(number, 10)
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
		for i := len(text); i < width; i++ {
			if _, err := w.Write([]byte{' '}); err != nil {
				return err
			}
		}
	case nlNumberFormatRight:
		text := strconv.FormatInt(number, 10)
		for i := len(text); i < width; i++ {
			if _, err := w.Write([]byte{' '}); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
	case nlNumberFormatRightZero:
		if number < 0 {
			if _, err := w.Write([]byte{'-'}); err != nil {
				return err
			}
			text := strconv.FormatUint(nlUnsignedAbs(number), 10)
			for i := len(text); i < width-1; i++ {
				if _, err := w.Write([]byte{'0'}); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, text); err != nil {
				return err
			}
			return nil
		}
		text := strconv.FormatInt(number, 10)
		for i := len(text); i < width; i++ {
			if _, err := w.Write([]byte{'0'}); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, text); err != nil {
			return err
		}
	default:
		return nil
	}
	return nil
}

func nlUnsignedAbs(number int64) uint64 {
	if number >= 0 {
		return uint64(number)
	}
	return uint64(-(number + 1)) + 1
}

func nlCheckedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, false
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func nlInputError(inv *Invocation, name string, err error) error {
	return exitf(inv, exitCodeForError(err), "nl: %s: %v", name, err)
}

func nlInvalidValue(inv *Invocation, label, value string) error {
	return exitf(inv, 1, "nl: invalid value '%s' for %s", value, label)
}

func nlOptionf(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'nl --help' for more information.", args...)
}

func nlUsageError(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, format+"\nTry 'nl --help' for more information.", args...)
}

const nlVersionText = "nl (gbash) dev\n"

func writeNLHelp(inv *Invocation) error {
	help := `Usage: nl [OPTION]... [FILE]...
Write each FILE to standard output, with line numbers added.

  -b, --body-numbering=STYLE     select body numbering style
  -d, --section-delimiter=CC     use CC for logical page delimiters
  -f, --footer-numbering=STYLE   select footer numbering style
  -h, --header-numbering=STYLE   select header numbering style
  -i, --line-increment=NUMBER    line number increment at each line
  -l, --join-blank-lines=NUMBER  group of NUMBER empty lines counted as one
  -n, --number-format=FORMAT     insert line numbers according to FORMAT
  -p, --no-renumber              do not reset line numbers for logical pages
  -s, --number-separator=STRING  add STRING after line number
  -v, --starting-line-number=N   first line number for each section
  -w, --number-width=NUMBER      use NUMBER columns for line numbers
      --help                     display this help and exit
      --version                  output version information and exit
`
	_, err := io.WriteString(inv.Stdout, help)
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

var _ Command = (*NL)(nil)
