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
	return RunCommand(ctx, c, inv)
}

func (c *NL) Spec() CommandSpec {
	return CommandSpec{
		Name:      "nl",
		About:     "Number lines of files",
		Usage:     "nl [OPTION]... [FILE]...",
		AfterHelp: "STYLE is one of:\n\n  - a number all lines\n  - t number only nonempty lines\n  - n number no lines\n  - pBRE number only lines that contain a match for the basic regular\n          expression, BRE\n\n  FORMAT is one of:\n\n  - ln left justified, no leading zeros\n  - rn right justified, no leading zeros\n  - rz right justified, leading zeros",
		Options: []OptionSpec{
			{Name: "body-numbering", Short: 'b', Long: "body-numbering", Arity: OptionRequiredValue, ValueName: "STYLE", Help: "use STYLE for numbering body lines"},
			{Name: "section-delimiter", Short: 'd', Long: "section-delimiter", Arity: OptionRequiredValue, ValueName: "CC", Help: "use CC for separating logical pages"},
			{Name: "footer-numbering", Short: 'f', Long: "footer-numbering", Arity: OptionRequiredValue, ValueName: "STYLE", Help: "use STYLE for numbering footer lines"},
			{Name: "header-numbering", Short: 'h', Long: "header-numbering", Arity: OptionRequiredValue, ValueName: "STYLE", Help: "use STYLE for numbering header lines"},
			{Name: "line-increment", Short: 'i', Long: "line-increment", Arity: OptionRequiredValue, ValueName: "NUMBER", Help: "line number increment at each line"},
			{Name: "join-blank-lines", Short: 'l', Long: "join-blank-lines", Arity: OptionRequiredValue, ValueName: "NUMBER", Help: "group of NUMBER empty lines counted as one"},
			{Name: "number-format", Short: 'n', Long: "number-format", Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "insert line numbers according to FORMAT"},
			{Name: "no-renumber", Short: 'p', Long: "no-renumber", Help: "do not reset line numbers at logical pages"},
			{Name: "number-separator", Short: 's', Long: "number-separator", Arity: OptionRequiredValue, ValueName: "STRING", Help: "add STRING after (possible) line number"},
			{Name: "starting-line-number", Short: 'v', Long: "starting-line-number", Arity: OptionRequiredValue, ValueName: "NUMBER", Help: "first line number on each logical page"},
			{Name: "number-width", Short: 'w', Long: "number-width", Arity: OptionRequiredValue, ValueName: "NUMBER", Help: "use NUMBER columns for line numbers"},
			{Name: "help", Long: "help", Help: "Print help information."},
			{Name: "version", Long: "version", Help: "Print version information."},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, nlHelpText)
			return err
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, nlVersionText)
			return err
		},
	}
}

func (c *NL) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, names, err := parseNLMatches(inv, matches)
	if err != nil {
		return err
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

func parseNLMatches(inv *Invocation, matches *ParsedCommand) (nlOptions, []string, error) {
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
	for _, name := range matches.OptionOrder() {
		switch name {
		case "body-numbering":
			value := matches.Value("body-numbering")
			style, err := parseNLNumberingStyle(inv, value)
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.bodyStyle = style
		case "section-delimiter":
			opts.sectionDelimiter = normalizeNLSectionDelimiter(matches.Value("section-delimiter"))
		case "footer-numbering":
			value := matches.Value("footer-numbering")
			style, err := parseNLNumberingStyle(inv, value)
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.footerStyle = style
		case "header-numbering":
			value := matches.Value("header-numbering")
			style, err := parseNLNumberingStyle(inv, value)
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.headerStyle = style
		case "line-increment":
			value := matches.Value("line-increment")
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nlOptions{}, nil, nlInvalidValue(inv, "line increment", value)
			}
			opts.lineIncrement = parsed
		case "join-blank-lines":
			value := matches.Value("join-blank-lines")
			parsed, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nlOptions{}, nil, nlInvalidValue(inv, "join blank lines", value)
			}
			opts.joinBlankLines = parsed
		case "number-format":
			value := matches.Value("number-format")
			format, err := parseNLNumberFormat(inv, value)
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.numberFormat = format
		case "number-separator":
			opts.numberSeparator = []byte(matches.Value("number-separator"))
		case "starting-line-number":
			value := matches.Value("starting-line-number")
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nlOptions{}, nil, nlInvalidValue(inv, "starting line number", value)
			}
			opts.startLineNumber = parsed
		case "number-width":
			value := matches.Value("number-width")
			width, err := parseNLWidth(inv, value)
			if err != nil {
				return nlOptions{}, nil, err
			}
			opts.numberWidth = width
		case "no-renumber":
			opts.renumber = false
		}
	}
	return opts, matches.Args("file"), nil
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

const nlVersionText = "nl (gbash) dev\n"
const nlHelpText = `Usage: nl [OPTION]... [FILE]...
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

var _ Command = (*NL)(nil)
var _ SpecProvider = (*NL)(nil)
var _ ParsedRunner = (*NL)(nil)
