package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"maps"
	"math"
	"math/big"
	"reflect"
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
	kth    uint64
	chunks uint64
}

type splitOptions struct {
	mode             splitMode
	lines            uint64
	bytes            uint64
	lineBytes        uint64
	number           splitNumberSpec
	suffixKind       splitSuffixKind
	suffixStart      uint64
	suffixLen        int
	suffixAutoGrow   bool
	additionalSuffix string
	filter           string
	verbose          bool
	separator        byte
	elideEmpty       bool
}

type splitInput struct {
	data []byte
	abs  string
	info stdfs.FileInfo
}

type splitSuffixGenerator struct {
	alphabet    string
	autoGrow    bool
	width       int
	start       uint64
	initialized bool
	carry       string
	digits      []int
}

type splitSizeSuffix struct {
	token string
	mult  *big.Int
}

var splitSizeSuffixes = buildSplitSizeSuffixes()

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

	input, err := readSplitInput(ctx, inv, inputName)
	if err != nil {
		return err
	}

	if opts.mode == splitModeNumber {
		if splitNumberWritesStdout(&opts) {
			stdoutData := splitNumberStdoutData(input.data, &opts)
			if _, err := inv.Stdout.Write(stdoutData); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			return nil
		}
		return splitWriteNumberContent(ctx, inv, input, prefix, &opts)
	}

	rawChunks := splitContent(input.data, &opts)
	return splitWriteChunks(ctx, inv, input, prefix, &opts, rawChunks)
}

func splitWriteChunks(ctx context.Context, inv *Invocation, input splitInput, prefix string, opts *splitOptions, chunks [][]byte) error {
	suffixes, err := newSplitSuffixGenerator(inv, opts)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if opts.elideEmpty && len(chunk) == 0 {
			continue
		}
		if err := splitWriteChunk(ctx, inv, input, prefix, opts, suffixes, chunk); err != nil {
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
		value, err := parseSplitPositiveCount(matches.Value("obsolete-lines"), true)
		if err != nil {
			return exitf(inv, 1, "split: invalid number of lines %s", quoteGNUOperand(matches.Value("obsolete-lines")))
		}
		opts.mode = splitModeLines
		opts.lines = value
	}
	if matches.Has("lines") {
		selected++
		value, err := parseSplitPositiveCount(matches.Value("lines"), false)
		if err != nil {
			return exitf(inv, 1, "split: invalid number of lines %s", quoteGNUOperand(matches.Value("lines")))
		}
		opts.mode = splitModeLines
		opts.lines = value
	}
	if matches.Has("bytes") {
		selected++
		value, err := parseSplitSize(matches.Value("bytes"))
		if err != nil || value == 0 {
			return exitf(inv, 1, "split: invalid byte count %s", quoteGNUOperand(matches.Value("bytes")))
		}
		opts.mode = splitModeBytes
		opts.bytes = value
	}
	if matches.Has("line-bytes") {
		selected++
		value, err := parseSplitSize(matches.Value("line-bytes"))
		if err != nil || value == 0 {
			return exitf(inv, 1, "split: invalid byte count %s", quoteGNUOperand(matches.Value("line-bytes")))
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
		value, err := parseSplitSuffixLength(matches.Value("suffix-length"))
		if err != nil {
			return exitf(inv, 1, "split: invalid suffix length %s", quoteGNUOperand(matches.Value("suffix-length")))
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
				start, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return exitf(inv, 1, "split: invalid suffix start %s", quoteGNUOperand(value))
				}
				opts.suffixStart = start
				opts.suffixAutoGrow = false
			}
		case "hex-suffixes":
			opts.suffixKind = splitSuffixHex
			value := matches.Value("hex-suffixes")
			if value != "" {
				start, err := strconv.ParseUint(value, 16, 64)
				if err != nil {
					return exitf(inv, 1, "split: invalid suffix start %s", quoteGNUOperand(value))
				}
				opts.suffixStart = start
				opts.suffixAutoGrow = false
			}
		}
	}

	if opts.mode != splitModeNumber || opts.number.chunks == 0 {
		return nil
	}
	if opts.suffixStart < opts.number.chunks {
		required := splitRequiredSuffixLength(opts.suffixKind, splitSaturatingAdd(opts.suffixStart, opts.number.chunks-1))
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
		return 0, fmt.Errorf("split: multi-character separator %s", quoteGNUOperand(value))
	}
	return value[0], nil
}

func parseSplitPositiveCount(raw string, clampOverflow bool) (uint64, error) {
	value := new(big.Int)
	if _, ok := value.SetString(raw, 10); !ok || value.Sign() <= 0 {
		return 0, fmt.Errorf("invalid count")
	}
	return splitBigUint64(value, clampOverflow)
}

func parseSplitSize(raw string) (uint64, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty size")
	}

	digits := raw
	multiplier := big.NewInt(1)
	for _, suffix := range splitSizeSuffixes {
		if !strings.HasSuffix(raw, suffix.token) {
			continue
		}
		digits = raw[:len(raw)-len(suffix.token)]
		multiplier = suffix.mult
		if digits == "" {
			digits = "1"
		}
		break
	}
	value := new(big.Int)
	if _, ok := value.SetString(digits, 10); !ok || value.Sign() <= 0 {
		return 0, fmt.Errorf("invalid size")
	}
	value.Mul(value, multiplier)
	return splitBigUint64(value, true)
}

func parseSplitNumber(value string) (splitNumberSpec, error) {
	parts := strings.Split(value, "/")
	switch len(parts) {
	case 1:
		n, err := parseSplitStrictUint(parts[0])
		if err != nil || n == 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(parts[0]))
		}
		return splitNumberSpec{mode: splitNumberBytes, chunks: n}, nil
	case 2:
		switch parts[0] {
		case "l":
			n, err := parseSplitStrictUint(parts[1])
			if err != nil || n == 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(parts[1]))
			}
			return splitNumberSpec{mode: splitNumberLines, chunks: n}, nil
		case "r":
			n, err := parseSplitStrictUint(parts[1])
			if err != nil || n == 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(parts[1]))
			}
			return splitNumberSpec{mode: splitNumberRoundRobin, chunks: n}, nil
		default:
			k, err := parseSplitStrictUint(parts[0])
			if err != nil || k == 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %s", quoteGNUOperand(parts[0]))
			}
			n, err := parseSplitStrictUint(parts[1])
			if err != nil || n == 0 {
				return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(parts[1]))
			}
			if k > n {
				return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %s", quoteGNUOperand(parts[0]))
			}
			return splitNumberSpec{mode: splitNumberKthBytes, kth: k, chunks: n}, nil
		}
	case 3:
		mode := parts[0]
		k, err := parseSplitStrictUint(parts[1])
		if err != nil || k == 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %s", quoteGNUOperand(parts[1]))
		}
		n, err := parseSplitStrictUint(parts[2])
		if err != nil || n == 0 {
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(parts[2]))
		}
		if k > n {
			return splitNumberSpec{}, fmt.Errorf("invalid chunk number: %s", quoteGNUOperand(parts[1]))
		}
		switch mode {
		case "l":
			return splitNumberSpec{mode: splitNumberKthLines, kth: k, chunks: n}, nil
		case "r":
			return splitNumberSpec{mode: splitNumberKthRoundRobin, kth: k, chunks: n}, nil
		default:
			return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(value))
		}
	default:
		return splitNumberSpec{}, fmt.Errorf("invalid number of chunks: %s", quoteGNUOperand(value))
	}
}

func splitContent(data []byte, opts *splitOptions) [][]byte {
	switch opts.mode {
	case splitModeBytes:
		return splitByBytes(data, opts.bytes)
	case splitModeLines:
		return splitByRecordCount(splitRecords(data, opts.separator), opts.lines)
	case splitModeLineBytes:
		return splitByLineBytes(data, opts.separator, opts.lineBytes)
	case splitModeNumber:
		return nil
	default:
		return nil
	}
}

func splitNumberStdoutData(data []byte, opts *splitOptions) []byte {
	records := splitRecords(data, opts.separator)
	switch opts.number.mode {
	case splitNumberKthBytes:
		return splitExtractByteChunk(data, opts.number.kth, opts.number.chunks)
	case splitNumberKthLines:
		return splitExtractLineChunk(records, len(data), opts.number.kth, opts.number.chunks)
	case splitNumberKthRoundRobin:
		return splitExtractRoundRobinChunk(records, opts.number.kth, opts.number.chunks)
	default:
		return nil
	}
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

func splitByBytes(data []byte, size uint64) [][]byte {
	if len(data) == 0 || size == 0 {
		return nil
	}
	if size >= uint64(len(data)) {
		return [][]byte{append([]byte(nil), data...)}
	}

	chunkSize := int(size)
	out := make([][]byte, 0, (len(data)+chunkSize-1)/chunkSize)
	for start := 0; start < len(data); start += chunkSize {
		end := min(start+chunkSize, len(data))
		out = append(out, append([]byte(nil), data[start:end]...))
	}
	return out
}

func splitByRecordCount(records [][]byte, count uint64) [][]byte {
	if len(records) == 0 || count == 0 {
		return nil
	}
	if count >= uint64(len(records)) {
		return [][]byte{bytesJoin(records)}
	}

	step := int(count)
	out := make([][]byte, 0, (len(records)+step-1)/step)
	for start := 0; start < len(records); start += step {
		end := min(start+step, len(records))
		out = append(out, bytesJoin(records[start:end]))
	}
	return out
}

func splitByLineBytes(data []byte, sep byte, limit uint64) [][]byte {
	if len(data) == 0 || limit == 0 {
		return nil
	}
	if limit > uint64(len(data)) {
		return [][]byte{append([]byte(nil), data...)}
	}

	maxChunk := int(limit)
	var out [][]byte
	nOut := 0
	hold := make([]byte, 0)
	splitLine := false
	writeChunk := func(newFile bool, part []byte) {
		if len(part) == 0 {
			return
		}
		piece := append([]byte(nil), part...)
		if newFile || len(out) == 0 {
			out = append(out, piece)
			return
		}
		out[len(out)-1] = append(out[len(out)-1], piece...)
	}

	sob := data
	for len(sob) > 0 {
		nLeft := len(sob)
		splitRest := 0
		hasChunkEnd := false
		eol := -1
		if maxChunk-nOut-len(hold) <= nLeft {
			hasChunkEnd = true
			splitRest = maxChunk - nOut - len(hold)
			if splitRest > 0 {
				eol = bytes.LastIndexByte(sob[:splitRest], sep)
			}
		} else {
			eol = bytes.LastIndexByte(sob, sep)
		}

		if len(hold) > 0 && (eol >= 0 || nOut == 0) {
			writeChunk(nOut == 0, hold)
			nOut += len(hold)
			hold = hold[:0]
		}

		if eol >= 0 {
			splitLine = true
			nWrite := eol + 1
			writeChunk(nOut == 0, sob[:nWrite])
			nOut += nWrite
			sob = sob[nWrite:]
			nLeft -= nWrite
			if hasChunkEnd {
				splitRest -= nWrite
			}
		}

		if len(sob) > 0 && !splitLine {
			nWrite := len(sob)
			if hasChunkEnd {
				nWrite = splitRest
			}
			if nWrite > 0 {
				writeChunk(nOut == 0, sob[:nWrite])
				nOut += nWrite
				sob = sob[nWrite:]
				nLeft -= nWrite
				if hasChunkEnd {
					splitRest -= nWrite
				}
			}
		}

		if (hasChunkEnd && splitRest > 0) || (!hasChunkEnd && nLeft > 0) {
			nBuf := nLeft
			if hasChunkEnd {
				nBuf = splitRest
			}
			hold = append(hold, sob[:nBuf]...)
			sob = sob[nBuf:]
		}

		if hasChunkEnd {
			nOut = 0
			splitLine = false
		}
	}

	if len(hold) > 0 {
		writeChunk(nOut == 0, hold)
	}
	return out
}

func splitExtractLineChunk(records [][]byte, totalBytes int, kth, chunks uint64) []byte {
	if chunks == 0 || kth == 0 || kth > chunks {
		return []byte{}
	}
	if len(records) == 0 {
		return []byte{}
	}

	base := uint64(totalBytes) / chunks
	remainder := uint64(totalBytes) % chunks
	chunkIndex := uint64(0)
	offset := uint64(0)
	var out []byte
	for _, record := range records {
		if chunkIndex+1 == kth {
			out = append(out, record...)
		}
		offset += uint64(len(record))
		chunkIndex = splitChunkIndexForOffset(base, remainder, chunks, offset)
	}
	return out
}

func splitExtractRoundRobinChunk(records [][]byte, kth, chunks uint64) []byte {
	if chunks == 0 || kth == 0 || kth > chunks {
		return []byte{}
	}
	var out []byte
	for i, record := range records {
		if uint64(i)%chunks+1 == kth {
			out = append(out, record...)
		}
	}
	return out
}

func splitExtractByteChunk(data []byte, kth, chunks uint64) []byte {
	if chunks == 0 || kth == 0 || kth > chunks {
		return []byte{}
	}
	fileSize := uint64(len(data))
	start := (kth-1)*(fileSize/chunks) + min(kth-1, fileSize%chunks)
	end := fileSize
	if kth != chunks {
		end = kth*(fileSize/chunks) + min(kth, fileSize%chunks)
	}
	if start >= fileSize || start >= end {
		return []byte{}
	}
	return append([]byte(nil), data[int(start):int(end)]...)
}

func splitWriteNumberContent(ctx context.Context, inv *Invocation, input splitInput, prefix string, opts *splitOptions) error {
	suffixes, err := newSplitSuffixGenerator(inv, opts)
	if err != nil {
		return err
	}

	writeChunk := func(chunk []byte) error {
		if opts.elideEmpty && len(chunk) == 0 {
			return nil
		}
		return splitWriteChunk(ctx, inv, input, prefix, opts, suffixes, chunk)
	}

	switch opts.number.mode {
	case splitNumberBytes:
		return splitEmitByteChunks(input.data, opts.number.chunks, opts.elideEmpty, writeChunk)
	case splitNumberLines:
		return splitEmitLineChunks(splitRecords(input.data, opts.separator), len(input.data), opts.number.chunks, opts.elideEmpty, writeChunk)
	case splitNumberRoundRobin:
		return splitEmitRoundRobin(splitRecords(input.data, opts.separator), opts.number.chunks, opts.elideEmpty, writeChunk)
	default:
		return nil
	}
}

func splitWriteChunk(ctx context.Context, inv *Invocation, input splitInput, prefix string, opts *splitOptions, suffixes *splitSuffixGenerator, chunk []byte) error {
	name, err := suffixes.Next(inv)
	if err != nil {
		return err
	}
	displayName := prefix + name + opts.additionalSuffix
	target := gbfs.Resolve(inv.Cwd, displayName)
	resolvedTarget, targetInfo := splitResolveOutputTarget(ctx, inv, target)
	if splitWouldOverwriteInput(ctx, inv, target, resolvedTarget, input.abs, input.info, targetInfo) {
		return exitf(inv, 1, "split: %s would overwrite input; aborting", quoteGNUOperand(resolvedTarget))
	}
	if opts.verbose {
		if _, err := fmt.Fprintf(inv.Stdout, "creating file '%s'\n", displayName); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if opts.filter != "" {
		return runSplitFilter(ctx, inv, opts.filter, displayName, chunk)
	}
	if err := writeFileContents(ctx, inv, resolvedTarget, chunk, stdfs.FileMode(0o644)); err != nil {
		return exitf(inv, 1, "split: %s: %s", displayName, splitWriteErrorText(err))
	}
	return nil
}

func splitEmitByteChunks(data []byte, chunks uint64, elideEmpty bool, emit func([]byte) error) error {
	if chunks == 0 {
		return nil
	}

	nonEmpty := min(chunks, uint64(len(data)))
	base := uint64(0)
	remainder := uint64(0)
	if chunks > 0 {
		base = uint64(len(data)) / chunks
		remainder = uint64(len(data)) % chunks
	}
	offset := 0
	for i := range nonEmpty {
		size := base
		if i < remainder {
			size++
		}
		end := min(offset+int(size), len(data))
		if err := emit(append([]byte(nil), data[offset:end]...)); err != nil {
			return err
		}
		offset = end
	}
	if elideEmpty {
		return nil
	}
	for i := nonEmpty; i < chunks; i++ {
		if err := emit(nil); err != nil {
			return err
		}
	}
	return nil
}

func splitEmitLineChunks(records [][]byte, totalBytes int, chunks uint64, elideEmpty bool, emit func([]byte) error) error {
	if chunks == 0 {
		return nil
	}
	if len(records) == 0 {
		if elideEmpty {
			return nil
		}
		for range chunks {
			if err := emit(nil); err != nil {
				return err
			}
		}
		return nil
	}

	base := uint64(totalBytes) / chunks
	remainder := uint64(totalBytes) % chunks
	chunkIndex := uint64(0)
	offset := uint64(0)
	var current []byte
	flushUntil := func(target uint64) error {
		if elideEmpty {
			if chunkIndex >= target {
				return nil
			}
			if len(current) > 0 {
				if err := emit(current); err != nil {
					return err
				}
				current = nil
			}
			chunkIndex = min(target, chunks)
			return nil
		}
		for chunkIndex < target && chunkIndex < chunks {
			if err := emit(current); err != nil {
				return err
			}
			current = nil
			chunkIndex++
		}
		return nil
	}

	for _, record := range records {
		current = append(current, record...)
		offset += uint64(len(record))
		nextChunk := splitChunkIndexForOffset(base, remainder, chunks, offset)
		if err := flushUntil(nextChunk); err != nil {
			return err
		}
	}

	if !elideEmpty || len(current) > 0 {
		if err := emit(current); err != nil {
			return err
		}
	}
	chunkIndex++
	if elideEmpty {
		return nil
	}
	for ; chunkIndex < chunks; chunkIndex++ {
		if err := emit(nil); err != nil {
			return err
		}
	}
	return nil
}

func splitEmitRoundRobin(records [][]byte, chunks uint64, elideEmpty bool, emit func([]byte) error) error {
	if chunks == 0 {
		return nil
	}
	nonEmpty := min(chunks, uint64(len(records)))
	for chunkIndex := range nonEmpty {
		var out []byte
		for recordIndex := chunkIndex; recordIndex < uint64(len(records)); recordIndex += chunks {
			out = append(out, records[recordIndex]...)
		}
		if err := emit(out); err != nil {
			return err
		}
	}
	if elideEmpty {
		return nil
	}
	for chunkIndex := nonEmpty; chunkIndex < chunks; chunkIndex++ {
		if err := emit(nil); err != nil {
			return err
		}
	}
	return nil
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

func splitRequiredSuffixLength(kind splitSuffixKind, value uint64) int {
	base := uint64(26)
	switch kind {
	case splitSuffixNumeric:
		base = 10
	case splitSuffixHex:
		base = 16
	}
	length := 1
	for value >= base {
		value /= base
		length++
	}
	return length
}

func newSplitSuffixGenerator(inv *Invocation, opts *splitOptions) (*splitSuffixGenerator, error) {
	gen := &splitSuffixGenerator{
		alphabet:    splitSuffixAlphabet(opts.suffixKind),
		autoGrow:    opts.suffixAutoGrow,
		width:       opts.suffixLen,
		start:       opts.suffixStart,
		carry:       "",
		initialized: false,
	}
	if gen.width <= 0 {
		gen.width = 2
	}
	if !gen.autoGrow && opts.suffixKind != splitSuffixAlpha {
		maxDigits := splitRequiredSuffixLength(opts.suffixKind, opts.suffixStart)
		if maxDigits > gen.width {
			return nil, exitf(inv, 1, "split: output file suffixes exhausted")
		}
	}
	return gen, nil
}

func (g *splitSuffixGenerator) Next(inv *Invocation) (string, error) {
	if !g.initialized {
		if err := g.init(inv); err != nil {
			return "", err
		}
		g.initialized = true
		return g.current(), nil
	}
	if err := g.increment(inv); err != nil {
		return "", err
	}
	return g.current(), nil
}

func (g *splitSuffixGenerator) init(inv *Invocation) error {
	g.digits = make([]int, g.width)
	if g.start == 0 || g.alphabet == splitSuffixAlphabet(splitSuffixAlpha) {
		return nil
	}
	base := uint64(len(g.alphabet))
	value := g.start
	for i := g.width - 1; i >= 0 && value > 0; i-- {
		g.digits[i] = int(value % base)
		value /= base
	}
	if value != 0 {
		return exitf(inv, 1, "split: output file suffixes exhausted")
	}
	return nil
}

func (g *splitSuffixGenerator) increment(inv *Invocation) error {
	base := len(g.alphabet)
	for i := len(g.digits) - 1; i >= 0; i-- {
		g.digits[i]++
		if g.autoGrow && i == 0 && g.digits[i]+1 == base {
			g.carry += string(g.alphabet[g.digits[i]])
			g.width++
			g.digits = make([]int, g.width)
			return nil
		}
		if g.digits[i] < base {
			return nil
		}
		g.digits[i] = 0
	}
	return exitf(inv, 1, "split: output file suffixes exhausted")
}

func (g *splitSuffixGenerator) current() string {
	var b strings.Builder
	b.Grow(len(g.carry) + len(g.digits))
	b.WriteString(g.carry)
	for _, digit := range g.digits {
		b.WriteByte(g.alphabet[digit])
	}
	return b.String()
}

func splitSuffixAlphabet(kind splitSuffixKind) string {
	switch kind {
	case splitSuffixNumeric:
		return "0123456789"
	case splitSuffixHex:
		return "0123456789abcdef"
	default:
		return "abcdefghijklmnopqrstuvwxyz"
	}
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

func readSplitInput(ctx context.Context, inv *Invocation, inputName string) (splitInput, error) {
	if inputName == "-" {
		data, err := readAllStdin(ctx, inv)
		if err != nil {
			return splitInput{}, err
		}
		return splitInput{data: data, info: splitStdinFileInfo(inv)}, nil
	}

	readName := inputName
	if resolved, err := canonicalizeReadlink(ctx, inv, inputName, readlinkModeCanonicalizeExisting); err == nil && resolved != "" {
		readName = resolved
	}
	data, abs, err := readAllFile(ctx, inv, readName)
	if err != nil {
		return splitInput{}, err
	}
	info, _, err := statPath(ctx, inv, readName)
	if err != nil {
		return splitInput{}, err
	}
	return splitInput{data: data, abs: abs, info: info}, nil
}

func splitWouldOverwriteInput(ctx context.Context, inv *Invocation, target, resolvedTarget, inputAbs string, inputInfo, targetInfo stdfs.FileInfo) bool {
	if inputAbs != "" && (target == inputAbs || resolvedTarget == inputAbs) {
		return true
	}
	if inputInfo != nil {
		if targetInfo != nil && splitSameFile(inputInfo, targetInfo) {
			return true
		}
	}
	if inputAbs == "" || inv == nil || inv.FS == nil {
		return false
	}
	targetReal, err := inv.FS.Realpath(ctx, resolvedTarget)
	if err != nil {
		return false
	}
	inputReal, err := inv.FS.Realpath(ctx, inputAbs)
	if err != nil {
		return false
	}
	return targetReal == inputReal
}

func splitWriteErrorText(err error) string {
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Err != nil {
		err = exitErr.Err
	}
	if err == nil {
		return "unknown error"
	}
	if text := shellWriteErrorText(err); text != "" {
		return text
	}
	return readAllErrorText(err)
}

func normalizeSplitArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "---io"):
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

func splitResolveOutputTarget(ctx context.Context, inv *Invocation, target string) (string, stdfs.FileInfo) {
	resolved := target
	if next, err := canonicalizeReadlink(ctx, inv, target, readlinkModeCanonicalizeExisting); err == nil && next != "" {
		resolved = next
	}
	info, _, exists, err := statMaybe(ctx, inv, resolved)
	if err != nil || !exists {
		return resolved, nil
	}
	return resolved, info
}

type splitUnderlyingReader interface {
	UnderlyingReader() io.Reader
}

type splitStatter interface {
	Stat() (stdfs.FileInfo, error)
}

func splitResolveReader(reader io.Reader) io.Reader {
	for reader != nil {
		underlying, ok := reader.(splitUnderlyingReader)
		if !ok {
			return reader
		}
		next := underlying.UnderlyingReader()
		if next == nil || next == reader {
			return reader
		}
		reader = next
	}
	return nil
}

func splitStdinFileInfo(inv *Invocation) stdfs.FileInfo {
	if inv == nil || inv.Stdin == nil {
		return nil
	}
	reader := splitResolveReader(inv.Stdin)
	statter, ok := reader.(splitStatter)
	if !ok {
		return nil
	}
	info, err := statter.Stat()
	if err != nil {
		return nil
	}
	return info
}

func splitSameFile(a, b stdfs.FileInfo) bool {
	if testSameFile(a, b) {
		return true
	}
	nodeA, okA := splitNodeID(a)
	nodeB, okB := splitNodeID(b)
	return okA && okB && nodeA == nodeB
}

func splitNodeID(info stdfs.FileInfo) (uint64, bool) {
	if info == nil {
		return 0, false
	}
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return 0, false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return 0, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0, false
	}
	field := value.FieldByName("NodeID")
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(field.Int()), true
	default:
		return 0, false
	}
}

func buildSplitSizeSuffixes() []splitSizeSuffix {
	letters := []struct {
		upper    string
		lower    string
		exponent int
	}{
		{"Y", "y", 8},
		{"Z", "z", 7},
		{"E", "e", 6},
		{"P", "p", 5},
		{"T", "t", 4},
		{"G", "g", 3},
		{"M", "m", 2},
		{"K", "k", 1},
	}

	out := make([]splitSizeSuffix, 0, len(letters)*6+2)
	for _, letter := range letters {
		out = append(out,
			splitSizeSuffix{token: letter.upper + "iB", mult: splitBigPow(1024, letter.exponent)},
			splitSizeSuffix{token: letter.lower + "iB", mult: splitBigPow(1024, letter.exponent)},
			splitSizeSuffix{token: letter.upper + "B", mult: splitBigPow(1000, letter.exponent)},
			splitSizeSuffix{token: letter.lower + "B", mult: splitBigPow(1000, letter.exponent)},
			splitSizeSuffix{token: letter.upper, mult: splitBigPow(1024, letter.exponent)},
			splitSizeSuffix{token: letter.lower, mult: splitBigPow(1024, letter.exponent)},
		)
	}
	out = append(out,
		splitSizeSuffix{token: "b", mult: big.NewInt(512)},
		splitSizeSuffix{token: "B", mult: big.NewInt(1)},
	)
	return out
}

func splitBigPow(base int64, exponent int) *big.Int {
	result := big.NewInt(1)
	multiplier := big.NewInt(base)
	for range exponent {
		result.Mul(result, multiplier)
	}
	return result
}

func splitBigUint64(value *big.Int, clampOverflow bool) (uint64, error) {
	if value == nil || value.Sign() <= 0 {
		return 0, fmt.Errorf("invalid value")
	}
	if value.IsUint64() {
		return value.Uint64(), nil
	}
	if clampOverflow {
		return math.MaxUint64, nil
	}
	return 0, fmt.Errorf("invalid value")
}

func parseSplitStrictUint(raw string) (uint64, error) {
	if raw == "" {
		return 0, fmt.Errorf("invalid value")
	}
	if strings.HasPrefix(raw, "+") {
		raw = raw[1:]
		if raw == "" {
			return 0, fmt.Errorf("invalid value")
		}
	}
	return strconv.ParseUint(raw, 10, 64)
}

func parseSplitSuffixLength(raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("invalid suffix length")
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value > uint64(math.MaxInt) {
		return 0, fmt.Errorf("invalid suffix length")
	}
	return int(value), nil
}

func splitChunkIndexForOffset(base, remainder, chunks, offset uint64) uint64 {
	if chunks == 0 {
		return 0
	}
	totalBytes := base*chunks + remainder
	if offset >= totalBytes {
		return chunks - 1
	}
	if base == 0 {
		return offset
	}
	largeBytes := remainder * (base + 1)
	if offset < largeBytes {
		return offset / (base + 1)
	}
	return remainder + (offset-largeBytes)/base
}

func splitSaturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

var _ Command = (*Split)(nil)
var _ SpecProvider = (*Split)(nil)
var _ ParsedRunner = (*Split)(nil)
