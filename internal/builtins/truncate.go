package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"math"
	"math/big"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/ewhauser/gbash/policy"
)

type Truncate struct{}

func NewTruncate() *Truncate {
	return &Truncate{}
}

func (c *Truncate) Name() string {
	return "truncate"
}

func (c *Truncate) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Truncate) Spec() CommandSpec {
	return CommandSpec{
		Name:      "truncate",
		About:     "Shrink or extend the size of each file to the specified size.",
		Usage:     "truncate [OPTION]... [FILE]...",
		AfterHelp: "SIZE is an integer with an optional prefix and optional unit.",
		Options: []OptionSpec{
			{
				Name:  "io-blocks",
				Short: 'o',
				Long:  "io-blocks",
				Help:  "treat SIZE as the number of I/O blocks of the file rather than bytes (NOT IMPLEMENTED)",
			},
			{
				Name:  "no-create",
				Short: 'c',
				Long:  "no-create",
				Help:  "do not create files that do not exist",
			},
			{
				Name:      "reference",
				Short:     'r',
				Long:      "reference",
				ValueName: "RFILE",
				Arity:     OptionRequiredValue,
				Help:      "base the size of each file on the size of RFILE",
			},
			{
				Name:      "size",
				Short:     's',
				Long:      "size",
				ValueName: "SIZE",
				Arity:     OptionRequiredValue,
				Help:      "set or adjust the size of each file according to SIZE, which is in bytes unless --io-blocks is specified",
			},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions:         true,
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			NegativeNumberPositional: true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Truncate) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	files := matches.Args("file")
	if len(files) == 0 {
		return exitf(inv, 1, "truncate: missing file operand")
	}

	hasReference := matches.Has("reference")
	hasSize := matches.Has("size")
	if !hasReference && !hasSize {
		return commandUsageError(inv, c.Name(), "you must specify either '--size' or '--reference'")
	}

	var (
		mode         = truncateMode{kind: truncateModeExtend, value: 0}
		referencePtr *uint64
	)

	if hasSize {
		parsedMode, err := parseTruncateModeAndSize(matches.Value("size"))
		if err != nil {
			return exitf(inv, 1, "truncate: Invalid number: %s", err)
		}
		mode = parsedMode
	}

	if hasReference {
		referenceSize, err := truncateReferenceSize(ctx, inv, matches.Value("reference"))
		if err != nil {
			return err
		}
		referencePtr = &referenceSize
		if mode.isAbsolute() {
			return exitf(inv, 1, "truncate: you must specify a relative '--size' with '--reference'")
		}
	}

	// GNU/uutils expose --io-blocks but currently leave it unimplemented.
	// Keep accepting the flag so scripts port cleanly in the sandbox.
	_ = matches.Has("io-blocks")

	noCreate := matches.Has("no-create")
	for _, name := range files {
		if err := c.truncatePath(ctx, inv, name, noCreate, referencePtr, mode); err != nil {
			return err
		}
	}
	return nil
}

type truncateModeKind int

const (
	truncateModeAbsolute truncateModeKind = iota
	truncateModeExtend
	truncateModeReduce
	truncateModeAtMost
	truncateModeAtLeast
	truncateModeRoundDown
	truncateModeRoundUp
)

type truncateMode struct {
	kind  truncateModeKind
	value uint64
}

func (m truncateMode) isAbsolute() bool {
	return m.kind == truncateModeAbsolute
}

func (m truncateMode) toSize(current uint64) (uint64, bool) {
	switch m.kind {
	case truncateModeAbsolute:
		return m.value, true
	case truncateModeExtend:
		return current + m.value, true
	case truncateModeReduce:
		if m.value >= current {
			return 0, true
		}
		return current - m.value, true
	case truncateModeAtMost:
		return min(current, m.value), true
	case truncateModeAtLeast:
		return max(current, m.value), true
	case truncateModeRoundDown:
		if m.value == 0 {
			return 0, false
		}
		return current - (current % m.value), true
	case truncateModeRoundUp:
		return truncateRoundUpMultiple(current, m.value)
	default:
		return 0, true
	}
}

func truncateRoundUpMultiple(current, multiple uint64) (uint64, bool) {
	if multiple == 0 {
		return 0, false
	}
	remainder := current % multiple
	if remainder == 0 {
		return current, true
	}
	delta := multiple - remainder
	if current > math.MaxUint64-delta {
		return 0, false
	}
	return current + delta, true
}

func parseTruncateModeAndSize(raw string) (truncateMode, error) {
	sizeString := strings.TrimSpace(raw)
	if sizeString == "" {
		return truncateMode{}, truncateParseFailure(sizeString)
	}

	modeKind := truncateModeAbsolute
	switch sizeString[0] {
	case '+':
		modeKind = truncateModeExtend
		sizeString = sizeString[1:]
	case '-':
		modeKind = truncateModeReduce
		sizeString = sizeString[1:]
	case '<':
		modeKind = truncateModeAtMost
		sizeString = sizeString[1:]
	case '>':
		modeKind = truncateModeAtLeast
		sizeString = sizeString[1:]
	case '/':
		modeKind = truncateModeRoundDown
		sizeString = sizeString[1:]
	case '%':
		modeKind = truncateModeRoundUp
		sizeString = sizeString[1:]
	}

	value, err := parseTruncateSizeValue(sizeString)
	if err != nil {
		return truncateMode{}, err
	}
	return truncateMode{kind: modeKind, value: value}, nil
}

type truncateNumberBase int

const (
	truncateBaseDecimal truncateNumberBase = iota
	truncateBaseOctal
	truncateBaseHex
	truncateBaseBinary
)

func determineTruncateNumberBase(size string) truncateNumberBase {
	if len(size) <= 1 {
		return truncateBaseDecimal
	}
	if strings.HasPrefix(size, "0x") {
		return truncateBaseHex
	}
	if strings.HasPrefix(size, "0b") && len(size) > 2 {
		return truncateBaseBinary
	}

	numDigits := 0
	for _, r := range size {
		if r < '0' || r > '9' {
			break
		}
		numDigits++
	}
	allZeros := true
	for _, r := range size {
		if r != '0' {
			allZeros = false
			break
		}
	}
	if strings.HasPrefix(size, "0") && numDigits > 1 && !allZeros {
		return truncateBaseOctal
	}
	return truncateBaseDecimal
}

func parseTruncateSizeValue(size string) (uint64, error) {
	if size == "" {
		return 0, truncateParseFailure(size)
	}

	base := determineTruncateNumberBase(size)
	numeric, unit := splitTruncateNumericAndUnit(size, base)

	if unit == "%" {
		number, err := parseTruncateNumber(size, numeric, 10, false)
		if err != nil {
			return 0, err
		}
		return truncatePercentOfPhysicalMemory(number, size)
	}

	factor, err := truncateUnitFactor(size, numeric, unit)
	if err != nil {
		return 0, err
	}

	number, err := parseTruncateNumber(size, numeric, truncateRadix(base), base == truncateBaseDecimal)
	if err != nil {
		return 0, err
	}

	total := new(big.Int).Mul(number, factor)
	if !total.IsUint64() {
		return 0, truncateSizeTooBig(size)
	}
	return total.Uint64(), nil
}

func splitTruncateNumericAndUnit(size string, base truncateNumberBase) (numeric, unit string) {
	switch base {
	case truncateBaseHex:
		end := 2
		for end < len(size) && isASCIIDigitOrHex(size[end]) {
			end++
		}
		return size[:end], size[end:]
	case truncateBaseBinary:
		end := 2
		for end < len(size) && (size[end] == '0' || size[end] == '1') {
			end++
		}
		return size[:end], size[end:]
	default:
		end := 0
		for end < len(size) && size[end] >= '0' && size[end] <= '9' {
			end++
		}
		return size[:end], size[end:]
	}
}

func truncateRadix(base truncateNumberBase) int {
	switch base {
	case truncateBaseOctal:
		return 8
	case truncateBaseHex:
		return 16
	case truncateBaseBinary:
		return 2
	default:
		return 10
	}
}

func parseTruncateNumber(original, numeric string, radix int, allowEmptyDecimal bool) (*big.Int, error) {
	switch radix {
	case 10:
		if numeric == "" {
			if allowEmptyDecimal {
				return big.NewInt(1), nil
			}
			return nil, truncateParseFailure(original)
		}
	case 8:
		numeric = strings.TrimLeft(numeric, "0")
		if numeric == "" {
			numeric = "0"
		}
	case 16:
		numeric = strings.TrimPrefix(numeric, "0x")
		if numeric == "" {
			return nil, truncateParseFailure(original)
		}
	case 2:
		numeric = strings.TrimPrefix(numeric, "0b")
		if numeric == "" {
			return nil, truncateParseFailure(original)
		}
	}

	value := new(big.Int)
	if _, ok := value.SetString(numeric, radix); !ok {
		return nil, truncateParseFailure(original)
	}
	return value, nil
}

func truncateUnitFactor(original, numeric, unit string) (*big.Int, error) {
	switch unit {
	case "":
		return big.NewInt(1), nil
	case "b":
		return big.NewInt(512), nil
	case "KiB", "kiB", "K", "k":
		return truncateBigPow(1024, 1), nil
	case "MiB", "miB", "M", "m":
		return truncateBigPow(1024, 2), nil
	case "GiB", "giB", "G", "g":
		return truncateBigPow(1024, 3), nil
	case "TiB", "tiB", "T", "t":
		return truncateBigPow(1024, 4), nil
	case "PiB", "piB", "P", "p":
		return truncateBigPow(1024, 5), nil
	case "EiB", "eiB", "E", "e":
		return truncateBigPow(1024, 6), nil
	case "ZiB", "ziB", "Z", "z":
		return truncateBigPow(1024, 7), nil
	case "YiB", "yiB", "Y", "y":
		return truncateBigPow(1024, 8), nil
	case "RiB", "riB", "R", "r":
		return truncateBigPow(1024, 9), nil
	case "QiB", "qiB", "Q", "q":
		return truncateBigPow(1024, 10), nil
	case "KB", "kB":
		return truncateBigPow(1000, 1), nil
	case "MB", "mB":
		return truncateBigPow(1000, 2), nil
	case "GB", "gB":
		return truncateBigPow(1000, 3), nil
	case "TB", "tB":
		return truncateBigPow(1000, 4), nil
	case "PB", "pB":
		return truncateBigPow(1000, 5), nil
	case "EB", "eB":
		return truncateBigPow(1000, 6), nil
	case "ZB", "zB":
		return truncateBigPow(1000, 7), nil
	case "YB", "yB":
		return truncateBigPow(1000, 8), nil
	case "RB", "rB":
		return truncateBigPow(1000, 9), nil
	case "QB", "qB":
		return truncateBigPow(1000, 10), nil
	default:
		if numeric == "" {
			return nil, truncateParseFailure(original)
		}
		return nil, truncateInvalidSuffix(original)
	}
}

func truncateBigPow(base int64, exponent int) *big.Int {
	result := big.NewInt(1)
	factor := big.NewInt(base)
	for range exponent {
		result.Mul(result, factor)
	}
	return result
}

func truncatePercentOfPhysicalMemory(_ *big.Int, original string) (uint64, error) {
	// GNU/uutils interpret N% relative to host physical memory. The sandbox does
	// not expose host memory totals, so reject the form instead of leaking host data.
	return 0, truncatePhysicalMemoryUnavailable(original)
}

type truncateSizeError string

func (e truncateSizeError) Error() string {
	return string(e)
}

func truncateParseFailure(raw string) error {
	return truncateSizeError(quoteGNUOperand(raw))
}

func truncateInvalidSuffix(raw string) error {
	return truncateSizeError(quoteGNUOperand(raw))
}

func truncateSizeTooBig(raw string) error {
	return truncateSizeError(fmt.Sprintf("%s: Value too large for defined data type", quoteGNUOperand(raw)))
}

func truncatePhysicalMemoryUnavailable(raw string) error {
	return truncateSizeError(raw)
}

func truncateReferenceSize(ctx context.Context, inv *Invocation, raw string) (uint64, error) {
	info, _, err := statPath(ctx, inv, raw)
	if err != nil {
		return 0, exitf(inv, 1, "truncate: cannot stat %s: %s", quoteGNUOperand(raw), truncateErrorText(err))
	}
	if info.Size() <= 0 {
		return 0, nil
	}
	return uint64(info.Size()), nil
}

func (c *Truncate) truncatePath(ctx context.Context, inv *Invocation, raw string, noCreate bool, referenceSize *uint64, mode truncateMode) error {
	abs, err := allowPath(ctx, inv, policy.FileActionWrite, raw)
	if err != nil {
		return &ExitError{Code: exitCodeForError(err), Err: err}
	}

	info, err := inv.FS.Stat(ctx, abs)
	switch {
	case err == nil:
		if info.Mode()&stdfs.ModeNamedPipe != 0 {
			return exitf(inv, 1, "truncate: cannot open %s for writing: No such device or address", quoteGNUOperand(raw))
		}
	case errors.Is(err, stdfs.ErrNotExist):
		if noCreate {
			return nil
		}
		if ensureErr := truncateEnsureCreatableParent(ctx, inv, abs); ensureErr != nil {
			return truncateOpenError(inv, raw, ensureErr)
		}
		info = nil
	default:
		info = nil
	}

	openFlags := os.O_WRONLY
	if !noCreate {
		openFlags |= os.O_CREATE
	}
	file, err := inv.FS.OpenFile(ctx, abs, openFlags, truncatePerm(info))
	if err != nil {
		if noCreate && errors.Is(err, stdfs.ErrNotExist) {
			return nil
		}
		return truncateOpenError(inv, raw, err)
	}
	defer func() { _ = file.Close() }()

	if info == nil {
		if statInfo, statErr := file.Stat(); statErr == nil {
			info = statInfo
			if info.Mode()&stdfs.ModeNamedPipe != 0 {
				return exitf(inv, 1, "truncate: cannot open %s for writing: No such device or address", quoteGNUOperand(raw))
			}
		}
	}

	baseSize := uint64(0)
	if referenceSize != nil {
		baseSize = *referenceSize
	} else if info != nil && info.Size() > 0 {
		baseSize = uint64(info.Size())
	}

	targetSize, ok := mode.toSize(baseSize)
	if !ok {
		return exitf(inv, 1, "truncate: division by zero")
	}

	if native, ok := file.(interface{ Truncate(int64) error }); ok {
		if targetSize > math.MaxInt64 {
			return &ExitError{Code: 1, Err: fmt.Errorf("truncate: target size exceeds supported runtime limit")}
		}
		if err := native.Truncate(int64(targetSize)); err != nil {
			return truncateOpenError(inv, raw, err)
		}
		recordFileMutation(inv.TraceRecorder(), "truncate", abs, abs, abs)
		return nil
	}

	if err := file.Close(); err != nil {
		return truncateOpenError(inv, raw, err)
	}
	return c.truncateFallback(ctx, inv, raw, abs, info, targetSize)
}

func truncateEnsureCreatableParent(ctx context.Context, inv *Invocation, abs string) error {
	parent := path.Dir(abs)
	info, err := inv.FS.Stat(ctx, parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "open", Path: abs, Err: syscall.ENOTDIR}
	}
	return nil
}

func truncatePerm(info stdfs.FileInfo) stdfs.FileMode {
	if info == nil {
		return 0o644
	}
	perm := info.Mode().Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}

func (c *Truncate) truncateFallback(ctx context.Context, inv *Invocation, raw, abs string, info stdfs.FileInfo, targetSize uint64) error {
	currentSize := uint64(0)
	if info != nil && info.Size() > 0 {
		currentSize = uint64(info.Size())
	}

	switch {
	case targetSize == currentSize:
		return nil
	case targetSize < currentSize:
		data, _, err := readAllFile(ctx, inv, abs)
		if err != nil {
			return truncateOpenError(inv, raw, err)
		}
		if targetSize < uint64(len(data)) {
			data = data[:int(targetSize)]
		}
		if err := truncateRewriteFile(ctx, inv, abs, data, truncatePerm(info)); err != nil {
			return truncateOpenError(inv, raw, err)
		}
	default:
		flag := os.O_WRONLY | os.O_APPEND
		if info == nil {
			flag = os.O_CREATE | os.O_WRONLY
		}
		file, err := inv.FS.OpenFile(ctx, abs, flag, truncatePerm(info))
		if err != nil {
			return truncateOpenError(inv, raw, err)
		}
		defer func() { _ = file.Close() }()

		remaining := targetSize
		if info != nil {
			remaining -= currentSize
		}
		if err := truncateWriteZeros(file, remaining); err != nil {
			return truncateOpenError(inv, raw, err)
		}
	}

	recordFileMutation(inv.TraceRecorder(), "truncate", abs, abs, abs)
	return nil
}

func truncateRewriteFile(ctx context.Context, inv *Invocation, abs string, data []byte, perm stdfs.FileMode) error {
	file, err := inv.FS.OpenFile(ctx, abs, os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	if len(data) == 0 {
		return nil
	}
	_, err = file.Write(data)
	return err
}

func truncateWriteZeros(w io.Writer, remaining uint64) error {
	if remaining == 0 {
		return nil
	}

	zeros := make([]byte, 32*1024)
	for remaining > 0 {
		chunkSize := len(zeros)
		if uint64(chunkSize) > remaining {
			chunkSize = int(remaining)
		}
		if _, err := w.Write(zeros[:chunkSize]); err != nil {
			return err
		}
		remaining -= uint64(chunkSize)
	}
	return nil
}

func truncateOpenError(inv *Invocation, raw string, err error) error {
	return exitf(inv, 1, "truncate: cannot open %s for writing: %s", quoteGNUOperand(raw), truncateErrorText(err))
}

func truncateErrorText(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return truncateErrorText(pathErr.Err)
	}

	switch {
	case errors.Is(err, stdfs.ErrNotExist):
		return "No such file or directory"
	case truncateIsDirectoryError(err):
		return "Is a directory"
	case errors.Is(err, stdfs.ErrPermission):
		return "Permission denied"
	case errors.Is(err, syscall.ENOTDIR):
		return "Not a directory"
	case errors.Is(err, stdfs.ErrInvalid):
		return "Invalid argument"
	default:
		return err.Error()
	}
}

func truncateIsDirectoryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EISDIR) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "is a directory")
}

func isASCIIDigitOrHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

var _ Command = (*Truncate)(nil)
var _ SpecProvider = (*Truncate)(nil)
var _ ParsedRunner = (*Truncate)(nil)
