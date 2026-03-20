package builtins

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unsafe"
)

type OD struct{}

func NewOD() *OD {
	return &OD{}
}

func (c *OD) Name() string {
	return "od"
}

func (c *OD) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *OD) Spec() CommandSpec {
	return CommandSpec{
		Name:  "od",
		About: "Write an unambiguous representation of FILE to standard output.",
		Usage: "od [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "address-radix", Short: 'A', Long: "address-radix", Arity: OptionRequiredValue, ValueName: "RADIX", Help: "output format for file offsets; RADIX is one of [doxn], for Decimal, Octal, Hex or None"},
			{Name: "skip-bytes", Short: 'j', Long: "skip-bytes", Arity: OptionRequiredValue, ValueName: "BYTES", Help: "skip BYTES input bytes first"},
			{Name: "read-bytes", Short: 'N', Long: "read-bytes", Arity: OptionRequiredValue, ValueName: "BYTES", Help: "limit dump to BYTES input bytes"},
			{Name: "endian", Long: "endian", Arity: OptionRequiredValue, ValueName: "big|little", Help: "swap input bytes according to the specified order"},
			{Name: "strings", Short: 'S', Long: "strings", Arity: OptionOptionalValue, ValueName: "BYTES", Help: "output strings of at least BYTES graphic chars; 3 is implied when BYTES is not specified"},
			{Name: "a", Short: 'a', Help: "same as -t a, select named characters, ignoring high-order bit"},
			{Name: "b", Short: 'b', Help: "same as -t o1, select octal bytes"},
			{Name: "c", Short: 'c', Help: "same as -t c, select printable characters or backslash escapes"},
			{Name: "d", Short: 'd', Help: "same as -t u2, select unsigned decimal 2-byte units"},
			{Name: "D", Short: 'D', Help: "same as -t u4, select unsigned decimal 4-byte units"},
			{Name: "o", Short: 'o', Help: "same as -t o2, select octal 2-byte units"},
			{Name: "I", Short: 'I', Help: "same as -t d8, select decimal 8-byte units"},
			{Name: "L", Short: 'L', Help: "same as -t d8, select decimal 8-byte units"},
			{Name: "i", Short: 'i', Help: "same as -t d4, select decimal 4-byte units"},
			{Name: "l", Short: 'l', Help: "same as -t d8, select decimal 8-byte units"},
			{Name: "x", Short: 'x', Help: "same as -t x2, select hexadecimal 2-byte units"},
			{Name: "h", Short: 'h', Help: "same as -t x2, select hexadecimal 2-byte units"},
			{Name: "O", Short: 'O', Help: "same as -t o4, select octal 4-byte units"},
			{Name: "s", Short: 's', Help: "same as -t d2, select decimal 2-byte units"},
			{Name: "X", Short: 'X', Help: "same as -t x4, select hexadecimal 4-byte units"},
			{Name: "H", Short: 'H', Help: "same as -t x4, select hexadecimal 4-byte units"},
			{Name: "e", Short: 'e', Help: "same as -t fD, select doubles"},
			{Name: "f", Short: 'f', Help: "same as -t fF, select floats"},
			{Name: "F", Short: 'F', Help: "same as -t fD, select doubles"},
			{Name: "format", Short: 't', Long: "format", Arity: OptionRequiredValue, ValueName: "TYPE", Repeatable: true, Help: "select output format or formats"},
			{Name: "output-duplicates", Short: 'v', Long: "output-duplicates", Help: "do not use * to mark line suppression"},
			{Name: "width", Short: 'w', Long: "width", Arity: OptionOptionalValue, ValueName: "BYTES", Help: "output BYTES bytes per output line; 32 is implied when BYTES is not specified"},
			{Name: "traditional", Long: "traditional", Help: "accept arguments in the traditional format"},
			{Name: "help", Long: "help", Help: "display this help and exit"},
			{Name: "version", Long: "version", Help: "output version information and exit"},
		},
		Args: []ArgSpec{
			{Name: "operand", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			StopAtFirstPositional:    true,
		},
		VersionRenderer: func(w io.Writer, _ CommandSpec) error {
			_, err := io.WriteString(w, odVersionText)
			return err
		},
	}
}

func (c *OD) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	if matches.Has("help") {
		return RenderCommandHelp(inv.Stdout, &matches.Spec)
	}
	if matches.Has("version") {
		return RenderCommandVersion(inv.Stdout, &matches.Spec)
	}

	opts, err := parseODMatches(inv, matches)
	if err != nil {
		return err
	}
	data, err := readODInputs(ctx, inv, &opts)
	if err != nil {
		return err
	}

	if opts.stringMinLength != nil {
		return runODStrings(inv, data, &opts)
	}
	return runODDump(inv, data, &opts)
}

type odOptions struct {
	byteOrder        binary.ByteOrder
	skipBytes        uint64
	readBytes        *uint64
	label            *uint64
	inputNames       []string
	formats          []odFormat
	lineBytes        int
	widthSpecified   bool
	outputDuplicates bool
	radix            odRadix
	stringMinLength  *int
	traditional      bool
	offsetParsingOff bool
}

type odRadix int

const (
	odRadixOctal odRadix = iota
	odRadixDecimal
	odRadixHexadecimal
	odRadixNone
)

type odFormatKind int

const (
	odFormatASCII odFormatKind = iota
	odFormatChar
	odFormatSigned
	odFormatUnsigned
	odFormatFloat
)

type odFloatKind int

const (
	odFloat16 odFloatKind = iota
	odBFloat16
	odFloat32
	odFloat64
	odFloat128
)

type odFormat struct {
	kind         odFormatKind
	floatKind    odFloatKind
	byteSize     int
	printWidth   int
	base         int
	addASCIIDump bool
}

func parseODMatches(inv *Invocation, matches *ParsedCommand) (odOptions, error) {
	opts := odOptions{
		byteOrder: nativeByteOrder(),
		lineBytes: 16,
		radix:     odRadixOctal,
	}
	formatValues := matches.Values("format")
	formatIndex := 0

	for _, name := range matches.OptionOrder() {
		switch name {
		case "address-radix":
			if err := applyODAddressRadix(&opts, matches.Value(name)); err != nil {
				return odOptions{}, err
			}
			opts.offsetParsingOff = true
		case "skip-bytes":
			value := matches.Value(name)
			n, err := parseODByteCount(value)
			if err != nil {
				return odOptions{}, exitf(inv, 1, "od: invalid -j argument %q", value)
			}
			opts.skipBytes = n
			opts.offsetParsingOff = true
		case "read-bytes":
			value := matches.Value(name)
			n, err := parseODByteCount(value)
			if err != nil {
				return odOptions{}, exitf(inv, 1, "od: invalid -N argument %q", value)
			}
			opts.readBytes = &n
			opts.offsetParsingOff = true
		case "endian":
			if err := applyODEndian(&opts, matches.Value(name), inv); err != nil {
				return odOptions{}, err
			}
		case "strings":
			value := matches.Value(name)
			if value == "" {
				n := 3
				opts.stringMinLength = &n
			} else {
				n, err := parseODStringLength(value)
				if err != nil {
					return odOptions{}, err
				}
				opts.stringMinLength = &n
			}
		case "format":
			if formatIndex >= len(formatValues) {
				return odOptions{}, exitf(inv, 1, "od: missing format specification")
			}
			formats, err := parseODTypeString(formatValues[formatIndex])
			if err != nil {
				return odOptions{}, exitf(inv, 1, "od: %v", err)
			}
			formatIndex++
			opts.formats = append(opts.formats, formats...)
			opts.offsetParsingOff = true
		case "output-duplicates":
			opts.outputDuplicates = true
			opts.offsetParsingOff = true
		case "width":
			value := matches.Value(name)
			if value == "" {
				opts.widthSpecified = true
				opts.lineBytes = 32
			} else {
				if err := applyODWidth(&opts, value, inv); err != nil {
					return odOptions{}, err
				}
			}
			opts.offsetParsingOff = true
		case "traditional":
			opts.traditional = true
		default:
			format, ok := odTraditionalFormat(name[0])
			if !ok {
				continue
			}
			opts.formats = append(opts.formats, format)
		}
	}

	inputNames, skip, label, err := parseODInputs(matches.Args("operand"), opts.traditional, opts.offsetParsingOff)
	if err != nil {
		return odOptions{}, exitf(inv, 1, "od: %s", err)
	}
	opts.inputNames = inputNames
	if skip != nil && (opts.traditional || !opts.offsetParsingOff) {
		opts.skipBytes = *skip
		opts.label = label
	}

	if len(opts.formats) == 0 {
		opts.formats = []odFormat{odUnsignedFormat(2, 8)}
	}

	maxSize := 1
	for _, format := range opts.formats {
		if format.byteSize > maxSize {
			maxSize = format.byteSize
		}
	}
	if opts.widthSpecified && opts.lineBytes <= 0 {
		return odOptions{}, exitf(inv, 1, "od: invalid -w argument '0'")
	}
	if opts.lineBytes%maxSize != 0 {
		_, _ = fmt.Fprintf(inv.Stderr, "od: warning: invalid width %d; using %d instead\n", opts.lineBytes, maxSize)
		opts.lineBytes = maxSize
	}

	return opts, nil
}

func applyODAddressRadix(opts *odOptions, value string) error {
	if value == "" {
		return &ExitError{Code: 1, Err: fmt.Errorf("od: invalid radix '%s'", value)}
	}
	switch value[0] {
	case 'o':
		opts.radix = odRadixOctal
	case 'd':
		opts.radix = odRadixDecimal
	case 'x':
		opts.radix = odRadixHexadecimal
	case 'n':
		opts.radix = odRadixNone
	default:
		return fmt.Errorf("od: invalid radix %q", value)
	}
	return nil
}

func applyODEndian(opts *odOptions, value string, inv *Invocation) error {
	switch {
	case odKeywordPrefix(value, "little"):
		opts.byteOrder = binary.LittleEndian
	case odKeywordPrefix(value, "big"):
		opts.byteOrder = binary.BigEndian
	default:
		return exitf(inv, 1, "od: invalid endianness %q", value)
	}
	return nil
}

func applyODWidth(opts *odOptions, value string, inv *Invocation) error {
	opts.widthSpecified = true
	n, err := parseODByteCount(value)
	if err != nil || n == 0 {
		return exitf(inv, 1, "od: invalid -w argument '%s'", value)
	}
	if n > uint64(^uint(0)>>1) {
		return exitf(inv, 1, "od: invalid -w argument '%s'", value)
	}
	opts.lineBytes = int(n)
	return nil
}

func parseODStringLength(value string) (int, error) {
	n, err := parseODByteCount(value)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("od: invalid string length %q", value)
	}
	if n > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("od: invalid string length %q", value)
	}
	return int(n), nil
}

func parseODInputs(operands []string, traditional, offsetParsingOff bool) (inputs []string, offset, label *uint64, err error) {
	if traditional {
		return parseODTraditionalInputs(operands)
	}
	if !offsetParsingOff && (len(operands) == 1 || len(operands) == 2) {
		candidate := operands[len(operands)-1]
		offset, err := parseODOffsetOperand(candidate)
		if err == nil {
			if len(operands) == 1 && strings.HasPrefix(operands[0], "+") {
				return []string{"-"}, &offset, nil, nil
			}
			if len(operands) == 2 {
				return []string{operands[0]}, &offset, nil, nil
			}
		} else if errors.Is(err, errODOffsetRange) {
			return nil, nil, nil, fmt.Errorf("%s: %s", candidate, odOffsetErrorText(err))
		}
	}
	if len(operands) == 0 {
		return []string{"-"}, nil, nil, nil
	}
	return operands, nil, nil, nil
}

func parseODTraditionalInputs(operands []string) (inputs []string, offset, label *uint64, err error) {
	switch len(operands) {
	case 0:
		return []string{"-"}, nil, nil, nil
	case 1:
		if offset, err := parseODOffsetOperand(operands[0]); err == nil {
			return []string{"-"}, &offset, nil, nil
		}
		return operands, nil, nil, nil
	case 2:
		if offset0, err0 := parseODOffsetOperand(operands[0]); err0 == nil {
			if offset1, err1 := parseODOffsetOperand(operands[1]); err1 == nil {
				return []string{"-"}, &offset0, &offset1, nil
			}
		}
		offset1, err := parseODOffsetOperand(operands[1])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %s", operands[1], odOffsetErrorText(err))
		}
		return []string{operands[0]}, &offset1, nil, nil
	case 3:
		offset, err := parseODOffsetOperand(operands[1])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %s", operands[1], odOffsetErrorText(err))
		}
		label, err := parseODOffsetOperand(operands[2])
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %s", operands[2], odOffsetErrorText(err))
		}
		return []string{operands[0]}, &offset, &label, nil
	default:
		return nil, nil, nil, fmt.Errorf("extra operand %q", operands[3])
	}
}

var (
	errODOffsetParse = errors.New("parse failed")
	errODOffsetRange = errors.New("result too large")
)

func parseODOffsetOperand(value string) (uint64, error) {
	if value == "" || strings.Contains(value, " ") || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "++") || strings.HasPrefix(value, "+-") {
		return 0, errODOffsetParse
	}
	start := 0
	end := len(value)
	base := 8
	multiplier := uint64(1)
	if strings.HasPrefix(value, "+") {
		start++
	}
	if strings.HasPrefix(value[start:], "0x") || strings.HasPrefix(value[start:], "0X") {
		start += 2
		base = 16
	} else {
		if strings.HasSuffix(value[:end], "b") {
			end--
			multiplier = 512
		}
		if strings.HasSuffix(value[:end], ".") {
			end--
			base = 10
		}
	}
	if start >= end {
		return 0, errODOffsetParse
	}
	n, err := strconv.ParseUint(value[start:end], base, 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && errors.Is(numErr.Err, strconv.ErrRange) {
			return 0, errODOffsetRange
		}
		return 0, errODOffsetParse
	}
	if multiplier != 1 {
		if n > math.MaxUint64/multiplier {
			return 0, errODOffsetRange
		}
		n *= multiplier
	}
	return n, nil
}

func parseODByteCount(value string) (uint64, error) {
	if value == "" {
		return 0, fmt.Errorf("invalid count")
	}
	start := 0
	end := len(value)
	base := 10
	multiplier := uint64(1)
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		start = 2
		base = 16
	} else if value[0] == '0' {
		base = 8
	}
	if start >= end {
		return 0, fmt.Errorf("invalid count")
	}

	switch {
	case end >= 2 && strings.HasSuffix(value, "KB"):
		multiplier, end = 1000, end-2
	case end >= 2 && strings.HasSuffix(value, "MB"):
		multiplier, end = 1_000_000, end-2
	case end >= 2 && strings.HasSuffix(value, "GB"):
		multiplier, end = 1_000_000_000, end-2
	case end >= 2 && strings.HasSuffix(value, "TB"):
		multiplier, end = 1_000_000_000_000, end-2
	case end >= 2 && strings.HasSuffix(value, "PB"):
		multiplier, end = 1_000_000_000_000_000, end-2
	case end >= 2 && strings.HasSuffix(value, "EB"):
		multiplier, end = 1_000_000_000_000_000_000, end-2
	case strings.HasSuffix(value, "b") && base != 16:
		multiplier, end = 512, end-1
	case strings.HasSuffix(value, "k") || strings.HasSuffix(value, "K"):
		multiplier, end = 1<<10, end-1
	case strings.HasSuffix(value, "m") || strings.HasSuffix(value, "M"):
		multiplier, end = 1<<20, end-1
	case strings.HasSuffix(value, "G"):
		multiplier, end = 1<<30, end-1
	case strings.HasSuffix(value, "T"):
		multiplier, end = 1<<40, end-1
	case strings.HasSuffix(value, "P"):
		multiplier, end = 1<<50, end-1
	case strings.HasSuffix(value, "E") && base != 16:
		multiplier, end = 1<<60, end-1
	}

	if start >= end {
		return 0, fmt.Errorf("invalid count")
	}
	n, err := strconv.ParseUint(value[start:end], base, 64)
	if err != nil {
		return 0, err
	}
	if n > math.MaxUint64/multiplier {
		return 0, fmt.Errorf("invalid count")
	}
	return n * multiplier, nil
}

func readODInputs(ctx context.Context, inv *Invocation, opts *odOptions) ([]byte, error) {
	names := opts.inputNames
	if len(names) == 0 {
		names = []string{"-"}
	}

	limitBytes := uint64(0)
	limited := false
	if opts.readBytes != nil {
		limited = true
		limitBytes = opts.skipBytes
		if math.MaxUint64-limitBytes < *opts.readBytes {
			limitBytes = math.MaxUint64
		} else {
			limitBytes += *opts.readBytes
		}
	}

	var data []byte
	for _, name := range names {
		if limited && uint64(len(data)) >= limitBytes {
			break
		}

		var (
			reader io.Reader
			closer io.Closer
		)
		if name == "-" {
			reader = inv.Stdin
		} else {
			file, _, err := openRead(ctx, inv, name)
			if err != nil {
				return nil, odInputError(inv, name, err)
			}
			reader = file
			closer = file
		}

		chunk, err := odReadAll(ctx, inv, reader, limited, limitBytes-uint64(len(data)))
		if closer != nil {
			_ = closer.Close()
		}
		if err != nil {
			return nil, &ExitError{Code: 1, Err: err}
		}
		data = append(data, chunk...)
	}
	return data, nil
}

func runODStrings(inv *Invocation, data []byte, opts *odOptions) error {
	start := minUint64(opts.skipBytes, uint64(len(data)))
	data = data[start:]
	limit := uint64(len(data))
	if opts.readBytes != nil && *opts.readBytes < limit {
		limit = *opts.readBytes
	}
	data = data[:limit]

	current := make([]byte, 0, 32)
	startOffset := opts.skipBytes
	offset := opts.skipBytes
	printString := func(pos uint64, text []byte) error {
		switch opts.radix {
		case odRadixNone:
			_, err := fmt.Fprintf(inv.Stdout, "%s\n", string(text))
			return err
		case odRadixDecimal:
			_, err := fmt.Fprintf(inv.Stdout, "%07d %s\n", pos, string(text))
			return err
		case odRadixHexadecimal:
			_, err := fmt.Fprintf(inv.Stdout, "%07x %s\n", pos, string(text))
			return err
		default:
			_, err := fmt.Fprintf(inv.Stdout, "%07o %s\n", pos, string(text))
			return err
		}
	}

	for i, b := range data {
		if 0x20 <= b && b <= 0x7e {
			if len(current) == 0 {
				startOffset = offset
			}
			current = append(current, b)
		} else {
			if b == 0 && len(current) >= derefODStringLength(opts.stringMinLength) {
				if err := printString(startOffset, current); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			current = current[:0]
		}
		offset++
		if opts.readBytes != nil && uint64(i+1) >= *opts.readBytes {
			if len(current) >= derefODStringLength(opts.stringMinLength) {
				if err := printString(startOffset, current); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			break
		}
	}
	return nil
}

func runODDump(inv *Invocation, data []byte, opts *odOptions) error {
	start := minUint64(opts.skipBytes, uint64(len(data)))
	data = data[start:]
	if opts.readBytes != nil && uint64(len(data)) > *opts.readBytes {
		data = data[:*opts.readBytes]
	}

	info := newODOutputInfo(opts.lineBytes, opts.formats, opts.outputDuplicates)
	offset := odInputOffset{radix: opts.radix, bytePos: opts.skipBytes, label: opts.label}

	var previous []byte
	duplicate := false
	wroteLine := false
	for len(data) > 0 {
		lineLen := minInt(len(data), info.lineBytes)
		line := data[:lineLen]
		data = data[lineLen:]

		if !info.outputDuplicates && lineLen == info.lineBytes && bytes.Equal(line, previous) {
			if !duplicate {
				duplicate = true
				if _, err := fmt.Fprintln(inv.Stdout, "*"); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			offset.increase(uint64(lineLen))
			continue
		}
		duplicate = false
		if lineLen == info.lineBytes {
			previous = append(previous[:0], line...)
		}
		if err := writeODLine(inv.Stdout, offset.format(), line, lineLen, info, opts.byteOrder); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		wroteLine = true
		offset.increase(uint64(lineLen))
	}
	if !wroteLine && offset.radix == odRadixNone && offset.label == nil {
		return nil
	}
	if err := offset.printFinal(inv.Stdout); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	return nil
}

type odInputOffset struct {
	radix   odRadix
	bytePos uint64
	label   *uint64
}

func (o *odInputOffset) increase(n uint64) {
	o.bytePos += n
	if o.label != nil {
		value := *o.label + n
		o.label = &value
	}
}

func (o odInputOffset) format() string {
	switch o.radix {
	case odRadixDecimal:
		if o.label != nil {
			return fmt.Sprintf("%07d (%07d)", o.bytePos, *o.label)
		}
		return fmt.Sprintf("%07d", o.bytePos)
	case odRadixHexadecimal:
		if o.label != nil {
			return fmt.Sprintf("%06X (%06X)", o.bytePos, *o.label)
		}
		return fmt.Sprintf("%06X", o.bytePos)
	case odRadixNone:
		if o.label != nil {
			return fmt.Sprintf("(%07o)", *o.label)
		}
		return ""
	default:
		if o.label != nil {
			return fmt.Sprintf("%07o (%07o)", o.bytePos, *o.label)
		}
		return fmt.Sprintf("%07o", o.bytePos)
	}
}

func (o odInputOffset) printFinal(w io.Writer) error {
	if o.radix == odRadixNone && o.label == nil {
		return nil
	}
	_, err := fmt.Fprintln(w, o.format())
	return err
}

type odOutputInfo struct {
	lineBytes        int
	printWidthLine   int
	byteSizeBlock    int
	outputDuplicates bool
	formats          []odSpacedFormat
}

type odSpacedFormat struct {
	format       odFormat
	spacing      [16]int
	addASCIIDump bool
}

func newODOutputInfo(lineBytes int, formats []odFormat, outputDuplicates bool) odOutputInfo {
	blockSize := 1
	for _, format := range formats {
		if format.byteSize > blockSize {
			blockSize = format.byteSize
		}
	}
	out := odOutputInfo{
		lineBytes:        lineBytes,
		printWidthLine:   1,
		byteSizeBlock:    blockSize,
		outputDuplicates: outputDuplicates,
		formats:          make([]odSpacedFormat, 0, len(formats)),
	}
	for _, format := range formats {
		blockWidth := format.printWidth * (blockSize / format.byteSize)
		if width := blockWidth * (lineBytes / blockSize); width > out.printWidthLine {
			out.printWidthLine = width
		}
		out.formats = append(out.formats, odSpacedFormat{
			format:       format,
			spacing:      odAlignment(format, blockSize, blockWidth),
			addASCIIDump: format.addASCIIDump,
		})
	}
	return out
}

func odAlignment(format odFormat, blockSize, blockWidth int) [16]int {
	var spacing [16]int
	byteSize := format.byteSize
	itemsInBlock := blockSize / byteSize
	missing := blockWidth - format.printWidth*itemsInBlock
	for itemsInBlock > 0 {
		avg := missing / itemsInBlock
		for i := 0; i < itemsInBlock; i++ {
			spacing[i*byteSize] += avg
			missing -= avg
		}
		itemsInBlock /= 2
		byteSize *= 2
	}
	return spacing
}

func writeODLine(w io.Writer, prefix string, raw []byte, lineLen int, info odOutputInfo, order binary.ByteOrder) error {
	padded := make([]byte, info.lineBytes)
	copy(padded, raw)
	displayPrefix := prefix
	first := true
	for i := range info.formats {
		format := &info.formats[i]
		var b strings.Builder
		if format.addASCIIDump {
			b.WriteString(strings.Repeat(" ", odLeadingDelimiter(format.format)))
			for j := 0; j < info.lineBytes; j += format.format.byteSize {
				if j > 0 {
					gap := format.spacing[j%info.byteSizeBlock] + odInterItemSpacing(format.format)
					if gap > 0 {
						b.WriteString(strings.Repeat(" ", gap))
					}
				}
				if j < lineLen {
					end := min(j+format.format.byteSize, len(padded))
					b.WriteString(format.format.format(padded[j:end], order))
					continue
				}
				b.WriteString(strings.Repeat(" ", format.format.printWidth))
			}
			missing := max(info.printWidthLine-utf8RuneCountString(b.String()), 0)
			b.WriteString(strings.Repeat(" ", missing))
			b.WriteString("  ")
			b.WriteString(odASCIIDump(raw))
		} else {
			for j := 0; j < lineLen; j += format.format.byteSize {
				end := min(j+format.format.byteSize, len(padded))
				b.WriteString(format.format.format(padded[j:end], order))
			}
		}
		if first {
			if _, err := fmt.Fprint(w, displayPrefix); err != nil {
				return err
			}
			first = false
		} else if _, err := fmt.Fprint(w, strings.Repeat(" ", utf8RuneCountString(displayPrefix))); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, b.String()); err != nil {
			return err
		}
	}
	return nil
}

func odLeadingDelimiter(format odFormat) int {
	switch format.kind {
	case odFormatASCII, odFormatChar:
		return 1
	default:
		return format.printWidth
	}
}

func odInterItemSpacing(format odFormat) int {
	switch format.kind {
	case odFormatASCII, odFormatChar:
		return 0
	default:
		return max(format.printWidth-2, 0)
	}
}

func (f odFormat) format(raw []byte, order binary.ByteOrder) string {
	switch f.kind {
	case odFormatASCII:
		return fmt.Sprintf("%4s", odASCIIName(raw[0]&0x7f))
	case odFormatChar:
		return fmt.Sprintf("%4s", odCharName(raw[0]))
	case odFormatSigned:
		value := odUint(raw, order)
		return fmt.Sprintf(" %*d", f.printWidth-1, odSignExtend(value, f.byteSize))
	case odFormatUnsigned:
		value := odUint(raw, order)
		switch f.base {
		case 8:
			return fmt.Sprintf(" %0*o", f.printWidth-1, value)
		case 16:
			return fmt.Sprintf(" %0*x", f.printWidth-1, value)
		default:
			return fmt.Sprintf(" %*d", f.printWidth-1, value)
		}
	case odFormatFloat:
		return odFormatFloatValue(f, raw, order)
	default:
		return ""
	}
}

func odUint(raw []byte, order binary.ByteOrder) uint64 {
	var buf [8]byte
	if order == binary.BigEndian {
		copy(buf[8-len(raw):], raw)
		return binary.BigEndian.Uint64(buf[:])
	}
	copy(buf[:], raw)
	return binary.LittleEndian.Uint64(buf[:])
}

func odSignExtend(value uint64, size int) int64 {
	shift := 64 - size*8
	return int64(value<<shift) >> shift
}

func odFormatFloatValue(format odFormat, raw []byte, order binary.ByteOrder) string {
	switch format.floatKind {
	case odFloat16:
		bits := uint16(odUint(raw, order))
		value := odHalfToFloat64(bits)
		return " " + odFormatFloatCompact(value, format.printWidth-1, 8, true)
	case odBFloat16:
		bits := uint16(odUint(raw, order))
		value := float64(math.Float32frombits(uint32(bits) << 16))
		return " " + odFormatFloatCompact(value, format.printWidth-1, 8, true)
	case odFloat32:
		bits := uint32(odUint(raw, order))
		return " " + odFormatFloat32(math.Float32frombits(bits))
	case odFloat64:
		bits := odUint(raw, order)
		return " " + odFormatFloat64(math.Float64frombits(bits))
	default:
		return " " + odFormatFloat128(raw, order, format.printWidth-1)
	}
}

func odFormatFloat32(value float32) string {
	if math.IsNaN(float64(value)) {
		return fmt.Sprintf("%15s", "NaN")
	}
	if math.IsInf(float64(value), 1) {
		return fmt.Sprintf("%15s", "inf")
	}
	if math.IsInf(float64(value), -1) {
		return fmt.Sprintf("%15s", "-inf")
	}
	return odFormatFloatGeneral(float64(value), 15, 8)
}

func odFormatFloat64(value float64) string {
	if math.IsNaN(value) {
		return fmt.Sprintf("%24s", "NaN")
	}
	if math.IsInf(value, 1) {
		return fmt.Sprintf("%24s", "inf")
	}
	if math.IsInf(value, -1) {
		return fmt.Sprintf("%24s", "-inf")
	}
	return odFormatFloatGeneral(value, 24, 17)
}

func odFormatFloatCompact(value float64, width, precision int, compact bool) string {
	text := odFormatFloatGeneral(value, width, precision)
	if !compact {
		return text
	}
	trimmed := strings.TrimLeft(text, " ")
	if lower := strings.ToLower(trimmed); lower == "nan" || lower == "inf" || lower == "-inf" {
		return fmt.Sprintf("%*s", width, trimmed)
	}
	exp := ""
	if idx := strings.IndexAny(trimmed, "eE"); idx >= 0 {
		exp = trimmed[idx:]
		trimmed = trimmed[:idx]
	}
	for strings.Contains(trimmed, ".") && strings.HasSuffix(trimmed, "0") {
		trimmed = strings.TrimSuffix(trimmed, "0")
	}
	trimmed = strings.TrimSuffix(trimmed, ".")
	if trimmed == "" || trimmed == "-" || trimmed == "+" {
		trimmed += "0"
	}
	return fmt.Sprintf("%*s", width, trimmed+exp)
}

func odFormatFloatGeneral(value float64, width, precision int) string {
	if value == 0 {
		if math.Signbit(value) {
			return fmt.Sprintf("%*s", width, "-0")
		}
		return fmt.Sprintf("%*s", width, "0")
	}
	if math.IsInf(value, 1) {
		return fmt.Sprintf("%*s", width, "inf")
	}
	if math.IsInf(value, -1) {
		return fmt.Sprintf("%*s", width, "-inf")
	}
	if math.IsNaN(value) {
		return fmt.Sprintf("%*s", width, "NaN")
	}
	if math.Abs(value) < math.SmallestNonzeroFloat64 {
		return fmt.Sprintf("%*e", width, value)
	}

	logv := int(math.Floor(math.Log10(math.Abs(value))))
	r := math.Pow10(logv)
	if (value > 0 && r > value) || (value < 0 && -r < value) {
		logv--
	}
	switch {
	case logv >= 0 && logv < precision:
		return fmt.Sprintf("%*.*f", width, (precision-1)-logv, value)
	case logv == -1:
		return fmt.Sprintf("%*.*g", width, precision, value)
	default:
		return odFormatExp(value, width, precision-1)
	}
}

func odFormatExp(value float64, width, precision int) string {
	text := fmt.Sprintf("%*.*e", width, precision, value)
	if strings.Contains(text, "e-") {
		return text
	}
	return strings.Replace(text, "e", "e+", 1)
}

func odFormatFloat128(raw []byte, order binary.ByteOrder, width int) string {
	if len(raw) < 16 {
		padded := make([]byte, 16)
		copy(padded, raw)
		raw = padded
	}
	var bytes16 [16]byte
	if order == binary.BigEndian {
		copy(bytes16[:], raw[:16])
	} else {
		for i := range 16 {
			bytes16[15-i] = raw[i]
		}
	}

	sign := bytes16[0] >> 7
	exp := uint16(bytes16[0]&0x7f)<<8 | uint16(bytes16[1])
	frac := new(big.Int).SetBytes(bytes16[2:])
	if exp == 0 && frac.Sign() == 0 {
		if sign == 1 {
			return fmt.Sprintf("%*s", width, "-0")
		}
		return fmt.Sprintf("%*s", width, "0")
	}
	if exp == 0x7fff {
		if frac.Sign() == 0 {
			if sign == 1 {
				return fmt.Sprintf("%*s", width, "-inf")
			}
			return fmt.Sprintf("%*s", width, "inf")
		}
		return fmt.Sprintf("%*s", width, "NaN")
	}

	prec := uint(256)
	mant := new(big.Float).SetPrec(prec).SetInt(frac)
	if exp != 0 {
		implicit := new(big.Int).Lsh(big.NewInt(1), 112)
		mant.Add(mant, new(big.Float).SetPrec(prec).SetInt(implicit))
	}
	exp2 := int(exp) - 16383 - 112
	value := new(big.Float).SetPrec(prec).SetMode(big.ToNearestEven)
	value.Copy(mant)
	value.SetMantExp(value, value.MantExp(nil)+exp2)
	if sign == 1 {
		value.Neg(value)
	}

	text := value.Text('e', 21)
	if !strings.Contains(text, "e-") {
		text = strings.Replace(text, "e", "e+", 1)
	}
	return fmt.Sprintf("%*s", width, text)
}

func odHalfToFloat64(bits uint16) float64 {
	sign := uint32(bits>>15) & 0x1
	exp := uint32(bits>>10) & 0x1f
	frac := uint32(bits & 0x3ff)
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float64frombits(uint64(sign) << 63)
		}
		for frac&0x400 == 0 {
			frac <<= 1
			exp--
		}
		exp++
		frac &^= 0x400
	case 0x1f:
		if frac == 0 {
			if sign == 1 {
				return math.Inf(-1)
			}
			return math.Inf(1)
		}
		return math.NaN()
	}
	exp += 127 - 15
	bits32 := sign<<31 | exp<<23 | frac<<13
	return float64(math.Float32frombits(bits32))
}

func odTraditionalFormat(ch byte) (odFormat, bool) {
	switch ch {
	case 'a':
		return odFormat{kind: odFormatASCII, byteSize: 1, printWidth: 4}, true
	case 'b':
		return odUnsignedFormat(1, 8), true
	case 'c':
		return odFormat{kind: odFormatChar, byteSize: 1, printWidth: 4}, true
	case 'd':
		return odUnsignedFormat(2, 10), true
	case 'D':
		return odUnsignedFormat(4, 10), true
	case 'o':
		return odUnsignedFormat(2, 8), true
	case 'O':
		return odUnsignedFormat(4, 8), true
	case 's':
		return odSignedFormat(2), true
	case 'i':
		return odSignedFormat(4), true
	case 'I', 'L', 'l':
		return odSignedFormat(8), true
	case 'x', 'h':
		return odUnsignedFormat(2, 16), true
	case 'X', 'H':
		return odUnsignedFormat(4, 16), true
	case 'e':
		return odFormat{kind: odFormatFloat, floatKind: odFloat64, byteSize: 8, printWidth: 25}, true
	case 'f':
		return odFormat{kind: odFormatFloat, floatKind: odFloat32, byteSize: 4, printWidth: 16}, true
	case 'F':
		return odFormat{kind: odFormatFloat, floatKind: odFloat64, byteSize: 8, printWidth: 25}, true
	default:
		return odFormat{}, false
	}
}

func odUnsignedFormat(size, base int) odFormat {
	switch {
	case base == 8 && size == 1:
		return odFormat{kind: odFormatUnsigned, byteSize: 1, printWidth: 4, base: 8}
	case base == 8 && size == 2:
		return odFormat{kind: odFormatUnsigned, byteSize: 2, printWidth: 7, base: 8}
	case base == 8 && size == 4:
		return odFormat{kind: odFormatUnsigned, byteSize: 4, printWidth: 12, base: 8}
	case base == 8 && size == 8:
		return odFormat{kind: odFormatUnsigned, byteSize: 8, printWidth: 23, base: 8}
	case base == 16 && size == 1:
		return odFormat{kind: odFormatUnsigned, byteSize: 1, printWidth: 3, base: 16}
	case base == 16 && size == 2:
		return odFormat{kind: odFormatUnsigned, byteSize: 2, printWidth: 5, base: 16}
	case base == 16 && size == 4:
		return odFormat{kind: odFormatUnsigned, byteSize: 4, printWidth: 9, base: 16}
	case base == 16 && size == 8:
		return odFormat{kind: odFormatUnsigned, byteSize: 8, printWidth: 17, base: 16}
	case base == 10 && size == 1:
		return odFormat{kind: odFormatUnsigned, byteSize: 1, printWidth: 4, base: 10}
	case base == 10 && size == 2:
		return odFormat{kind: odFormatUnsigned, byteSize: 2, printWidth: 6, base: 10}
	case base == 10 && size == 4:
		return odFormat{kind: odFormatUnsigned, byteSize: 4, printWidth: 11, base: 10}
	default:
		return odFormat{kind: odFormatUnsigned, byteSize: 8, printWidth: 21, base: 10}
	}
}

func odSignedFormat(size int) odFormat {
	switch size {
	case 1:
		return odFormat{kind: odFormatSigned, byteSize: 1, printWidth: 5}
	case 2:
		return odFormat{kind: odFormatSigned, byteSize: 2, printWidth: 7}
	case 4:
		return odFormat{kind: odFormatSigned, byteSize: 4, printWidth: 12}
	default:
		return odFormat{kind: odFormatSigned, byteSize: 8, printWidth: 21}
	}
}

func parseODTypeString(spec string) ([]odFormat, error) {
	var formats []odFormat
	original := spec
	for spec != "" {
		ch := spec[0]
		spec = spec[1:]

		var format odFormat
		switch ch {
		case 'a':
			format = odFormat{kind: odFormatASCII, byteSize: 1, printWidth: 4}
		case 'c':
			format = odFormat{kind: odFormatChar, byteSize: 1, printWidth: 4}
		case 'd', 'o', 'u', 'x':
			size := 4
			var err error
			size, spec, err = parseODTypeSize(spec, false, size)
			if err != nil {
				return nil, err
			}
			if size != 1 && size != 2 && size != 4 && size != 8 {
				return nil, fmt.Errorf("invalid type size %d in %q", size, original)
			}
			switch ch {
			case 'd':
				format = odSignedFormat(size)
			case 'o':
				format = odUnsignedFormat(size, 8)
			case 'u':
				format = odUnsignedFormat(size, 10)
			case 'x':
				format = odUnsignedFormat(size, 16)
			}
		case 'f':
			size := 4
			floatKind := odFloat32
			if spec != "" {
				switch spec[0] {
				case 'B':
					size, floatKind, spec = 2, odBFloat16, spec[1:]
				case 'H':
					size, floatKind, spec = 2, odFloat16, spec[1:]
				case 'F':
					size, floatKind, spec = 4, odFloat32, spec[1:]
				case 'D':
					size, floatKind, spec = 8, odFloat64, spec[1:]
				case 'L':
					size, floatKind, spec = 16, odFloat128, spec[1:]
				default:
					var err error
					size, spec, err = parseODTypeSize(spec, true, size)
					if err != nil {
						return nil, err
					}
					switch size {
					case 2:
						floatKind = odFloat16
					case 4:
						floatKind = odFloat32
					case 8:
						floatKind = odFloat64
					case 16:
						floatKind = odFloat128
					default:
						return nil, fmt.Errorf("invalid type size %d in %q", size, original)
					}
				}
			}
			format = odFormat{kind: odFormatFloat, floatKind: floatKind, byteSize: size, printWidth: odFloatWidth(floatKind)}
		default:
			return nil, fmt.Errorf("invalid character %q in format %q", string(ch), original)
		}
		if spec != "" && spec[0] == 'z' {
			format.addASCIIDump = true
			spec = spec[1:]
		}
		formats = append(formats, format)
	}
	if len(formats) == 0 {
		return nil, fmt.Errorf("missing format specification")
	}
	return formats, nil
}

func parseODTypeSize(spec string, allowFloat bool, defaultSize int) (size int, rest string, err error) {
	if spec == "" {
		return defaultSize, spec, nil
	}
	switch spec[0] {
	case 'C':
		return 1, spec[1:], nil
	case 'S':
		return 2, spec[1:], nil
	case 'I':
		return 4, spec[1:], nil
	case 'L':
		return 8, spec[1:], nil
	}
	if allowFloat {
		switch spec[0] {
		case 'F':
			return 4, spec[1:], nil
		case 'D':
			return 8, spec[1:], nil
		}
	}
	i := 0
	for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
		i++
	}
	if i == 0 {
		return defaultSize, spec, nil
	}
	n, err := strconv.Atoi(spec[:i])
	if err != nil {
		return 0, "", err
	}
	return n, spec[i:], nil
}

func odKeywordPrefix(value, keyword string) bool {
	return value != "" && strings.HasPrefix(keyword, value)
}

func odReadAll(ctx context.Context, inv *Invocation, r io.Reader, limited bool, remaining uint64) ([]byte, error) {
	if limited {
		return readAllReader(ctx, inv, io.LimitReader(r, int64(remaining)))
	}
	return readAllReader(ctx, inv, r)
}

func odInputError(inv *Invocation, name string, err error) error {
	if errors.Is(err, stdfs.ErrNotExist) {
		return exitf(inv, 1, "od: %s: No such file or directory", odDisplayName(name))
	}
	return exitf(inv, 1, "od: %s: %v", odDisplayName(name), err)
}

func odDisplayName(name string) string {
	if strings.ContainsAny(name, " \t\r\n") {
		return "'" + strings.ReplaceAll(name, "'", "'\\''") + "'"
	}
	return name
}

func odOffsetErrorText(err error) string {
	if errors.Is(err, errODOffsetRange) {
		return odRangeMessage()
	}
	return "parse failed"
}

func odRangeMessage() string {
	text := errODOffsetRange.Error()
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}

func odFloatWidth(kind odFloatKind) int {
	switch kind {
	case odFloat16, odBFloat16, odFloat32:
		return 16
	case odFloat64:
		return 25
	default:
		return 40
	}
}

var odASCIIControl = [...]string{
	"nul", "soh", "stx", "etx", "eot", "enq", "ack", "bel", "bs", "ht", "nl", "vt", "ff", "cr", "so", "si",
	"dle", "dc1", "dc2", "dc3", "dc4", "nak", "syn", "etb", "can", "em", "sub", "esc", "fs", "gs", "rs", "us",
}

var odCharControl = [...]string{
	`\0`, "001", "002", "003", "004", "005", "006", `\a`, `\b`, `\t`, `\n`, `\v`, `\f`, `\r`, "016", "017",
	"020", "021", "022", "023", "024", "025", "026", "027", "030", "031", "032", "033", "034", "035", "036", "037",
}

func odASCIIName(b byte) string {
	switch {
	case b < 32:
		return odASCIIControl[b]
	case b == 32:
		return "sp"
	case b == 127:
		return "del"
	default:
		return string([]byte{b})
	}
}

func odCharName(b byte) string {
	switch {
	case b < 32:
		return odCharControl[b]
	case b == 32:
		return " "
	case b < 127:
		return string([]byte{b})
	case b == 127:
		return "177"
	default:
		return fmt.Sprintf("%03o", b)
	}
}

func odASCIIDump(data []byte) string {
	var b strings.Builder
	b.WriteByte('>')
	for _, ch := range data {
		if ch >= 0x20 && ch <= 0x7e {
			b.WriteByte(ch)
		} else {
			b.WriteByte('.')
		}
	}
	b.WriteByte('<')
	return b.String()
}

func nativeByteOrder() binary.ByteOrder {
	var i uint16 = 1
	if *(*byte)(unsafe.Pointer(&i)) == 1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func derefODStringLength(value *int) int {
	if value == nil {
		return 3
	}
	return *value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func utf8RuneCountString(s string) int {
	return len([]rune(s))
}

const odVersionText = `od (gbash)
`

var _ Command = (*OD)(nil)
