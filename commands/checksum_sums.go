package commands

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	stdfs "io/fs"
	"os"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/policy"
	"golang.org/x/crypto/blake2b"
)

type checksumSum struct {
	name             string
	tagName          string
	digestLen        int
	newHash          func() hash.Hash
	supportsLength   bool
	defaultLengthLen int
}

type checksumVerbosity int

const (
	checksumVerbosityStatus checksumVerbosity = iota
	checksumVerbosityQuiet
	checksumVerbosityNormal
	checksumVerbosityWarn
)

type checksumOptions struct {
	files         []string
	check         bool
	binary        bool
	text          bool
	tag           bool
	zero          bool
	ignoreMissing bool
	strict        bool
	verbosity     checksumVerbosity
	textFlagSet   bool
	binaryFlagSet bool
	tagFlagSet    bool
	quietFlagSet  bool
	statusFlagSet bool
	warnFlagSet   bool
	strictFlagSet bool
	ignoreFlagSet bool
	lengthBytes   int
}

type checksumLineFormat int

const (
	checksumLineFormatAlgo checksumLineFormat = iota
	checksumLineFormatUntagged
	checksumLineFormatSingleSpace
)

type checksumLine struct {
	sum         string
	filename    string
	format      checksumLineFormat
	lengthBytes int
}

type checksumStats struct {
	correct         int
	failedChecksum  int
	failedOpen      int
	badFormat       int
	totalConsidered int
}

type checksumLineError int

const (
	checksumLineSkipped checksumLineError = iota
	checksumLineImproper
	checksumLineFailedChecksum
	checksumLineFailedOpen
	checksumLineIgnoredMissing
	checksumLineOK
)

const checksumSumsHelpText = `%s - compute or check %s message digests

Usage: %s [OPTION]... [FILE]...

Options:
  -b, --binary          read in binary mode
  -c, --check           read %s sums from the FILEs and check them
%s
      --tag             create a BSD-style checksum
  -t, --text            read in text mode (default)
  -z, --zero            end each output line with NUL, not newline
      --ignore-missing  don't fail or report status for missing files
      --quiet           don't print OK for each successfully verified file
      --status          don't output anything, status code shows success
      --strict          exit non-zero for improperly formatted checksum lines
  -w, --warn            warn about improperly formatted checksum lines
      --help            display this help and exit
      --version         output version information and exit
`

func NewMD5Sum() *checksumSum {
	return &checksumSum{
		name:      "md5sum",
		tagName:   "MD5",
		digestLen: md5.Size,
		newHash:   md5.New,
	}
}

func NewSHA1Sum() *checksumSum {
	return &checksumSum{
		name:      "sha1sum",
		tagName:   "SHA1",
		digestLen: sha1.Size,
		newHash:   sha1.New,
	}
}

func NewSHA224Sum() *checksumSum {
	return &checksumSum{
		name:      "sha224sum",
		tagName:   "SHA224",
		digestLen: sha256.Size224,
		newHash:   sha256.New224,
	}
}

func NewSHA256Sum() *checksumSum {
	return &checksumSum{
		name:      "sha256sum",
		tagName:   "SHA256",
		digestLen: sha256.Size,
		newHash:   sha256.New,
	}
}

func NewSHA384Sum() *checksumSum {
	return &checksumSum{
		name:      "sha384sum",
		tagName:   "SHA384",
		digestLen: sha512.Size384,
		newHash:   sha512.New384,
	}
}

func NewSHA512Sum() *checksumSum {
	return &checksumSum{
		name:      "sha512sum",
		tagName:   "SHA512",
		digestLen: sha512.Size,
		newHash:   sha512.New,
	}
}

func NewB2Sum() *checksumSum {
	return &checksumSum{
		name:             "b2sum",
		tagName:          "BLAKE2b",
		digestLen:        blake2b.Size,
		supportsLength:   true,
		defaultLengthLen: blake2b.Size,
	}
}

func (c *checksumSum) Name() string {
	return c.name
}

func (c *checksumSum) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *checksumSum) Spec() CommandSpec {
	options := []OptionSpec{
		{Name: "binary", Short: 'b', Long: "binary", Help: "read in binary mode"},
		{Name: "check", Short: 'c', Long: "check", Help: fmt.Sprintf("read %s sums from the FILEs and check them", c.tagName)},
		{Name: "tag", Long: "tag", Help: "create a BSD-style checksum"},
		{Name: "text", Short: 't', Long: "text", Help: "read in text mode (default)"},
		{Name: "zero", Short: 'z', Long: "zero", Help: "end each output line with NUL, not newline"},
		{Name: "ignore-missing", Long: "ignore-missing", Help: "don't fail or report status for missing files"},
		{Name: "quiet", Long: "quiet", Help: "don't print OK for each successfully verified file"},
		{Name: "status", Long: "status", Help: "don't output anything, status code shows success"},
		{Name: "strict", Long: "strict", Help: "exit non-zero for improperly formatted checksum lines"},
		{Name: "warn", Short: 'w', Long: "warn", Help: "warn about improperly formatted checksum lines"},
	}
	if c.supportsLength {
		options = append(options[:2], append([]OptionSpec{
			{Name: "length", Short: 'l', Long: "length", ValueName: "BITS", Arity: OptionRequiredValue, Help: "digest length in bits"},
		}, options[2:]...)...)
	}

	return CommandSpec{
		Name:    c.name,
		About:   fmt.Sprintf("%s - compute or check %s message digests", c.name, c.tagName),
		Usage:   fmt.Sprintf("%s [OPTION]... [FILE]...", c.name),
		Options: options,
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
		HelpRenderer: func(w io.Writer, _ CommandSpec) error {
			lengthLine := ""
			if c.supportsLength {
				lengthLine = "  -l, --length=BITS     digest length in bits\n"
			}
			_, err := fmt.Fprintf(w, checksumSumsHelpText, c.name, c.tagName, c.name, c.tagName, lengthLine)
			return err
		},
	}
}

func (c *checksumSum) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := c.optionsFromMatches(inv, matches)
	if err != nil {
		return err
	}
	if len(opts.files) == 0 {
		opts.files = []string{"-"}
	}
	if opts.check {
		return c.runCheckMode(ctx, inv, opts)
	}
	return c.runDigestMode(ctx, inv, opts)
}

func (c *checksumSum) optionsFromMatches(inv *Invocation, matches *ParsedCommand) (checksumOptions, error) {
	opts := checksumOptions{
		text:        true,
		verbosity:   checksumVerbosityNormal,
		lengthBytes: c.digestLen,
	}

	for _, name := range matches.OptionOrder() {
		switch name {
		case "check":
			opts.check = true
		case "binary":
			opts.binary = true
			opts.text = false
			opts.binaryFlagSet = true
		case "text":
			opts.text = true
			opts.binary = false
			opts.textFlagSet = true
		case "tag":
			opts.tag = true
			opts.tagFlagSet = true
		case "zero":
			opts.zero = true
		case "ignore-missing":
			opts.ignoreMissing = true
			opts.ignoreFlagSet = true
		case "quiet":
			opts.verbosity = checksumVerbosityQuiet
			opts.quietFlagSet = true
		case "status":
			opts.verbosity = checksumVerbosityStatus
			opts.statusFlagSet = true
		case "strict":
			opts.strict = true
			opts.strictFlagSet = true
		case "warn":
			opts.verbosity = checksumVerbosityWarn
			opts.warnFlagSet = true
		case "length":
			lengthBytes, err := parseChecksumLengthBits(matches.Value("length"))
			if err != nil {
				return checksumOptions{}, writeChecksumLengthError(inv, c.name, matches.Value("length"), err)
			}
			opts.lengthBytes = lengthBytes
		}
	}
	opts.files = matches.Args("file")

	if !opts.check {
		if opts.ignoreFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --ignore-missing option is meaningful only when verifying checksums", c.name)
		}
		if opts.quietFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --quiet option is meaningful only when verifying checksums", c.name)
		}
		if opts.statusFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --status option is meaningful only when verifying checksums", c.name)
		}
		if opts.strictFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --strict option is meaningful only when verifying checksums", c.name)
		}
		if opts.warnFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --warn option is meaningful only when verifying checksums", c.name)
		}
	}
	if opts.check {
		if opts.tagFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --tag option is meaningless when verifying checksums", c.name)
		}
		if opts.binaryFlagSet || opts.textFlagSet {
			return checksumOptions{}, exitf(inv, 1, "%s: the --binary and --text options are meaningless when verifying checksums", c.name)
		}
	}
	if opts.textFlagSet && opts.tag {
		return checksumOptions{}, exitf(inv, 1, "%s: --tag does not support --text mode", c.name)
	}

	return opts, nil
}

func (c *checksumSum) runDigestMode(ctx context.Context, inv *Invocation, opts checksumOptions) error {
	exitCode := 0
	for _, name := range opts.files {
		data, err := c.readDigestInput(ctx, inv, name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			c.reportOpenError(inv.Stderr, name, err)
			exitCode = 1
			continue
		}
		line, err := c.renderDigestLine(data, name, opts)
		if err != nil {
			return &ExitError{Code: 1, Err: err}
		}
		if _, err := inv.Stdout.Write(line); err != nil {
			return &ExitError{Code: 1, Err: err}
		}
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

func (c *checksumSum) runCheckMode(ctx context.Context, inv *Invocation, opts checksumOptions) error {
	failed := false
	for _, name := range opts.files {
		data, displayName, err := c.readChecksumList(ctx, inv, name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			c.reportOpenError(inv.Stderr, displayName, err)
			failed = true
			continue
		}
		if err := c.verifyChecksumList(ctx, inv, opts, displayName, data); err != nil {
			if policy.IsDenied(err) {
				return err
			}
			failed = true
		}
	}
	if failed {
		return &ExitError{Code: 1}
	}
	return nil
}

func (c *checksumSum) readDigestInput(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
	if name == "-" {
		return readAllStdin(ctx, inv)
	}
	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, &stdfs.PathError{Op: "open", Path: name, Err: stdfs.ErrInvalid}
	}
	data, _, err := readAllFile(ctx, inv, name)
	return data, err
}

func (c *checksumSum) readChecksumList(ctx context.Context, inv *Invocation, name string) (data []byte, displayName string, err error) {
	if name == "-" {
		data, err = readAllStdin(ctx, inv)
		return data, "standard input", err
	}
	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return nil, name, err
	}
	if info.IsDir() {
		return nil, name, &stdfs.PathError{Op: "open", Path: name, Err: stdfs.ErrInvalid}
	}
	data, _, err = readAllFile(ctx, inv, name)
	return data, name, err
}

func (c *checksumSum) renderDigestLine(data []byte, name string, opts checksumOptions) ([]byte, error) {
	sum, err := c.digestHex(data, opts.lengthBytes)
	if err != nil {
		return nil, err
	}
	escaped, prefix := checksumEscapeFilename(name, opts.zero)
	terminator := byte('\n')
	if opts.zero {
		terminator = 0
	}

	if opts.tag {
		line := prefix + c.tagLabel(opts.lengthBytes) + " (" + escaped + ") = " + sum
		return append([]byte(line), terminator), nil
	}

	mode := " "
	if opts.binary {
		mode = "*"
	}
	line := prefix + sum + " " + mode + escaped
	return append([]byte(line), terminator), nil
}

func (c *checksumSum) digestHex(data []byte, lengthBytes int) (string, error) {
	h, err := c.newDigest(lengthBytes)
	if err != nil {
		return "", err
	}
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *checksumSum) verifyChecksumList(ctx context.Context, inv *Invocation, opts checksumOptions, listName string, data []byte) error {
	lines := strings.Split(string(data), "\n")
	var cachedFormat *checksumLineFormat
	stats := checksumStats{}

	for i, lineText := range lines {
		lineErr := c.processChecksumLine(ctx, inv, opts, lineText, i, &cachedFormat)
		switch lineErr {
		case checksumLineSkipped:
			continue
		case checksumLineImproper:
			stats.totalConsidered++
			stats.badFormat++
			if opts.verbosity == checksumVerbosityWarn {
				_, _ = fmt.Fprintf(inv.Stderr, "%s: %s: %d: improperly formatted %s checksum line\n", c.name, listName, i+1, c.tagName)
			}
		case checksumLineFailedChecksum:
			stats.totalConsidered++
			stats.failedChecksum++
		case checksumLineFailedOpen:
			stats.totalConsidered++
			stats.failedOpen++
		case checksumLineIgnoredMissing:
			stats.totalConsidered++
		case checksumLineOK:
			stats.totalConsidered++
			stats.correct++
		}
	}

	if stats.totalConsidered-stats.badFormat == 0 {
		if opts.verbosity > checksumVerbosityStatus {
			_, _ = fmt.Fprintf(inv.Stderr, "%s: %s: no properly formatted checksum lines found\n", c.name, listName)
		}
		return &ExitError{Code: 1}
	}

	if opts.verbosity > checksumVerbosityStatus {
		c.printCheckSummary(inv, stats)
	}
	if opts.ignoreMissing && stats.correct == 0 {
		if opts.verbosity > checksumVerbosityStatus {
			_, _ = fmt.Fprintf(inv.Stderr, "%s: %s: no file was verified\n", c.name, listName)
		}
		return &ExitError{Code: 1}
	}
	if opts.strict && stats.badFormat > 0 {
		return &ExitError{Code: 1}
	}
	if stats.failedOpen > 0 && !opts.ignoreMissing {
		return &ExitError{Code: 1}
	}
	if stats.failedChecksum > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func (c *checksumSum) processChecksumLine(ctx context.Context, inv *Invocation, opts checksumOptions, lineText string, lineIndex int, cachedFormat **checksumLineFormat) checksumLineError {
	if lineText == "" || strings.HasPrefix(lineText, "#") {
		return checksumLineSkipped
	}

	line, ok := c.parseChecksumLine(lineText, cachedFormat)
	if !ok {
		return checksumLineImproper
	}

	name := line.filename
	if line.format == checksumLineFormatSingleSpace && lineIndex == 0 && strings.HasPrefix(name, "*") {
		name = name[1:]
	}
	name = checksumUnescapeFilename(name)

	data, err := c.readVerifyTarget(ctx, inv, name)
	if err != nil {
		if policy.IsDenied(err) {
			return checksumLineFailedOpen
		}
		if opts.ignoreMissing && errorsIsNotExist(err) {
			return checksumLineIgnoredMissing
		}
		c.reportOpenError(inv.Stderr, name, err)
		c.writeVerifyResult(inv, name, "FAILED open or read", opts.verbosity)
		return checksumLineFailedOpen
	}

	status := "OK"
	lineSum := strings.ToLower(line.sum)
	digest, err := c.digestHex(data, line.lengthBytes)
	if err != nil {
		c.reportOpenError(inv.Stderr, name, err)
		c.writeVerifyResult(inv, name, "FAILED open or read", opts.verbosity)
		return checksumLineFailedOpen
	}
	if digest != lineSum {
		status = "FAILED"
		c.writeVerifyResult(inv, name, status, opts.verbosity)
		return checksumLineFailedChecksum
	}
	c.writeVerifyResult(inv, name, status, opts.verbosity)
	return checksumLineOK
}

func (c *checksumSum) readVerifyTarget(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
	if name == "-" {
		return readAllStdin(ctx, inv)
	}
	info, _, err := statPath(ctx, inv, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, &stdfs.PathError{Op: "open", Path: name, Err: stdfs.ErrInvalid}
	}
	data, _, err := readAllFile(ctx, inv, name)
	return data, err
}

func (c *checksumSum) parseChecksumLine(line string, cachedFormat **checksumLineFormat) (checksumLine, bool) {
	if parsed, ok := c.parseTaggedChecksumLine(line); ok {
		return parsed, true
	}

	if *cachedFormat != nil {
		switch **cachedFormat {
		case checksumLineFormatUntagged:
			return c.parseChecksumUntaggedLine(line)
		case checksumLineFormatSingleSpace:
			return c.parseChecksumSingleSpaceLine(line)
		}
	}

	if parsed, ok := c.parseChecksumUntaggedLine(line); ok {
		mode := checksumLineFormatUntagged
		*cachedFormat = &mode
		return parsed, true
	}
	if parsed, ok := c.parseChecksumSingleSpaceLine(line); ok {
		mode := checksumLineFormatSingleSpace
		*cachedFormat = &mode
		return parsed, true
	}
	return checksumLine{}, false
}

func (c *checksumSum) parseTaggedChecksumLine(line string) (checksumLine, bool) {
	trimmed := strings.TrimPrefix(strings.TrimLeft(line, " \t"), "\\")

	var sep string
	var lengthBytes int
	switch {
	case strings.HasPrefix(trimmed, c.tagName+" ("):
		sep = ") = "
		trimmed = trimmed[len(c.tagName)+2:]
		lengthBytes = c.digestLen
	case c.supportsLength && strings.HasPrefix(trimmed, c.tagName+"-"):
		closeIdx := strings.Index(trimmed, " (")
		if closeIdx < 0 {
			return checksumLine{}, false
		}
		lengthLabel := trimmed[len(c.tagName)+1 : closeIdx]
		parsedLength, ok := parseTaggedLengthBits(lengthLabel)
		if !ok {
			return checksumLine{}, false
		}
		lengthBytes = parsedLength
		sep = ") = "
		trimmed = trimmed[closeIdx+2:]
	case strings.HasPrefix(trimmed, c.tagName+"("):
		sep = ")= "
		trimmed = trimmed[len(c.tagName)+1:]
		lengthBytes = c.digestLen
	default:
		return checksumLine{}, false
	}

	idx := strings.LastIndex(trimmed, sep)
	if idx < 0 {
		return checksumLine{}, false
	}
	filename := trimmed[:idx]
	sum := trimmed[idx+len(sep):]
	if lengthBytes == 0 {
		lengthBytes = c.digestLen
	}
	if !checksumLooksValid(sum, lengthBytes) {
		return checksumLine{}, false
	}
	return checksumLine{sum: sum, filename: filename, format: checksumLineFormatAlgo, lengthBytes: lengthBytes}, true
}

func (c *checksumSum) parseChecksumUntaggedLine(line string) (checksumLine, bool) {
	line = strings.TrimPrefix(line, "\\")
	if len(line) < 4 {
		return checksumLine{}, false
	}
	space := strings.IndexByte(line, ' ')
	if space <= 0 || space+2 > len(line) {
		return checksumLine{}, false
	}
	sum := line[:space]
	lengthBytes, ok := c.parseDigestLength(sum)
	if !ok {
		return checksumLine{}, false
	}
	rest := line[space:]
	switch {
	case strings.HasPrefix(rest, "  "):
		return checksumLine{sum: sum, filename: rest[2:], format: checksumLineFormatUntagged, lengthBytes: lengthBytes}, true
	case strings.HasPrefix(rest, " *"):
		return checksumLine{sum: sum, filename: rest[2:], format: checksumLineFormatUntagged, lengthBytes: lengthBytes}, true
	default:
		return checksumLine{}, false
	}
}

func (c *checksumSum) parseChecksumSingleSpaceLine(line string) (checksumLine, bool) {
	line = strings.TrimPrefix(line, "\\")
	space := strings.IndexByte(line, ' ')
	if space <= 0 || space+1 > len(line) {
		return checksumLine{}, false
	}
	sum := line[:space]
	lengthBytes, ok := c.parseDigestLength(sum)
	if !ok {
		return checksumLine{}, false
	}
	return checksumLine{sum: sum, filename: line[space+1:], format: checksumLineFormatSingleSpace, lengthBytes: lengthBytes}, true
}

func checksumLooksValid(sum string, digestLen int) bool {
	return checksumLooksHex(sum) && len(sum) == digestLen*2
}

func (c *checksumSum) parseDigestLength(sum string) (int, bool) {
	if !checksumLooksHex(sum) {
		return 0, false
	}
	if c.supportsLength {
		if len(sum)%2 != 0 || sum == "" || len(sum) > c.digestLen*2 {
			return 0, false
		}
		return len(sum) / 2, true
	}
	if len(sum) != c.digestLen*2 {
		return 0, false
	}
	return c.digestLen, true
}

func (c *checksumSum) tagLabel(lengthBytes int) string {
	if !c.supportsLength || lengthBytes == c.defaultLengthLen {
		return c.tagName
	}
	return fmt.Sprintf("%s-%d", c.tagName, lengthBytes*8)
}

func (c *checksumSum) newDigest(lengthBytes int) (hash.Hash, error) {
	if c.supportsLength {
		return blake2b.New(lengthBytes, nil)
	}
	return c.newHash(), nil
}

func parseTaggedLengthBits(value string) (int, bool) {
	lengthBytes, err := parseChecksumLengthBits(value)
	if err != nil {
		return 0, false
	}
	return lengthBytes, true
}

func parseChecksumLengthBits(value string) (int, error) {
	bits, err := strconv.Atoi(value)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
			return 0, fmt.Errorf("too_large")
		}
		return 0, fmt.Errorf("invalid")
	}
	switch {
	case bits == 0 || bits == 512:
		return blake2b.Size, nil
	case bits < 0:
		return 0, fmt.Errorf("invalid")
	case bits > 512:
		return 0, fmt.Errorf("too_large")
	case bits%8 != 0:
		return 0, fmt.Errorf("not_multiple_of_8")
	default:
		return bits / 8, nil
	}
}

func writeChecksumLengthError(inv *Invocation, cmdName, value string, err error) error {
	_ = exitf(inv, 1, "%s: invalid length: '%s'", cmdName, value)
	switch err.Error() {
	case "too_large":
		return exitf(inv, 1, "%s: maximum digest length for 'BLAKE2b' is 512 bits", cmdName)
	case "not_multiple_of_8":
		return exitf(inv, 1, "%s: length is not a multiple of 8", cmdName)
	default:
		return &ExitError{Code: 1}
	}
}

func checksumLooksHex(sum string) bool {
	if sum == "" {
		return false
	}
	for _, r := range sum {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func checksumEscapeFilename(name string, zero bool) (escaped, prefix string) {
	if zero {
		return name, ""
	}
	replacedBackslash := strings.ReplaceAll(name, "\\", "\\\\")
	replaced := strings.ReplaceAll(replacedBackslash, "\n", "\\n")
	if replaced != name {
		return replaced, "\\"
	}
	return name, ""
}

func checksumUnescapeFilename(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+1 < len(name) {
			switch name[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

func (c *checksumSum) writeVerifyResult(inv *Invocation, name, status string, verbosity checksumVerbosity) {
	switch status {
	case "OK":
		if verbosity <= checksumVerbosityQuiet {
			return
		}
	case "FAILED":
		if verbosity == checksumVerbosityStatus {
			return
		}
	default:
	}
	_, _ = fmt.Fprintf(inv.Stdout, "%s: %s\n", name, status)
}

func (c *checksumSum) printCheckSummary(inv *Invocation, stats checksumStats) {
	if stats.badFormat > 0 {
		unit := "lines are"
		if stats.badFormat == 1 {
			unit = "line is"
		}
		_, _ = fmt.Fprintf(inv.Stderr, "%s: WARNING: %d %s improperly formatted\n", c.name, stats.badFormat, unit)
	}
	if stats.failedChecksum > 0 {
		unit := "checksums did"
		if stats.failedChecksum == 1 {
			unit = "checksum did"
		}
		_, _ = fmt.Fprintf(inv.Stderr, "%s: WARNING: %d computed %s NOT match\n", c.name, stats.failedChecksum, unit)
	}
	if stats.failedOpen > 0 {
		unit := "files could"
		if stats.failedOpen == 1 {
			unit = "file could"
		}
		_, _ = fmt.Fprintf(inv.Stderr, "%s: WARNING: %d listed %s not be read\n", c.name, stats.failedOpen, unit)
	}
}

func (c *checksumSum) reportOpenError(w io.Writer, name string, err error) {
	message := checksumOpenErrorText(err)
	_, _ = fmt.Fprintf(w, "%s: %s: %s\n", c.name, name, message)
}

func checksumOpenErrorText(err error) string {
	switch {
	case errorsIsNotExist(err):
		return "No such file or directory"
	case errorsIsDirectory(err):
		return "Is a directory"
	default:
		return err.Error()
	}
}

func errorsIsNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || errors.Is(err, stdfs.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "no such file or directory") || strings.Contains(strings.ToLower(err.Error()), "file does not exist"))
}

func errorsIsDirectory(err error) bool {
	if err == nil {
		return false
	}
	if pe, ok := err.(*stdfs.PathError); ok && pe.Err == stdfs.ErrInvalid {
		return true
	}
	return strings.Contains(err.Error(), "is a directory")
}

var _ Command = (*checksumSum)(nil)
