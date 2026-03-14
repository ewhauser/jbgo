package builtins

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ewhauser/gbash/policy"
)

type Csplit struct{}

type csplitOptions struct {
	splitName       csplitSplitName
	keepFiles       bool
	quiet           bool
	elideEmptyFiles bool
	suppressMatched bool
}

type csplitPatternKind int

const (
	csplitPatternUpToLine csplitPatternKind = iota
	csplitPatternUpToMatch
	csplitPatternSkipToMatch
)

type csplitPattern struct {
	kind    csplitPatternKind
	line    int
	regex   *regexp.Regexp
	offset  int
	execute csplitExecutePattern
}

func (p csplitPattern) String() string {
	switch p.kind {
	case csplitPatternUpToLine:
		return strconv.Itoa(p.line)
	case csplitPatternUpToMatch:
		if p.offset == 0 {
			return "/" + p.regex.String() + "/"
		}
		return fmt.Sprintf("/%s/%+d", p.regex.String(), p.offset)
	case csplitPatternSkipToMatch:
		if p.offset == 0 {
			return "%" + p.regex.String() + "%"
		}
		return fmt.Sprintf("%%%s%%%+d", p.regex.String(), p.offset)
	default:
		return ""
	}
}

type csplitExecutePattern struct {
	always bool
	times  int
}

type csplitSplitName struct {
	prefix string
	format csplitSuffixFormat
}

type csplitSuffixFormat struct {
	before string
	after  string
	flags  csplitSuffixFlags
	width  int
	hasW   bool
	prec   int
	hasP   bool
	verb   byte
}

type csplitSuffixFlags struct {
	alt   bool
	zero  bool
	left  bool
	plus  bool
	space bool
}

type csplitGeneratedSplit struct {
	name string
	data []byte
}

type csplitSplitWriter struct {
	inv     *Invocation
	options *csplitOptions
	counter int
	current *bytes.Buffer
	name    string
	size    int
	devNull bool
	outputs []csplitGeneratedSplit
}

type csplitBufferedLine struct {
	ln   int
	line string
}

type csplitInputSplitter struct {
	lines  []string
	index  int
	buffer []csplitBufferedLine
	size   int
	rewind bool
}

type csplitErrorKind int

const (
	csplitErrLineOutOfRange csplitErrorKind = iota
	csplitErrLineOutOfRangeOnRepetition
	csplitErrMatchNotFound
	csplitErrMatchNotFoundOnRepetition
	csplitErrLineNumberZero
	csplitErrLineNumberSmallerThanPrevious
	csplitErrInvalidPattern
	csplitErrInvalidNumber
	csplitErrSuffixFormatIncorrect
	csplitErrSuffixFormatTooManyPercents
)

type csplitError struct {
	kind       csplitErrorKind
	pattern    string
	repetition int
	current    int
	previous   int
	number     string
}

func (e *csplitError) Error() string {
	switch e.kind {
	case csplitErrLineOutOfRange:
		return fmt.Sprintf("%s: line number out of range", quoteGNUOperand(e.pattern))
	case csplitErrLineOutOfRangeOnRepetition:
		return fmt.Sprintf("%s: line number out of range on repetition %d", quoteGNUOperand(e.pattern), e.repetition)
	case csplitErrMatchNotFound:
		return fmt.Sprintf("%s: match not found", quoteGNUOperand(e.pattern))
	case csplitErrMatchNotFoundOnRepetition:
		return fmt.Sprintf("%s: match not found on repetition %d", quoteGNUOperand(e.pattern), e.repetition)
	case csplitErrLineNumberZero:
		return "0: line number must be greater than zero"
	case csplitErrLineNumberSmallerThanPrevious:
		return fmt.Sprintf("line number '%d' is smaller than preceding line number, %d", e.current, e.previous)
	case csplitErrInvalidPattern:
		return fmt.Sprintf("%s: invalid pattern", quoteGNUOperand(e.pattern))
	case csplitErrInvalidNumber:
		return fmt.Sprintf("invalid number: %s", quoteGNUOperand(e.number))
	case csplitErrSuffixFormatIncorrect:
		return "incorrect conversion specification in suffix"
	case csplitErrSuffixFormatTooManyPercents:
		return "too many % conversion specifications in suffix"
	default:
		return "unknown error"
	}
}

func NewCsplit() *Csplit {
	return &Csplit{}
}

func (c *Csplit) Name() string {
	return "csplit"
}

func (c *Csplit) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Csplit) Spec() CommandSpec {
	return CommandSpec{
		Name:      "csplit",
		About:     "Split a file into sections determined by context lines.",
		Usage:     "csplit [OPTION]... FILE PATTERN...",
		AfterHelp: "Output pieces of FILE separated by PATTERN(s) to files 'xx00', 'xx01', ..., and output byte counts of each piece to standard output.",
		Options: []OptionSpec{
			{Name: "suffix-format", Short: 'b', Long: "suffix-format", Arity: OptionRequiredValue, ValueName: "FORMAT", Help: "use sprintf FORMAT instead of %02d"},
			{Name: "prefix", Short: 'f', Long: "prefix", Arity: OptionRequiredValue, ValueName: "PREFIX", Help: "use PREFIX instead of 'xx'"},
			{Name: "keep-files", Short: 'k', Long: "keep-files", Help: "do not remove output files on errors"},
			{Name: "suppress-matched", Long: "suppress-matched", Help: "suppress the lines matching PATTERN"},
			{Name: "digits", Short: 'n', Long: "digits", Arity: OptionRequiredValue, ValueName: "DIGITS", Help: "use specified number of digits instead of 2"},
			{Name: "quiet", Short: 'q', ShortAliases: []rune{'s'}, Long: "quiet", HelpAliases: []string{"silent"}, Help: "do not print counts of output file sizes"},
			{Name: "elide-empty-files", Short: 'z', Long: "elide-empty-files", Help: "remove empty output files"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Required: true},
			{Name: "pattern", ValueName: "PATTERN", Required: true, Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ContinueShortGroupValues: true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Csplit) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := parseCsplitOptions(matches)
	if err != nil {
		return exitf(inv, 1, "csplit: %s", err)
	}

	lines, err := readCsplitLines(ctx, inv, matches.Arg("file"))
	if err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			return err
		}
		return exitf(inv, 1, "csplit: %s", err)
	}

	patterns, err := parseCsplitPatterns(inv, matches.Args("pattern"))
	if err != nil {
		return exitf(inv, 1, "csplit: %s", err)
	}

	splitter := newCsplitInputSplitter(lines)
	writer := newCsplitSplitWriter(inv, &opts)
	allUpToLine := true
	for _, pattern := range patterns {
		if pattern.kind != csplitPatternUpToLine {
			allUpToLine = false
			break
		}
	}

	runErr := runCsplitPatterns(writer, splitter, patterns)
	if runErr == nil {
		splitter.rewindBuffer()
		if _, firstLine, ok := splitter.next(); ok {
			if err := writer.newWriter(); err != nil {
				return err
			}
			if err := writer.writeln(firstLine); err != nil {
				return err
			}
			for {
				_, line, ok := splitter.next()
				if !ok {
					break
				}
				if err := writer.writeln(line); err != nil {
					return err
				}
			}
			if err := writer.finishSplit(); err != nil {
				return err
			}
		} else if allUpToLine && opts.suppressMatched {
			if err := writer.newWriter(); err != nil {
				return err
			}
			if err := writer.finishSplit(); err != nil {
				return err
			}
		}
	}

	if runErr != nil && !opts.keepFiles {
		return exitf(inv, 1, "csplit: %s", runErr)
	}

	created, writeErr := writeCsplitOutputs(ctx, inv, writer.outputs)
	if writeErr != nil {
		if !opts.keepFiles {
			removeCsplitOutputs(ctx, inv, created)
		}
		return exitf(inv, 1, "csplit: %s", writeErr)
	}
	if runErr != nil {
		return exitf(inv, 1, "csplit: %s", runErr)
	}
	return nil
}

func parseCsplitOptions(matches *ParsedCommand) (csplitOptions, error) {
	digits := 2
	if value := matches.Value("digits"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return csplitOptions{}, &csplitError{kind: csplitErrInvalidNumber, number: value}
		}
		digits = n
	}

	suffixFormat, err := parseCsplitSuffixFormat(matches.Value("suffix-format"), digits)
	if err != nil {
		return csplitOptions{}, err
	}

	return csplitOptions{
		splitName: csplitSplitName{
			prefix: defaultString(matches.Value("prefix"), "xx"),
			format: suffixFormat,
		},
		keepFiles:       matches.Has("keep-files"),
		quiet:           matches.Has("quiet"),
		elideEmptyFiles: matches.Has("elide-empty-files"),
		suppressMatched: matches.Has("suppress-matched"),
	}, nil
}

func readCsplitLines(ctx context.Context, inv *Invocation, name string) ([]string, error) {
	var reader io.Reader
	if name == "-" {
		reader = inv.Stdin
	} else {
		info, _, exists, err := statMaybe(ctx, inv, policy.FileActionStat, name)
		if err == nil && exists && info.IsDir() {
			return nil, errors.New("read error: Is a directory")
		}

		file, _, err := openRead(ctx, inv, name)
		if err != nil {
			return nil, fmt.Errorf("cannot open %s for reading: %s", quoteGNUOperand(name), csplitIOMessage(err))
		}
		defer func() { _ = file.Close() }()
		reader = file
	}

	buf := bufio.NewReader(reader)
	lines := make([]string, 0, 64)
	for {
		line, err := buf.ReadBytes('\n')
		if len(line) > 0 {
			if !utf8.Valid(line) {
				return nil, errors.New("read error: stream did not contain valid UTF-8")
			}
			lines = append(lines, string(line))
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		return nil, fmt.Errorf("read error: %s", csplitIOMessage(err))
	}
	return lines, nil
}

func parseCsplitPatterns(inv *Invocation, args []string) ([]csplitPattern, error) {
	patterns, err := extractCsplitPatterns(args)
	if err != nil {
		return nil, err
	}
	prev := 0
	for _, pattern := range patterns {
		if pattern.kind != csplitPatternUpToLine {
			continue
		}
		switch {
		case pattern.line == 0:
			return nil, &csplitError{kind: csplitErrLineNumberZero}
		case pattern.line == prev:
			if _, warnErr := fmt.Fprintf(inv.Stderr, "csplit: warning: line number '%d' is the same as preceding line number\n", pattern.line); warnErr != nil {
				return nil, &ExitError{Code: 1, Err: warnErr}
			}
			prev = pattern.line
		case pattern.line < prev:
			return nil, &csplitError{kind: csplitErrLineNumberSmallerThanPrevious, current: pattern.line, previous: prev}
		default:
			prev = pattern.line
		}
	}
	return patterns, nil
}

func extractCsplitPatterns(args []string) ([]csplitPattern, error) {
	patterns := make([]csplitPattern, 0, len(args))
	for i := 0; i < len(args); i++ {
		execute := csplitExecutePattern{times: 1}
		if i+1 < len(args) {
			if next, ok := parseCsplitQuantifier(args[i+1]); ok {
				execute = next
				i++
			}
		}

		pattern, err := parseCsplitPattern(args[i], execute)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

func parseCsplitQuantifier(arg string) (csplitExecutePattern, bool) {
	if len(arg) < 3 || arg[0] != '{' || arg[len(arg)-1] != '}' {
		return csplitExecutePattern{}, false
	}
	body := arg[1 : len(arg)-1]
	if body == "*" {
		return csplitExecutePattern{always: true}, true
	}
	n, err := strconv.Atoi(body)
	if err != nil || n < 0 {
		return csplitExecutePattern{}, false
	}
	return csplitExecutePattern{times: n + 1}, true
}

func parseCsplitPattern(arg string, execute csplitExecutePattern) (csplitPattern, error) {
	if line, err := strconv.Atoi(arg); err == nil {
		return csplitPattern{
			kind:    csplitPatternUpToLine,
			line:    line,
			execute: execute,
		}, nil
	}

	if len(arg) >= 3 && (arg[0] == '/' || arg[0] == '%') {
		delim := arg[0]
		end := strings.LastIndexByte(arg[1:], delim)
		if end >= 0 {
			end++
			body := arg[1:end]
			offsetRaw := arg[end+1:]
			offset := 0
			if offsetRaw != "" {
				value, err := strconv.Atoi(offsetRaw)
				if err != nil {
					return csplitPattern{}, &csplitError{kind: csplitErrInvalidPattern, pattern: arg}
				}
				offset = value
			}
			re, err := regexp.Compile(body)
			if err != nil {
				return csplitPattern{}, &csplitError{kind: csplitErrInvalidPattern, pattern: arg}
			}

			kind := csplitPatternUpToMatch
			if delim == '%' {
				kind = csplitPatternSkipToMatch
			}
			return csplitPattern{
				kind:    kind,
				regex:   re,
				offset:  offset,
				execute: execute,
			}, nil
		}
	}

	return csplitPattern{}, &csplitError{kind: csplitErrInvalidPattern, pattern: arg}
}

func runCsplitPatterns(writer *csplitSplitWriter, splitter *csplitInputSplitter, patterns []csplitPattern) error {
	for _, pattern := range patterns {
		patternText := pattern.String()
		switch pattern.kind {
		case csplitPatternUpToLine:
			upToLine := pattern.line
			limit, iter := csplitExecutionState(pattern.execute)
			for ith := 1; ; ith++ {
				if limit >= 0 && ith > limit {
					break
				}
				if err := writer.newWriter(); err != nil {
					return err
				}
				err := writer.doToLine(patternText, upToLine, splitter)
				if err != nil {
					if csplitErrorIs(err, csplitErrLineOutOfRange) && ith != 1 {
						return &csplitError{kind: csplitErrLineOutOfRangeOnRepetition, pattern: patternText, repetition: ith - 1}
					}
					return err
				}
				upToLine += pattern.line
				if !iter {
					break
				}
			}
		case csplitPatternUpToMatch, csplitPatternSkipToMatch:
			limit, iter := csplitExecutionState(pattern.execute)
			for ith := 1; ; ith++ {
				if limit >= 0 && ith > limit {
					break
				}
				if pattern.kind == csplitPatternSkipToMatch {
					writer.asDevNull()
				} else if err := writer.newWriter(); err != nil {
					return err
				}
				err := writer.doToMatch(patternText, pattern.regex, pattern.offset, splitter)
				if err != nil {
					if csplitErrorIs(err, csplitErrMatchNotFound) && pattern.execute.always {
						return nil
					}
					if csplitErrorIs(err, csplitErrMatchNotFound) && limit != 1 && limit >= 0 && ith != 1 {
						return &csplitError{kind: csplitErrMatchNotFoundOnRepetition, pattern: patternText, repetition: ith - 1}
					}
					return err
				}
				if !iter {
					break
				}
			}
		}
	}
	return nil
}

func csplitExecutionState(exec csplitExecutePattern) (limit int, iterate bool) {
	if exec.always {
		return -1, true
	}
	return exec.times, exec.times != 1
}

func newCsplitSplitWriter(inv *Invocation, options *csplitOptions) *csplitSplitWriter {
	return &csplitSplitWriter{
		inv:     inv,
		options: options,
		outputs: make([]csplitGeneratedSplit, 0, 8),
	}
}

func (w *csplitSplitWriter) newWriter() error {
	w.name = w.options.splitName.get(w.counter)
	w.current = &bytes.Buffer{}
	w.counter++
	w.size = 0
	w.devNull = false
	return nil
}

func (w *csplitSplitWriter) asDevNull() {
	w.current = nil
	w.name = ""
	w.size = 0
	w.devNull = true
}

func (w *csplitSplitWriter) writeln(line string) error {
	if w.devNull {
		return nil
	}
	if w.current == nil {
		return &ExitError{Code: 1, Err: errors.New("csplit internal error: split not created")}
	}
	if _, err := w.current.WriteString(line); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	w.size += len(line)
	return nil
}

func (w *csplitSplitWriter) finishSplit() error {
	if w.devNull {
		return nil
	}
	if w.current == nil {
		return nil
	}
	if w.options.elideEmptyFiles && w.size == 0 {
		w.counter--
		w.current = nil
		w.name = ""
		w.size = 0
		return nil
	}
	if !w.options.quiet {
		if _, err := fmt.Fprintf(w.inv.Stdout, "%d\n", w.size); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	w.outputs = append(w.outputs, csplitGeneratedSplit{
		name: w.name,
		data: append([]byte(nil), w.current.Bytes()...),
	})
	w.current = nil
	w.name = ""
	w.size = 0
	return nil
}

func (w *csplitSplitWriter) doToLine(pattern string, n int, input *csplitInputSplitter) error {
	input.rewindBuffer()
	input.setSizeOfBuffer(1)

	ret := &csplitError{kind: csplitErrLineOutOfRange, pattern: pattern}
	for {
		ln, line, ok := input.next()
		if !ok {
			break
		}
		switch {
		case n <= ln:
			_, _ = input.addLineToBuffer(ln, line)
			ret = nil
			goto done
		case n == ln+1:
			if !w.options.suppressMatched {
				_, _ = input.addLineToBuffer(ln, line)
			}
			ret = nil
			goto done
		}
		if err := w.writeln(line); err != nil {
			return err
		}
	}

done:
	if err := w.finishSplit(); err != nil {
		return err
	}
	if ret == nil {
		return nil
	}
	return ret
}

func (w *csplitSplitWriter) doToMatch(pattern string, re *regexp.Regexp, offset int, input *csplitInputSplitter) error {
	if offset >= 0 {
		for _, line := range input.drainBuffer() {
			if err := w.writeln(line); err != nil {
				return err
			}
		}
		input.setSizeOfBuffer(1)

		for {
			ln, line, ok := input.next()
			if !ok {
				break
			}
			if re.MatchString(csplitLineForMatch(line)) {
				nextLineSuppressMatched := false
				switch {
				case !w.options.suppressMatched && offset == 0:
					_, _ = input.addLineToBuffer(ln, line)
				case !w.options.suppressMatched:
					if err := w.writeln(line); err != nil {
						return err
					}
				case offset > 0:
					nextLineSuppressMatched = true
					if err := w.writeln(line); err != nil {
						return err
					}
				}
				offset--
				for offset > 0 {
					_, extraLine, ok := input.next()
					if !ok {
						if err := w.finishSplit(); err != nil {
							return err
						}
						return &csplitError{kind: csplitErrLineOutOfRange, pattern: pattern}
					}
					if err := w.writeln(extraLine); err != nil {
						return err
					}
					offset--
				}
				if err := w.finishSplit(); err != nil {
					return err
				}
				if nextLineSuppressMatched {
					input.next()
				}
				return nil
			}
			if err := w.writeln(line); err != nil {
				return err
			}
		}
	} else {
		offsetSize := -offset
		input.setSizeOfBuffer(offsetSize)
		for {
			ln, line, ok := input.next()
			if !ok {
				break
			}
			if re.MatchString(csplitLineForMatch(line)) {
				for _, pending := range input.shrinkBufferToSize() {
					if err := w.writeln(pending); err != nil {
						return err
					}
				}
				if w.options.suppressMatched {
					_, _ = input.addLineToBuffer(ln, line)
				} else {
					input.setSizeOfBuffer(offsetSize + 1)
					_, _ = input.addLineToBuffer(ln, line)
				}
				if err := w.finishSplit(); err != nil {
					return err
				}
				if input.bufferLen() < offsetSize {
					return &csplitError{kind: csplitErrLineOutOfRange, pattern: pattern}
				}
				return nil
			}
			if evicted, ok := input.addLineToBuffer(ln, line); ok {
				if err := w.writeln(evicted); err != nil {
					return err
				}
			}
		}
		for _, pending := range input.drainBuffer() {
			if err := w.writeln(pending); err != nil {
				return err
			}
		}
	}

	if err := w.finishSplit(); err != nil {
		return err
	}
	return &csplitError{kind: csplitErrMatchNotFound, pattern: pattern}
}

func newCsplitInputSplitter(lines []string) *csplitInputSplitter {
	return &csplitInputSplitter{
		lines:  lines,
		buffer: make([]csplitBufferedLine, 0, 4),
		size:   1,
	}
}

func (s *csplitInputSplitter) rewindBuffer() {
	s.rewind = true
}

func (s *csplitInputSplitter) shrinkBufferToSize() []string {
	if len(s.buffer) <= s.size {
		return nil
	}
	count := len(s.buffer) - s.size
	lines := make([]string, count)
	for i := range count {
		lines[i] = s.buffer[i].line
	}
	s.buffer = append([]csplitBufferedLine(nil), s.buffer[count:]...)
	return lines
}

func (s *csplitInputSplitter) drainBuffer() []string {
	if len(s.buffer) == 0 {
		return nil
	}
	lines := make([]string, len(s.buffer))
	for i, item := range s.buffer {
		lines[i] = item.line
	}
	s.buffer = s.buffer[:0]
	return lines
}

func (s *csplitInputSplitter) setSizeOfBuffer(size int) {
	s.size = size
}

func (s *csplitInputSplitter) addLineToBuffer(ln int, line string) (string, bool) {
	if s.rewind {
		s.buffer = append([]csplitBufferedLine{{ln: ln, line: line}}, s.buffer...)
		return "", false
	}
	if len(s.buffer) >= s.size {
		head := s.buffer[0].line
		s.buffer = append(s.buffer[1:], csplitBufferedLine{ln: ln, line: line})
		return head, true
	}
	s.buffer = append(s.buffer, csplitBufferedLine{ln: ln, line: line})
	return "", false
}

func (s *csplitInputSplitter) bufferLen() int {
	return len(s.buffer)
}

func (s *csplitInputSplitter) next() (ln int, line string, ok bool) {
	if s.rewind {
		if len(s.buffer) > 0 {
			item := s.buffer[0]
			s.buffer = s.buffer[1:]
			return item.ln, item.line, true
		}
		s.rewind = false
	}
	if s.index >= len(s.lines) {
		return 0, "", false
	}
	ln = s.index
	line = s.lines[s.index]
	s.index++
	return ln, line, true
}

func writeCsplitOutputs(ctx context.Context, inv *Invocation, outputs []csplitGeneratedSplit) ([]string, error) {
	created := make([]string, 0, len(outputs))
	for _, output := range outputs {
		abs := inv.FS.Resolve(output.name)
		parent := path.Dir(abs)
		info, err := inv.FS.Stat(ctx, parent)
		if err != nil {
			return created, fmt.Errorf("%s: %s", output.name, csplitIOMessage(err))
		}
		if !info.IsDir() {
			return created, fmt.Errorf("%s: Not a directory", output.name)
		}

		file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return created, fmt.Errorf("%s: %s", output.name, csplitIOMessage(err))
		}
		if _, err := file.Write(output.data); err != nil {
			_ = file.Close()
			return created, fmt.Errorf("%s: %s", output.name, csplitIOMessage(err))
		}
		if err := file.Close(); err != nil {
			return created, fmt.Errorf("%s: %s", output.name, csplitIOMessage(err))
		}
		recordFileMutation(inv.TraceRecorder(), "write", abs, abs, abs)
		created = append(created, abs)
	}
	return created, nil
}

func removeCsplitOutputs(ctx context.Context, inv *Invocation, files []string) {
	for _, file := range files {
		_ = inv.FS.Remove(ctx, file, false)
	}
}

func parseCsplitSuffixFormat(raw string, digits int) (csplitSuffixFormat, error) {
	if raw == "" {
		raw = fmt.Sprintf("%%0%du", digits)
	}

	var out csplitSuffixFormat
	i := 0
	specCount := 0
	var literal strings.Builder

	for i < len(raw) {
		if raw[i] != '%' {
			literal.WriteByte(raw[i])
			i++
			continue
		}
		if i+1 < len(raw) && raw[i+1] == '%' {
			literal.WriteByte('%')
			i += 2
			continue
		}

		specCount++
		if specCount > 1 {
			return csplitSuffixFormat{}, &csplitError{kind: csplitErrSuffixFormatTooManyPercents}
		}
		out.before = literal.String()
		literal.Reset()
		i++

		for i < len(raw) {
			switch raw[i] {
			case '#':
				out.flags.alt = true
			case '0':
				out.flags.zero = true
			case '-':
				out.flags.left = true
			case '+':
				out.flags.plus = true
			case ' ':
				out.flags.space = true
			default:
				goto width
			}
			i++
		}

	width:
		if i < len(raw) && csplitIsASCIIDigit(raw[i]) {
			start := i
			for i < len(raw) && csplitIsASCIIDigit(raw[i]) {
				i++
			}
			value, _ := strconv.Atoi(raw[start:i])
			out.width = value
			out.hasW = true
		}
		if i < len(raw) && raw[i] == '.' {
			i++
			start := i
			for i < len(raw) && csplitIsASCIIDigit(raw[i]) {
				i++
			}
			if start == i {
				return csplitSuffixFormat{}, &csplitError{kind: csplitErrSuffixFormatIncorrect}
			}
			value, _ := strconv.Atoi(raw[start:i])
			out.prec = value
			out.hasP = true
		}
		if i >= len(raw) {
			return csplitSuffixFormat{}, &csplitError{kind: csplitErrSuffixFormatIncorrect}
		}
		switch raw[i] {
		case 'd', 'i', 'u', 'o', 'x', 'X':
			out.verb = raw[i]
		default:
			return csplitSuffixFormat{}, &csplitError{kind: csplitErrSuffixFormatIncorrect}
		}
		i++
	}

	if specCount == 0 {
		return csplitSuffixFormat{}, &csplitError{kind: csplitErrSuffixFormatIncorrect}
	}
	out.after = literal.String()
	return out, nil
}

func (s *csplitSplitName) get(n int) string {
	return s.prefix + s.format.format(uint64(n))
}

func (f csplitSuffixFormat) format(value uint64) string {
	prefix := ""
	var digits string
	switch f.verb {
	case 'd', 'i', 'u':
		digits = strconv.FormatUint(value, 10)
	case 'o':
		digits = strconv.FormatUint(value, 8)
	case 'x':
		digits = strconv.FormatUint(value, 16)
	case 'X':
		digits = strings.ToUpper(strconv.FormatUint(value, 16))
	}

	if f.hasP {
		f.flags.zero = false
		if f.prec == 0 && value == 0 {
			digits = ""
		}
		for len(digits) < f.prec {
			digits = "0" + digits
		}
	}

	switch f.verb {
	case 'o':
		if f.flags.alt && (digits == "" || digits[0] != '0') {
			prefix = "0"
		}
	case 'x':
		if f.flags.alt && value != 0 {
			prefix = "0x"
		}
	case 'X':
		if f.flags.alt && value != 0 {
			prefix = "0X"
		}
	case 'd', 'i':
		if f.flags.plus {
			prefix = "+"
		} else if f.flags.space {
			prefix = " "
		}
	}

	core := prefix + digits
	if f.hasW && len(core) < f.width {
		pad := f.width - len(core)
		switch {
		case f.flags.left:
			core += strings.Repeat(" ", pad)
		case f.flags.zero:
			core = prefix + strings.Repeat("0", pad) + digits
		default:
			core = strings.Repeat(" ", pad) + core
		}
	}

	return f.before + core + f.after
}

func csplitLineForMatch(line string) string {
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line
}

func csplitErrorIs(err error, kind csplitErrorKind) bool {
	var splitErr *csplitError
	return errors.As(err, &splitErr) && splitErr != nil && splitErr.kind == kind
}

func csplitIOMessage(err error) string {
	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	default:
		return err.Error()
	}
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func csplitIsASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

var _ Command = (*Csplit)(nil)
var _ SpecProvider = (*Csplit)(nil)
var _ ParsedRunner = (*Csplit)(nil)
