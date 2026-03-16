package builtins

import (
	"bytes"
	"context"
	"fmt"
	stdfs "io/fs"
	"maps"
	"math"
	"strconv"
	"strings"

	gbfs "github.com/ewhauser/gbash/fs"
)

type Split struct{}

type splitMode int

const (
	splitModeLines splitMode = iota
	splitModeBytes
	splitModeLineBytes
	splitModeNumber
)

type splitNumberMode int

const (
	splitNumberBytes splitNumberMode = iota
	splitNumberKthBytes
	splitNumberLines
	splitNumberKthLines
	splitNumberRoundRobin
	splitNumberKthRoundRobin
)

type splitSuffixKind int

const (
	splitSuffixAlpha splitSuffixKind = iota
	splitSuffixNumeric
	splitSuffixHex
)

type splitNumberSpec struct {
	mode   splitNumberMode
	kth    int
	chunks int
}

type splitOptions struct {
	mode             splitMode
	lines            int
	bytes            int
	lineBytes        int
	number           splitNumberSpec
	suffixKind       splitSuffixKind
	suffixStart      int
	suffixLen        int
	suffixAutoGrow   bool
	additionalSuffix string
	filter           string
	verbose          bool
	separator        byte
	elideEmpty       bool
}

type splitChunkOutput struct {
	name string
	data []byte
}

func NewSplit() *Split {
	return &Split{}
}

func (c *Split) Name() string {
	return "split"
}

func (c *Split) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Split) NormalizeInvocation(inv *Invocation) *Invocation {
	if inv == nil || len(inv.Args) == 0 {
		return inv
	}
	args := normalizeSplitArgs(inv.Args)
	if splitSliceEqual(args, inv.Args) {
		return inv
	}
	clone := *inv
	clone.Args = args
	return &clone
}

func (c *Split) Spec() CommandSpec {
	return CommandSpec{
		Name:  "split",
		About: "Output pieces of FILE to PREFIXaa, PREFIXab, ...; default size is 1000 lines, and default PREFIX is 'x'.",
		Options: []OptionSpec{
			{Name: "bytes", Short: 'b', Long: "bytes", Arity: OptionRequiredValue, ValueName: "SIZE", Help: "put SIZE bytes per output file"},
			{Name: "line-bytes", Short: 'C', Long: "line-bytes", Arity: OptionRequiredValue, ValueName: "SIZE", Help: "put at most SIZE bytes of records per output file"},
			{Name: "lines", Short: 'l', Long: "lines", Arity: OptionRequiredValue, ValueName: "NUMBER", Help: "put NUMBER lines per output file"},
			{Name: "number", Short: 'n', Long: "number", Arity: OptionRequiredValue, ValueName: "CHUNKS", Help: "generate CHUNKS output files"},
			{Name: "additional-suffix", Long: "additional-suffix", Arity: OptionRequiredValue, ValueName: "SUFFIX", Help: "append an additional SUFFIX to file names"},
			{Name: "filter", Long: "filter", Arity: OptionRequiredValue, ValueName: "COMMAND", Help: "write to shell COMMAND; file name is $FILE"},
			{Name: "elide-empty-files", Short: 'e', Long: "elide-empty-files", Help: "do not generate empty output files with '-n'"},
			{Name: "numeric-suffixes", Short: 'd', Long: "numeric-suffixes", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "FROM", Help: "use numeric suffixes instead of alphabetic"},
			{Name: "hex-suffixes", Short: 'x', Long: "hex-suffixes", Arity: OptionOptionalValue, OptionalValueEqualsOnly: true, ValueName: "FROM", Help: "use hexadecimal suffixes instead of alphabetic"},
			{Name: "suffix-length", Short: 'a', Long: "suffix-length", Arity: OptionRequiredValue, ValueName: "N", Help: "use suffixes of length N"},
			{Name: "verbose", Long: "verbose", Help: "print a diagnostic just before each output file is opened"},
			{Name: "separator", Short: 't', Long: "separator", Arity: OptionRequiredValue, ValueName: "SEP", Repeatable: true, Help: "use SEP instead of newline as the record separator"},
			{Name: "io-blksize", Long: "io-blksize", Hidden: true, Arity: OptionRequiredValue, ValueName: "SIZE"},
			{Name: "obsolete-lines", Long: "obsolete-lines", Hidden: true, Arity: OptionRequiredValue, ValueName: "NUMBER"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
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

func (c *Split) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, inputName, prefix, err := parseSplitMatches(inv, matches)
	if err != nil {
		return err
	}

	var data []byte
	var inputAbs string
	if inputName == "-" {
		data, err = readAllStdin(ctx, inv)
		if err != nil {
			return err
		}
	} else {
		data, inputAbs, err = readAllFile(ctx, inv, inputName)
		if err != nil {
			return err
		}
	}

	chunks, stdoutData, err := splitContent(data, &opts)
	if err != nil {
		return err
	}
	if stdoutData != nil {
		if _, err := inv.Stdout.Write(stdoutData); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		return nil
	}

	for _, chunk := range chunks {
		target := gbfs.Resolve(inv.Cwd, prefix+chunk.name+opts.additionalSuffix)
		if inputAbs != "" && sameSplitOutputPath(ctx, target, inputAbs, inv) {
			return exitf(inv, 1, "split: %s would overwrite input; aborting", quoteGNUOperand(target))
		}
		if opts.verbose {
			if _, err := fmt.Fprintf(inv.Stdout, "creating file '%s'\n", prefix+chunk.name+opts.additionalSuffix); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
		}
		if opts.filter != "" {
			if err := runSplitFilter(ctx, inv, opts.filter, prefix+chunk.name+opts.additionalSuffix, chunk.data); err != nil {
				return err
			}
			continue
		}
		if err := writeFileContents(ctx, inv, target, chunk.data, stdfs.FileMode(0o644)); err != nil {
			return err
		}
	}
	return nil
}

func parseSplitMatches(inv *Invocation, matches *ParsedCommand) (opts splitOptions, inputName, prefix string, err error) {
	opts = splitOptions{
		mode:           splitModeLines,
		lines:          1000,
		suffixKind:     splitSuffixAlpha,
		suffixLen:      2,
		suffixAutoGrow: true,
		separator:      '\n',
	}
	if matches == nil {
		return opts, "-", "x", nil
	}

	if err := parseSplitStrategy(inv, matches, &opts); err != nil {
		return splitOptions{}, "", "", err
	}
	if err := parseSplitSuffixConfig(inv, matches, &opts); err != nil {
		return splitOptions{}, "", "", err
	}
	if err := parseSplitSeparator(inv, matches, &opts); err != nil {
		return splitOptions{}, "", "", err
	}

	opts.additionalSuffix = matches.Value("additional-suffix")
	if strings.Contains(opts.additionalSuffix, "/") {
		return splitOptions{}, "", "", exitf(inv, 1, "split: %q contains directory separator", opts.additionalSuffix)
	}
	opts.filter = matches.Value("filter")
	opts.verbose = matches.Has("verbose")
	opts.elideEmpty = matches.Has("elide-empty-files")
	if opts.filter != "" && splitNumberWritesStdout(&opts) {
		return splitOptions{}, "", "", exitf(inv, 1, "split: cannot use --filter with a chunk-extracting --number mode")
	}

	args := matches.Args("arg")
	inputName = "-"
	prefix = "x"
	switch len(args) {
	case 0:
	case 1:
		inputName = args[0]
	case 2:
		inputName = args[0]
		prefix = args[1]
	default:
		return splitOptions{}, "", "", exitf(inv, 1, "split: extra operand %s", quoteGNUOperand(args[2]))
	}
	return opts, inputName, prefix, nil
}

func parseSplitStrategy(inv *Invocation, matches *ParsedCommand, opts *splitOptions) error {
	selected := 0
	if matches.Has("obsolete-lines") {
		selected++
		value, err := parseSplitPositiveInt(inv, matches.Value("obsolete-lines"), "number of lines")
		if err != nil {
			return err
		}
		opts.mode = splitModeLines
		opts.lines = value
	}
	if matches.Has("lines") {
		selected++
		value, err := parseSplitPositiveInt(inv, matches.Value("lines"), "number of lines")
		if err != nil {
			return err
		}
		opts.mode = splitModeLines
		opts.lines = value
	}
	if matches.Has("bytes") {
		selected++
		value, err := parseSplitSize(matches.Value("bytes"))
		if err != nil || value <= 0 {
			return exitf(inv, 1, "split: invalid byte count %q", matches.Value("bytes"))
		}
		opts.mode = splitModeBytes
		opts.bytes = value
	}
	if matches.Has("line-bytes") {
		selected++
		value, err := parseSplitSize(matches.Value("line-bytes"))
		if err != nil || value <= 0 {
			return exitf(inv, 1, "split: invalid byte count %q", matches.Value("line-bytes"))
		}
		opts.mode = splitModeLineBytes
		opts.lineBytes = value
	}
	if matches.Has("number") {
		selected++
		spec, err := parseSplitNumber(matches.Value("number"))
		if err != nil {
			return exitf(inv, 1, "split: %s", err.Error())
		}
		opts.mode = splitModeNumber
		opts.number = spec
	}
	if selected > 1 {
		return exitf(inv, 1, "split: cannot split in more than one way")
	}
	return nil
}

func parseSplitSuffixConfig(inv *Invocation, matches *ParsedCommand, opts *splitOptions) error {
	if matches.Has("suffix-length") {
		value, err := strconv.Atoi(matches.Value("suffix-length"))
		if err != nil || value < 0 {
			return exitf(inv, 1, "split: invalid suffix length %q", matches.Value("suffix-length"))
		}
		if value > 0 {
			opts.suffixLen = value
			opts.suffixAutoGrow = false
		}
	}

	modeOrder := matches.OptionOrder()
	for _, name := range modeOrder {
		switch name {
		case "numeric-suffixes":
			opts.suffixKind = splitSuffixNumeric
			value := matches.Value("numeric-suffixes")
			if value != "" {
				start, err := strconv.Atoi(value)
				if err != nil || start < 0 {
					return exitf(inv, 1, "split: invalid suffix start %q", value)
				}
				opts.suffixStart = start
				opts.suffixAutoGrow = false
			}
		case "hex-suffixes":
			opts.suffixKind = splitSuffixHex
			value := matches.Value("hex-suffixes")
			if value != "" {
				start, err := strconv.ParseInt(value, 16, 64)
				if err != nil || start < 0 {
					return exitf(inv, 1, "split: invalid suffix start %q", value)
				}
				opts.suffixStart = int(start)
				opts.suffixAutoGrow = false
			}
		}
	}

	if opts.mode == splitModeNumber && opts.number.chunks > 0 && opts.suffixStart < opts.number.chunks {
		required := splitRequiredSuffixLength(opts.suffixKind, opts.suffixStart+opts.number.chunks-1)
		if matches.Has("suffix-length") && opts.suffixLen > 0 && opts.suffixLen < required {
			return exitf(inv, 1, "split: output file suffixes exhausted")
		}
		if !matches.Has("suffix-length") {
			opts.suffixLen = max(opts.suffixLen, required)
			opts.suffixAutoGrow = false
		}
	}
	return nil
}

func parseSplitSeparator(inv *Invocation, matches *ParsedCommand, opts *splitOptions) error {
	values := matches.Values("separator")
	if len(values) == 0 {
		return nil
	}
	sep, err := decodeSplitSeparator(values[0])
	if err != nil {
		return err
	}
	for _, value := range values[1:] {
		other, err := decodeSplitSeparator(value)
		if err != nil {
			return err
		}
		if other != sep {
			return exitf(inv, 1, "split: multiple separator characters specified")
		}
	}
	opts.separator = sep
	return nil
}

func decodeSplitSeparator(value string) (byte, error) {
	if value == "\\0" {
		return 0, nil
	}
	if len(value) != 1 {
		return 0, fmt.Errorf("split: multi-character separator %q", value)
	}
	return value[0], nil
}

func parseSplitPositiveInt(inv *Invocation, value, label string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, exitf(inv, 1, "split: invalid %s %q", label, value)
	}
	return n, nil
}

func parseSplitSize(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("empty size")
	}
	multiplier := 1
	last := value[len(value)-1]
	switch last {
	case 'b':
		multiplier = 1
		value = value[:len(value)-1]
	case 'k', 'K':
		multiplier = 1024
		value = value[:len(value)-1]
	case 'm', 'M':
		multiplier = 1024 * 1024
		value = value[:len(value)-1]
	case 'g', 'G':
		multiplier = 1024 * 1024 * 1024
		value = value[:len(value)-1]
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size")
	}
	return n * multiplier, nil
}

func parseSplitNumber(value string) (splitNumberSpec, error) {
	parts := strings.Split(value, "/")
	switch len(parts) {
	case 1:
		n, err := strconv.Atoi(parts[0])
		if err != nil || n <= 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", parts[0])
		}
		return splitNumberSpec{mode: splitNumberBytes, chunks: n}, nil
	case 2:
		switch parts[0] {
		case "l":
			n, err := strconv.Atoi(parts[1])
			if err != nil || n <= 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", parts[1])
			}
			return splitNumberSpec{mode: splitNumberLines, chunks: n}, nil
		case "r":
			n, err := strconv.Atoi(parts[1])
			if err != nil || n <= 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", parts[1])
			}
			return splitNumberSpec{mode: splitNumberRoundRobin, chunks: n}, nil
		default:
			k, err := strconv.Atoi(parts[0])
			if err != nil || k <= 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %q", parts[0])
			}
			n, err := strconv.Atoi(parts[1])
			if err != nil || n <= 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", parts[1])
			}
			if k > n {
				return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %q", parts[0])
			}
			return splitNumberSpec{mode: splitNumberKthBytes, kth: k, chunks: n}, nil
		}
	case 3:
		mode := parts[0]
		k, err := strconv.Atoi(parts[1])
		if err != nil || k <= 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %q", parts[1])
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil || n <= 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", parts[2])
		}
		if k > n {
			return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %q", parts[1])
		}
		switch mode {
		case "l":
			return splitNumberSpec{mode: splitNumberKthLines, kth: k, chunks: n}, nil
		case "r":
			return splitNumberSpec{mode: splitNumberKthRoundRobin, kth: k, chunks: n}, nil
		default:
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", value)
		}
	default:
		return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %q", value)
	}
}

func splitContent(data []byte, opts *splitOptions) ([]splitChunkOutput, []byte, error) {
	switch opts.mode {
	case splitModeBytes:
		return splitWriteChunks(splitByBytes(data, opts.bytes, false), opts)
	case splitModeLines:
		return splitWriteChunks(splitByRecordCount(splitRecords(data, opts.separator), opts.lines, false), opts)
	case splitModeLineBytes:
		return splitWriteChunks(splitByLineBytes(splitRecords(data, opts.separator), opts.lineBytes, false), opts)
	case splitModeNumber:
		return splitNumberContent(data, opts)
	default:
		return nil, nil, nil
	}
}

func splitNumberContent(data []byte, opts *splitOptions) ([]splitChunkOutput, []byte, error) {
	switch opts.number.mode {
	case splitNumberBytes:
		return splitWriteChunks(splitByByteChunks(data, opts.number.chunks, opts.elideEmpty), opts)
	case splitNumberKthBytes:
		chunks := splitByByteChunks(data, opts.number.chunks, false)
		if opts.number.kth-1 >= len(chunks) {
			return nil, nil, nil
		}
		return nil, chunks[opts.number.kth-1], nil
	case splitNumberLines:
		return splitWriteChunks(splitByDistributedLines(splitRecords(data, opts.separator), opts.number.chunks, opts.elideEmpty), opts)
	case splitNumberKthLines:
		chunks := splitByDistributedLines(splitRecords(data, opts.separator), opts.number.chunks, false)
		if opts.number.kth-1 >= len(chunks) {
			return nil, nil, nil
		}
		return nil, chunks[opts.number.kth-1], nil
	case splitNumberRoundRobin:
		return splitWriteChunks(splitRoundRobin(splitRecords(data, opts.separator), opts.number.chunks, opts.elideEmpty), opts)
	case splitNumberKthRoundRobin:
		chunks := splitRoundRobin(splitRecords(data, opts.separator), opts.number.chunks, false)
		if opts.number.kth-1 >= len(chunks) {
			return nil, nil, nil
		}
		return nil, chunks[opts.number.kth-1], nil
	default:
		return nil, nil, nil
	}
}

func splitWriteChunks(rawChunks [][]byte, opts *splitOptions) ([]splitChunkOutput, []byte, error) {
	out := make([]splitChunkOutput, 0, len(rawChunks))
	for i, chunk := range rawChunks {
		if opts.elideEmpty && len(chunk) == 0 {
			continue
		}
		name, err := splitSuffixName(i, opts)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, splitChunkOutput{name: name, data: chunk})
	}
	return out, nil, nil
}

func splitRecords(data []byte, sep byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	if sep == '\n' {
		return splitLines(data)
	}
	return bytes.SplitAfter(data, []byte{sep})
}

func splitByBytes(data []byte, size int, includeEmpty bool) [][]byte {
	if len(data) == 0 {
		if includeEmpty {
			return [][]byte{{}}
		}
		return nil
	}
	out := make([][]byte, 0, (len(data)+size-1)/size)
	for start := 0; start < len(data); start += size {
		end := min(start+size, len(data))
		out = append(out, append([]byte(nil), data[start:end]...))
	}
	return out
}

func splitByRecordCount(records [][]byte, count int, includeEmpty bool) [][]byte {
	if len(records) == 0 {
		if includeEmpty {
			return [][]byte{{}}
		}
		return nil
	}
	var out [][]byte
	for start := 0; start < len(records); start += count {
		end := min(start+count, len(records))
		out = append(out, bytesJoin(records[start:end]))
	}
	return out
}

func splitByLineBytes(records [][]byte, limit int, includeEmpty bool) [][]byte {
	if len(records) == 0 {
		if includeEmpty {
			return [][]byte{{}}
		}
		return nil
	}
	var out [][]byte
	current := make([][]byte, 0)
	currentSize := 0
	flush := func() {
		if len(current) == 0 {
			return
		}
		out = append(out, bytesJoin(current))
		current = nil
		currentSize = 0
	}
	for _, record := range records {
		if len(record) > limit {
			flush()
			out = append(out, append([]byte(nil), record...))
			continue
		}
		if currentSize > 0 && currentSize+len(record) > limit {
			flush()
		}
		current = append(current, append([]byte(nil), record...))
		currentSize += len(record)
	}
	flush()
	return out
}

func splitByByteChunks(data []byte, chunks int, elideEmpty bool) [][]byte {
	out := make([][]byte, 0, chunks)
	if chunks <= 0 {
		return out
	}
	base := 0
	remainder := 0
	if chunks > 0 {
		base = len(data) / chunks
		remainder = len(data) % chunks
	}
	offset := 0
	for i := range chunks {
		size := base
		if i < remainder {
			size++
		}
		end := min(offset+size, len(data))
		chunk := append([]byte(nil), data[offset:end]...)
		offset = end
		if len(chunk) == 0 && elideEmpty {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func splitByDistributedLines(records [][]byte, chunks int, elideEmpty bool) [][]byte {
	if chunks <= 0 {
		return nil
	}
	if len(records) == 0 {
		if elideEmpty {
			return nil
		}
		out := make([][]byte, chunks)
		for i := range out {
			out[i] = []byte{}
		}
		return out
	}
	totalBytes := 0
	for _, record := range records {
		totalBytes += len(record)
	}
	targets := make([]int, chunks)
	base := totalBytes / chunks
	remainder := totalBytes % chunks
	for i := range targets {
		targets[i] = base
		if i < remainder {
			targets[i]++
		}
	}

	out := make([][]byte, 0, chunks)
	current := make([][]byte, 0)
	currentSize := 0
	chunkIndex := 0
	flush := func() {
		out = append(out, bytesJoin(current))
		current = nil
		currentSize = 0
	}
	for _, record := range records {
		if chunkIndex < chunks-1 && len(current) > 0 && currentSize >= targets[chunkIndex] {
			flush()
			chunkIndex++
		}
		current = append(current, append([]byte(nil), record...))
		currentSize += len(record)
	}
	flush()
	for len(out) < chunks {
		if elideEmpty {
			break
		}
		out = append(out, []byte{})
	}
	return out
}

func splitRoundRobin(records [][]byte, chunks int, elideEmpty bool) [][]byte {
	if chunks <= 0 {
		return nil
	}
	out := make([][]byte, chunks)
	for i, record := range records {
		out[i%chunks] = append(out[i%chunks], record...)
	}
	if !elideEmpty {
		return out
	}
	filtered := make([][]byte, 0, len(out))
	for _, chunk := range out {
		if len(chunk) == 0 {
			continue
		}
		filtered = append(filtered, chunk)
	}
	return filtered
}

func splitNumberWritesStdout(opts *splitOptions) bool {
	if opts.mode != splitModeNumber {
		return false
	}
	switch opts.number.mode {
	case splitNumberKthBytes, splitNumberKthLines, splitNumberKthRoundRobin:
		return true
	default:
		return false
	}
}

func splitRequiredSuffixLength(kind splitSuffixKind, value int) int {
	base := 26
	switch kind {
	case splitSuffixNumeric:
		base = 10
	case splitSuffixHex:
		base = 16
	}
	if value <= 0 {
		return 1
	}
	return int(math.Floor(math.Log(float64(value))/math.Log(float64(base)))) + 1
}

func splitSuffixName(index int, opts *splitOptions) (string, error) {
	value := opts.suffixStart + index
	switch opts.suffixKind {
	case splitSuffixNumeric:
		text := strconv.Itoa(value)
		width := opts.suffixLen
		if opts.suffixAutoGrow {
			width = max(width, len(text))
		} else if len(text) > width {
			return "", exitf(nil, 1, "split: output file suffixes exhausted")
		}
		return fmt.Sprintf("%0*d", width, value), nil
	case splitSuffixHex:
		text := strings.ToLower(strconv.FormatInt(int64(value), 16))
		width := opts.suffixLen
		if opts.suffixAutoGrow {
			width = max(width, len(text))
		} else if len(text) > width {
			return "", exitf(nil, 1, "split: output file suffixes exhausted")
		}
		return fmt.Sprintf("%0*x", width, value), nil
	default:
		return splitAlphaSuffix(value, opts.suffixLen, opts.suffixAutoGrow)
	}
}

func splitAlphaSuffix(index, width int, autoGrow bool) (string, error) {
	if index < 0 {
		return "", exitf(nil, 1, "split: output file suffixes exhausted")
	}
	required := 1
	limit := 26
	for index >= limit {
		required++
		if limit > math.MaxInt/26 {
			break
		}
		limit *= 26
	}
	if !autoGrow && required > width {
		return "", exitf(nil, 1, "split: output file suffixes exhausted")
	}
	if autoGrow {
		width = max(width, required)
	}
	digits := make([]byte, width)
	value := index
	for i := width - 1; i >= 0; i-- {
		digits[i] = byte('a' + (value % 26))
		value /= 26
	}
	return string(digits), nil
}

func runSplitFilter(ctx context.Context, inv *Invocation, filter, fileName string, data []byte) error {
	env := make(map[string]string, len(inv.Env)+1)
	maps.Copy(env, inv.Env)
	env["FILE"] = fileName
	result, err := executeCommand(ctx, inv, &executeCommandOptions{
		Argv:    []string{"sh", "-c", filter},
		Env:     env,
		WorkDir: inv.Cwd,
		Stdin:   bytes.NewReader(data),
		Stdout:  inv.Stdout,
		Stderr:  inv.Stderr,
	})
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if result == nil || result.ExitCode == 0 {
		return nil
	}
	return exitForExecutionResult(result)
}

func sameSplitOutputPath(ctx context.Context, target, inputAbs string, inv *Invocation) bool {
	if target == inputAbs {
		return true
	}
	if inv == nil || inv.FS == nil {
		return false
	}
	targetReal, err := inv.FS.Realpath(ctx, target)
	if err != nil {
		return false
	}
	inputReal, err := inv.FS.Realpath(ctx, inputAbs)
	if err != nil {
		return false
	}
	return targetReal == inputReal
}

func normalizeSplitArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "---io-blksize"):
			out = append(out, strings.TrimPrefix(arg, "-"))
		case isObsoleteSplitLinesArg(arg):
			out = append(out, "--obsolete-lines="+strings.TrimPrefix(arg, "-"))
		default:
			out = append(out, arg)
		}
	}
	return out
}

func isObsoleteSplitLinesArg(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return false
	}
	for i := 1; i < len(arg); i++ {
		if arg[i] < '0' || arg[i] > '9' {
			return false
		}
	}
	return true
}

func splitSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesJoin(parts [][]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

var _ Command = (*Split)(nil)
var _ SpecProvider = (*Split)(nil)
var _ ParsedRunner = (*Split)(nil)
