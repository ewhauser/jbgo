package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Cut struct{}

type cutMode int

const (
	cutModeBytes cutMode = iota
	cutModeCharacters
	cutModeFields
)

type cutOptions struct {
	mode                cutMode
	modeList            string
	modeCount           int
	delimiter           []byte
	delimiterSpecified  bool
	outputDelimiter     []byte
	outputDelimiterSet  bool
	whitespaceDelimited bool
	onlyDelimited       bool
	zeroTerminated      bool
	complement          bool
	showHelp            bool
	showVersion         bool
}

type cutRange struct {
	low  uint64
	high uint64
}

func NewCut() *Cut {
	return &Cut{}
}

func (c *Cut) Name() string {
	return "cut"
}

func (c *Cut) Run(ctx context.Context, inv *Invocation) error {
	opts, files, ranges, err := parseCutArgs(inv)
	if err != nil {
		return err
	}
	if opts.showHelp {
		_, _ = fmt.Fprint(inv.Stdout, cutHelpText)
		return nil
	}
	if opts.showVersion {
		_, _ = fmt.Fprint(inv.Stdout, cutVersionText)
		return nil
	}

	lineEnding := byte('\n')
	if opts.zeroTerminated {
		lineEnding = 0
	}

	if len(files) == 0 {
		files = []string{"-"}
	}

	exitCode := 0
	stdinRead := false
	for _, name := range files {
		var data []byte
		switch name {
		case "-":
			if stdinRead {
				continue
			}
			stdinRead = true
			data, err = readAllStdin(inv)
		default:
			data, err = readAllFileForCut(ctx, inv, name)
		}
		if err != nil {
			if cutWriteInputError(inv, name, err) {
				exitCode = 1
				continue
			}
			return err
		}

		var out []byte
		switch opts.mode {
		case cutModeBytes, cutModeCharacters:
			out = cutByPositions(data, ranges, &opts, lineEnding)
		case cutModeFields:
			out = cutByFields(data, ranges, &opts, lineEnding)
		}
		if len(out) == 0 {
			continue
		}
		if _, err := inv.Stdout.Write(out); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}

	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func parseCutArgs(inv *Invocation) (cutOptions, []string, []cutRange, error) {
	opts := cutOptions{
		delimiter: []byte{'\t'},
	}
	args := append([]string(nil), inv.Args...)

	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			args = args[1:]
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}

		var consumed int
		var err error
		if strings.HasPrefix(arg, "--") {
			consumed, err = parseCutLongOption(inv, args, &opts)
		} else {
			consumed, err = parseCutShortOptions(inv, args, &opts)
		}
		if err != nil {
			return cutOptions{}, nil, nil, err
		}
		args = args[consumed:]
	}

	if opts.showHelp || opts.showVersion {
		return opts, args, nil, nil
	}
	if opts.modeCount == 0 {
		return cutOptions{}, nil, nil, cutUsageError(inv, "cut: you must specify a list of bytes, characters, or fields")
	}
	if opts.delimiterSpecified && opts.whitespaceDelimited {
		return cutOptions{}, nil, nil, cutUsageError(inv, "cut: an input delimiter may be specified only when operating on fields")
	}
	if opts.mode != cutModeFields {
		if opts.delimiterSpecified {
			return cutOptions{}, nil, nil, cutUsageError(inv, "cut: an input delimiter may be specified only when operating on fields")
		}
		if opts.onlyDelimited {
			return cutOptions{}, nil, nil, cutUsageError(inv, "cut: suppressing non-delimited lines makes sense\n\tonly when operating on fields")
		}
		if opts.whitespaceDelimited {
			return cutOptions{}, nil, nil, cutUsageError(inv, "cut: whitespace-delimited input is meaningful only when operating on fields")
		}
	}

	ranges, err := parseCutRanges(inv, opts.mode, opts.modeList, opts.complement)
	if err != nil {
		return cutOptions{}, nil, nil, err
	}
	delimiter, err := parseCutDelimiter(inv, opts.delimiter, opts.delimiterSpecified)
	if err != nil {
		return cutOptions{}, nil, nil, err
	}
	opts.delimiter = delimiter
	opts.outputDelimiter = parseCutOutputDelimiter(opts.outputDelimiter, opts.outputDelimiterSet)

	return opts, args, ranges, nil
}

func parseCutLongOption(inv *Invocation, args []string, opts *cutOptions) (int, error) {
	arg := args[0]
	name := strings.TrimPrefix(arg, "--")
	value := ""
	hasValue := false
	if before, after, ok := strings.Cut(name, "="); ok {
		name = before
		value = after
		hasValue = true
	}

	match, err := matchCutLongOption(inv, name)
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
	case "complement":
		opts.complement = true
		return 1, nil
	case "only-delimited":
		opts.onlyDelimited = true
		return 1, nil
	case "zero-terminated":
		opts.zeroTerminated = true
		return 1, nil
	case "bytes":
		list, consumed, err := cutOptionValue(inv, args, hasValue, value, "bytes")
		if err != nil {
			return 0, err
		}
		if err := setCutMode(inv, opts, cutModeBytes, list); err != nil {
			return 0, err
		}
		return consumed, nil
	case "characters":
		list, consumed, err := cutOptionValue(inv, args, hasValue, value, "characters")
		if err != nil {
			return 0, err
		}
		if err := setCutMode(inv, opts, cutModeCharacters, list); err != nil {
			return 0, err
		}
		return consumed, nil
	case "fields":
		list, consumed, err := cutOptionValue(inv, args, hasValue, value, "fields")
		if err != nil {
			return 0, err
		}
		if err := setCutMode(inv, opts, cutModeFields, list); err != nil {
			return 0, err
		}
		return consumed, nil
	case "delimiter":
		delim, consumed, err := cutOptionValue(inv, args, hasValue, value, "delimiter")
		if err != nil {
			return 0, err
		}
		opts.delimiter = []byte(delim)
		opts.delimiterSpecified = true
		return consumed, nil
	case "output-delimiter":
		delim, consumed, err := cutOptionValue(inv, args, hasValue, value, "output-delimiter")
		if err != nil {
			return 0, err
		}
		opts.outputDelimiter = []byte(delim)
		opts.outputDelimiterSet = true
		return consumed, nil
	default:
		return 0, cutUsageError(inv, fmt.Sprintf("cut: unrecognized option '%s'", arg))
	}
}

func parseCutShortOptions(inv *Invocation, args []string, opts *cutOptions) (int, error) {
	arg := args[0]
	for i := 1; i < len(arg); i++ {
		switch arg[i] {
		case 'b':
			list, consumed, err := cutShortOptionValue(inv, args, arg, i, "b")
			if err != nil {
				return 0, err
			}
			if err := setCutMode(inv, opts, cutModeBytes, list); err != nil {
				return 0, err
			}
			return consumed, nil
		case 'c':
			list, consumed, err := cutShortOptionValue(inv, args, arg, i, "c")
			if err != nil {
				return 0, err
			}
			if err := setCutMode(inv, opts, cutModeCharacters, list); err != nil {
				return 0, err
			}
			return consumed, nil
		case 'f':
			list, consumed, err := cutShortOptionValue(inv, args, arg, i, "f")
			if err != nil {
				return 0, err
			}
			if err := setCutMode(inv, opts, cutModeFields, list); err != nil {
				return 0, err
			}
			return consumed, nil
		case 'd':
			delim, consumed, err := cutShortOptionValue(inv, args, arg, i, "d")
			if err != nil {
				return 0, err
			}
			opts.delimiter = []byte(delim)
			opts.delimiterSpecified = true
			return consumed, nil
		case 's':
			opts.onlyDelimited = true
		case 'z':
			opts.zeroTerminated = true
		case 'n':
			// GNU compatibility no-op.
		case 'w':
			opts.whitespaceDelimited = true
		default:
			return 0, cutUsageError(inv, fmt.Sprintf("cut: invalid option -- '%c'", arg[i]))
		}
	}
	return 1, nil
}

func matchCutLongOption(inv *Invocation, name string) (string, error) {
	candidates := []string{
		"bytes",
		"characters",
		"complement",
		"delimiter",
		"fields",
		"help",
		"only-delimited",
		"output-delimiter",
		"version",
		"zero-terminated",
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
		return "", cutUsageError(inv, fmt.Sprintf("cut: unrecognized option '%s'", "--"+name))
	case 1:
		return matches[0], nil
	default:
		return "", cutUsageError(inv, fmt.Sprintf("cut: option '%s' is ambiguous", "--"+name))
	}
}

func cutOptionValue(inv *Invocation, args []string, hasValue bool, value, name string) (optionValue string, consumed int, err error) {
	if hasValue {
		return value, 1, nil
	}
	if len(args) < 2 {
		return "", 0, cutUsageError(inv, fmt.Sprintf("cut: option '--%s' requires an argument", name))
	}
	return args[1], 2, nil
}

func cutShortOptionValue(inv *Invocation, args []string, arg string, index int, name string) (optionValue string, consumed int, err error) {
	if rest := arg[index+1:]; rest != "" {
		return rest, 1, nil
	}
	if len(args) < 2 {
		return "", 0, cutUsageError(inv, fmt.Sprintf("cut: option requires an argument -- '%s'", name))
	}
	return args[1], 2, nil
}

func setCutMode(inv *Invocation, opts *cutOptions, mode cutMode, list string) error {
	opts.modeCount++
	if opts.modeCount > 1 {
		return cutUsageError(inv, "cut: only one type of list may be specified")
	}
	opts.mode = mode
	opts.modeList = list
	return nil
}

func parseCutRanges(inv *Invocation, mode cutMode, value string, complement bool) ([]cutRange, error) {
	parts := strings.Split(value, ",")
	ranges := make([]cutRange, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		current, err := parseCutRangePart(mode, part)
		if err != nil {
			return nil, cutRangeError(inv, mode, part, err)
		}
		ranges = append(ranges, current)
	}
	if complement {
		return cutComplementRanges(ranges), nil
	}
	return ranges, nil
}

func parseCutRangePart(mode cutMode, part string) (cutRange, error) {
	if part == "" {
		return cutRange{}, errCutRangeStartsAtOne
	}
	if part == "-" {
		return cutRange{}, errCutRangeNoEndpoint
	}
	if !strings.Contains(part, "-") {
		n, err := parseCutRangeNumber(part)
		if err != nil {
			return cutRange{}, err
		}
		return cutRange{low: n, high: n}, nil
	}

	left, right, _ := strings.Cut(part, "-")
	switch {
	case left == "" && right == "":
		return cutRange{}, errCutRangeNoEndpoint
	case left == "":
		n, err := parseCutRangeNumber(right)
		if err != nil {
			return cutRange{}, err
		}
		return cutRange{low: 1, high: n}, nil
	case right == "":
		n, err := parseCutRangeNumber(left)
		if err != nil {
			return cutRange{}, err
		}
		return cutRange{low: n, high: math.MaxUint64}, nil
	default:
		low, err := parseCutRangeNumber(left)
		if err != nil {
			return cutRange{}, err
		}
		high, err := parseCutRangeNumberAllowZero(right)
		if err != nil {
			return cutRange{}, err
		}
		if high < low {
			return cutRange{}, errCutRangeDecreasing
		}
		return cutRange{low: low, high: high}, nil
	}
}

func parseCutRangeNumber(value string) (uint64, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errCutRangeInvalid
	}
	if n == 0 {
		return 0, errCutRangeStartsAtOne
	}
	return n, nil
}

func parseCutRangeNumberAllowZero(value string) (uint64, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errCutRangeInvalid
	}
	return n, nil
}

var (
	errCutRangeStartsAtOne = errors.New("starts-at-one")
	errCutRangeInvalid     = errors.New("invalid")
	errCutRangeNoEndpoint  = errors.New("no-endpoint")
	errCutRangeDecreasing  = errors.New("decreasing")
)

func cutRangeError(inv *Invocation, mode cutMode, value string, err error) error {
	switch {
	case errors.Is(err, errCutRangeStartsAtOne):
		if mode == cutModeFields {
			return cutUsageError(inv, "cut: fields are numbered from 1")
		}
		return cutUsageError(inv, "cut: byte/character positions are numbered from 1")
	case errors.Is(err, errCutRangeNoEndpoint):
		return cutUsageError(inv, fmt.Sprintf("cut: invalid range with no endpoint: %s", value))
	case errors.Is(err, errCutRangeDecreasing):
		return cutUsageError(inv, "cut: invalid decreasing range")
	default:
		if mode == cutModeFields {
			return cutUsageError(inv, "cut: invalid field range")
		}
		return cutUsageError(inv, "cut: invalid byte or character range")
	}
}

func cutComplementRanges(ranges []cutRange) []cutRange {
	if len(ranges) == 0 {
		return nil
	}
	merged := normalizeCutRanges(ranges)

	complemented := make([]cutRange, 0, len(merged)+1)
	nextLow := uint64(1)
	for _, current := range merged {
		if current.low > nextLow {
			complemented = append(complemented, cutRange{low: nextLow, high: current.low - 1})
		}
		if current.high == math.MaxUint64 {
			return complemented
		}
		nextLow = current.high + 1
	}
	complemented = append(complemented, cutRange{low: nextLow, high: math.MaxUint64})
	return complemented
}

func normalizeCutRanges(ranges []cutRange) []cutRange {
	if len(ranges) == 0 {
		return nil
	}

	normalized := append([]cutRange(nil), ranges...)
	for i := 1; i < len(normalized); i++ {
		current := normalized[i]
		j := i - 1
		for ; j >= 0; j-- {
			if normalized[j].low <= current.low {
				break
			}
			normalized[j+1] = normalized[j]
		}
		normalized[j+1] = current
	}

	merged := make([]cutRange, 0, len(normalized))
	for _, current := range normalized {
		if len(merged) == 0 {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		if current.low <= last.high {
			if current.high > last.high {
				last.high = current.high
			}
			continue
		}
		merged = append(merged, current)
	}
	return merged
}

func parseCutDelimiter(inv *Invocation, value []byte, specified bool) ([]byte, error) {
	if !specified {
		return value, nil
	}
	if len(value) == 0 || string(value) == "''" {
		return []byte{0}, nil
	}
	if len(value) == 1 {
		return value, nil
	}
	if utf8.Valid(value) && utf8.RuneCount(value) == 1 {
		return value, nil
	}
	return nil, cutUsageError(inv, "cut: the delimiter must be a single character")
}

func parseCutOutputDelimiter(value []byte, specified bool) []byte {
	if !specified {
		return nil
	}
	if len(value) == 0 || string(value) == "''" {
		return []byte{0}
	}
	return value
}

func cutByPositions(data []byte, ranges []cutRange, opts *cutOptions, lineEnding byte) []byte {
	records := splitCutRecords(data, lineEnding)
	if len(records) == 0 {
		return nil
	}
	normalized := normalizeCutRanges(ranges)

	var out []byte
	for _, record := range records {
		if opts.outputDelimiterSet {
			wroteAny := false
			for _, current := range normalized {
				start := current.low - 1
				if start >= uint64(len(record)) {
					break
				}
				end := min(current.high, uint64(len(record)))
				if end <= start {
					continue
				}
				if wroteAny {
					out = append(out, opts.outputDelimiter...)
				}
				out = append(out, record[start:end]...)
				wroteAny = true
			}
			out = append(out, lineEnding)
			continue
		}
		for idx, b := range record {
			if !cutIndexSelected(uint64(idx+1), normalized) {
				continue
			}
			out = append(out, b)
		}
		out = append(out, lineEnding)
	}
	return out
}

func cutByFields(data []byte, ranges []cutRange, opts *cutOptions, lineEnding byte) []byte {
	if len(data) == 0 {
		return nil
	}
	if len(opts.delimiter) == 1 && opts.delimiter[0] == lineEnding {
		return cutByFieldsAcrossStream(data, ranges, opts, lineEnding)
	}

	records := splitCutRecords(data, lineEnding)
	if len(records) == 0 {
		return nil
	}

	var out []byte
	for _, record := range records {
		line, ok := cutSingleFieldRecord(record, ranges, opts)
		if !ok {
			continue
		}
		out = append(out, line...)
		out = append(out, lineEnding)
	}
	return out
}

func cutByFieldsAcrossStream(data []byte, ranges []cutRange, opts *cutOptions, lineEnding byte) []byte {
	if !bytes.Contains(data, []byte{lineEnding}) {
		if opts.onlyDelimited {
			return nil
		}
		out := append([]byte(nil), data...)
		if len(out) == 0 || out[len(out)-1] != lineEnding {
			out = append(out, lineEnding)
		}
		return out
	}

	fields := splitCutRecords(data, lineEnding)
	joiner := []byte{lineEnding}
	if opts.outputDelimiterSet {
		joiner = opts.outputDelimiter
	}
	out := joinSelectedFields(fields, ranges, joiner)
	out = append(out, lineEnding)
	return out
}

func cutSingleFieldRecord(record []byte, ranges []cutRange, opts *cutOptions) ([]byte, bool) {
	fields, found := splitCutFields(record, opts)
	if !found {
		if opts.onlyDelimited {
			return nil, false
		}
		return append([]byte(nil), record...), true
	}

	joiner := opts.delimiter
	if opts.whitespaceDelimited && !opts.outputDelimiterSet {
		joiner = []byte{'\t'}
	}
	if opts.outputDelimiterSet {
		joiner = opts.outputDelimiter
	}
	return joinSelectedFields(fields, ranges, joiner), true
}

func splitCutFields(record []byte, opts *cutOptions) ([][]byte, bool) {
	if opts.whitespaceDelimited {
		return splitWhitespaceFields(record)
	}
	if len(opts.delimiter) == 1 {
		return splitExactFieldsByte(record, opts.delimiter[0])
	}
	return splitExactFields(record, opts.delimiter)
}

func splitExactFieldsByte(record []byte, delim byte) ([][]byte, bool) {
	found := false
	fields := make([][]byte, 0, 4)
	start := 0
	for i, b := range record {
		if b != delim {
			continue
		}
		found = true
		fields = append(fields, record[start:i])
		start = i + 1
	}
	if !found {
		return nil, false
	}
	fields = append(fields, record[start:])
	return fields, true
}

func splitExactFields(record, delim []byte) ([][]byte, bool) {
	found := false
	fields := make([][]byte, 0, 4)
	start := 0
	for {
		idx := bytes.Index(record[start:], delim)
		if idx < 0 {
			break
		}
		found = true
		fields = append(fields, record[start:start+idx])
		start += idx + len(delim)
	}
	if !found {
		return nil, false
	}
	fields = append(fields, record[start:])
	return fields, true
}

func splitWhitespaceFields(record []byte) ([][]byte, bool) {
	fields := make([][]byte, 0, 4)
	found := false
	start := 0
	for i := 0; i < len(record); i++ {
		if record[i] != ' ' && record[i] != '\t' {
			continue
		}
		found = true
		fields = append(fields, record[start:i])
		for i+1 < len(record) && (record[i+1] == ' ' || record[i+1] == '\t') {
			i++
		}
		start = i + 1
	}
	if !found {
		return nil, false
	}
	fields = append(fields, record[start:])
	return fields, true
}

func joinSelectedFields(fields [][]byte, ranges []cutRange, joiner []byte) []byte {
	var out []byte
	wroteField := false
	for idx, field := range fields {
		if !cutIndexSelected(uint64(idx+1), ranges) {
			continue
		}
		if wroteField {
			out = append(out, joiner...)
		}
		out = append(out, field...)
		wroteField = true
	}
	return out
}

func cutIndexSelected(index uint64, ranges []cutRange) bool {
	for _, current := range ranges {
		if index < current.low {
			continue
		}
		if index <= current.high {
			return true
		}
	}
	return false
}

func splitCutRecords(data []byte, lineEnding byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	records := make([][]byte, 0, 1+bytes.Count(data, []byte{lineEnding}))
	start := 0
	for i, b := range data {
		if b != lineEnding {
			continue
		}
		records = append(records, append([]byte(nil), data[start:i]...))
		start = i + 1
	}
	if start < len(data) {
		records = append(records, append([]byte(nil), data[start:]...))
	}
	return records
}

func readAllFileForCut(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
	data, _, err := readAllFile(ctx, inv, name)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func cutWriteInputError(inv *Invocation, name string, err error) bool {
	if inv == nil || inv.Stderr == nil {
		return false
	}
	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		_, _ = fmt.Fprintf(inv.Stderr, "cut: %s: No such file or directory\n", name)
		return true
	default:
		if pathErr := (*stdfs.PathError)(nil); errors.As(err, &pathErr) && errors.Is(pathErr.Err, stdfs.ErrNotExist) {
			_, _ = fmt.Fprintf(inv.Stderr, "cut: %s: No such file or directory\n", name)
			return true
		}
	}
	return false
}

func cutUsageError(inv *Invocation, message string) error {
	return exitf(inv, 1, "%s\nTry 'cut --help' for more information.", message)
}

const cutHelpText = `Usage: cut OPTION... [FILE]...
Print selected parts of lines from each FILE to standard output.

  -b, --bytes=LIST              select only these bytes
  -c, --characters=LIST         select only these characters
  -d, --delimiter=DELIM         use DELIM instead of TAB for field delimiter
  -f, --fields=LIST             select only these fields
  -s, --only-delimited          do not print lines not containing delimiters
  -z, --zero-terminated         line delimiter is NUL, not newline
      --complement              complement the set of selected bytes, characters or fields
      --output-delimiter=STR    use STR as the output delimiter
      --help                    display this help and exit
      --version                 output version information and exit
`

const cutVersionText = "cut (gbash) dev\n"

var _ Command = (*Cut)(nil)
