package builtins

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	goruntime "runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/commandutil"
	textwidth "golang.org/x/text/width"
)

type WC struct{}

type wcTotalWhen int

const (
	wcTotalAuto wcTotalWhen = iota
	wcTotalAlways
	wcTotalOnly
	wcTotalNever
)

const wcMaterializeFiles0Limit = 10 << 20

type wcOptions struct {
	lines         bool
	words         bool
	bytes         bool
	chars         bool
	maxLineLength bool
	debug         bool
	files0From    string
	totalWhen     wcTotalWhen
}

type wcCounts struct {
	lines         int
	words         int
	bytes         int
	chars         int
	maxLineLength int
}

type wcInputEntry struct {
	label          string
	invalidMessage string
}

type wcCountResult struct {
	counts      wcCounts
	printCounts bool
	err         error
}

type wcLimitedReader struct {
	reader    io.Reader
	remaining int64
	maxBytes  int64
	overflow  bool
}

func NewWC() *WC {
	return &WC{}
}

func wcMinimumWidth() int {
	if goruntime.GOOS == "darwin" {
		return 8
	}
	return 1
}

func (c *WC) Name() string {
	return "wc"
}

func (c *WC) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *WC) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}

	changed := false
	args := append([]string(nil), inv.Args...)
	parsingOptions := true
	for i, arg := range args {
		if !parsingOptions {
			break
		}
		if arg == "--" {
			parsingOptions = false
			continue
		}
		if arg == "-h" {
			args[i] = "--help"
			changed = true
		}
	}
	if !changed {
		return inv
	}

	clone := *inv
	clone.Args = args
	return &clone
}

func (c *WC) Spec() CommandSpec {
	return CommandSpec{
		Name:  "wc",
		About: "Print newline, word, and byte counts for each FILE, and a total line if more than one FILE is specified.",
		Usage: "wc [OPTION]... [FILE]...\n  or:  wc [OPTION]... --files0-from=F",
		AfterHelp: "The counts are always printed in this order: newline, word, character, byte, maximum line length.\n" +
			"In gbash, '-h' is also accepted as an alias for '--help'.",
		Options: []OptionSpec{
			{Name: "bytes", Short: 'c', Long: "bytes", Help: "print the byte counts"},
			{Name: "chars", Short: 'm', Long: "chars", Help: "print the character counts"},
			{Name: "files0-from", Long: "files0-from", Arity: OptionRequiredValue, ValueName: "F", Help: "read input from the files specified by NUL-terminated names in file F; if F is - then read names from standard input"},
			{Name: "lines", Short: 'l', Long: "lines", Help: "print the newline counts"},
			{Name: "max-line-length", Short: 'L', Long: "max-line-length", Help: "print the maximum display width"},
			{Name: "total", Long: "total", Arity: OptionRequiredValue, ValueName: "WHEN", Help: "when to print a line with total counts: auto, always, only, never"},
			{Name: "words", Short: 'w', Long: "words", Help: "print the word counts"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "debug", Long: "debug", Hidden: true, Help: "accepted for compatibility"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoVersion:              true,
		},
	}
}

func (c *WC) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	}

	opts, err := parseWCMatches(inv, matches)
	if err != nil {
		return err
	}

	files := matches.Args("file")
	if opts.files0From != "" {
		if len(files) > 0 {
			return exitf(inv, 1, "wc: extra operand %s\nfile operands cannot be combined with --files0-from\nTry 'wc --help' for more information.", quoteGNUOperand(files[0]))
		}
		return c.runWCFiles0From(ctx, inv, opts)
	}

	if len(files) == 0 {
		return c.runWCImplicitStdin(ctx, inv, opts)
	}
	return wcRunEntries(ctx, inv, opts, wcEntriesForFiles(files), len(files), false, false)
}

func parseWCMatches(inv *Invocation, matches *ParsedCommand) (wcOptions, error) {
	opts := wcOptions{
		lines:         matches.Has("lines"),
		words:         matches.Has("words"),
		bytes:         matches.Has("bytes"),
		chars:         matches.Has("chars"),
		maxLineLength: matches.Has("max-line-length"),
		debug:         matches.Has("debug"),
		files0From:    matches.Value("files0-from"),
		totalWhen:     wcTotalAuto,
	}
	if !opts.lines && !opts.words && !opts.bytes && !opts.chars && !opts.maxLineLength {
		opts.lines = true
		opts.words = true
		opts.bytes = true
	}
	if matches.Has("total") {
		totalWhen, ok := wcParseTotalWhen(matches.Value("total"))
		if !ok {
			return wcOptions{}, exitf(inv, 1, "wc: invalid argument %q for '--total'", matches.Value("total"))
		}
		opts.totalWhen = totalWhen
	}
	return opts, nil
}

func wcParseTotalWhen(value string) (wcTotalWhen, bool) {
	type totalValue struct {
		name string
		when wcTotalWhen
	}

	values := []totalValue{
		{name: "auto", when: wcTotalAuto},
		{name: "always", when: wcTotalAlways},
		{name: "only", when: wcTotalOnly},
		{name: "never", when: wcTotalNever},
	}

	var (
		match    wcTotalWhen
		matchSet bool
	)
	for _, candidate := range values {
		if candidate.name == value {
			return candidate.when, true
		}
		if strings.HasPrefix(candidate.name, value) {
			if matchSet {
				return 0, false
			}
			match = candidate.when
			matchSet = true
		}
	}
	return match, matchSet
}

func (c *WC) runWCImplicitStdin(ctx context.Context, inv *Invocation, opts wcOptions) error {
	result := wcCountReader(ctx, inv, inv.Stdin)
	columnWidth := wcOutputWidth(ctx, inv, opts, nil, true, false)

	if result.printCounts {
		if err := writeWCLine(inv.Stdout, result.counts, opts, "", columnWidth); err != nil {
			return err
		}
	}
	if result.err != nil {
		if err := wcWriteError(inv, "standard input", result.err); err != nil {
			return err
		}
		return &ExitError{Code: 1}
	}
	return nil
}

func (c *WC) runWCFiles0From(ctx context.Context, inv *Invocation, opts wcOptions) error {
	source := opts.files0From

	if source == "-" {
		return wcRunFiles0FromStream(ctx, inv, opts, source, inv.Stdin, 1)
	}

	info, _, err := statPath(ctx, inv, source)
	if err != nil {
		return exitf(inv, 1, "wc: cannot open %s for reading: %s", wcErrorOperand(source), readAllErrorText(err))
	}
	if info.IsDir() {
		return exitf(inv, 1, "wc: %s: read error: Is a directory", wcErrorOperand(source))
	}
	if info.Mode().IsRegular() && info.Size() <= wcMaterializeFiles0Limit {
		return wcRunFiles0FromMaterialized(ctx, inv, opts, source)
	}
	columnWidth := 1
	if info.Mode().IsRegular() {
		columnWidth = wcFiles0StreamWidthForRegularSource(ctx, inv, opts, source)
	}
	return wcRunFiles0FromPathStream(ctx, inv, opts, source, columnWidth)
}

func wcRunFiles0FromMaterialized(ctx context.Context, inv *Invocation, opts wcOptions, source string) error {
	file, _, err := openRead(ctx, inv, source)
	if err != nil {
		return exitf(inv, 1, "wc: cannot open %s for reading: %s", wcErrorOperand(source), readAllErrorText(err))
	}
	defer func() { _ = file.Close() }()

	data, readErr, overflow := wcReadAllReaderPartial(ctx, inv, file)
	if overflow {
		return exitf(inv, 1, "wc: %s: read error: %s", wcErrorOperand(source), readAllErrorText(readErr))
	}
	if readErr != nil {
		return exitf(inv, 1, "wc: %s: read error: %s", wcErrorOperand(source), readAllErrorText(readErr))
	}

	entries := parseWCFiles0Entries(source, data)
	return wcRunEntries(ctx, inv, opts, entries, len(entries), false, false)
}

func wcRunFiles0FromPathStream(ctx context.Context, inv *Invocation, opts wcOptions, source string, columnWidth int) error {
	file, _, err := openRead(ctx, inv, source)
	if err != nil {
		return exitf(inv, 1, "wc: cannot open %s for reading: %s", wcErrorOperand(source), readAllErrorText(err))
	}
	defer func() { _ = file.Close() }()

	return wcRunFiles0FromStream(ctx, inv, opts, source, file, columnWidth)
}

func wcRunFiles0FromStream(ctx context.Context, inv *Invocation, opts wcOptions, source string, reader io.Reader, columnWidth int) error {
	reader = commandutil.ReaderWithContext(ctx, reader)
	reader = wcLimitReaderForInvocation(inv, reader)
	buf := bufio.NewReader(reader)

	var (
		total      wcCounts
		inputCount int
		exitCode   int
	)

	for {
		record, done, err := wcReadFiles0Record(buf)
		if err != nil {
			if writeErr := wcWriteErrorMessage(inv, fmt.Sprintf("wc: %s: read error: %s", wcErrorOperand(source), readAllErrorText(err))); writeErr != nil {
				return writeErr
			}
			exitCode = 1
			break
		}
		if done {
			break
		}

		inputCount++
		entry := wcFiles0Entry(source, record, inputCount)
		entryExit, err := wcProcessEntry(ctx, inv, opts, entry, columnWidth, &total)
		if err != nil {
			return err
		}
		if entryExit != 0 {
			exitCode = entryExit
		}
	}

	if wcTotalVisible(opts.totalWhen, inputCount) {
		if err := writeWCLine(inv.Stdout, total, opts, wcTotalLabel(opts), columnWidth); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func wcFiles0StreamWidthForRegularSource(ctx context.Context, inv *Invocation, opts wcOptions, source string) int {
	file, _, err := openRead(ctx, inv, source)
	if err != nil {
		return 1
	}
	defer func() { _ = file.Close() }()

	acc := wcWidthAccumulator{minWidth: 1}
	reader := commandutil.ReaderWithContext(ctx, file)
	reader = wcLimitReaderForInvocation(inv, reader)
	buf := bufio.NewReader(reader)
	inputCount := 0

	for {
		record, done, err := wcReadFiles0Record(buf)
		if err != nil {
			break
		}
		if done {
			break
		}

		inputCount++
		acc.addEntry(ctx, inv, wcFiles0Entry(source, record, inputCount))
	}

	return acc.width(opts)
}

func wcLimitReaderForInvocation(inv *Invocation, reader io.Reader) io.Reader {
	if inv == nil {
		return reader
	}
	maxFileBytes := inv.Limits.MaxFileBytes
	if maxFileBytes <= 0 || maxFileBytes == math.MaxInt64 {
		return reader
	}
	return &wcLimitedReader{
		reader:    reader,
		remaining: maxFileBytes,
		maxBytes:  maxFileBytes,
	}
}

func (r *wcLimitedReader) Read(p []byte) (int, error) {
	if r.overflow {
		return 0, wcMaxFileBytesError(r.maxBytes)
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}

	n, err := r.reader.Read(p)
	if int64(n) <= r.remaining {
		r.remaining -= int64(n)
		return n, err
	}

	allowed := max(int(r.remaining), 0)
	r.remaining = 0
	r.overflow = true
	if allowed == 0 {
		return 0, wcMaxFileBytesError(r.maxBytes)
	}
	return allowed, wcMaxFileBytesError(r.maxBytes)
}

func wcReadFiles0Record(reader *bufio.Reader) ([]byte, bool, error) {
	record, err := reader.ReadBytes(0)
	switch {
	case err == nil:
		return record[:len(record)-1], false, nil
	case errors.Is(err, io.EOF):
		if len(record) == 0 {
			return nil, true, nil
		}
		return record, false, nil
	default:
		return nil, false, err
	}
}

func parseWCFiles0Entries(source string, data []byte) []wcInputEntry {
	if len(data) == 0 {
		return nil
	}

	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}

	entries := make([]wcInputEntry, 0, len(parts))
	for i, part := range parts {
		entries = append(entries, wcFiles0Entry(source, part, i+1))
	}
	return entries
}

func wcFiles0Entry(source string, raw []byte, index int) wcInputEntry {
	if len(raw) == 0 {
		return wcInputEntry{
			invalidMessage: fmt.Sprintf("wc: %s:%d: invalid zero-length file name", wcFiles0SourceOperand(source), index),
		}
	}

	name := string(raw)
	if source == "-" && name == "-" {
		return wcInputEntry{invalidMessage: "wc: when reading file names from standard input, no file name of '-' allowed"}
	}
	return wcInputEntry{label: name}
}

func wcEntriesForFiles(files []string) []wcInputEntry {
	entries := make([]wcInputEntry, 0, len(files))
	for _, file := range files {
		entries = append(entries, wcInputEntry{label: file})
	}
	return entries
}

func wcRunEntries(ctx context.Context, inv *Invocation, opts wcOptions, entries []wcInputEntry, inputCount int, implicitStdin, streamingFiles0 bool) error {
	columnWidth := wcOutputWidth(ctx, inv, opts, entries, implicitStdin, streamingFiles0)
	total := wcCounts{}
	exitCode := 0

	for _, entry := range entries {
		entryExit, err := wcProcessEntry(ctx, inv, opts, entry, columnWidth, &total)
		if err != nil {
			return err
		}
		if entryExit != 0 {
			exitCode = entryExit
		}
	}

	if wcTotalVisible(opts.totalWhen, inputCount) {
		if err := writeWCLine(inv.Stdout, total, opts, wcTotalLabel(opts), columnWidth); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func wcProcessEntry(ctx context.Context, inv *Invocation, opts wcOptions, entry wcInputEntry, width int, total *wcCounts) (int, error) {
	if entry.invalidMessage != "" {
		if err := wcWriteErrorMessage(inv, entry.invalidMessage); err != nil {
			return 0, err
		}
		return 1, nil
	}

	result := wcCountNamedInput(ctx, inv, opts, entry.label)
	if result.printCounts {
		if opts.totalWhen != wcTotalOnly {
			if err := writeWCLine(inv.Stdout, result.counts, opts, entry.label, width); err != nil {
				return 0, err
			}
		}
		wcAddCounts(total, result.counts)
	}
	if result.err != nil {
		if err := wcWriteError(inv, entry.label, result.err); err != nil {
			return 0, err
		}
		return 1, nil
	}
	return 0, nil
}

func wcAddCounts(dst *wcCounts, src wcCounts) {
	if dst == nil {
		return
	}
	dst.lines += src.lines
	dst.words += src.words
	dst.bytes += src.bytes
	dst.chars += src.chars
	if src.maxLineLength > dst.maxLineLength {
		dst.maxLineLength = src.maxLineLength
	}
}

func wcCountNamedInput(ctx context.Context, inv *Invocation, opts wcOptions, name string) wcCountResult {
	if name == "-" {
		return wcCountReader(ctx, inv, inv.Stdin)
	}

	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return wcCountResult{err: err}
	}
	if info.IsDir() {
		return wcCountResult{
			counts:      wcCounts{},
			printCounts: true,
			err:         &ExitError{Code: 1, Err: fmt.Errorf("is a directory")},
		}
	}

	if wcBytesOnly(opts) && info.Mode().IsRegular() {
		return wcCountResult{
			counts:      wcCounts{bytes: int(info.Size())},
			printCounts: true,
		}
	}

	file, _, err := openRead(ctx, inv, name)
	if err != nil {
		return wcCountResult{err: err}
	}
	defer func() { _ = file.Close() }()

	return wcCountReader(ctx, inv, file)
}

func wcCountReader(ctx context.Context, inv *Invocation, reader io.Reader) wcCountResult {
	data, err, overflow := wcReadAllReaderPartial(ctx, inv, reader)
	if overflow {
		return wcCountResult{err: err}
	}
	return wcCountResult{
		counts:      countWC(data, inv),
		printCounts: true,
		err:         err,
	}
}

func wcReadAllReaderPartial(ctx context.Context, inv *Invocation, reader io.Reader) ([]byte, error, bool) {
	if reader == nil {
		reader = strings.NewReader("")
	}

	maxFileBytes := int64(0)
	if inv != nil {
		maxFileBytes = inv.Limits.MaxFileBytes
	}

	reader = commandutil.ReaderWithContext(ctx, reader)
	return wcReadAllReaderPartialWithLimit(reader, maxFileBytes)
}

func wcReadAllReaderPartialWithLimit(reader io.Reader, maxFileBytes int64) ([]byte, error, bool) {
	var data bytes.Buffer
	chunk := make([]byte, 32*1024)
	totalRead := int64(0)
	enforceLimit := maxFileBytes > 0 && maxFileBytes != math.MaxInt64

	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			totalRead += int64(n)
			if enforceLimit && totalRead > maxFileBytes {
				return nil, &ExitError{
					Code: 1,
					Err:  wcMaxFileBytesDiagnostic(maxFileBytes),
				}, true
			}
			_, _ = data.Write(chunk[:n])
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return data.Bytes(), nil, false
		}
		return data.Bytes(), wcWrapReadError(err), false
	}
}

func wcMaxFileBytesError(maxFileBytes int64) error {
	return &ExitError{
		Code: 1,
		Err:  wcMaxFileBytesDiagnostic(maxFileBytes),
	}
}

func wcMaxFileBytesDiagnostic(maxFileBytes int64) error {
	return fmt.Errorf("input exceeds maximum file size of %d bytes", maxFileBytes)
}

func wcWrapReadError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return &ExitError{Code: 1, Err: err}
}

func wcBytesOnly(opts wcOptions) bool {
	return opts.bytes && !opts.lines && !opts.words && !opts.chars && !opts.maxLineLength
}

func countWC(data []byte, inv *Invocation) wcCounts {
	return wcCounts{
		lines:         bytes.Count(data, []byte{'\n'}),
		words:         wcCountWords(data, inv),
		bytes:         len(data),
		chars:         utf8.RuneCount(data),
		maxLineLength: wcMaxLineLength(data),
	}
}

func wcCountWords(data []byte, inv *Invocation) int {
	posix := inv != nil && inv.Env["POSIXLY_CORRECT"] != ""
	count := 0
	inWord := false
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if wcSingleByteWordSeparator(data[0], inv) {
				inWord = false
				data = data[1:]
				continue
			}
			if !inWord {
				count++
				inWord = true
			}
			data = data[1:]
			continue
		}
		isSpace := false
		if posix {
			isSpace = r == ' ' || (r >= '\t' && r <= '\r')
		} else {
			isSpace = unicode.IsSpace(r) || wcExtraWordSeparator(r)
		}
		if isSpace {
			inWord = false
		} else if !inWord {
			count++
			inWord = true
		}
		data = data[size:]
	}
	return count
}

func wcMaxLineLength(data []byte) int {
	maxWidth := 0
	current := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			current++
			data = data[1:]
			if current > maxWidth {
				maxWidth = current
			}
			continue
		}

		switch r {
		case '\n', '\r', '\f':
			if current > maxWidth {
				maxWidth = current
			}
			current = 0
		case '\t':
			current -= current % 8
			current += 8
		default:
			if !unicode.IsControl(r) {
				current += wcRuneDisplayWidth(r)
			}
		}

		if current > maxWidth {
			maxWidth = current
		}
		data = data[size:]
	}
	return maxWidth
}

func wcRuneDisplayWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) {
		return 0
	}

	switch textwidth.LookupRune(r).Kind() {
	case textwidth.EastAsianWide, textwidth.EastAsianFullwidth:
		return 2
	default:
		return 1
	}
}

func wcOutputWidth(ctx context.Context, inv *Invocation, opts wcOptions, entries []wcInputEntry, implicitStdin, streamingFiles0 bool) int {
	if opts.totalWhen == wcTotalOnly {
		return 1
	}
	if implicitStdin {
		if wcEnabledCount(opts) == 1 {
			return 1
		}
		return wcMinimumWidth()
	}
	if streamingFiles0 {
		return 1
	}
	if wcEnabledCount(opts) == 1 && len(entries) == 1 {
		return 1
	}

	acc := wcWidthAccumulator{minWidth: 1}
	for _, entry := range entries {
		acc.addEntry(ctx, inv, entry)
	}
	return acc.width(opts)
}

type wcWidthAccumulator struct {
	entryCount int
	minWidth   int
	totalSize  int64
}

func (a *wcWidthAccumulator) addEntry(ctx context.Context, inv *Invocation, entry wcInputEntry) {
	if a == nil {
		return
	}
	a.entryCount++
	if entry.invalidMessage != "" {
		return
	}
	if entry.label == "-" {
		a.minWidth = wcMinimumWidth()
		return
	}

	info, _, err := statPath(ctx, inv, entry.label)
	if err != nil {
		return
	}
	if info.Mode().IsRegular() {
		a.totalSize += info.Size()
		return
	}
	a.minWidth = wcMinimumWidth()
}

func (a wcWidthAccumulator) width(opts wcOptions) int {
	if wcEnabledCount(opts) == 1 && a.entryCount == 1 {
		return 1
	}
	if a.totalSize == 0 {
		if a.minWidth == 0 {
			return 1
		}
		return a.minWidth
	}

	columnWidth := len(strconv.FormatInt(a.totalSize, 10))
	if a.minWidth == 0 {
		return columnWidth
	}
	if columnWidth < a.minWidth {
		return a.minWidth
	}
	return columnWidth
}

func wcEnabledCount(opts wcOptions) int {
	count := 0
	if opts.lines {
		count++
	}
	if opts.words {
		count++
	}
	if opts.chars {
		count++
	}
	if opts.bytes {
		count++
	}
	if opts.maxLineLength {
		count++
	}
	return count
}

func wcTotalVisible(totalWhen wcTotalWhen, numInputs int) bool {
	switch totalWhen {
	case wcTotalAlways, wcTotalOnly:
		return true
	case wcTotalNever:
		return false
	default:
		return numInputs > 1
	}
}

func writeWCLine(w io.Writer, counts wcCounts, opts wcOptions, label string, width int) error {
	parts := make([]string, 0, 5)
	if opts.lines {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.lines))
	}
	if opts.words {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.words))
	}
	if opts.chars {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.chars))
	}
	if opts.bytes {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.bytes))
	}
	if opts.maxLineLength {
		parts = append(parts, fmt.Sprintf("%*d", width, counts.maxLineLength))
	}

	line := strings.Join(parts, " ")
	if label != "" {
		line += " " + wcDisplayLabel(label)
	}
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func wcTotalLabel(opts wcOptions) string {
	if opts.totalWhen == wcTotalOnly {
		return ""
	}
	return "total"
}

func wcExtraWordSeparator(r rune) bool {
	switch r {
	case '\u00A0', '\u2007', '\u202F', '\u2060':
		return true
	default:
		return false
	}
}

func wcSingleByteWordSeparator(b byte, inv *Invocation) bool {
	if inv == nil {
		return false
	}
	locale := strings.ToUpper(wcFirstNonEmpty(inv.Env["LC_ALL"], inv.Env["LC_CTYPE"], inv.Env["LANG"]))
	switch {
	case strings.Contains(locale, "8859-1"), strings.Contains(locale, "ISO8859-1"), strings.Contains(locale, "LATIN1"):
		return b == 0xA0
	case strings.Contains(locale, "KOI8-R"):
		return b == 0x9A
	default:
		return false
	}
}

func wcFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func wcWriteError(inv *Invocation, operand string, err error) error {
	return wcWriteErrorMessage(inv, fmt.Sprintf("wc: %s: %s", wcErrorOperand(operand), readAllErrorText(err)))
}

func wcWriteErrorMessage(inv *Invocation, message string) error {
	if _, err := io.WriteString(inv.Stderr, message+"\n"); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

func wcFiles0SourceOperand(source string) string {
	if source == "-" {
		return "-"
	}
	return wcErrorOperand(source)
}

func wcErrorOperand(label string) string {
	if label == "" || label == "-" {
		return label
	}
	if strings.ContainsAny(label, "\a\b\f\n\r\t\v") {
		return wcDisplayLabel(label)
	}
	if strings.ContainsAny(label, " '\"") {
		return quoteGNUOperand(label)
	}
	return label
}

func wcDisplayLabel(label string) string {
	if label == "" || !strings.ContainsAny(label, "\a\b\f\n\r\t\v") {
		return label
	}

	var out strings.Builder
	start := 0
	for i := 0; i < len(label); i++ {
		escaped, ok := wcEscapedControlByte(label[i])
		if !ok {
			continue
		}
		if start < i {
			out.WriteString(quoteGNUOperand(label[start:i]))
		}
		out.WriteString("$'")
		out.WriteString(escaped)
		out.WriteByte('\'')
		start = i + 1
	}
	if start < len(label) {
		out.WriteString(quoteGNUOperand(label[start:]))
	}
	if out.Len() == 0 {
		return label
	}
	return out.String()
}

func wcEscapedControlByte(b byte) (string, bool) {
	switch b {
	case '\a':
		return "\\a", true
	case '\b':
		return "\\b", true
	case '\f':
		return "\\f", true
	case '\n':
		return "\\n", true
	case '\r':
		return "\\r", true
	case '\t':
		return "\\t", true
	case '\v':
		return "\\v", true
	default:
		return "", false
	}
}

var _ Command = (*WC)(nil)
var _ SpecProvider = (*WC)(nil)
var _ ParsedRunner = (*WC)(nil)
var _ ParseInvocationNormalizer = (*WC)(nil)
