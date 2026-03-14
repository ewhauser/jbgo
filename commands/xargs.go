package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	xargsExitCommandNonzero = 123
	xargsExitCommand255     = 124
	xargsExitCommandSignal  = 125
	xargsExitCannotRun      = 126
	xargsExitNotFound       = 127

	xargsPosixArgMin      = 4096
	xargsDefaultArgBuffer = 128 * 1024
	xargsSyntheticArgMax  = 1024 * 1024
	xargsArgHeadroom      = 2048
)

type XArgs struct{}

type xargsReadMode int

const (
	xargsReadShell xargsReadMode = iota
	xargsReadDelimited
)

type xargsOptions struct {
	inputFile string
	keepStdin bool

	readMode  xargsReadMode
	delimiter byte

	eofStr     *string
	replacePat *string

	linesPerExec int
	argsPerExec  int

	openTTY        bool
	interactive    bool
	noRunIfEmpty   bool
	maxChars       int
	maxCharsSet    bool
	verbose        bool
	showLimits     bool
	exitIfTooLarge bool
	maxProcs       int
	slotVar        string
}

type xargsInputItem struct {
	value string
	group int
}

type xargsInputParseResult struct {
	items   []xargsInputItem
	errText string
}

type xargsTask struct {
	argv []string
}

type xargsExecResult struct {
	slot     int
	argv     []string
	stdout   string
	stderr   string
	exitCode int
}

func NewXArgs() *XArgs {
	return &XArgs{}
}

func (c *XArgs) Name() string {
	return "xargs"
}

func (c *XArgs) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *XArgs) Spec() CommandSpec {
	return CommandSpec{
		Name:  "xargs",
		About: "Build and execute command lines from standard input.",
		Usage: "xargs [OPTION]... [COMMAND [INITIAL-ARGS]...]",
		Options: []OptionSpec{
			{Name: "null", Short: '0', Long: "null", Help: "items are separated by a null, not whitespace"},
			{Name: "arg-file", Short: 'a', Long: "arg-file", ValueName: "FILE", Arity: OptionRequiredValue, Help: "read arguments from FILE, not standard input"},
			{Name: "delimiter", Short: 'd', Long: "delimiter", ValueName: "CHAR", Arity: OptionRequiredValue, Help: "items are separated by CHAR, not whitespace"},
			{Name: "eof", Short: 'e', Long: "eof", ValueName: "END", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "set logical EOF string; END defaults to no EOF string"},
			{Name: "eof-posix", Short: 'E', ValueName: "END", Arity: OptionRequiredValue, Help: "set logical EOF string"},
			{Name: "replace", Short: 'i', Long: "replace", ValueName: "R", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "replace occurrences of R in initial arguments; default is {}"},
			{Name: "replace-posix", Short: 'I', ValueName: "R", Arity: OptionRequiredValue, Help: "replace occurrences of R in initial arguments"},
			{Name: "max-lines", Short: 'l', Long: "max-lines", ValueName: "MAX", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, Help: "use at most MAX nonblank input lines per command line; default is 1"},
			{Name: "max-lines-posix", Short: 'L', ValueName: "MAX", Arity: OptionRequiredValue, Help: "use at most MAX nonblank input lines per command line"},
			{Name: "max-args", Short: 'n', Long: "max-args", ValueName: "MAX", Arity: OptionRequiredValue, Help: "use at most MAX arguments per command line"},
			{Name: "open-tty", Short: 'o', Long: "open-tty", Help: "reopen stdin as /dev/tty in the child process"},
			{Name: "interactive", Short: 'p', Long: "interactive", Help: "prompt before running each command line"},
			{Name: "no-run-if-empty", Short: 'r', Long: "no-run-if-empty", Help: "do not run COMMAND if there are no arguments"},
			{Name: "max-chars", Short: 's', Long: "max-chars", ValueName: "MAX", Arity: OptionRequiredValue, Help: "limit command line length to MAX characters"},
			{Name: "show-limits", Long: "show-limits", Help: "display command length limits"},
			{Name: "verbose", Short: 't', Long: "verbose", Help: "print commands before executing them"},
			{Name: "exit", Short: 'x', Long: "exit", Help: "exit if the size limit is exceeded"},
			{Name: "max-procs", Short: 'P', Long: "max-procs", ValueName: "MAX", Arity: OptionRequiredValue, Help: "run up to MAX processes at a time"},
			{Name: "process-slot-var", Long: "process-slot-var", ValueName: "VAR", Arity: OptionRequiredValue, Help: "set VAR to a unique child slot number"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "operand", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
		},
	}
}

func (c *XArgs) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		spec := c.Spec()
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	opts, command, err := parseXArgsMatches(inv, matches)
	if err != nil {
		return err
	}

	maxSupported := xargsMaxCharsLimit(inv.Env)
	if opts.maxCharsSet && opts.maxChars > maxSupported {
		xargsWarn(inv, "warning: value %d for -s option is too large, using %d instead", opts.maxChars, maxSupported)
		opts.maxChars = maxSupported
	}
	if !opts.maxCharsSet {
		opts.maxChars = xargsDefaultMaxChars(inv.Env)
	}
	if opts.readMode == xargsReadDelimited && opts.eofStr != nil {
		xargsWarn(inv, "warning: the -E option has no effect if -0 or -d is used.")
	}
	if opts.replacePat != nil || opts.linesPerExec > 0 {
		opts.exitIfTooLarge = true
	}
	if opts.showLimits {
		if err := writeXArgsLimits(inv, &opts, maxSupported); err != nil {
			return err
		}
	}
	if len(command) == 0 {
		command = []string{"echo"}
	}

	inputData, err := readXArgsInput(ctx, inv, &opts)
	if err != nil {
		return err
	}
	parsed := parseXArgsInput(inv, inputData, &opts)

	tasks, err := buildXArgsTasks(inv, &opts, command, parsed.items)
	if err != nil {
		return err
	}

	status, err := runXArgsTasks(ctx, inv, &opts, tasks)
	if err != nil {
		return err
	}
	if parsed.errText != "" {
		if _, writeErr := fmt.Fprintln(inv.Stderr, parsed.errText); writeErr != nil {
			return &ExitError{Code: 1, Err: writeErr}
		}
		if status < 1 {
			status = 1
		}
	}
	if status != 0 {
		return &ExitError{Code: status}
	}
	return nil
}

var _ SpecProvider = (*XArgs)(nil)
var _ ParsedRunner = (*XArgs)(nil)

func parseXArgsMatches(inv *Invocation, matches *ParsedCommand) (opts xargsOptions, command []string, err error) {
	opts = xargsOptions{
		inputFile: "-",
		readMode:  xargsReadShell,
		maxProcs:  1,
	}

	valueIndex := make(map[string]int)
	nextValue := func(name string) string {
		values := matches.Values(name)
		idx := valueIndex[name]
		if idx >= len(values) {
			valueIndex[name] = idx + 1
			return ""
		}
		valueIndex[name] = idx + 1
		return values[idx]
	}

	for _, name := range matches.OptionOrder() {
		switch name {
		case "null":
			opts.readMode = xargsReadDelimited
			opts.delimiter = 0
		case "arg-file":
			value := nextValue(name)
			opts.inputFile = value
			opts.keepStdin = value != "-"
		case "delimiter":
			value := nextValue(name)
			delim, parseErr := parseXArgsDelimiter(value)
			if parseErr != nil {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: %v", parseErr)
			}
			opts.readMode = xargsReadDelimited
			opts.delimiter = delim
		case "eof":
			value := nextValue(name)
			if value == "" {
				opts.eofStr = nil
			} else {
				opts.eofStr = &value
			}
		case "eof-posix":
			value := nextValue(name)
			if value == "" {
				opts.eofStr = nil
			} else {
				opts.eofStr = &value
			}
		case "replace":
			value := nextValue(name)
			if value == "" {
				value = "{}"
			}
			xargsApplyReplace(inv, &opts, value)
		case "replace-posix":
			value := nextValue(name)
			xargsApplyReplace(inv, &opts, value)
		case "max-lines":
			value := nextValue(name)
			count := 1
			if value != "" {
				parsed, parseErr := parseXArgsNumber(inv, value, 'L', 1, math.MaxInt, true)
				if parseErr != nil {
					return xargsOptions{}, nil, parseErr
				}
				count = parsed
			}
			xargsApplyLines(inv, &opts, count, "--max-lines/-l")
		case "max-lines-posix":
			value := nextValue(name)
			count, parseErr := parseXArgsNumber(inv, value, 'L', 1, math.MaxInt, true)
			if parseErr != nil {
				return xargsOptions{}, nil, parseErr
			}
			xargsApplyLines(inv, &opts, count, "-L")
		case "max-args":
			value := nextValue(name)
			count, parseErr := parseXArgsNumber(inv, value, 'n', 1, math.MaxInt, true)
			if parseErr != nil {
				return xargsOptions{}, nil, parseErr
			}
			xargsApplyArgs(inv, &opts, count)
		case "open-tty":
			opts.openTTY = true
		case "interactive":
			opts.interactive = true
			opts.verbose = true
		case "no-run-if-empty":
			opts.noRunIfEmpty = true
		case "max-chars":
			value := nextValue(name)
			count, parseErr := parseXArgsNumber(inv, value, 's', 1, math.MaxInt, true)
			if parseErr != nil {
				return xargsOptions{}, nil, parseErr
			}
			opts.maxChars = count
			opts.maxCharsSet = true
		case "show-limits":
			opts.showLimits = true
		case "verbose":
			opts.verbose = true
		case "exit":
			opts.exitIfTooLarge = true
		case "max-procs":
			value := nextValue(name)
			count, parseErr := parseXArgsNumber(inv, value, 'P', 0, math.MaxInt, true)
			if parseErr != nil {
				return xargsOptions{}, nil, parseErr
			}
			opts.maxProcs = count
		case "process-slot-var":
			value := nextValue(name)
			if strings.Contains(value, "=") {
				return xargsOptions{}, nil, exitf(inv, 1, "xargs: option --process-slot-var may not be set to a value which includes '='")
			}
			opts.slotVar = value
		}
	}

	return opts, matches.Args("operand"), nil
}

func parseXArgsNumber(inv *Invocation, raw string, option byte, minValue, maxValue int, fatal bool) (int, error) {
	value64, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, exitf(inv, 1, "xargs: invalid number %q for -%c option\nTry 'xargs --help' for more information.", raw, option)
	}

	if value64 < int64(minValue) {
		if !fatal {
			return minValue, nil
		}
		return 0, exitf(inv, 1, "xargs: value %s for -%c option should be >= %d\nTry 'xargs --help' for more information.", raw, option, minValue)
	}
	if maxValue >= 0 && value64 > int64(maxValue) {
		if !fatal {
			return maxValue, nil
		}
		return 0, exitf(inv, 1, "xargs: value %s for -%c option should be <= %d\nTry 'xargs --help' for more information.", raw, option, maxValue)
	}
	return int(value64), nil
}

func parseXArgsDelimiter(raw string) (byte, error) {
	if raw == "" {
		return 0, fmt.Errorf("invalid input delimiter specification %q", raw)
	}
	if len(raw) == 1 {
		return raw[0], nil
	}
	if strings.HasPrefix(raw, `\x`) {
		if len(raw) <= 2 {
			return 0, fmt.Errorf("invalid escape sequence %s in input delimiter specification", raw)
		}
		value, err := strconv.ParseUint(raw[2:], 16, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid escape sequence %s in input delimiter specification", raw)
		}
		return byte(value), nil
	}
	if raw[0] == '\\' && raw[1] >= '0' && raw[1] <= '7' {
		value, err := strconv.ParseUint(raw[1:], 8, 8)
		if err != nil {
			return 0, fmt.Errorf("invalid escape sequence %s in input delimiter specification", raw)
		}
		return byte(value), nil
	}
	if raw[0] != '\\' {
		return 0, fmt.Errorf("multibyte input delimiters are not supported")
	}
	switch raw {
	case `\a`:
		return '\a', nil
	case `\b`:
		return '\b', nil
	case `\f`:
		return '\f', nil
	case `\n`:
		return '\n', nil
	case `\r`:
		return '\r', nil
	case `\t`:
		return '\t', nil
	case `\v`:
		return '\v', nil
	case `\\`:
		return '\\', nil
	case `\0`:
		return 0, nil
	default:
		return 0, fmt.Errorf("invalid escape sequence %s in input delimiter specification", raw)
	}
}

func xargsApplyReplace(inv *Invocation, opts *xargsOptions, pattern string) {
	opts.replacePat = &pattern
	if opts.argsPerExec != 0 {
		xargsWarnMutuallyExclusive(inv, "--replace/-I/-i", "--max-args")
		opts.argsPerExec = 0
	}
	if opts.linesPerExec != 0 {
		xargsWarnMutuallyExclusive(inv, "--replace/-I/-i", "--max-lines")
		opts.linesPerExec = 0
	}
}

func xargsApplyLines(inv *Invocation, opts *xargsOptions, count int, name string) {
	opts.linesPerExec = count
	if opts.argsPerExec != 0 {
		xargsWarnMutuallyExclusive(inv, name, "--max-args")
		opts.argsPerExec = 0
	}
	if opts.replacePat != nil {
		xargsWarnMutuallyExclusive(inv, name, "--replace")
		opts.replacePat = nil
	}
}

func xargsApplyArgs(inv *Invocation, opts *xargsOptions, count int) {
	opts.argsPerExec = count
	if opts.linesPerExec != 0 {
		xargsWarnMutuallyExclusive(inv, "--max-args/-n", "--max-lines")
		opts.linesPerExec = 0
	}
	if opts.replacePat != nil {
		if count == 1 {
			opts.argsPerExec = 0
			return
		}
		xargsWarnMutuallyExclusive(inv, "--max-args/-n", "--replace")
		opts.replacePat = nil
	}
}

func xargsWarnMutuallyExclusive(inv *Invocation, option, offending string) {
	xargsWarn(inv, "warning: options %s and %s are mutually exclusive, ignoring previous %s value", offending, option, offending)
}

func xargsWarn(inv *Invocation, format string, args ...any) {
	if inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "xargs: "+format+"\n", args...)
}

func readXArgsInput(ctx context.Context, inv *Invocation, opts *xargsOptions) ([]byte, error) {
	if opts.inputFile == "-" {
		return readAllStdin(inv)
	}
	data, _, err := readAllFile(ctx, inv, opts.inputFile)
	if err != nil {
		return nil, exitf(inv, 1, "xargs: Cannot open input file %s", quoteGNUOperand(opts.inputFile))
	}
	return data, nil
}

func xargsMaxCharsLimit(env map[string]string) int {
	envSize := xargsEnvironmentSize(env)
	limit := xargsSyntheticArgMax - envSize - xargsArgHeadroom
	if limit < xargsPosixArgMin {
		return xargsPosixArgMin
	}
	return limit
}

func xargsDefaultMaxChars(env map[string]string) int {
	limit := xargsMaxCharsLimit(env)
	if limit < xargsDefaultArgBuffer {
		return limit
	}
	return xargsDefaultArgBuffer
}

func xargsEnvironmentSize(env map[string]string) int {
	total := 0
	for key, value := range env {
		total += len(key) + len(value) + 2
	}
	return total
}

func writeXArgsLimits(inv *Invocation, opts *xargsOptions, maxSupported int) error {
	if inv == nil || inv.Stderr == nil {
		return nil
	}
	usable := max(xargsSyntheticArgMax-xargsEnvironmentSize(inv.Env)-xargsArgHeadroom, 0)
	_, err := fmt.Fprintf(
		inv.Stderr,
		"Your environment variables take up %d bytes\nPOSIX upper limit on argument length (this system): %d\nPOSIX smallest allowable upper limit on argument length (all systems): %d\nMaximum length of command we could actually use: %d\nSize of command buffer we are actually using: %d\nMaximum parallelism (--max-procs must be no greater): %d\n",
		xargsEnvironmentSize(inv.Env),
		xargsSyntheticArgMax,
		xargsPosixArgMin,
		usable,
		opts.maxChars,
		math.MaxInt32,
	)
	return err
}

func parseXArgsInput(inv *Invocation, data []byte, opts *xargsOptions) xargsInputParseResult {
	if opts.readMode == xargsReadDelimited {
		return parseXArgsDelimitedInput(data, opts.delimiter)
	}
	return parseXArgsShellInput(inv, data, opts)
}

func parseXArgsDelimitedInput(data []byte, delimiter byte) xargsInputParseResult {
	if len(data) == 0 {
		return xargsInputParseResult{}
	}
	items := make([]xargsInputItem, 0, strings.Count(string(data), string([]byte{delimiter}))+1)
	start := 0
	group := 1
	for i, ch := range data {
		if ch != delimiter {
			continue
		}
		items = append(items, xargsInputItem{value: string(data[start:i]), group: group})
		group++
		start = i + 1
	}
	if start < len(data) {
		items = append(items, xargsInputItem{value: string(data[start:]), group: group})
	}
	return xargsInputParseResult{items: items}
}

func parseXArgsShellInput(inv *Invocation, data []byte, opts *xargsOptions) xargsInputParseResult {
	const (
		xargsStateSpace = iota
		xargsStateNorm
		xargsStateQuote
		xargsStateBackslash
	)

	var (
		items       []xargsInputItem
		buf         []byte
		state       = xargsStateSpace
		quote       byte
		firstOnLine = true
		seenArg     bool
		group       = 1
		prev        byte
		prevSet     bool
		nullWarned  bool
	)

	appendToken := func(token string) {
		items = append(items, xargsInputItem{value: token, group: group})
	}

	for i := 0; ; i++ {
		atEOF := i >= len(data)
		var c byte
		if !atEOF {
			c = data[i]
		}

		switch state {
		case xargsStateSpace:
			if atEOF {
				return xargsInputParseResult{items: items}
			}
			if xargsIsSpace(c) {
				prev = c
				prevSet = true
				continue
			}
			state = xargsStateNorm
		case xargsStateQuote:
			if atEOF || c == '\n' {
				kind := "single"
				if quote == '"' {
					kind = "double"
				}
				return xargsInputParseResult{
					items:   items,
					errText: fmt.Sprintf("xargs: unmatched %s quote; by default quotes are special to xargs unless you use the -0 option", kind),
				}
			}
			if c == quote {
				state = xargsStateNorm
				seenArg = true
				prev = c
				prevSet = true
				continue
			}
			if c == 0 {
				if !nullWarned {
					xargsWarn(inv, "WARNING: a NUL character occurred in the input.  It cannot be passed through in the argument list.  Did you mean to use the --null option?")
					nullWarned = true
				}
				prev = c
				prevSet = true
				continue
			}
			buf = append(buf, c)
			prev = c
			prevSet = true
			continue
		case xargsStateBackslash:
			state = xargsStateNorm
			if atEOF {
				token := string(buf)
				if xargsMatchesEOF(opts.eofStr, token) {
					if firstOnLine {
						return xargsInputParseResult{items: items}
					}
					appendToken(token)
					return xargsInputParseResult{items: items}
				}
				if len(buf) > 0 || seenArg {
					appendToken(token)
				}
				return xargsInputParseResult{items: items}
			}
			if c == 0 {
				if !nullWarned {
					xargsWarn(inv, "WARNING: a NUL character occurred in the input.  It cannot be passed through in the argument list.  Did you mean to use the --null option?")
					nullWarned = true
				}
				prev = c
				prevSet = true
				continue
			}
			buf = append(buf, c)
			seenArg = true
			prev = c
			prevSet = true
			continue
		}

		if atEOF || c == '\n' {
			lineEnded := !atEOF && prevSet && !xargsIsBlank(prev)
			if len(buf) == 0 {
				switch {
				case seenArg:
					if xargsMatchesEOF(opts.eofStr, "") {
						if firstOnLine {
							return xargsInputParseResult{items: items}
						}
						appendToken("")
						return xargsInputParseResult{items: items}
					}
					appendToken("")
				case atEOF:
					return xargsInputParseResult{items: items}
				default:
					state = xargsStateSpace
					firstOnLine = true
					seenArg = false
					prev = c
					prevSet = true
					continue
				}
			} else {
				token := string(buf)
				buf = buf[:0]
				if xargsMatchesEOF(opts.eofStr, token) {
					if firstOnLine {
						return xargsInputParseResult{items: items}
					}
					appendToken(token)
					return xargsInputParseResult{items: items}
				}
				appendToken(token)
			}
			if lineEnded {
				group++
			}
			state = xargsStateSpace
			firstOnLine = true
			seenArg = false
			if atEOF {
				return xargsInputParseResult{items: items}
			}
			prev = c
			prevSet = true
			continue
		}

		if opts.replacePat == nil && xargsIsBlank(c) {
			token := string(buf)
			buf = buf[:0]
			if xargsMatchesEOF(opts.eofStr, token) {
				if firstOnLine {
					return xargsInputParseResult{items: items}
				}
				appendToken(token)
				return xargsInputParseResult{items: items}
			}
			appendToken(token)
			state = xargsStateSpace
			firstOnLine = false
			prev = c
			prevSet = true
			continue
		}

		switch c {
		case '\\':
			state = xargsStateBackslash
			prev = c
			prevSet = true
			continue
		case '\'', '"':
			state = xargsStateQuote
			quote = c
			prev = c
			prevSet = true
			continue
		}

		if c == 0 {
			if !nullWarned {
				xargsWarn(inv, "WARNING: a NUL character occurred in the input.  It cannot be passed through in the argument list.  Did you mean to use the --null option?")
				nullWarned = true
			}
			prev = c
			prevSet = true
			continue
		}

		buf = append(buf, c)
		seenArg = true
		prev = c
		prevSet = true
	}
}

func xargsIsSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func xargsIsBlank(ch byte) bool {
	return ch == ' ' || ch == '\t'
}

func xargsMatchesEOF(eofStr *string, token string) bool {
	return eofStr != nil && token == *eofStr
}

func buildXArgsTasks(inv *Invocation, opts *xargsOptions, command []string, items []xargsInputItem) ([]xargsTask, error) {
	if opts.replacePat != nil {
		tasks := make([]xargsTask, 0, len(items))
		for _, item := range items {
			argv := replaceXArgsPlaceholders(command, *opts.replacePat, item.value)
			if xargsArgvSize(argv) > opts.maxChars {
				return nil, exitf(inv, 1, "xargs: argument line too long")
			}
			tasks = append(tasks, xargsTask{argv: argv})
		}
		if len(tasks) == 0 && !opts.noRunIfEmpty {
			argv := replaceXArgsPlaceholders(command, *opts.replacePat, "")
			if xargsArgvSize(argv) > opts.maxChars {
				return nil, exitf(inv, 1, "xargs: argument line too long")
			}
			tasks = append(tasks, xargsTask{argv: argv})
		}
		return tasks, nil
	}

	if len(items) == 0 {
		if opts.noRunIfEmpty {
			return nil, nil
		}
		if xargsArgvSize(command) > opts.maxChars {
			return nil, exitf(inv, 1, "xargs: argument line too long")
		}
		return []xargsTask{{argv: append([]string(nil), command...)}}, nil
	}

	tasks := make([]xargsTask, 0)
	batch := make([]string, 0)
	lineCount := 0
	lastGroup := 0

	flush := func() {
		if len(batch) == 0 {
			return
		}
		argv := append(append([]string(nil), command...), batch...)
		tasks = append(tasks, xargsTask{argv: argv})
		batch = batch[:0]
		lineCount = 0
		lastGroup = 0
	}

	for _, item := range items {
		if opts.linesPerExec > 0 && len(batch) > 0 && item.group != lastGroup && lineCount >= opts.linesPerExec {
			flush()
		}
		if opts.argsPerExec > 0 && len(batch) >= opts.argsPerExec {
			flush()
		}

		candidateBatch := append(append([]string(nil), batch...), item.value)
		candidateArgv := append(append([]string(nil), command...), candidateBatch...)
		if xargsArgvSize(candidateArgv) > opts.maxChars {
			if len(batch) == 0 || opts.exitIfTooLarge {
				return nil, exitf(inv, 1, "xargs: argument line too long")
			}
			flush()
			candidateBatch = []string{item.value}
			candidateArgv = append(append([]string(nil), command...), candidateBatch...)
			if xargsArgvSize(candidateArgv) > opts.maxChars {
				return nil, exitf(inv, 1, "xargs: argument line too long")
			}
		}

		if len(batch) == 0 {
			lineCount = 1
			lastGroup = item.group
		} else if item.group != lastGroup {
			lineCount++
			lastGroup = item.group
		}
		batch = append(batch, item.value)
	}

	flush()
	return tasks, nil
}

func replaceXArgsPlaceholders(args []string, placeholder, item string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = strings.ReplaceAll(arg, placeholder, item)
	}
	return out
}

func xargsArgvSize(argv []string) int {
	total := 0
	for _, arg := range argv {
		total += len(arg) + 1
	}
	return total
}

func runXArgsTasks(ctx context.Context, inv *Invocation, opts *xargsOptions, tasks []xargsTask) (int, error) {
	if len(tasks) == 0 {
		return 0, nil
	}
	if opts.maxProcs <= 1 || len(tasks) == 1 {
		return runXArgsTasksSequential(ctx, inv, opts, tasks)
	}
	return runXArgsTasksParallel(ctx, inv, opts, tasks)
}

func runXArgsTasksSequential(ctx context.Context, inv *Invocation, opts *xargsOptions, tasks []xargsTask) (int, error) {
	status := 0
	for _, task := range tasks {
		if opts.interactive {
			run, err := confirmXArgsTask(ctx, inv, task.argv)
			if err != nil {
				return 0, err
			}
			if !run {
				continue
			}
		}
		result, err := runXArgsTask(ctx, inv, opts, task, 0)
		if err != nil {
			return 0, err
		}
		status = max(status, xargsStatusForExecResult(&result))
		if err := writeXArgsExecOutputs(inv, opts, result); err != nil {
			return 0, err
		}
		if result.exitCode == 255 || status >= xargsExitNotFound {
			break
		}
	}
	return status, nil
}

func runXArgsTasksParallel(ctx context.Context, inv *Invocation, opts *xargsOptions, tasks []xargsTask) (int, error) {
	// The current runtime subexec path is not safe for concurrent inv.Exec calls,
	// so in the sandbox we preserve -P parsing and batching behavior but execute
	// the resulting commands serially.
	return runXArgsTasksSequential(ctx, inv, opts, tasks)
}

func confirmXArgsTask(ctx context.Context, inv *Invocation, argv []string) (bool, error) {
	reader, closer, err := xargsPromptReader(ctx, inv)
	if err != nil {
		return false, err
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	if _, err := fmt.Fprintf(inv.Stderr, "%s ?...", shellJoinArgs(argv)); err != nil {
		return false, &ExitError{Code: 1, Err: err}
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		return false, exitf(inv, 1, "xargs: Failed to read from standard input")
	}
	if line == "" {
		return false, nil
	}
	switch line[0] {
	case 'y', 'Y':
		return true, nil
	default:
		return false, nil
	}
}

func xargsPromptReader(ctx context.Context, inv *Invocation) (io.Reader, io.Closer, error) {
	file, _, err := openRead(ctx, inv, "/dev/tty")
	if err == nil {
		return file, file, nil
	}
	if inv.Stdin != nil {
		return inv.Stdin, nil, nil
	}
	return nil, nil, exitf(inv, 1, "xargs: failed to open /dev/tty for reading")
}

func runXArgsTask(ctx context.Context, inv *Invocation, opts *xargsOptions, task xargsTask, slot int) (xargsExecResult, error) {
	result := xargsExecResult{
		slot: slot,
		argv: append([]string(nil), task.argv...),
	}
	if len(task.argv) == 0 {
		return result, nil
	}

	env := cloneEnv(inv.Env)
	if opts.slotVar != "" {
		if env == nil {
			env = make(map[string]string, 1)
		}
		env[opts.slotVar] = strconv.Itoa(slot)
	}

	stdin, closer, err := xargsChildStdin(ctx, inv, opts)
	if err != nil {
		return result, err
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	if _, ok, err := resolveCommand(ctx, inv, env, inv.Cwd, task.argv[0]); err != nil {
		return result, &ExitError{Code: exitCodeForError(err), Err: err}
	} else if !ok {
		result.exitCode = xargsExitNotFound
		result.stderr = fmt.Sprintf("xargs: %s: No such file or directory\n", task.argv[0])
		return result, nil
	}

	execResult, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    task.argv,
		Env:     env,
		WorkDir: inv.Cwd,
		Stdin:   stdin,
	})
	if err != nil {
		code := exitCodeForError(err)
		return result, &ExitError{Code: code, Err: err}
	}
	if execResult != nil {
		result.stdout = execResult.Stdout
		result.stderr = execResult.Stderr
		result.exitCode = execResult.ExitCode
	}
	return result, nil
}

func xargsChildStdin(ctx context.Context, inv *Invocation, opts *xargsOptions) (io.Reader, io.Closer, error) {
	if opts.openTTY {
		file, _, err := openRead(ctx, inv, "/dev/tty")
		if err != nil {
			return nil, nil, exitf(inv, 1, "xargs: %v", err)
		}
		return file, file, nil
	}
	if opts.keepStdin {
		return inv.Stdin, nil, nil
	}
	return strings.NewReader(""), nil, nil
}

func xargsStatusForExecResult(result *xargsExecResult) int {
	switch {
	case result.exitCode == 0:
		return 0
	case result.exitCode == 255:
		return xargsExitCommand255
	case result.exitCode == xargsExitNotFound:
		return xargsExitNotFound
	case result.exitCode > 128:
		return xargsExitCommandSignal
	default:
		return xargsExitCommandNonzero
	}
}

func writeXArgsExecOutputs(inv *Invocation, opts *xargsOptions, result xargsExecResult) error {
	if opts.verbose {
		if _, err := fmt.Fprintln(inv.Stderr, shellJoinArgs(result.argv)); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if result.stdout != "" {
		if _, err := fmt.Fprint(inv.Stdout, result.stdout); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if result.stderr != "" {
		if _, err := fmt.Fprint(inv.Stderr, result.stderr); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if result.exitCode == 255 {
		if _, err := fmt.Fprintf(inv.Stderr, "xargs: %s: exited with status 255; aborting\n", result.argv[0]); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

var _ Command = (*XArgs)(nil)
