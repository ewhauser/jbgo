package builtins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ewhauser/gbash/internal/commandutil"
)

type Dd struct{}

func NewDd() *Dd {
	return &Dd{}
}

func (c *Dd) Name() string {
	return "dd"
}

func (c *Dd) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Dd) Spec() CommandSpec {
	return CommandSpec{
		Name:      "dd",
		About:     "Copy a file, converting and formatting according to the operands.",
		Usage:     "dd [OPERAND]...\n  or:  dd OPTION",
		AfterHelp: ddAfterHelpText,
		Args: []ArgSpec{
			{Name: "operand", ValueName: "OPERAND", Repeatable: true},
		},
		Parse: ParseConfig{
			InferLongOptions: true,
			AutoHelp:         true,
			AutoVersion:      true,
		},
	}
}

func (c *Dd) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	settings, err := parseDdOperands(inv, matches.Positionals())
	if err != nil {
		return err
	}
	return runDd(ctx, inv, &settings)
}

type ddStatusLevel int

const (
	ddStatusDefault ddStatusLevel = iota
	ddStatusProgress
	ddStatusNoxfer
	ddStatusNone
)

type ddConversionKind int

const (
	ddConversionNone ddConversionKind = iota
	ddConversionASCII
	ddConversionEBCDIC
	ddConversionIBM
)

type ddCaseMode int

const (
	ddCaseNone ddCaseMode = iota
	ddCaseUpper
	ddCaseLower
)

type ddBlockMode int

const (
	ddBlockModeNone ddBlockMode = iota
	ddBlockModeBlock
	ddBlockModeUnblock
)

type ddNumber struct {
	value uint64
	bytes bool
}

type ddInputFlags struct {
	direct     bool
	directory  bool
	dsync      bool
	sync       bool
	fullblock  bool
	nonblock   bool
	noatime    bool
	nocache    bool
	noctty     bool
	nofollow   bool
	countBytes bool
	skipBytes  bool
}

type ddOutputFlags struct {
	append    bool
	direct    bool
	directory bool
	dsync     bool
	sync      bool
	nonblock  bool
	noatime   bool
	nocache   bool
	noctty    bool
	nofollow  bool
	seekBytes bool
}

type ddConvOptions struct {
	kind            ddConversionKind
	caseMode        ddCaseMode
	explicitBlock   bool
	explicitUnblock bool
	blockMode       ddBlockMode
	swab            bool
	sync            bool
	sparse          bool
	noerror         bool
	excl            bool
	nocreat         bool
	notrunc         bool
	fdatasync       bool
	fsync           bool
}

func (o ddConvOptions) hasDataTransform() bool {
	return o.kind != ddConversionNone ||
		o.caseMode != ddCaseNone ||
		o.blockMode != ddBlockModeNone ||
		o.swab ||
		o.sync
}

type ddSettings struct {
	infile     string
	infileSet  bool
	outfile    string
	outfileSet bool
	ibs        int
	obs        int
	cbs        int
	skip       ddNumber
	seek       ddNumber
	count      *ddNumber
	iflags     ddInputFlags
	oflags     ddOutputFlags
	conv       ddConvOptions
	status     ddStatusLevel
	buffered   bool
}

type ddReadStats struct {
	recordsComplete uint64
	recordsPartial  uint64
	recordsTrunc    uint32
	bytesTotal      uint64
}

func (s *ddReadStats) add(other ddReadStats) {
	s.recordsComplete += other.recordsComplete
	s.recordsPartial += other.recordsPartial
	s.recordsTrunc += other.recordsTrunc
	s.bytesTotal += other.bytesTotal
}

func (s ddReadStats) recordCount() uint64 {
	return s.recordsComplete + s.recordsPartial
}

type ddWriteStats struct {
	recordsComplete uint64
	recordsPartial  uint64
	bytesTotal      uint64
}

func (s *ddWriteStats) add(other ddWriteStats) {
	s.recordsComplete += other.recordsComplete
	s.recordsPartial += other.recordsPartial
	s.bytesTotal += other.bytesTotal
}

type ddInput struct {
	reader io.Reader
	label  string
	closer io.Closer
}

type ddTransformState struct {
	blockCarry    []byte
	blockOverflow bool
}

type ddSizeSuffix struct {
	text string
	mult uint64
}

var ddSizeSuffixes = []ddSizeSuffix{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"PiB", 1 << 50}, {"EiB", 1 << 60},
	{"kB", 1000}, {"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
	{"TB", 1000 * 1000 * 1000 * 1000}, {"PB", 1000 * 1000 * 1000 * 1000 * 1000},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40}, {"P", 1 << 50}, {"E", 1 << 60},
	{"B", 1}, {"c", 1}, {"w", 2}, {"b", 512},
}

type ddOutputWriter interface {
	WriteData([]byte) (ddWriteStats, error)
	Flush() (ddWriteStats, error)
	Sync() error
	Finalize(context.Context, *Invocation) error
}

type ddOutputMaterializer interface {
	Materialize(context.Context, *Invocation) error
}

type ddSparseWriter interface {
	SkipZeros(int) (ddWriteStats, error)
}

type ddStdoutWriter struct {
	writer   io.Writer
	obs      int
	buffered bool
	pending  []byte
}

type ddFileWriter struct {
	abs      string
	perm     stdfs.FileMode
	obs      int
	buffered bool
	pending  []byte
	data     []byte
	cursor   int
}

type ddRedirectedFileWriter struct {
	*ddFileWriter
	handle any
}

type ddSeekableWriter struct {
	writer io.Writer
	seeker interface {
		Seek(int64, int) (int64, error)
	}
	statter interface {
		Stat() (stdfs.FileInfo, error)
	}
	obs        int
	buffered   bool
	pending    []byte
	targetSize int64
}

func parseDdOperands(inv *Invocation, operands []string) (ddSettings, error) {
	settings := ddSettings{
		ibs:    512,
		obs:    512,
		status: ddStatusDefault,
	}

	var (
		bsSet  bool
		bsSize int
	)

	for _, operand := range operands {
		key, value, ok := strings.Cut(operand, "=")
		if !ok {
			return settings, ddUsageError(inv, "unrecognized operand %s", quoteGNUOperand(operand))
		}
		switch key {
		case "bs":
			size, err := parseDdBlockSize(inv, "bs", value)
			if err != nil {
				return settings, err
			}
			bsSet = true
			bsSize = size
		case "cbs":
			size, err := parseDdBlockSize(inv, "cbs", value)
			if err != nil {
				return settings, err
			}
			settings.cbs = size
		case "conv":
			if err := parseDdConvFlags(inv, &settings.conv, value); err != nil {
				return settings, err
			}
		case "count":
			n, err := parseDdNumber(inv, value)
			if err != nil {
				return settings, err
			}
			settings.count = &n
		case "ibs":
			size, err := parseDdBlockSize(inv, "ibs", value)
			if err != nil {
				return settings, err
			}
			settings.ibs = size
		case "if":
			settings.infile = value
			settings.infileSet = true
		case "iflag":
			if err := parseDdInputFlags(inv, &settings.iflags, value); err != nil {
				return settings, err
			}
		case "obs":
			size, err := parseDdBlockSize(inv, "obs", value)
			if err != nil {
				return settings, err
			}
			settings.obs = size
		case "of":
			settings.outfile = value
			settings.outfileSet = true
		case "oflag":
			if err := parseDdOutputFlags(inv, &settings.oflags, value); err != nil {
				return settings, err
			}
		case "seek", "oseek":
			n, err := parseDdNumber(inv, value)
			if err != nil {
				return settings, err
			}
			settings.seek = n
		case "skip", "iseek":
			n, err := parseDdNumber(inv, value)
			if err != nil {
				return settings, err
			}
			settings.skip = n
		case "status":
			level, err := parseDdStatusLevel(inv, value)
			if err != nil {
				return settings, err
			}
			settings.status = level
		default:
			return settings, ddUsageError(inv, "unrecognized operand %s", quoteGNUOperand(operand))
		}
	}

	if bsSet {
		settings.ibs = bsSize
		settings.obs = bsSize
	}
	settings.skip = forceDdBytes(settings.skip, settings.iflags.skipBytes)
	settings.seek = forceDdBytes(settings.seek, settings.oflags.seekBytes)
	if settings.count != nil {
		count := forceDdBytes(*settings.count, settings.iflags.countBytes)
		settings.count = &count
	}
	if err := validateDdConv(inv, &settings); err != nil {
		return settings, err
	}
	if err := validateDdFlags(inv, &settings); err != nil {
		return settings, err
	}
	settings.buffered = !bsSet || settings.conv.hasDataTransform()
	return settings, nil
}

func parseDdConvFlags(inv *Invocation, opts *ddConvOptions, raw string) error {
	for flag := range strings.SplitSeq(raw, ",") {
		switch flag {
		case "ascii":
			if opts.kind != ddConversionNone && opts.kind != ddConversionASCII {
				return exitf(inv, 1, "dd: cannot combine any two of {ascii,ebcdic,ibm}")
			}
			opts.kind = ddConversionASCII
		case "ebcdic":
			if opts.kind != ddConversionNone && opts.kind != ddConversionEBCDIC {
				return exitf(inv, 1, "dd: cannot combine any two of {ascii,ebcdic,ibm}")
			}
			opts.kind = ddConversionEBCDIC
		case "ibm":
			if opts.kind != ddConversionNone && opts.kind != ddConversionIBM {
				return exitf(inv, 1, "dd: cannot combine any two of {ascii,ebcdic,ibm}")
			}
			opts.kind = ddConversionIBM
		case "ucase":
			if opts.caseMode == ddCaseLower {
				return exitf(inv, 1, "dd: cannot combine lcase and ucase")
			}
			opts.caseMode = ddCaseUpper
		case "lcase":
			if opts.caseMode == ddCaseUpper {
				return exitf(inv, 1, "dd: cannot combine lcase and ucase")
			}
			opts.caseMode = ddCaseLower
		case "block":
			opts.explicitBlock = true
		case "unblock":
			opts.explicitUnblock = true
		case "swab":
			opts.swab = true
		case "sync":
			opts.sync = true
		case "sparse":
			opts.sparse = true
		case "noerror":
			opts.noerror = true
		case "excl":
			opts.excl = true
		case "nocreat":
			opts.nocreat = true
		case "notrunc":
			opts.notrunc = true
		case "fdatasync":
			opts.fdatasync = true
		case "fsync":
			opts.fsync = true
		default:
			return ddUsageError(inv, "invalid conversion: %s", quoteGNUOperand(flag))
		}
	}
	return nil
}

func parseDdInputFlags(inv *Invocation, flags *ddInputFlags, raw string) error {
	for flag := range strings.SplitSeq(raw, ",") {
		switch flag {
		case "direct":
			flags.direct = true
		case "directory":
			flags.directory = true
		case "dsync":
			flags.dsync = true
		case "sync":
			flags.sync = true
		case "fullblock":
			flags.fullblock = true
		case "nonblock":
			flags.nonblock = true
		case "noatime":
			flags.noatime = true
		case "nocache":
			flags.nocache = true
		case "noctty":
			flags.noctty = true
		case "nofollow":
			flags.nofollow = true
		case "count_bytes":
			flags.countBytes = true
		case "skip_bytes":
			flags.skipBytes = true
		case "append", "seek_bytes":
			// GNU dd ignores output-only flags when passed as iflag=...
		default:
			return ddUsageError(inv, "invalid input flag: %s", quoteGNUOperand(flag))
		}
	}
	return nil
}

func parseDdOutputFlags(inv *Invocation, flags *ddOutputFlags, raw string) error {
	for flag := range strings.SplitSeq(raw, ",") {
		switch flag {
		case "append":
			flags.append = true
		case "direct":
			flags.direct = true
		case "directory":
			flags.directory = true
		case "dsync":
			flags.dsync = true
		case "sync":
			flags.sync = true
		case "nonblock":
			flags.nonblock = true
		case "noatime":
			flags.noatime = true
		case "nocache":
			flags.nocache = true
		case "noctty":
			flags.noctty = true
		case "nofollow":
			flags.nofollow = true
		case "seek_bytes":
			flags.seekBytes = true
		case "fullblock", "count_bytes", "skip_bytes":
			// GNU dd ignores input-only flags when passed as oflag=...
		default:
			return ddUsageError(inv, "invalid output flag: %s", quoteGNUOperand(flag))
		}
	}
	return nil
}

func parseDdStatusLevel(inv *Invocation, raw string) (ddStatusLevel, error) {
	switch raw {
	case "none":
		return ddStatusNone, nil
	case "noxfer":
		return ddStatusNoxfer, nil
	case "progress":
		return ddStatusProgress, nil
	default:
		return ddStatusDefault, ddUsageError(inv, "invalid status level: %s", quoteGNUOperand(raw))
	}
}

func parseDdNumber(inv *Invocation, raw string) (ddNumber, error) {
	value, err := parseDdSize(inv, raw)
	if err != nil {
		return ddNumber{}, err
	}
	return ddNumber{value: value, bytes: ddCountsBytes(raw)}, nil
}

func parseDdBlockSize(inv *Invocation, label, raw string) (int, error) {
	value, err := parseDdSize(inv, raw)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(raw))
	}
	if value > math.MaxInt {
		return 0, exitf(inv, 1, "dd: %s=N cannot fit into memory", label)
	}
	return int(value), nil
}

func parseDdSize(inv *Invocation, raw string) (uint64, error) {
	parts := strings.Split(raw, "x")
	if len(parts) == 1 {
		return parseDdSizePart(inv, raw, parts[0])
	}
	total := uint64(1)
	zeroProduct := false
	for _, part := range parts {
		if part == "0" {
			ddWarn(inv, "warning: %s is a zero multiplier; use %s if that is intended", quoteGNUOperand("0x"), quoteGNUOperand("00x"))
		}
		if zeroProduct {
			if !ddValidSizePartSyntax(part) {
				return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(raw))
			}
			continue
		}
		n, err := parseDdSizePart(inv, raw, part)
		if err != nil {
			return 0, err
		}
		if n == 0 {
			total = 0
			zeroProduct = true
			continue
		}
		if n != 0 && total > math.MaxUint64/n {
			return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(raw))
		}
		total *= n
	}
	return total, nil
}

func parseDdSizePart(inv *Invocation, full, part string) (uint64, error) {
	if part == "" {
		return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(full))
	}
	for _, candidate := range ddSizeSuffixes {
		if !strings.HasSuffix(part, candidate.text) {
			continue
		}
		digits := part[:len(part)-len(candidate.text)]
		if digits == "" {
			return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(full))
		}
		base, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(full))
		}
		if candidate.mult != 0 && base > math.MaxUint64/candidate.mult {
			return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(full))
		}
		return base * candidate.mult, nil
	}
	value, err := strconv.ParseUint(part, 10, 64)
	if err != nil {
		return 0, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(full))
	}
	return value, nil
}

func ddCountsBytes(raw string) bool {
	for part := range strings.SplitSeq(raw, "x") {
		if len(part) < 2 || !strings.HasSuffix(part, "B") {
			continue
		}
		prefix := part[:len(part)-1]
		last := prefix[len(prefix)-1]
		if last >= '0' && last <= '9' {
			return true
		}
	}
	return false
}

func ddValidSizePartSyntax(part string) bool {
	if part == "" {
		return false
	}
	for _, candidate := range ddSizeSuffixes {
		if !strings.HasSuffix(part, candidate.text) {
			continue
		}
		part = part[:len(part)-len(candidate.text)]
		break
	}
	if part == "" {
		return false
	}
	for _, r := range part {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateDdConv(inv *Invocation, settings *ddSettings) error {
	if settings.conv.excl && settings.conv.nocreat {
		return exitf(inv, 1, "dd: cannot combine excl and nocreat")
	}

	implied := ddBlockModeNone
	switch settings.conv.kind {
	case ddConversionASCII:
		implied = ddBlockModeUnblock
	case ddConversionEBCDIC, ddConversionIBM:
		implied = ddBlockModeBlock
	}
	if settings.cbs == 0 {
		settings.conv.blockMode = ddBlockModeNone
		return nil
	}
	if settings.conv.explicitBlock && (settings.conv.explicitUnblock || implied == ddBlockModeUnblock) {
		return exitf(inv, 1, "dd: cannot combine block and unblock")
	}
	if settings.conv.explicitUnblock && implied == ddBlockModeBlock {
		return exitf(inv, 1, "dd: cannot combine block and unblock")
	}
	switch {
	case settings.conv.explicitBlock:
		settings.conv.blockMode = ddBlockModeBlock
	case settings.conv.explicitUnblock:
		settings.conv.blockMode = ddBlockModeUnblock
	default:
		settings.conv.blockMode = implied
	}
	return nil
}

func validateDdFlags(inv *Invocation, settings *ddSettings) error {
	if settings.iflags.direct && settings.iflags.nocache {
		return exitf(inv, 1, "dd: cannot combine direct and nocache")
	}
	if settings.oflags.direct && settings.oflags.nocache {
		return exitf(inv, 1, "dd: cannot combine direct and nocache")
	}
	return nil
}

func forceDdBytes(num ddNumber, force bool) ddNumber {
	if force {
		num.bytes = true
	}
	return num
}

func runDd(ctx context.Context, inv *Invocation, settings *ddSettings) error {
	input, err := openDdInput(ctx, inv, settings)
	if err != nil {
		return err
	}
	defer func() {
		_ = input.Close()
	}()

	output, err := openDdOutput(ctx, inv, settings)
	if err != nil {
		return err
	}
	if ddUsesSamePath(inv, settings) {
		if materializer, ok := output.(ddOutputMaterializer); ok {
			if err := materializer.Materialize(ctx, inv); err != nil {
				return err
			}
			_ = input.Close()
			input, err = openDdInput(ctx, inv, settings)
			if err != nil {
				return err
			}
		}
	}

	return runDdWithIO(ctx, inv, settings, input, output)
}

func runDdWithIO(ctx context.Context, inv *Invocation, settings *ddSettings, input *ddInput, output ddOutputWriter) error {
	start := time.Now()
	lastProgress := start
	progressPrinted := false
	hadReadError := false
	var (
		swabCarry    byte
		hasSwabCarry bool
	)
	transformState := ddTransformState{}
	var (
		readStats  ddReadStats
		writeStats ddWriteStats
	)

	writeChunk := func(chunk []byte) error {
		transformed, trunc := ddTransformBlock(chunk, settings.conv, settings.cbs, false, &transformState)
		readStats.recordsTrunc += trunc

		writeUpdate, writeErr := ddWriteOutputBlock(output, transformed, settings)
		if writeErr != nil {
			return &ExitError{Code: 1, Err: writeErr}
		}
		writeStats.add(writeUpdate)

		if settings.status == ddStatusProgress && time.Since(lastProgress) >= time.Second {
			if err := ddWriteProgressLine(inv.Stderr, writeStats.bytesTotal, time.Since(start), true); err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			progressPrinted = true
			lastProgress = time.Now()
		}
		return nil
	}

	for ddBelowCount(settings.count, readStats) {
		requestSize := settings.ibs
		if settings.count != nil && settings.count.bytes {
			remaining := settings.count.value - readStats.bytesTotal
			if remaining == 0 {
				break
			}
			if remaining < uint64(requestSize) {
				requestSize = int(remaining)
			}
		}

		chunk, update, eof, readErr := readDdBlock(input.reader, requestSize, settings.iflags.fullblock)
		syntheticSync := false
		if readErr != nil {
			if !settings.conv.noerror {
				return exitf(inv, 1, "dd: error reading %s: %v", quoteGNUOperand(input.label), readErr)
			}
			ddWarn(inv, "error reading %s: %v", quoteGNUOperand(input.label), readErr)
			hadReadError = true
			if len(chunk) == 0 {
				if !settings.conv.sync {
					break
				}
				update = ddReadStats{
					recordsPartial: 1,
					bytesTotal:     uint64(requestSize),
				}
				syntheticSync = true
			}
		}
		if len(chunk) == 0 && !syntheticSync {
			break
		}

		readStats.add(update)
		if settings.conv.sync && len(chunk) < requestSize {
			pad := byte(0)
			if settings.conv.blockMode != ddBlockModeNone {
				pad = ' '
			}
			chunk = append(chunk, bytes.Repeat([]byte{pad}, requestSize-len(chunk))...)
		}
		if settings.conv.swab {
			chunk, swabCarry, hasSwabCarry = ddPrepareSwabChunk(chunk, swabCarry, hasSwabCarry, eof)
		}
		if len(chunk) > 0 {
			if err := writeChunk(chunk); err != nil {
				return err
			}
		}
		if eof {
			break
		}
	}

	if settings.conv.swab && hasSwabCarry {
		chunk, _, _ := ddPrepareSwabChunk(nil, swabCarry, hasSwabCarry, true)
		if len(chunk) > 0 {
			if err := writeChunk(chunk); err != nil {
				return err
			}
		}
	}
	if settings.conv.blockMode != ddBlockModeNone {
		transformed, trunc := ddTransformBlock(nil, settings.conv, settings.cbs, true, &transformState)
		readStats.recordsTrunc += trunc
		if len(transformed) > 0 {
			writeUpdate, writeErr := ddWriteOutputBlock(output, transformed, settings)
			if writeErr != nil {
				return &ExitError{Code: 1, Err: writeErr}
			}
			writeStats.add(writeUpdate)
		}
	}

	flushStats, err := output.Flush()
	if err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	writeStats.add(flushStats)
	if err := output.Sync(); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if err := output.Finalize(ctx, inv); err != nil {
		return err
	}

	if err := ddWriteFinalStats(inv.Stderr, settings.status, progressPrinted, readStats, writeStats, time.Since(start)); err != nil {
		return &ExitError{Code: 1, Err: err}
	}
	if hadReadError {
		return &ExitError{Code: 1}
	}
	return nil
}

func ddBelowCount(count *ddNumber, stats ddReadStats) bool {
	if count == nil {
		return true
	}
	if count.bytes {
		return stats.bytesTotal < count.value
	}
	return stats.recordCount() < count.value
}

func ddPrepareSwabChunk(chunk []byte, carry byte, hasCarry, finalize bool) ([]byte, byte, bool) {
	buf := make([]byte, 0, len(chunk)+1)
	if hasCarry {
		buf = append(buf, carry)
	}
	buf = append(buf, chunk...)
	if len(buf) == 0 {
		return nil, 0, false
	}
	if !finalize && len(buf)%2 == 1 {
		carry = buf[len(buf)-1]
		hasCarry = true
		buf = buf[:len(buf)-1]
	} else {
		hasCarry = false
	}
	if len(buf) == 0 {
		return nil, carry, hasCarry
	}
	ddSwab(buf)
	return buf, carry, hasCarry
}

func ddWriteOutputBlock(output ddOutputWriter, data []byte, settings *ddSettings) (ddWriteStats, error) {
	if len(data) == 0 {
		return ddWriteStats{}, nil
	}
	if settings != nil && settings.conv.sparse {
		if sparseWriter, ok := output.(ddSparseWriter); ok && ddIsAllZero(data) {
			return sparseWriter.SkipZeros(len(data))
		}
	}
	return output.WriteData(data)
}

func ddIsAllZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return len(data) > 0
}

func (in *ddInput) Close() error {
	if in == nil || in.closer == nil {
		return nil
	}
	return in.closer.Close()
}

func ddUsesSamePath(inv *Invocation, settings *ddSettings) bool {
	if settings == nil || !settings.infileSet || !settings.outfileSet {
		return false
	}
	if settings.infile == "" || settings.outfile == "" {
		return false
	}
	return allowPath(inv, settings.infile) == allowPath(inv, settings.outfile)
}

func ddScaledOffset(inv *Invocation, operand string, value ddNumber, blockSize int) (uint64, error) {
	if value.bytes || value.value == 0 {
		return value.value, nil
	}
	if value.value > math.MaxUint64/uint64(blockSize) {
		return 0, exitf(inv, 1, "dd: %s offset is too large", operand)
	}
	return value.value * uint64(blockSize), nil
}

func ddRedirectPath(handle any) string {
	meta, ok := handle.(commandutil.RedirectMetadata)
	if !ok {
		return ""
	}
	return meta.RedirectPath()
}

func ddRedirectOffset(handle any) int64 {
	meta, ok := handle.(commandutil.RedirectMetadata)
	if !ok {
		return 0
	}
	return meta.RedirectOffset()
}

func ddRedirectFlags(handle any) int {
	meta, ok := handle.(commandutil.RedirectMetadata)
	if !ok {
		return 0
	}
	return meta.RedirectFlags()
}

func ddStatHandle(ctx context.Context, inv *Invocation, handle any, path string) (stdfs.FileInfo, error) {
	if statter, ok := handle.(interface {
		Stat() (stdfs.FileInfo, error)
	}); ok {
		return statter.Stat()
	}
	if path == "" {
		return nil, stdfs.ErrInvalid
	}
	info, _, err := statPath(ctx, inv, path)
	return info, err
}

func ddHandleIsNamedPipe(ctx context.Context, inv *Invocation, handle any, path string) bool {
	if path == "" {
		return false
	}
	info, err := ddStatHandle(ctx, inv, handle, path)
	if err != nil || info == nil {
		return false
	}
	return info.Mode()&stdfs.ModeNamedPipe != 0
}

func ddSeekCurrent(handle any, delta uint64) bool {
	if delta == 0 {
		return true
	}
	if delta > math.MaxInt64 {
		return false
	}
	seeker, ok := handle.(interface {
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		return false
	}
	_, err := seeker.Seek(int64(delta), io.SeekCurrent)
	return err == nil
}

func openDdInput(ctx context.Context, inv *Invocation, settings *ddSettings) (*ddInput, error) {
	var (
		reader io.Reader
		label  string
		closer io.Closer
		path   string
	)
	if !settings.infileSet {
		reader = inv.Stdin
		label = "standard input"
		path = ddRedirectPath(reader)
		if path != "" {
			label = path
		}
	} else {
		if settings.infile == "" {
			return nil, exitf(inv, 1, "dd: failed to open %s: No such file or directory", quoteGNUOperand(settings.infile))
		}
		if settings.iflags.nofollow {
			if info, _, err := lstatPath(ctx, inv, settings.infile); err == nil && info.Mode()&stdfs.ModeSymlink != 0 {
				return nil, exitf(inv, 1, "dd: failed to open %s: Too many levels of symbolic links", quoteGNUOperand(settings.infile))
			}
		}
		if settings.iflags.directory {
			info, _, err := statPath(ctx, inv, settings.infile)
			if err != nil {
				return nil, exitf(inv, 1, "dd: failed to open %s: %s", quoteGNUOperand(settings.infile), readAllErrorText(err))
			}
			if !info.IsDir() {
				return nil, exitf(inv, 1, "dd: failed to open %s: Not a directory", quoteGNUOperand(settings.infile))
			}
		}
		file, _, err := openRead(ctx, inv, settings.infile)
		if err != nil {
			return nil, exitf(inv, 1, "dd: failed to open %s: %s", quoteGNUOperand(settings.infile), readAllErrorText(err))
		}
		reader = file
		label = settings.infile
		closer = file
		path = settings.infile
	}

	if settings.iflags.directory {
		info, err := ddStatHandle(ctx, inv, reader, path)
		if err != nil || !info.IsDir() {
			if closer != nil {
				_ = closer.Close()
			}
			return nil, exitf(inv, 1, "dd: setting flags for %s: Not a directory", quoteGNUOperand(label))
		}
	}

	if settings.iflags.nocache && !settings.infileSet && path == "" {
		return nil, exitf(inv, 1, "dd: failed to discard cache for %s: Illegal seek", quoteGNUOperand(label))
	}

	skipBytes, err := ddScaledOffset(inv, "skip", settings.skip, settings.ibs)
	if err != nil {
		if closer != nil {
			_ = closer.Close()
		}
		return nil, err
	}
	if skipBytes > 0 {
		if !ddSeekCurrent(reader, skipBytes) {
			discarded, err := ddDiscard(reader, skipBytes)
			if err != nil {
				if closer != nil {
					_ = closer.Close()
				}
				return nil, exitf(inv, 1, "dd: error reading %s: %v", quoteGNUOperand(label), err)
			}
			if discarded < skipBytes && (path == "" || ddHandleIsNamedPipe(ctx, inv, reader, path)) {
				ddWarn(inv, "%s: cannot skip to specified offset", quoteGNUOperand(label))
			}
		}
	}
	return &ddInput{reader: reader, label: label, closer: closer}, nil
}

func openDdOutput(ctx context.Context, inv *Invocation, settings *ddSettings) (ddOutputWriter, error) {
	seekBytes, err := ddScaledOffset(inv, "seek", settings.seek, settings.obs)
	if err != nil {
		return nil, err
	}
	if settings.oflags.directory && !settings.outfileSet {
		return nil, exitf(inv, 1, "dd: setting flags for %s: Not a directory", quoteGNUOperand("standard output"))
	}

	if !settings.outfileSet {
		if path := ddRedirectPath(inv.Stdout); path != "" {
			if flags := ddRedirectFlags(inv.Stdout); flags&os.O_APPEND != 0 {
				if seekBytes > math.MaxInt64 {
					return nil, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(strconv.FormatUint(seekBytes, 10)))
				}
				return &ddStdoutWriter{
					writer:   inv.Stdout,
					obs:      settings.obs,
					buffered: settings.buffered,
				}, nil
			}
			if seekBytes == 0 {
				return &ddStdoutWriter{
					writer:   inv.Stdout,
					obs:      settings.obs,
					buffered: settings.buffered,
				}, nil
			}
			if seekBytes > math.MaxInt64 {
				return nil, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(strconv.FormatUint(seekBytes, 10)))
			}
			if writer, ok := openDdSeekableWriter(inv.Stdout, settings, int64(seekBytes)); ok {
				return writer, nil
			}
			if ddHandleIsNamedPipe(ctx, inv, inv.Stdout, path) {
				return nil, exitf(inv, 1, "dd: %s: cannot seek: Illegal seek", quoteGNUOperand("standard output"))
			}
			writer, err := openDdPathOutput(ctx, inv, settings, path, ddRedirectOffset(inv.Stdout), true)
			if err != nil {
				return nil, err
			}
			if fileWriter, ok := writer.(*ddFileWriter); ok {
				return &ddRedirectedFileWriter{ddFileWriter: fileWriter, handle: inv.Stdout}, nil
			}
			return writer, nil
		}
	}

	if !settings.outfileSet {
		if seekBytes > 0 {
			return nil, exitf(inv, 1, "dd: %s: cannot seek: Illegal seek", quoteGNUOperand("standard output"))
		}
		return &ddStdoutWriter{
			writer:   inv.Stdout,
			obs:      settings.obs,
			buffered: settings.buffered,
		}, nil
	}
	return openDdPathOutput(ctx, inv, settings, settings.outfile, 0, false)
}

func openDdPathOutput(ctx context.Context, inv *Invocation, settings *ddSettings, target string, initialOffset int64, seekFromCurrent bool) (ddOutputWriter, error) {
	seekBytes, err := ddScaledOffset(inv, "seek", settings.seek, settings.obs)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, exitf(inv, 1, "dd: failed to open %s: No such file or directory", quoteGNUOperand(target))
	}
	if settings.oflags.nofollow {
		if info, _, err := lstatPath(ctx, inv, target); err == nil && info.Mode()&stdfs.ModeSymlink != 0 {
			return nil, exitf(inv, 1, "dd: failed to open %s: Too many levels of symbolic links", quoteGNUOperand(target))
		}
	}

	abs := allowPath(inv, target)
	if err := ensureParentDirExists(ctx, inv, abs); err != nil {
		return nil, exitf(inv, 1, "dd: failed to open %s: %s", quoteGNUOperand(target), ddPathErrorText(err))
	}

	info, _, exists, err := statMaybe(ctx, inv, target)
	if err != nil {
		return nil, exitf(inv, 1, "dd: failed to open %s: %s", quoteGNUOperand(target), readAllErrorText(err))
	}
	if settings.conv.excl && exists {
		return nil, exitf(inv, 1, "dd: failed to open %s: File exists", quoteGNUOperand(target))
	}
	if settings.conv.nocreat && !exists {
		return nil, exitf(inv, 1, "dd: failed to open %s: No such file or directory", quoteGNUOperand(target))
	}
	if settings.oflags.directory {
		if !exists || (info != nil && !info.IsDir()) {
			return nil, exitf(inv, 1, "dd: failed to open %s: Invalid argument", quoteGNUOperand(target))
		}
	}
	if exists && info != nil && info.IsDir() {
		return nil, exitf(inv, 1, "dd: failed to open %s: Invalid argument", quoteGNUOperand(target))
	}

	perm := stdfs.FileMode(0o644)
	existing := []byte{}
	cursor64 := max(initialOffset, int64(0))
	if seekBytes > 0 {
		if seekFromCurrent {
			if seekBytes > uint64(math.MaxInt64-cursor64) {
				return nil, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(strconv.FormatUint(seekBytes, 10)))
			}
			cursor64 += int64(seekBytes)
		} else {
			cursor64 = int64(seekBytes)
		}
	}
	needExisting := exists && info != nil && (cursor64 > 0 || settings.conv.notrunc)
	if exists && info != nil {
		perm = info.Mode().Perm()
		if perm == 0 {
			perm = 0o644
		}
		if needExisting {
			data, _, readErr := readAllFile(ctx, inv, target)
			if readErr != nil {
				return nil, exitf(inv, 1, "dd: error reading %s: %s", quoteGNUOperand(target), readAllErrorText(readErr))
			}
			existing = data
		}
	}
	if cursor64 > math.MaxInt {
		return nil, exitf(inv, 1, "dd: invalid number: %s", quoteGNUOperand(strconv.FormatInt(cursor64, 10)))
	}

	cursor := int(cursor64)
	var data []byte
	switch {
	case settings.oflags.append && settings.conv.notrunc:
		data = append([]byte(nil), existing...)
		cursor = len(data)
	case settings.conv.notrunc:
		data = append([]byte(nil), existing...)
		if cursor > len(data) {
			data = append(data, make([]byte, cursor-len(data))...)
		}
	default:
		prefixLen := minInt(cursor, len(existing))
		if prefixLen > 0 {
			data = append([]byte(nil), existing[:prefixLen]...)
		} else {
			data = []byte{}
		}
		if cursor > len(data) {
			data = append(data, make([]byte, cursor-len(data))...)
		}
	}

	return &ddFileWriter{
		abs:      abs,
		perm:     perm,
		obs:      settings.obs,
		buffered: settings.buffered,
		data:     data,
		cursor:   cursor,
	}, nil
}

func openDdSeekableWriter(handle io.Writer, settings *ddSettings, seekBytes int64) (*ddSeekableWriter, bool) {
	seeker, ok := handle.(interface {
		Seek(int64, int) (int64, error)
	})
	if !ok {
		return nil, false
	}
	statter, ok := handle.(interface {
		Stat() (stdfs.FileInfo, error)
	})
	if !ok {
		return nil, false
	}
	position, err := seeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, false
	}
	if seekBytes > 0 {
		position, err = seeker.Seek(seekBytes, io.SeekCurrent)
		if err != nil {
			return nil, false
		}
	}
	return &ddSeekableWriter{
		writer:     handle,
		seeker:     seeker,
		statter:    statter,
		obs:        settings.obs,
		buffered:   settings.buffered,
		targetSize: position,
	}, true
}

func readDdBlock(reader io.Reader, size int, fullblock bool) ([]byte, ddReadStats, bool, error) {
	if size <= 0 {
		return nil, ddReadStats{}, true, nil
	}
	buf := make([]byte, size)
	total := 0
	for total < size {
		n, err := reader.Read(buf[total:])
		if n > 0 {
			total += n
			if !fullblock {
				stats := ddReadStats{bytesTotal: uint64(total)}
				if total == size {
					stats.recordsComplete = 1
				} else {
					stats.recordsPartial = 1
				}
				switch {
				case err == nil:
					return buf[:total], stats, false, nil
				case errors.Is(err, io.EOF):
					return buf[:total], stats, true, nil
				default:
					return buf[:total], stats, false, err
				}
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			if total == 0 {
				return nil, ddReadStats{}, true, nil
			}
			stats := ddReadStats{recordsPartial: 1, bytesTotal: uint64(total)}
			if total == size {
				stats.recordsComplete = 1
				stats.recordsPartial = 0
			}
			return buf[:total], stats, true, nil
		}
		if total == 0 {
			return nil, ddReadStats{}, false, err
		}
		stats := ddReadStats{recordsPartial: 1, bytesTotal: uint64(total)}
		return buf[:total], stats, false, err
	}
	return buf, ddReadStats{recordsComplete: 1, bytesTotal: uint64(size)}, false, nil
}

func ddDiscard(reader io.Reader, bytesToSkip uint64) (uint64, error) {
	var (
		discarded uint64
		buf       = make([]byte, 32*1024)
	)
	for discarded < bytesToSkip {
		want := len(buf)
		if remaining := bytesToSkip - discarded; remaining < uint64(want) {
			want = int(remaining)
		}
		n, err := reader.Read(buf[:want])
		discarded += uint64(n)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return discarded, nil
		}
		return discarded, err
	}
	return discarded, nil
}

func ddTransformBlock(input []byte, conv ddConvOptions, cbs int, eof bool, state *ddTransformState) ([]byte, uint32) {
	data := append([]byte(nil), input...)
	if conv.kind == ddConversionASCII {
		ddTranslate(data, ddEBCDICToASCII[:])
		ddApplyCase(data, conv.caseMode)
		if conv.blockMode != ddBlockModeNone {
			return ddApplyBlockMode(data, conv.blockMode, cbs, eof, state)
		}
		return data, 0
	}

	var trunc uint32
	if conv.blockMode != ddBlockModeNone {
		data, trunc = ddApplyBlockMode(data, conv.blockMode, cbs, eof, state)
	}
	ddApplyCase(data, conv.caseMode)
	switch conv.kind {
	case ddConversionEBCDIC:
		ddTranslate(data, ddASCIIToEBCDIC[:])
	case ddConversionIBM:
		ddTranslate(data, ddASCIIToIBM[:])
	}
	return data, trunc
}

func ddTranslate(buf, table []byte) {
	for i, b := range buf {
		buf[i] = table[int(b)]
	}
}

func ddApplyCase(buf []byte, mode ddCaseMode) {
	for i, b := range buf {
		r := rune(b)
		switch mode {
		case ddCaseUpper:
			r = unicode.ToUpper(r)
		case ddCaseLower:
			r = unicode.ToLower(r)
		}
		if r <= math.MaxUint8 {
			buf[i] = byte(r)
		}
	}
}

func ddApplyBlockMode(buf []byte, mode ddBlockMode, cbs int, eof bool, state *ddTransformState) ([]byte, uint32) {
	if cbs <= 0 {
		return buf, 0
	}
	if state == nil {
		state = &ddTransformState{}
	}
	switch mode {
	case ddBlockModeBlock:
		return state.block(buf, cbs, eof)
	case ddBlockModeUnblock:
		return state.unblock(buf, cbs, eof), 0
	default:
		return buf, 0
	}
}

func (s *ddTransformState) block(buf []byte, cbs int, eof bool) ([]byte, uint32) {
	out := make([]byte, 0, len(buf))
	var truncated uint32
	flush := func(force bool) {
		if !force && len(s.blockCarry) == 0 && !s.blockOverflow {
			return
		}
		block := make([]byte, cbs)
		copy(block, s.blockCarry)
		for i := len(s.blockCarry); i < cbs; i++ {
			block[i] = ' '
		}
		if s.blockOverflow {
			truncated++
		}
		out = append(out, block...)
		s.blockCarry = s.blockCarry[:0]
		s.blockOverflow = false
	}
	for _, b := range buf {
		if b == '\n' {
			flush(true)
			continue
		}
		if len(s.blockCarry) < cbs {
			s.blockCarry = append(s.blockCarry, b)
		} else {
			s.blockOverflow = true
		}
	}
	if eof {
		flush(len(s.blockCarry) > 0 || s.blockOverflow)
	}
	return out, truncated
}

func (s *ddTransformState) unblock(buf []byte, cbs int, eof bool) []byte {
	if cbs <= 0 {
		return append([]byte(nil), buf...)
	}
	s.blockCarry = append(s.blockCarry, buf...)
	out := make([]byte, 0, len(s.blockCarry)+len(s.blockCarry)/cbs+1)
	for len(s.blockCarry) >= cbs {
		chunk := bytes.TrimRight(s.blockCarry[:cbs], " ")
		out = append(out, chunk...)
		out = append(out, '\n')
		s.blockCarry = s.blockCarry[cbs:]
	}
	if eof && len(s.blockCarry) > 0 {
		chunk := bytes.TrimRight(s.blockCarry, " ")
		out = append(out, chunk...)
		out = append(out, '\n')
		s.blockCarry = s.blockCarry[:0]
	}
	return out
}

func ddSwab(buf []byte) {
	for i := 1; i < len(buf); i += 2 {
		buf[i-1], buf[i] = buf[i], buf[i-1]
	}
}

func (w *ddStdoutWriter) WriteData(data []byte) (ddWriteStats, error) {
	if !w.buffered {
		return ddWriteChunks(w.writer, data, w.obs)
	}
	w.pending = append(w.pending, data...)
	full := len(w.pending) - (len(w.pending) % w.obs)
	if full == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := ddWriteChunks(w.writer, w.pending[:full], w.obs)
	if err != nil {
		return stats, err
	}
	copy(w.pending, w.pending[full:])
	w.pending = w.pending[:len(w.pending)-full]
	return stats, nil
}

func (w *ddStdoutWriter) Flush() (ddWriteStats, error) {
	if len(w.pending) == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := ddWriteChunks(w.writer, w.pending, w.obs)
	if err != nil {
		return stats, err
	}
	w.pending = nil
	return stats, nil
}

func (w *ddStdoutWriter) Sync() error {
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func (w *ddStdoutWriter) Finalize(context.Context, *Invocation) error {
	return nil
}

func (w *ddFileWriter) WriteData(data []byte) (ddWriteStats, error) {
	if !w.buffered {
		return w.writeChunks(data)
	}
	w.pending = append(w.pending, data...)
	full := len(w.pending) - (len(w.pending) % w.obs)
	if full == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := w.writeChunks(w.pending[:full])
	if err != nil {
		return stats, err
	}
	copy(w.pending, w.pending[full:])
	w.pending = w.pending[:len(w.pending)-full]
	return stats, nil
}

func (w *ddFileWriter) Flush() (ddWriteStats, error) {
	if len(w.pending) == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := w.writeChunks(w.pending)
	if err != nil {
		return stats, err
	}
	w.pending = nil
	return stats, nil
}

func (w *ddFileWriter) Sync() error {
	return nil
}

func (w *ddFileWriter) Finalize(ctx context.Context, inv *Invocation) error {
	w.ensureSize()
	return writeFileContents(ctx, inv, w.abs, w.data, w.perm)
}

func (w *ddFileWriter) Materialize(ctx context.Context, inv *Invocation) error {
	w.ensureSize()
	return writeFileContents(ctx, inv, w.abs, w.data, w.perm)
}

func (w *ddRedirectedFileWriter) Finalize(ctx context.Context, inv *Invocation) error {
	if err := w.ddFileWriter.Finalize(ctx, inv); err != nil {
		return err
	}
	ddSyncRedirectOffset(w.handle, w.cursor)
	return nil
}

func (w *ddRedirectedFileWriter) Materialize(ctx context.Context, inv *Invocation) error {
	if err := w.ddFileWriter.Materialize(ctx, inv); err != nil {
		return err
	}
	ddSyncRedirectOffset(w.handle, w.cursor)
	return nil
}

func (w *ddSeekableWriter) WriteData(data []byte) (ddWriteStats, error) {
	if !w.buffered {
		return w.writeChunks(data)
	}
	w.pending = append(w.pending, data...)
	full := len(w.pending) - (len(w.pending) % w.obs)
	if full == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := w.writeChunks(w.pending[:full])
	if err != nil {
		return stats, err
	}
	copy(w.pending, w.pending[full:])
	w.pending = w.pending[:len(w.pending)-full]
	return stats, nil
}

func (w *ddSeekableWriter) Flush() (ddWriteStats, error) {
	if len(w.pending) == 0 {
		return ddWriteStats{}, nil
	}
	stats, err := w.writeChunks(w.pending)
	if err != nil {
		return stats, err
	}
	w.pending = nil
	return stats, nil
}

func (w *ddSeekableWriter) Sync() error {
	if syncer, ok := w.writer.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	if flusher, ok := w.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func (w *ddSeekableWriter) Finalize(context.Context, *Invocation) error {
	if w.targetSize > 0 {
		info, err := w.statter.Stat()
		if err != nil {
			return err
		}
		if info.Size() < w.targetSize {
			if _, err := w.seeker.Seek(w.targetSize-1, io.SeekStart); err != nil {
				return err
			}
			if _, err := w.writer.Write([]byte{0}); err != nil {
				return err
			}
		}
	}
	_, err := w.seeker.Seek(w.targetSize, io.SeekStart)
	return err
}

func (w *ddSeekableWriter) SkipZeros(size int) (ddWriteStats, error) {
	if _, err := w.seeker.Seek(int64(size), io.SeekCurrent); err != nil {
		return ddWriteStats{}, err
	}
	w.targetSize += int64(size)
	return ddCountWriteStats(size, w.obs), nil
}

func (w *ddSeekableWriter) writeChunks(data []byte) (ddWriteStats, error) {
	stats, err := ddWriteChunks(w.writer, data, w.obs)
	if err != nil {
		return stats, err
	}
	w.targetSize += int64(len(data))
	return stats, nil
}

func (w *ddFileWriter) SkipZeros(size int) (ddWriteStats, error) {
	w.cursor += size
	w.ensureSize()
	return ddCountWriteStats(size, w.obs), nil
}

func (w *ddFileWriter) writeChunks(data []byte) (ddWriteStats, error) {
	stats := ddCountWriteStats(len(data), w.obs)
	for len(data) > 0 {
		chunkLen := minInt(len(data), w.obs)
		chunk := data[:chunkLen]
		w.write(chunk)
		data = data[chunkLen:]
	}
	return stats, nil
}

func (w *ddFileWriter) ensureSize() {
	if w.cursor > len(w.data) {
		w.data = append(w.data, make([]byte, w.cursor-len(w.data))...)
	}
}

func (w *ddFileWriter) write(chunk []byte) {
	end := w.cursor + len(chunk)
	if end > len(w.data) {
		w.data = append(w.data, make([]byte, end-len(w.data))...)
	}
	copy(w.data[w.cursor:end], chunk)
	w.cursor = end
}

func ddWriteChunks(writer io.Writer, data []byte, obs int) (ddWriteStats, error) {
	stats := ddCountWriteStats(len(data), obs)
	for len(data) > 0 {
		chunkLen := minInt(len(data), obs)
		chunk := data[:chunkLen]
		if _, err := writer.Write(chunk); err != nil {
			return stats, err
		}
		data = data[chunkLen:]
	}
	return stats, nil
}

func ddCountWriteStats(size, obs int) ddWriteStats {
	stats := ddWriteStats{}
	for size > 0 {
		chunkLen := minInt(size, obs)
		if chunkLen == obs {
			stats.recordsComplete++
		} else {
			stats.recordsPartial++
		}
		stats.bytesTotal += uint64(chunkLen)
		size -= chunkLen
	}
	return stats
}

func ddSyncRedirectOffset(handle any, cursor int) {
	seeker, ok := handle.(interface {
		Seek(offset int64, whence int) (int64, error)
	})
	if !ok {
		return
	}
	_, _ = seeker.Seek(int64(cursor), io.SeekStart)
}

func ddWriteFinalStats(w io.Writer, status ddStatusLevel, progressPrinted bool, reads ddReadStats, writes ddWriteStats, duration time.Duration) error {
	if w == nil || status == ddStatusNone {
		return nil
	}
	if status == ddStatusProgress && progressPrinted {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "%d+%d records in\n", reads.recordsComplete, reads.recordsPartial); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%d+%d records out\n", writes.recordsComplete, writes.recordsPartial); err != nil {
		return err
	}
	if reads.recordsTrunc > 0 {
		label := "records"
		if reads.recordsTrunc == 1 {
			label = "record"
		}
		if _, err := fmt.Fprintf(w, "%d truncated %s\n", reads.recordsTrunc, label); err != nil {
			return err
		}
	}
	if status == ddStatusNoxfer {
		return nil
	}
	_, err := fmt.Fprintln(w, ddFormatProgressLine(writes.bytesTotal, duration))
	return err
}

func ddWriteProgressLine(w io.Writer, bytesWritten uint64, duration time.Duration, rewrite bool) error {
	if w == nil {
		return nil
	}
	line := ddFormatProgressLine(bytesWritten, duration)
	if rewrite {
		_, err := fmt.Fprintf(w, "\r%s", line)
		return err
	}
	_, err := fmt.Fprintln(w, line)
	return err
}

func ddFormatProgressLine(bytesWritten uint64, duration time.Duration) string {
	seconds := duration.Seconds()
	if seconds <= 0 {
		seconds = float64(time.Nanosecond) / float64(time.Second)
	}
	durationText := strconv.FormatFloat(seconds, 'g', -1, 64)
	rateBytes := uint64(float64(bytesWritten) / seconds)
	rateText := ddFormatMagnitude(rateBytes, true)
	switch {
	case bytesWritten == 1:
		return fmt.Sprintf("1 byte copied, %s s, %s/s", durationText, rateText)
	case bytesWritten <= 999:
		return fmt.Sprintf("%d bytes copied, %s s, %s/s", bytesWritten, durationText, rateText)
	case bytesWritten <= 1023:
		return fmt.Sprintf("%d bytes (%s) copied, %s s, %s/s", bytesWritten, ddFormatMagnitude(bytesWritten, true), durationText, rateText)
	default:
		return fmt.Sprintf("%d bytes (%s, %s) copied, %s s, %s/s", bytesWritten, ddFormatMagnitude(bytesWritten, true), ddFormatMagnitude(bytesWritten, false), durationText, rateText)
	}
}

func ddFormatMagnitude(value uint64, si bool) string {
	type unit struct {
		threshold float64
		suffix    string
	}
	var units []unit
	if si {
		units = []unit{
			{1, "B"}, {1e3, "kB"}, {1e6, "MB"}, {1e9, "GB"}, {1e12, "TB"}, {1e15, "PB"}, {1e18, "EB"},
		}
	} else {
		units = []unit{
			{1, "B"}, {1 << 10, "KiB"}, {1 << 20, "MiB"}, {1 << 30, "GiB"}, {1 << 40, "TiB"}, {1 << 50, "PiB"}, {1 << 60, "EiB"},
		}
	}
	chosen := units[0]
	for _, unit := range units {
		if float64(value) >= unit.threshold {
			chosen = unit
			continue
		}
		break
	}
	quotient := float64(value) / chosen.threshold
	if quotient < 10 {
		return fmt.Sprintf("%.1f %s", quotient, chosen.suffix)
	}
	return fmt.Sprintf("%.0f %s", math.Round(quotient), chosen.suffix)
}

func ddWarn(inv *Invocation, format string, args ...any) {
	if inv == nil || inv.Stderr == nil {
		return
	}
	_, _ = fmt.Fprintf(inv.Stderr, "dd: "+format+"\n", args...)
}

func ddUsageError(inv *Invocation, format string, args ...any) error {
	return exitf(inv, 1, "dd: %s\nTry 'dd --help' for more information.", fmt.Sprintf(format, args...))
}

func ddPathErrorText(err error) string {
	if err == nil {
		return ""
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Err != nil {
		return ddPathErrorText(exitErr.Err)
	}
	if errors.Is(err, stdfs.ErrNotExist) {
		return "No such file or directory"
	}
	if errors.Is(err, stdfs.ErrPermission) {
		return "Permission denied"
	}
	if errors.Is(err, stdfs.ErrInvalid) {
		return "Invalid argument"
	}
	return err.Error()
}

const ddAfterHelpText = `Copy a file, converting and formatting according to the operands.

  bs=BYTES        read and write up to BYTES bytes at a time (default: 512);
                  overrides ibs and obs
  cbs=BYTES       convert BYTES bytes at a time
  conv=CONVS      convert the file as per the comma separated symbol list
  count=N         copy only N input blocks
  ibs=BYTES       read up to BYTES bytes at a time (default: 512)
  if=FILE         read from FILE instead of standard input
  iflag=FLAGS     read as per the comma separated symbol list
  obs=BYTES       write BYTES bytes at a time (default: 512)
  of=FILE         write to FILE instead of standard output
  oflag=FLAGS     write as per the comma separated symbol list
  seek=N          (or oseek=N) skip N obs-sized output blocks
  skip=N          (or iseek=N) skip N ibs-sized input blocks
  status=LEVEL    control whether transfer statistics are written to stderr

N and BYTES may be followed by the following multiplicative suffixes:
c=1, w=2, b=512, kB=1000, K=1024, MB=1000*1000, M=1024*1024,
GB=1000*1000*1000, G=1024*1024*1024, and likewise for T, P, and E.
Binary prefixes can also be used: KiB=K, MiB=M, and so on.
If N ends in 'B', it counts bytes instead of blocks.
`

var _ Command = (*Dd)(nil)
var _ SpecProvider = (*Dd)(nil)
var _ ParsedRunner = (*Dd)(nil)
