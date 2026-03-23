package builtins

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

type Shuf struct{}

type shufOptions struct {
	args            []string
	echo            bool
	inputRange      shufInputRange
	inputRangeRaw   string
	headCount       uint64
	headCountSet    bool
	output          string
	outputSet       bool
	randomSource    string
	randomSourceSet bool
	repeat          bool
	zeroTerminated  bool
}

type shufInputKind int

const (
	shufInputDefault shufInputKind = iota
	shufInputEcho
	shufInputRangeMode
)

type shufPreparedInput struct {
	kind    shufInputKind
	fileAbs string
	records [][]byte
	args    []string
	rngSpan shufInputRange
}

const (
	shufDefaultSparseCapacity = 128
	shufFullRangeMaxItems     = 16_777_216
	shufVersionText           = "shuf (gbash) dev\n"
	shufHelpText              = "" +
		"Usage: shuf [OPTION]... [FILE]\n" +
		"  or:  shuf -e [OPTION]... [ARG]...\n" +
		"  or:  shuf -i LO-HI [OPTION]...\n" +
		"Write a random permutation of the input lines to standard output.\n" +
		"\n" +
		"With no FILE, or when FILE is -, read standard input.\n" +
		"\n" +
		"Mandatory arguments to long options are mandatory for short options too.\n" +
		"  -e, --echo                treat each ARG as an input line\n" +
		"  -i, --input-range=LO-HI   treat each number LO through HI as an input line\n" +
		"  -n, --head-count=COUNT    output at most COUNT lines\n" +
		"  -o, --output=FILE         write result to FILE instead of standard output\n" +
		"      --random-source=FILE  get random bytes from FILE\n" +
		"  -r, --repeat              output lines can be repeated\n" +
		"  -z, --zero-terminated     line delimiter is NUL, not newline\n" +
		"      --help                display this help and exit\n" +
		"      --version             output version information and exit\n"
)

func NewShuf() *Shuf {
	return &Shuf{}
}

func (c *Shuf) Name() string {
	return "shuf"
}

func (c *Shuf) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Shuf) Spec() CommandSpec {
	return CommandSpec{
		Name:  "shuf",
		About: "Write a random permutation of the input lines to standard output.",
		Usage: "shuf [OPTION]... [FILE]\n  or:  shuf -e [OPTION]... [ARG]...\n  or:  shuf -i LO-HI [OPTION]...",
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, shufHelpText)
			return err
		},
		VersionRenderer: renderStaticVersion(shufVersionText),
		Options: []OptionSpec{
			{Name: "echo", Short: 'e', Long: "echo", Help: "treat each ARG as an input line"},
			{Name: "input-range", Short: 'i', Long: "input-range", Arity: OptionRequiredValue, ValueName: "LO-HI", Help: "treat each number LO through HI as an input line"},
			{Name: "head-count", Short: 'n', Long: "head-count", Arity: OptionRequiredValue, ValueName: "COUNT", Repeatable: true, Help: "output at most COUNT lines"},
			{Name: "output", Short: 'o', Long: "output", Arity: OptionRequiredValue, ValueName: "FILE", Help: "write result to FILE instead of standard output"},
			{Name: "random-source", Long: "random-source", Arity: OptionRequiredValue, ValueName: "FILE", Help: "get random bytes from FILE"},
			{Name: "repeat", Short: 'r', Long: "repeat", Help: "output lines can be repeated"},
			{Name: "zero-terminated", Short: 'z', Long: "zero-terminated", Help: "line delimiter is NUL, not newline"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "arg", ValueName: "ARG", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
		},
	}
}

func (c *Shuf) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		spec := c.Spec()
		return RenderCommandHelp(inv.Stdout, &spec)
	}
	if matches.Has("version") {
		spec := c.Spec()
		return RenderCommandVersion(inv.Stdout, &spec)
	}

	opts, err := parseShufMatches(inv, matches)
	if err != nil {
		return err
	}
	if opts.headCountSet && opts.headCount == 0 {
		if opts.outputSet {
			return shufWriteOutputFile(ctx, inv, opts.output, nil)
		}
		return nil
	}

	return c.runShuf(ctx, inv, &opts)
}

func parseShufMatches(inv *Invocation, matches *ParsedCommand) (shufOptions, error) {
	opts := shufOptions{
		args:            matches.Args("arg"),
		echo:            matches.Has("echo"),
		inputRangeRaw:   matches.Value("input-range"),
		output:          matches.Value("output"),
		outputSet:       matches.Has("output"),
		randomSource:    matches.Value("random-source"),
		randomSourceSet: matches.Has("random-source"),
		repeat:          matches.Has("repeat"),
		zeroTerminated:  matches.Has("zero-terminated"),
	}

	switch {
	case matches.Count("input-range") > 1:
		return shufOptions{}, exitf(inv, 1, "shuf: multiple -i options specified")
	case matches.Count("output") > 1:
		return shufOptions{}, exitf(inv, 1, "shuf: multiple output files specified")
	case matches.Count("random-source") > 1:
		return shufOptions{}, exitf(inv, 1, "shuf: multiple random sources specified")
	}

	if opts.echo && opts.inputRangeRaw != "" {
		return shufOptions{}, commandUsageError(inv, "shuf", "cannot combine -e and -i options")
	}
	if opts.inputRangeRaw != "" {
		if len(opts.args) > 0 {
			return shufOptions{}, commandUsageError(inv, "shuf", "extra operand %s", quoteGNUOperand(opts.args[0]))
		}
		inputRange, err := parseShufInputRange(inv, opts.inputRangeRaw)
		if err != nil {
			return shufOptions{}, err
		}
		opts.inputRange = inputRange
	} else if !opts.echo && len(opts.args) > 1 {
		return shufOptions{}, commandUsageError(inv, "shuf", "extra operand %s", quoteGNUOperand(opts.args[1]))
	}

	if matches.Count("head-count") > 0 {
		opts.headCountSet = true
		opts.headCount = math.MaxUint64
		for _, raw := range matches.Values("head-count") {
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return shufOptions{}, exitf(inv, 1, "shuf: invalid line count: %s", quoteGNUOperand(raw))
			}
			if value < opts.headCount {
				opts.headCount = value
			}
		}
	}

	return opts, nil
}

func parseShufInputRange(inv *Invocation, raw string) (shufInputRange, error) {
	startRaw, endRaw, ok := strings.Cut(raw, "-")
	if !ok {
		return shufInputRange{}, exitf(inv, 1, "shuf: invalid input range: %s", quoteGNUOperand(raw))
	}

	start, err := strconv.ParseUint(startRaw, 10, 64)
	if err != nil {
		return shufInputRange{}, exitf(inv, 1, "shuf: invalid input range: %s", quoteGNUOperand(raw))
	}
	end, err := strconv.ParseUint(endRaw, 10, 64)
	if err != nil {
		return shufInputRange{}, exitf(inv, 1, "shuf: invalid input range: %s", quoteGNUOperand(raw))
	}
	if start <= end {
		return shufInputRange{start: start, end: end}, nil
	}
	if end != math.MaxUint64 && start == end+1 {
		return shufInputRange{start: start, end: end, empty: true}, nil
	}
	return shufInputRange{}, exitf(inv, 1, "shuf: invalid input range: %s", quoteGNUOperand(raw))
}

func (c *Shuf) runShuf(ctx context.Context, inv *Invocation, opts *shufOptions) error {
	input, err := prepareShufInput(ctx, inv, opts)
	if err != nil {
		return err
	}

	rng := &shufRNGHandle{
		inv:          inv,
		randomSource: opts.randomSource,
		opener: func() (shufRandomSource, error) {
			if opts.randomSource == "" {
				rng, err := newShufDefaultRNG()
				if err != nil {
					return nil, &ExitError{Code: 1, Err: err}
				}
				return rng, nil
			}
			file, _, err := openRead(ctx, inv, opts.randomSource)
			if err != nil {
				return nil, exitf(inv, 1, "shuf: %s: %s", quoteGNUOperand(opts.randomSource), readAllErrorText(err))
			}
			return &shufFileRNG{
				file:   file,
				reader: bufio.NewReader(file),
			}, nil
		},
	}
	defer func() { _ = rng.Close() }()

	sep := byte('\n')
	if opts.zeroTerminated {
		sep = 0
	}

	var (
		outputBuf    bytes.Buffer
		outputFile   io.Closer
		outputWriter = inv.Stdout
		outputAbs    string
		bufferOutput bool
	)
	if opts.outputSet {
		outputAbs = allowPath(inv, opts.output)
		bufferOutput = outputAbs == input.fileAbs
		if !bufferOutput && opts.randomSourceSet && outputAbs == allowPath(inv, opts.randomSource) {
			bufferOutput = true
		}
		if bufferOutput {
			outputWriter = &outputBuf
		} else {
			file, abs, err := shufOpenOutputFile(ctx, inv, opts.output)
			if err != nil {
				return err
			}
			outputFile = file
			outputAbs = abs
			outputWriter = file
		}
	}

	buffered := bufio.NewWriter(outputWriter)
	runErr := runShufPreparedInput(ctx, inv, buffered, rng, &input, opts, sep)
	flushErr := buffered.Flush()
	if flushErr != nil && !shufBrokenPipe(flushErr) {
		if runErr == nil {
			runErr = &ExitError{Code: 1, Err: flushErr}
		}
	}

	if outputFile != nil {
		closeErr := outputFile.Close()
		if closeErr != nil && runErr == nil {
			runErr = &ExitError{Code: 1, Err: closeErr}
		}
		recordFileMutation(inv.TraceRecorder(), "write", outputAbs, outputAbs, outputAbs)
	}

	if opts.outputSet && bufferOutput {
		if err := shufWriteOutputFile(ctx, inv, opts.output, outputBuf.Bytes()); err != nil {
			return err
		}
	}

	if runErr != nil {
		return runErr
	}
	if flushErr != nil && shufBrokenPipe(flushErr) {
		return nil
	}
	return nil
}

func prepareShufInput(ctx context.Context, inv *Invocation, opts *shufOptions) (shufPreparedInput, error) {
	if opts.echo {
		return shufPreparedInput{
			kind: shufInputEcho,
			args: append([]string(nil), opts.args...),
		}, nil
	}
	if opts.inputRangeRaw != "" {
		return shufPreparedInput{
			kind:    shufInputRangeMode,
			rngSpan: opts.inputRange,
		}, nil
	}

	name := "-"
	if len(opts.args) > 0 {
		name = opts.args[0]
	}

	var (
		data []byte
		abs  string
		err  error
	)
	if name == "-" {
		data, err = readAllStdin(ctx, inv)
	} else {
		data, abs, err = readAllFile(ctx, inv, name)
	}
	if err != nil {
		if name == "-" {
			return shufPreparedInput{}, err
		}
		return shufPreparedInput{}, exitf(inv, 1, "shuf: %s: %s", quoteGNUOperand(name), readAllErrorText(err))
	}

	sep := byte('\n')
	if opts.zeroTerminated {
		sep = 0
	}
	return shufPreparedInput{
		kind:    shufInputDefault,
		fileAbs: abs,
		records: splitShufRecords(data, sep),
	}, nil
}

func splitShufRecords(data []byte, sep byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	records := bytes.Split(data, []byte{sep})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	return records
}

func runShufPreparedInput(ctx context.Context, inv *Invocation, writer *bufio.Writer, rng *shufRNGHandle, input *shufPreparedInput, opts *shufOptions, sep byte) error {
	switch input.kind {
	case shufInputEcho:
		return shufRunStringSlice(ctx, inv, writer, rng, input.args, opts, sep)
	case shufInputRangeMode:
		return shufRunRange(ctx, inv, writer, rng, input.rngSpan, opts, sep)
	default:
		return shufRunByteSlice(ctx, inv, writer, rng, input.records, opts, sep)
	}
}

func shufRunByteSlice(ctx context.Context, inv *Invocation, writer *bufio.Writer, rng *shufRNGHandle, records [][]byte, opts *shufOptions, sep byte) error {
	if opts.repeat {
		if len(records) == 0 {
			return exitf(inv, 1, "shuf: no lines to repeat")
		}
		if !opts.headCountSet {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				index, err := shufChooseIndex(rng, len(records))
				if err != nil {
					return shufRandomExecutionError(inv, opts.randomSource, err)
				}
				if err := shufWriteBytesRecord(writer, records[index], sep); err != nil {
					if shufBrokenPipe(err) {
						return nil
					}
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		for i := uint64(0); i < opts.headCount; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			index, err := shufChooseIndex(rng, len(records))
			if err != nil {
				return shufRandomExecutionError(inv, opts.randomSource, err)
			}
			if err := shufWriteBytesRecord(writer, records[index], sep); err != nil {
				if shufBrokenPipe(err) {
					return nil
				}
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	limit := len(records)
	if opts.headCountSet && opts.headCount < uint64(limit) {
		limit = int(opts.headCount)
	}
	if err := shufShufflePrefix(records, limit, rng); err != nil {
		return shufRandomExecutionError(inv, opts.randomSource, err)
	}
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := shufWriteBytesRecord(writer, records[i], sep); err != nil {
			if shufBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func shufRunStringSlice(ctx context.Context, inv *Invocation, writer *bufio.Writer, rng *shufRNGHandle, args []string, opts *shufOptions, sep byte) error {
	if opts.repeat {
		if len(args) == 0 {
			return exitf(inv, 1, "shuf: no lines to repeat")
		}
		if !opts.headCountSet {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				index, err := shufChooseIndex(rng, len(args))
				if err != nil {
					return shufRandomExecutionError(inv, opts.randomSource, err)
				}
				if err := shufWriteStringRecord(writer, args[index], sep); err != nil {
					if shufBrokenPipe(err) {
						return nil
					}
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		for i := uint64(0); i < opts.headCount; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			index, err := shufChooseIndex(rng, len(args))
			if err != nil {
				return shufRandomExecutionError(inv, opts.randomSource, err)
			}
			if err := shufWriteStringRecord(writer, args[index], sep); err != nil {
				if shufBrokenPipe(err) {
					return nil
				}
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	limit := len(args)
	if opts.headCountSet && opts.headCount < uint64(limit) {
		limit = int(opts.headCount)
	}
	if err := shufShufflePrefix(args, limit, rng); err != nil {
		return shufRandomExecutionError(inv, opts.randomSource, err)
	}
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := shufWriteStringRecord(writer, args[i], sep); err != nil {
			if shufBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
	}
	return nil
}

func shufRunRange(ctx context.Context, inv *Invocation, writer *bufio.Writer, rng *shufRNGHandle, inputRange shufInputRange, opts *shufOptions, sep byte) error {
	if opts.repeat {
		if inputRange.empty {
			return exitf(inv, 1, "shuf: no lines to repeat")
		}
		if !opts.headCountSet {
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				value, err := inputRange.choose(rng)
				if err != nil {
					return shufRandomExecutionError(inv, opts.randomSource, err)
				}
				if err := shufWriteUint64Record(writer, value, sep); err != nil {
					if shufBrokenPipe(err) {
						return nil
					}
					return &ExitError{Code: 1, Err: err}
				}
			}
		}
		for i := uint64(0); i < opts.headCount; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			value, err := inputRange.choose(rng)
			if err != nil {
				return shufRandomExecutionError(inv, opts.randomSource, err)
			}
			if err := shufWriteUint64Record(writer, value, sep); err != nil {
				if shufBrokenPipe(err) {
					return nil
				}
				return &ExitError{Code: 1, Err: err}
			}
		}
		return nil
	}

	if inputRange.empty {
		return nil
	}

	var headCount *uint64
	if opts.headCountSet {
		headCount = &opts.headCount
	}
	iter := newShufNonrepeatingRangeIterator(inputRange, rng, headCount)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, ok, err := iter.Next()
		if err != nil {
			return shufRandomExecutionError(inv, opts.randomSource, err)
		}
		if !ok {
			return nil
		}
		if err := shufWriteUint64Record(writer, value, sep); err != nil {
			if shufBrokenPipe(err) {
				return nil
			}
			return &ExitError{Code: 1, Err: err}
		}
	}
}

func shufChooseIndex(rng shufRandomSource, length int) (int, error) {
	if length <= 0 {
		return 0, nil
	}
	value, err := rng.generateAtMost(uint64(length - 1))
	if err != nil {
		return 0, err
	}
	return int(value), nil
}

func shufShufflePrefix[T any](items []T, amount int, rng shufRandomSource) error {
	if amount > len(items) {
		amount = len(items)
	}
	for i := 0; i < amount; i++ {
		other, err := rng.generateAtMost(uint64(len(items) - i - 1))
		if err != nil {
			return err
		}
		j := i + int(other)
		items[i], items[j] = items[j], items[i]
	}
	return nil
}

func shufWriteBytesRecord(writer *bufio.Writer, record []byte, sep byte) error {
	if _, err := writer.Write(record); err != nil {
		return err
	}
	return writer.WriteByte(sep)
}

func shufWriteStringRecord(writer *bufio.Writer, record string, sep byte) error {
	if _, err := writer.WriteString(record); err != nil {
		return err
	}
	return writer.WriteByte(sep)
}

func shufWriteUint64Record(writer *bufio.Writer, value uint64, sep byte) error {
	var scratch [20]byte
	out := strconv.AppendUint(scratch[:0], value, 10)
	if _, err := writer.Write(out); err != nil {
		return err
	}
	return writer.WriteByte(sep)
}

func shufOpenOutputFile(ctx context.Context, inv *Invocation, name string) (io.WriteCloser, string, error) {
	abs := allowPath(inv, name)
	file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, "", exitf(inv, 1, "shuf: %s: %s", quoteGNUOperand(name), readAllErrorText(err))
	}
	return file, abs, nil
}

func shufWriteOutputFile(ctx context.Context, inv *Invocation, name string, data []byte) error {
	file, abs, err := shufOpenOutputFile(ctx, inv, name)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return &ExitError{Code: 1, Err: err}
		}
	}
	if err := file.Close(); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	recordFileMutation(inv.TraceRecorder(), "write", abs, abs, abs)
	return nil
}

func shufRandomExecutionError(inv *Invocation, randomSource string, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	if randomSource == "" {
		return &ExitError{Code: 1, Err: err}
	}
	if errors.Is(err, errShufRandomSourceEOF) {
		return exitf(inv, 1, "shuf: %s: end of file", quoteGNUOperand(randomSource))
	}
	return exitf(inv, 1, "shuf: %s: %v", quoteGNUOperand(randomSource), err)
}

func shufBrokenPipe(err error) bool {
	if errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "broken pipe")
}

var _ Command = (*Shuf)(nil)
var _ SpecProvider = (*Shuf)(nil)
var _ ParsedRunner = (*Shuf)(nil)
