package builtins

import (
	"bufio"
	"errors"
	"io"
	"math"
	"math/big"
	"strings"
)

const (
	headCopyBufferSize     = 32 * 1024
	headSeekScanBufferSize = 64 * 1024
)

type headCountSuffix struct {
	token string
	mult  *big.Int
}

var headCountSuffixes = buildHeadCountSuffixes()

func parseHeadMatches(inv *Invocation, matches *ParsedCommand) (headOptions, error) {
	opts := headOptions{
		files: matches.Args("file"),
		mode:  headModeFirstLines,
		count: 10,
	}

	for _, occurrence := range matches.OptionOccurrences() {
		switch occurrence.Name {
		case "quiet":
			opts.quiet = true
			opts.verbose = false
		case "verbose":
			opts.verbose = true
			opts.quiet = false
		case "zero-terminated":
			opts.zeroTerminated = true
		case "presume-input-pipe":
			opts.presumeInputPipe = true
		case "lines":
			count, allButLast, err := parseHeadCount(occurrence.Value)
			if err != nil {
				return headOptions{}, exitf(inv, 1, "head: invalid number of lines: %s", quoteGNUOperand(occurrence.Value))
			}
			if allButLast {
				opts.mode = headModeAllButLastLines
			} else {
				opts.mode = headModeFirstLines
			}
			opts.count = count
		case "bytes":
			count, allButLast, err := parseHeadCount(occurrence.Value)
			if err != nil {
				return headOptions{}, exitf(inv, 1, "head: invalid number of bytes: %s", quoteGNUOperand(occurrence.Value))
			}
			if allButLast {
				opts.mode = headModeAllButLastBytes
			} else {
				opts.mode = headModeFirstBytes
			}
			opts.count = count
		}
	}

	if len(opts.files) == 0 {
		opts.files = []string{"-"}
	}
	return opts, nil
}

func normalizeHeadArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "--" {
		return append([]string(nil), args...)
	}
	normalizedFirst, ok := normalizeHeadObsoleteArg(args[0])
	if !ok {
		return append([]string(nil), args...)
	}
	out := append([]string(nil), normalizedFirst...)
	out = append(out, args[1:]...)
	return out
}

func normalizeHeadObsoleteArg(arg string) ([]string, bool) {
	if len(arg) < 2 || arg[0] != '-' || strings.HasPrefix(arg, "--") {
		return nil, false
	}

	numEnd := 1
	for numEnd < len(arg) && arg[numEnd] >= '0' && arg[numEnd] <= '9' {
		numEnd++
	}
	if numEnd == 1 {
		return nil, false
	}

	digits := arg[1:numEnd]
	quiet := false
	verbose := false
	zeroTerminated := false
	var multiplier *big.Int

	for i := numEnd; i < len(arg); i++ {
		switch arg[i] {
		case 'q':
			quiet = true
			verbose = false
		case 'v':
			verbose = true
			quiet = false
		case 'z':
			zeroTerminated = true
		case 'c':
			multiplier = big.NewInt(1)
		case 'b':
			multiplier = big.NewInt(512)
		case 'k':
			multiplier = big.NewInt(1 << 10)
		case 'm':
			multiplier = big.NewInt(1 << 20)
		default:
			return nil, false
		}
	}

	value, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	if multiplier != nil {
		value.Mul(value, multiplier)
	}

	out := make([]string, 0, 4)
	if quiet {
		out = append(out, "-q")
	}
	if verbose {
		out = append(out, "-v")
	}
	if zeroTerminated {
		out = append(out, "-z")
	}
	if multiplier != nil {
		out = append(out, "-c", headClampBigUint64String(value))
	} else {
		out = append(out, "-n", headClampBigUint64String(value))
	}
	return out, true
}

func parseHeadCount(raw string) (count uint64, allButLast bool, err error) {
	if raw == "" {
		return 0, false, io.ErrUnexpectedEOF
	}

	switch raw[0] {
	case '+':
		raw = raw[1:]
	case '-':
		allButLast = true
		raw = raw[1:]
	}

	count, err = parseHeadUnsignedSize(raw)
	if err != nil {
		return 0, false, err
	}
	return count, allButLast, nil
}

func parseHeadUnsignedSize(raw string) (uint64, error) {
	if raw == "" {
		return 0, io.ErrUnexpectedEOF
	}

	multiplier := big.NewInt(1)
	digits := raw
	for _, suffix := range headCountSuffixes {
		if !strings.HasSuffix(raw, suffix.token) {
			continue
		}
		digits = raw[:len(raw)-len(suffix.token)]
		multiplier = suffix.mult
		break
	}
	if digits == "" {
		return 0, io.ErrUnexpectedEOF
	}

	value := new(big.Int)
	if _, ok := value.SetString(digits, 10); !ok {
		return 0, io.ErrUnexpectedEOF
	}
	value.Mul(value, multiplier)
	return headClampBigUint64(value), nil
}

func buildHeadCountSuffixes() []headCountSuffix {
	letters := []struct {
		upper    string
		lower    string
		exponent int
	}{
		{"Q", "q", 10},
		{"R", "r", 9},
		{"Y", "y", 8},
		{"Z", "z", 7},
		{"E", "e", 6},
		{"P", "p", 5},
		{"T", "t", 4},
		{"G", "g", 3},
		{"M", "m", 2},
		{"K", "k", 1},
	}

	out := make([]headCountSuffix, 0, len(letters)*4+2)
	for _, letter := range letters {
		out = append(out,
			headCountSuffix{token: letter.upper + "iB", mult: headBigPow(1024, letter.exponent)},
			headCountSuffix{token: letter.lower + "iB", mult: headBigPow(1024, letter.exponent)},
			headCountSuffix{token: letter.upper + "B", mult: headBigPow(1000, letter.exponent)},
			headCountSuffix{token: letter.lower + "B", mult: headBigPow(1000, letter.exponent)},
		)
	}
	for _, letter := range letters {
		out = append(out,
			headCountSuffix{token: letter.upper, mult: headBigPow(1024, letter.exponent)},
			headCountSuffix{token: letter.lower, mult: headBigPow(1024, letter.exponent)},
		)
	}
	out = append(out,
		headCountSuffix{token: "b", mult: big.NewInt(512)},
		headCountSuffix{token: "B", mult: big.NewInt(1)},
	)
	return out
}

func headBigPow(base int64, exponent int) *big.Int {
	result := big.NewInt(1)
	factor := big.NewInt(base)
	for range exponent {
		result.Mul(result, factor)
	}
	return result
}

func headClampBigUint64(value *big.Int) uint64 {
	if value == nil || value.Sign() < 0 {
		return 0
	}
	if value.BitLen() > 64 {
		return math.MaxUint64
	}
	return value.Uint64()
}

func headClampBigUint64String(value *big.Int) string {
	return new(big.Int).SetUint64(headClampBigUint64(value)).String()
}

func headWriteFromReader(inv *Invocation, src io.Reader, sourceName string, opts headOptions) error {
	switch opts.mode {
	case headModeFirstBytes:
		return headWriteFirstBytes(inv, src, sourceName, opts.count)
	case headModeAllButLastBytes:
		return headWriteAllButLastBytes(inv, src, sourceName, opts)
	case headModeAllButLastLines:
		return headWriteAllButLastLines(inv, src, sourceName, opts)
	default:
		return headWriteFirstLines(inv, src, sourceName, opts.count, headRecordSeparator(opts))
	}
}

func headWriteFirstBytes(inv *Invocation, src io.Reader, sourceName string, count uint64) error {
	if count == 0 {
		return nil
	}

	buf := make([]byte, headCopyBufferSize)
	remaining := count
	for remaining > 0 {
		limit := len(buf)
		if remaining < uint64(limit) {
			limit = int(remaining)
		}
		n, err := src.Read(buf[:limit])
		if n > 0 {
			if _, writeErr := inv.Stdout.Write(buf[:n]); writeErr != nil {
				return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
			}
			remaining -= uint64(n)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	return nil
}

func headWriteFirstLines(inv *Invocation, src io.Reader, sourceName string, count uint64, separator byte) error {
	if count == 0 {
		return nil
	}

	reader := bufio.NewReaderSize(src, headCopyBufferSize)
	remaining := count
	for remaining > 0 {
		chunk, err := reader.ReadSlice(separator)
		if len(chunk) > 0 {
			if _, writeErr := inv.Stdout.Write(chunk); writeErr != nil {
				return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
			}
			if chunk[len(chunk)-1] == separator {
				remaining--
			}
		}
		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	return nil
}

func headWriteAllButLastBytes(inv *Invocation, src io.Reader, sourceName string, opts headOptions) error {
	if opts.count == 0 {
		return headCopyAll(inv, src, sourceName)
	}

	if !opts.presumeInputPipe {
		if seeker, ok := src.(interface {
			io.Reader
			io.Seeker
		}); ok {
			return headWriteAllButLastBytesSeekable(inv, seeker, opts.count, sourceName)
		}
	}

	if opts.count > uint64(math.MaxInt/2) {
		_, err := io.Copy(io.Discard, src)
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}

	buf := make([]byte, headCopyBufferSize)
	pending := make([]byte, 0, headCopyBufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			if uint64(len(pending)) > opts.count {
				writeCount := int(uint64(len(pending)) - opts.count)
				if _, writeErr := inv.Stdout.Write(pending[:writeCount]); writeErr != nil {
					return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
				}
				pending = append(pending[:0], pending[writeCount:]...)
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
}

func headWriteAllButLastLines(inv *Invocation, src io.Reader, sourceName string, opts headOptions) error {
	if opts.count == 0 {
		return headCopyAll(inv, src, sourceName)
	}

	separator := headRecordSeparator(opts)
	if !opts.presumeInputPipe {
		if seeker, ok := src.(interface {
			io.Reader
			io.Seeker
		}); ok {
			return headWriteAllButLastLinesSeekable(inv, seeker, opts.count, separator, sourceName)
		}
	}

	if opts.count > uint64(math.MaxInt/2) {
		_, err := io.Copy(io.Discard, src)
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}

	reader := bufio.NewReaderSize(src, headCopyBufferSize)
	queue := make([][]byte, 0, int(headMinUint64(opts.count, 8))+1)
	var current []byte
	for {
		chunk, err := reader.ReadSlice(separator)
		if len(chunk) > 0 {
			current = append(current, chunk...)
			if len(queue) > 0 && headBufferedRecordCount(queue, current) > opts.count {
				if _, writeErr := inv.Stdout.Write(queue[0]); writeErr != nil {
					return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
				}
				queue[0] = nil
				queue = queue[1:]
			}
			if chunk[len(chunk)-1] == separator {
				queue = append(queue, append([]byte(nil), current...))
				current = nil
				for len(queue) > 0 && headBufferedRecordCount(queue, current) > opts.count {
					if _, writeErr := inv.Stdout.Write(queue[0]); writeErr != nil {
						return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
					}
					queue[0] = nil
					queue = queue[1:]
				}
			}
		}

		if err == nil || errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if len(current) > 0 {
				queue = append(queue, append([]byte(nil), current...))
			}
			for uint64(len(queue)) > opts.count {
				if _, writeErr := inv.Stdout.Write(queue[0]); writeErr != nil {
					return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
				}
				queue[0] = nil
				queue = queue[1:]
			}
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
}

func headWriteAllButLastBytesSeekable(inv *Invocation, src interface {
	io.Reader
	io.Seeker
}, hold uint64, sourceName string) error {
	current, err := src.Seek(0, io.SeekCurrent)
	if err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	end, err := src.Seek(0, io.SeekEnd)
	if err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	if _, err := src.Seek(current, io.SeekStart); err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}

	remaining := end - current
	if remaining <= 0 || hold >= uint64(remaining) {
		return nil
	}
	return headWriteFirstBytes(inv, src, sourceName, uint64(remaining)-hold)
}

func headWriteAllButLastLinesSeekable(inv *Invocation, src interface {
	io.Reader
	io.Seeker
}, hold uint64, separator byte, sourceName string) error {
	current, err := src.Seek(0, io.SeekCurrent)
	if err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	end, err := src.Seek(0, io.SeekEnd)
	if err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	cutoff, err := headFindCutoffFromEnd(src, current, end, hold, separator)
	if err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	if _, err := src.Seek(current, io.SeekStart); err != nil {
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
	if cutoff <= current {
		return nil
	}
	return headWriteFirstBytes(inv, src, sourceName, uint64(cutoff-current))
}

func headFindCutoffFromEnd(src interface {
	io.Reader
	io.Seeker
}, start, end int64, hold uint64, separator byte) (int64, error) {
	if hold == 0 {
		return end, nil
	}
	if end <= start {
		return start, nil
	}

	buf := make([]byte, headSeekScanBufferSize)
	remaining := end - start
	lines := uint64(0)
	checkLastChunk := true

	for {
		readLen := min(remaining, int64(len(buf)))
		offset := start + remaining - readLen
		if _, err := src.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		chunk := buf[:readLen]
		if _, err := io.ReadFull(src, chunk); err != nil {
			return 0, err
		}
		remaining -= readLen

		if checkLastChunk {
			checkLastChunk = false
			if chunk[len(chunk)-1] != separator {
				lines = 1
			}
		}

		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] != separator {
				continue
			}
			lines++
			if lines == hold+1 {
				return offset + int64(i) + 1, nil
			}
		}
		if remaining == 0 {
			return start, nil
		}
	}
}

func headCopyAll(inv *Invocation, src io.Reader, sourceName string) error {
	buf := make([]byte, headCopyBufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := inv.Stdout.Write(buf[:n]); writeErr != nil {
				return exitf(inv, 1, "head: error writing 'standard output': %s", writeErr)
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return exitf(inv, 1, "head: error reading %s: %s", quoteGNUOperand(sourceName), readAllErrorText(err))
	}
}

func headRecordSeparator(opts headOptions) byte {
	if opts.zeroTerminated {
		return 0
	}
	return '\n'
}

func headBufferedRecordCount(queue [][]byte, current []byte) uint64 {
	count := uint64(len(queue))
	if len(current) > 0 {
		count++
	}
	return count
}

func headMinUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
