package builtins

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	sha30 "crypto/sha3"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	stdfs "io/fs"
	"strconv"
	"strings"

	"github.com/ewhauser/gbash/policy"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/sha3"
)

type cksumFamily string

const (
	cksumSysv     cksumFamily = "sysv"
	cksumBsd      cksumFamily = "bsd"
	cksumCRC      cksumFamily = "crc"
	cksumCRC32B   cksumFamily = "crc32b"
	cksumMD5      cksumFamily = "md5"
	cksumSHA1     cksumFamily = "sha1"
	cksumSHA2     cksumFamily = "sha2"
	cksumSHA3     cksumFamily = "sha3"
	cksumBlake2b  cksumFamily = "blake2b"
	cksumSM3      cksumFamily = "sm3"
	cksumShake128 cksumFamily = "shake128"
	cksumShake256 cksumFamily = "shake256"
)

type cksumAlgorithm struct {
	family cksumFamily
	bits   int
}

type cksumOptions struct {
	files         []string
	check         bool
	tag           bool
	binary        bool
	text          bool
	raw           bool
	base64        bool
	zero          bool
	debug         bool
	ignoreMissing bool
	quiet         bool
	status        bool
	strict        bool
	warn          bool
	algorithm     *cksumAlgorithm
}

type cksumVerbosity int

const (
	cksumVerbosityStatus cksumVerbosity = iota
	cksumVerbosityQuiet
	cksumVerbosityNormal
	cksumVerbosityWarn
)

type cksumLineFormat int

const (
	cksumLineFormatAlgo cksumLineFormat = iota
	cksumLineFormatUntagged
	cksumLineFormatSingleSpace
)

type cksumLine struct {
	algo     *cksumAlgorithm
	sum      []byte
	filename string
	prefix   string
	format   cksumLineFormat
}

type cksumCheckStats struct {
	correct         int
	failedChecksum  int
	failedOpen      int
	badFormat       int
	unsupportedAlgo int
	totalConsidered int
}

type cksumLineResult int

const (
	cksumLineSkipped cksumLineResult = iota
	cksumLineImproper
	cksumLineFailedChecksum
	cksumLineFailedOpen
	cksumLineIgnoredMissing
	cksumLineUnsupportedAlgorithm
	cksumLineOK
)

type Cksum struct{}

func NewCksum() *Cksum {
	return &Cksum{}
}

func (c *Cksum) Name() string {
	return "cksum"
}

func (c *Cksum) Run(ctx context.Context, inv *Invocation) error {
	return RunCommand(ctx, c, inv)
}

func (c *Cksum) Spec() CommandSpec {
	return CommandSpec{
		Name:  c.Name(),
		About: "cksum - compute and check checksums",
		Usage: "cksum [OPTION]... [FILE]...",
		Options: []OptionSpec{
			{Name: "algorithm", Short: 'a', Long: "algorithm", ValueName: "TYPE", Arity: OptionRequiredValue, Help: "select the digest type to compute"},
			{Name: "untagged", Long: "untagged", Help: "create an untagged checksum, reversing the default"},
			{Name: "tag", Long: "tag", Help: "create a BSD-style checksum"},
			{Name: "length", Short: 'l', Long: "length", ValueName: "BITS", Arity: OptionRequiredValue, Help: "digest length in bits"},
			{Name: "raw", Long: "raw", Help: "emit a raw binary digest, not an ASCII-armored one"},
			{Name: "check", Short: 'c', Long: "check", Help: "read checksums from FILEs and check them"},
			{Name: "base64", Long: "base64", Help: "emit base64, not hexadecimal, checksums"},
			{Name: "text", Short: 't', Long: "text", Help: "read in text mode"},
			{Name: "binary", Short: 'b', Long: "binary", Help: "read in binary mode"},
			{Name: "zero", Short: 'z', Long: "zero", Help: "end each output line with NUL, not newline"},
			{Name: "warn", Short: 'w', Long: "warn", Help: "warn about improperly formatted checksum lines"},
			{Name: "status", Long: "status", Help: "don't output anything, status code shows success"},
			{Name: "quiet", Long: "quiet", Help: "don't print OK for each successfully verified file"},
			{Name: "strict", Long: "strict", Help: "exit non-zero for improperly formatted checksum lines"},
			{Name: "ignore-missing", Long: "ignore-missing", Help: "don't fail or report status for missing files"},
			{Name: "debug", Long: "debug", Help: "print CPU hardware capability detection information"},
		},
		Args: []ArgSpec{
			{Name: "file", ValueName: "FILE", Repeatable: true},
		},
		Parse: ParseConfig{
			GroupShortOptions:        true,
			ShortOptionValueAttached: true,
			LongOptionValueEquals:    true,
			InferLongOptions:         true,
			AutoHelp:                 true,
			AutoVersion:              true,
		},
	}
}

func (c *Cksum) RunParsed(ctx context.Context, inv *Invocation, matches *ParsedCommand) error {
	opts, err := c.optionsFromMatches(inv, matches)
	if err != nil {
		return err
	}
	if len(opts.files) == 0 {
		opts.files = []string{"-"}
	}
	if opts.debug {
		c.printDebug(inv.Stderr)
	}
	if opts.check {
		return c.runCheckMode(ctx, inv, opts)
	}
	return c.runDigestMode(ctx, inv, opts)
}

func (c *Cksum) optionsFromMatches(inv *Invocation, matches *ParsedCommand) (cksumOptions, error) {
	opts := cksumOptions{tag: true}
	var algo *cksumAlgorithm
	var lengthValue string
	var tagExplicit bool
	var untaggedExplicit bool
	var binaryExplicit bool
	var textExplicit bool
	var checkOnlyFlag string
	verbosity := cksumVerbosityNormal

	for _, name := range matches.OptionOrder() {
		switch name {
		case "algorithm":
			parsed, err := parseCksumAlgorithm(matches.Value("algorithm"))
			if err != nil {
				return cksumOptions{}, exitf(inv, 1, "cksum: unknown algorithm: %s", matches.Value("algorithm"))
			}
			algo = &parsed
		case "untagged":
			opts.tag = false
			untaggedExplicit = true
		case "tag":
			opts.tag = true
			tagExplicit = true
		case "length":
			lengthValue = matches.Value("length")
		case "raw":
			opts.raw = true
		case "check":
			opts.check = true
		case "base64":
			opts.base64 = true
		case "text":
			opts.text = true
			opts.binary = false
			textExplicit = true
		case "binary":
			opts.binary = true
			opts.text = false
			binaryExplicit = true
		case "zero":
			opts.zero = true
		case "warn":
			opts.warn = true
			opts.status = false
			opts.quiet = false
			verbosity = cksumVerbosityWarn
			checkOnlyFlag = "warn"
		case "status":
			opts.status = true
			opts.warn = false
			opts.quiet = false
			verbosity = cksumVerbosityStatus
			checkOnlyFlag = "status"
		case "quiet":
			opts.quiet = true
			opts.warn = false
			opts.status = false
			verbosity = cksumVerbosityQuiet
			checkOnlyFlag = "quiet"
		case "strict":
			opts.strict = true
			checkOnlyFlag = "strict"
		case "ignore-missing":
			opts.ignoreMissing = true
			checkOnlyFlag = "ignore-missing"
		case "debug":
			opts.debug = true
		}
	}

	if algo == nil && !opts.check {
		defaultAlgo := cksumAlgorithm{family: cksumCRC}
		algo = &defaultAlgo
	}

	if algo != nil {
		if !algo.isSupported() {
			return cksumOptions{}, exitf(inv, 1, "cksum: %s is not supported", algo.tagLabel())
		}
		finalAlgo, err := sanitizeCksumLength(inv, *algo, lengthValue, opts.check)
		if err != nil {
			return cksumOptions{}, err
		}
		opts.algorithm = &finalAlgo
	}
	opts.files = matches.Args("file")

	if opts.text && opts.tag {
		return cksumOptions{}, exitf(inv, 1, "cksum: --text mode is only supported with --untagged")
	}
	if !opts.check && checkOnlyFlag != "" {
		return cksumOptions{}, exitf(inv, 1, "cksum: the --%s option is meaningful only when verifying checksums", checkOnlyFlag)
	}
	if opts.check && opts.algorithm != nil && opts.algorithm.isLegacy() {
		return cksumOptions{}, exitf(inv, 1, "cksum: --check is not supported with --algorithm={bsd,sysv,crc,crc32b}")
	}
	if opts.check && tagExplicit {
		return cksumOptions{}, exitf(inv, 1, "cksum: the --tag option is meaningless when verifying checksums")
	}
	if opts.check && (binaryExplicit || textExplicit) {
		return cksumOptions{}, exitf(inv, 1, "cksum: the --binary and --text options are meaningless when verifying checksums")
	}
	if opts.raw && len(opts.files) > 1 {
		return cksumOptions{}, exitf(inv, 1, "cksum: the --raw option is not supported with multiple files")
	}
	if opts.check && untaggedExplicit && opts.tag {
		opts.tag = false
	}
	_ = verbosity
	return opts, nil
}

func sanitizeCksumLength(inv *Invocation, algo cksumAlgorithm, value string, allowImplicitLength bool) (cksumAlgorithm, error) {
	if value == "" {
		switch algo.family {
		case cksumSHA2, cksumSHA3:
			if allowImplicitLength {
				return algo, nil
			}
			if algo.bits == 0 {
				return cksumAlgorithm{}, exitf(inv, 1, "cksum: --algorithm=%s requires specifying --length 224, 256, 384, or 512", algo.family)
			}
			return algo, nil
		default:
			return algo, nil
		}
	}

	switch algo.family {
	case cksumSHA2, cksumSHA3:
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			_ = exitf(inv, 1, "cksum: invalid length: '%s'", value)
			return cksumAlgorithm{}, exitf(inv, 1, "cksum: digest length for '%s' must be 224, 256, 384, or 512", strings.ToUpper(string(algo.family)))
		}
		switch n {
		case 224, 256, 384, 512:
			algo.bits = int(n)
			return algo, nil
		default:
			if _, err := fmt.Fprintf(inv.Stderr, "cksum: invalid length: '%s'\n", value); err != nil {
				return cksumAlgorithm{}, &ExitError{Code: 1, Err: err}
			}
			return cksumAlgorithm{}, exitf(inv, 1, "cksum: digest length for '%s' must be 224, 256, 384, or 512", strings.ToUpper(string(algo.family)))
		}
	case cksumBlake2b:
		lengthBytes, err := parseChecksumLengthBits(value)
		if err != nil {
			_ = exitf(inv, 1, "cksum: invalid length: '%s'", value)
			switch err.Error() {
			case "too_large":
				return cksumAlgorithm{}, exitf(inv, 1, "cksum: maximum digest length for 'BLAKE2b' is 512 bits")
			case "not_multiple_of_8":
				return cksumAlgorithm{}, exitf(inv, 1, "cksum: length is not a multiple of 8")
			default:
				return cksumAlgorithm{}, &ExitError{Code: 1}
			}
		}
		switch bits := lengthBytes * 8; bits {
		case 512:
			algo.bits = 0
			return algo, nil
		default:
			algo.bits = bits
			return algo, nil
		}
	case cksumShake128, cksumShake256:
		n, err := strconv.Atoi(value)
		if err != nil {
			return cksumAlgorithm{}, exitf(inv, 1, "cksum: invalid length: '%s'", value)
		}
		if n == 0 {
			algo.bits = 0
			return algo, nil
		}
		algo.bits = n
		return algo, nil
	default:
		if value == "0" {
			return algo, nil
		}
		return cksumAlgorithm{}, exitf(inv, 1, "cksum: --length is only supported with --algorithm blake2b, sha2, or sha3")
	}
}

func parseCksumAlgorithm(value string) (cksumAlgorithm, error) {
	switch strings.ToLower(value) {
	case "sysv":
		return cksumAlgorithm{family: cksumSysv}, nil
	case "bsd":
		return cksumAlgorithm{family: cksumBsd}, nil
	case "crc":
		return cksumAlgorithm{family: cksumCRC}, nil
	case "crc32b":
		return cksumAlgorithm{family: cksumCRC32B}, nil
	case "md5":
		return cksumAlgorithm{family: cksumMD5}, nil
	case "sha1":
		return cksumAlgorithm{family: cksumSHA1}, nil
	case "sha2":
		return cksumAlgorithm{family: cksumSHA2}, nil
	case "sha3":
		return cksumAlgorithm{family: cksumSHA3}, nil
	case "blake2b":
		return cksumAlgorithm{family: cksumBlake2b}, nil
	case "sm3":
		return cksumAlgorithm{family: cksumSM3}, nil
	case "sha224":
		return cksumAlgorithm{family: cksumSHA2, bits: 224}, nil
	case "sha256":
		return cksumAlgorithm{family: cksumSHA2, bits: 256}, nil
	case "sha384":
		return cksumAlgorithm{family: cksumSHA2, bits: 384}, nil
	case "sha512":
		return cksumAlgorithm{family: cksumSHA2, bits: 512}, nil
	case "shake128":
		return cksumAlgorithm{family: cksumShake128}, nil
	case "shake256":
		return cksumAlgorithm{family: cksumShake256}, nil
	default:
		return cksumAlgorithm{}, fmt.Errorf("unknown algorithm")
	}
}

func (a cksumAlgorithm) isLegacy() bool {
	return a.family == cksumSysv || a.family == cksumBsd || a.family == cksumCRC || a.family == cksumCRC32B
}

func (a cksumAlgorithm) isSupported() bool {
	return a.family != cksumSM3
}

func (a cksumAlgorithm) tagLabel() string {
	switch a.family {
	case cksumMD5:
		return "MD5"
	case cksumSHA1:
		return "SHA1"
	case cksumSHA2:
		return fmt.Sprintf("SHA%d", a.bits)
	case cksumSHA3:
		return fmt.Sprintf("SHA3-%d", a.bits)
	case cksumBlake2b:
		if a.bits == 0 {
			return "BLAKE2b"
		}
		return fmt.Sprintf("BLAKE2b-%d", a.bits)
	case cksumSM3:
		return "SM3"
	case cksumShake128:
		return "SHAKE128"
	case cksumShake256:
		return "SHAKE256"
	default:
		return strings.ToUpper(string(a.family))
	}
}

func (a cksumAlgorithm) diagnosticLabel() string {
	switch a.family {
	case cksumSHA2:
		if a.bits == 0 {
			return "SHA2"
		}
	case cksumSHA3:
		if a.bits == 0 {
			return "SHA3"
		}
	}
	return a.tagLabel()
}

func (a cksumAlgorithm) defaultBits() int {
	switch a.family {
	case cksumMD5:
		return 128
	case cksumSHA1:
		return 160
	case cksumSHA2, cksumSHA3:
		return a.bits
	case cksumBlake2b:
		if a.bits != 0 {
			return a.bits
		}
		return 512
	case cksumSM3:
		return 256
	case cksumShake128, cksumShake256:
		if a.bits != 0 {
			return a.bits
		}
		return 256
	default:
		return 0
	}
}

func (c *Cksum) runDigestMode(ctx context.Context, inv *Invocation, opts cksumOptions) error {
	exitCode := 0
	for _, name := range opts.files {
		data, err := c.readDigestInput(ctx, inv, name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %s\n", name, checksumOpenErrorText(err))
			exitCode = 1
			continue
		}
		line, err := renderCksumDigest(name, data, opts)
		if err != nil {
			return err
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

func (c *Cksum) runCheckMode(ctx context.Context, inv *Invocation, opts cksumOptions) error {
	failed := false
	verbosity := cksumVerbosityNormal
	switch {
	case opts.status:
		verbosity = cksumVerbosityStatus
	case opts.quiet:
		verbosity = cksumVerbosityQuiet
	case opts.warn:
		verbosity = cksumVerbosityWarn
	}

	for _, name := range opts.files {
		data, displayName, err := c.readChecksumList(ctx, inv, name)
		if err != nil {
			if policy.IsDenied(err) {
				return err
			}
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %s\n", displayName, checksumOpenErrorText(err))
			failed = true
			continue
		}
		if err := c.verifyChecksumList(ctx, inv, opts, verbosity, displayName, data); err != nil {
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

func (c *Cksum) readDigestInput(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
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

func (c *Cksum) readChecksumList(ctx context.Context, inv *Invocation, name string) (data []byte, displayName string, err error) {
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

func renderCksumDigest(name string, data []byte, opts cksumOptions) ([]byte, error) {
	raw, size, err := computeCksumDigest(*opts.algorithm, data)
	if err != nil {
		return nil, err
	}
	if opts.raw {
		return raw, nil
	}

	escaped, prefix := checksumEscapeFilename(name, opts.zero)
	term := byte('\n')
	if opts.zero {
		term = 0
	}

	if opts.algorithm.isLegacy() {
		line := prefix + renderLegacyDigest(*opts.algorithm, raw, size, escaped)
		return append([]byte(line), term), nil
	}

	encoded := hex.EncodeToString(raw)
	if opts.base64 {
		encoded = base64.StdEncoding.EncodeToString(raw)
	}
	if opts.tag {
		return append([]byte(prefix+opts.algorithm.tagLabel()+" ("+escaped+") = "+encoded), term), nil
	}
	mode := " "
	if opts.binary {
		mode = "*"
	}
	return append([]byte(prefix+encoded+" "+mode+escaped), term), nil
}

func renderLegacyDigest(algo cksumAlgorithm, raw []byte, size int, filename string) string {
	switch algo.family {
	case cksumSysv:
		sum := binary.BigEndian.Uint16(raw)
		line := fmt.Sprintf("%d %d", sum, (size+511)/512)
		if filename != "-" {
			line += " " + filename
		}
		return line
	case cksumBsd:
		sum := binary.BigEndian.Uint16(raw)
		line := fmt.Sprintf("%05d %5d", sum, (size+1023)/1024)
		if filename != "-" {
			line += " " + filename
		}
		return line
	default:
		sum := binary.BigEndian.Uint32(raw)
		line := fmt.Sprintf("%d %d", sum, size)
		if filename != "-" {
			line += " " + filename
		}
		return line
	}
}

func computeCksumDigest(algo cksumAlgorithm, data []byte) (digest []byte, size int, err error) {
	switch algo.family {
	case cksumSysv:
		var sum uint32
		for _, b := range data {
			sum += uint32(b)
		}
		sum = (sum & 0xffff) + (sum >> 16)
		sum = (sum & 0xffff) + (sum >> 16)
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, uint16(sum))
		return out, len(data), nil
	case cksumBsd:
		var sum uint16
		for _, b := range data {
			sum = (sum >> 1) + ((sum & 1) << 15)
			sum += uint16(b)
		}
		out := make([]byte, 2)
		binary.BigEndian.PutUint16(out, sum)
		return out, len(data), nil
	case cksumCRC:
		crc := posixCRC(data)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, crc)
		return out, len(data), nil
	case cksumCRC32B:
		sum := crc32.ChecksumIEEE(data)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, sum)
		return out, len(data), nil
	case cksumMD5:
		sum := md5.Sum(data)
		return sum[:], len(data), nil
	case cksumSHA1:
		sum := sha1.Sum(data)
		return sum[:], len(data), nil
	case cksumSHA2:
		switch algo.bits {
		case 224:
			sum := sha256.Sum224(data)
			return sum[:], len(data), nil
		case 256:
			sum := sha256.Sum256(data)
			return sum[:], len(data), nil
		case 384:
			sum := sha512.Sum384(data)
			return sum[:], len(data), nil
		case 512:
			sum := sha512.Sum512(data)
			return sum[:], len(data), nil
		}
	case cksumSHA3:
		switch algo.bits {
		case 224:
			sum := sha30.Sum224(data)
			return sum[:], len(data), nil
		case 256:
			sum := sha30.Sum256(data)
			return sum[:], len(data), nil
		case 384:
			sum := sha30.Sum384(data)
			return sum[:], len(data), nil
		case 512:
			sum := sha30.Sum512(data)
			return sum[:], len(data), nil
		}
	case cksumBlake2b:
		size := blake2b.Size
		if algo.bits != 0 {
			size = algo.bits / 8
		}
		h, err := blake2b.New(size, nil)
		if err != nil {
			return nil, 0, err
		}
		_, _ = h.Write(data)
		return h.Sum(nil), len(data), nil
	case cksumShake128:
		outBits := algo.bits
		if outBits == 0 {
			outBits = 256
		}
		out := make([]byte, (outBits+7)/8)
		h := sha3.NewShake128()
		_, _ = h.Write(data)
		_, _ = h.Read(out)
		maskTrailingBits(out, outBits)
		return out, len(data), nil
	case cksumShake256:
		outBits := algo.bits
		if outBits == 0 {
			outBits = 256
		}
		out := make([]byte, (outBits+7)/8)
		h := sha3.NewShake256()
		_, _ = h.Write(data)
		_, _ = h.Read(out)
		maskTrailingBits(out, outBits)
		return out, len(data), nil
	}
	return nil, 0, fmt.Errorf("unsupported cksum algorithm")
}

func maskTrailingBits(buf []byte, bitLen int) {
	if bitLen == 0 || len(buf) == 0 || bitLen%8 == 0 {
		return
	}
	extra := bitLen % 8
	buf[len(buf)-1] &= byte((1 << extra) - 1)
}

func posixCRC(data []byte) uint32 {
	crc := uint32(0)
	for _, b := range data {
		crc = posixCRCByte(crc, b)
	}
	for n := len(data); n > 0; n >>= 8 {
		crc = posixCRCByte(crc, byte(n))
	}
	return ^crc
}

func posixCRCByte(crc uint32, b byte) uint32 {
	crc ^= uint32(b) << 24
	for range 8 {
		if crc&0x80000000 != 0 {
			crc = (crc << 1) ^ 0x04c11db7
		} else {
			crc <<= 1
		}
	}
	return crc
}

func (c *Cksum) verifyChecksumList(ctx context.Context, inv *Invocation, opts cksumOptions, verbosity cksumVerbosity, listName string, data []byte) error {
	lines := strings.Split(string(data), "\n")
	var cached *cksumLineFormat
	stats := cksumCheckStats{}

	for i, lineText := range lines {
		lineText = strings.TrimSuffix(lineText, "\r")
		result := c.processChecksumLine(ctx, inv, opts, verbosity, lineText, i, &cached)
		switch result {
		case cksumLineSkipped:
			continue
		case cksumLineImproper:
			stats.totalConsidered++
			stats.badFormat++
			if verbosity == cksumVerbosityWarn {
				if opts.algorithm != nil && !opts.algorithm.isLegacy() {
					_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %d: improperly formatted %s checksum line\n", listName, i+1, opts.algorithm.diagnosticLabel())
				} else {
					_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %d: improperly formatted checksum line\n", listName, i+1)
				}
			}
		case cksumLineFailedChecksum:
			stats.totalConsidered++
			stats.failedChecksum++
		case cksumLineFailedOpen:
			stats.totalConsidered++
			stats.failedOpen++
		case cksumLineIgnoredMissing:
			stats.totalConsidered++
		case cksumLineUnsupportedAlgorithm:
			stats.totalConsidered++
			stats.unsupportedAlgo++
		case cksumLineOK:
			stats.totalConsidered++
			stats.correct++
		}
	}

	if stats.totalConsidered-stats.badFormat == 0 {
		if verbosity > cksumVerbosityStatus {
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: no properly formatted checksum lines found\n", listName)
		}
		return &ExitError{Code: 1}
	}
	if verbosity > cksumVerbosityStatus {
		if stats.badFormat > 0 {
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: WARNING: %d line%s improperly formatted\n", stats.badFormat, pluralSuffix(stats.badFormat))
		}
		if stats.failedChecksum > 0 {
			unit := "checksums did"
			if stats.failedChecksum == 1 {
				unit = "checksum did"
			}
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: WARNING: %d computed %s NOT match\n", stats.failedChecksum, unit)
		}
		if stats.failedOpen > 0 && !opts.ignoreMissing {
			unit := "listed files could not be read"
			if stats.failedOpen == 1 {
				unit = "listed file could not be read"
			}
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: WARNING: %d %s\n", stats.failedOpen, unit)
		}
		if stats.unsupportedAlgo > 0 {
			unit := "checksum line uses"
			if stats.unsupportedAlgo != 1 {
				unit = "checksum lines use"
			}
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: WARNING: %d %s unsupported algorithms\n", stats.unsupportedAlgo, unit)
		}
	}
	if opts.ignoreMissing && stats.correct == 0 {
		if verbosity > cksumVerbosityStatus {
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: no file was verified\n", listName)
		}
		return &ExitError{Code: 1}
	}
	if opts.strict && stats.badFormat > 0 {
		return &ExitError{Code: 1}
	}
	if stats.failedChecksum > 0 {
		return &ExitError{Code: 1}
	}
	if stats.failedOpen > 0 && !opts.ignoreMissing {
		return &ExitError{Code: 1}
	}
	if stats.unsupportedAlgo > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func pluralSuffix(n int) string {
	if n == 1 {
		return " is"
	}
	return "s are"
}

func (c *Cksum) processChecksumLine(ctx context.Context, inv *Invocation, opts cksumOptions, verbosity cksumVerbosity, lineText string, lineIndex int, cached **cksumLineFormat) cksumLineResult {
	if lineText == "" || strings.HasPrefix(lineText, "#") {
		return cksumLineSkipped
	}

	line, ok := parseCksumLine(lineText, cached, opts.algorithm)
	if !ok {
		return cksumLineImproper
	}

	name := checksumUnescapeFilename(line.filename)
	if !line.algo.isSupported() {
		_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %s is not supported\n", name, line.algo.tagLabel())
		return cksumLineUnsupportedAlgorithm
	}
	data, err := c.readVerifyTarget(ctx, inv, name)
	if err != nil {
		if policy.IsDenied(err) {
			return cksumLineFailedOpen
		}
		if !opts.ignoreMissing || !errorsIsNotExist(err) {
			_, _ = fmt.Fprintf(inv.Stderr, "cksum: %s: %s\n", name, checksumOpenErrorText(err))
			c.writeVerifyResult(inv, name, "FAILED open or read", true, verbosity)
		}
		if opts.ignoreMissing && errorsIsNotExist(err) {
			return cksumLineIgnoredMissing
		}
		return cksumLineFailedOpen
	}

	got, _, err := computeCksumDigest(*line.algo, data)
	if err != nil {
		return cksumLineFailedOpen
	}
	ok = bytes.Equal(got, line.sum)
	c.writeVerifyResult(inv, name, ternary(ok, "OK", "FAILED"), !ok, verbosity)
	if ok {
		return cksumLineOK
	}
	_ = lineIndex
	return cksumLineFailedChecksum
}

func ternary[T any](cond bool, yes, no T) T {
	if cond {
		return yes
	}
	return no
}

func (c *Cksum) readVerifyTarget(ctx context.Context, inv *Invocation, name string) ([]byte, error) {
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

func (c *Cksum) writeVerifyResult(inv *Invocation, name, status string, failed bool, verbosity cksumVerbosity) {
	if !failed {
		if verbosity <= cksumVerbosityQuiet {
			return
		}
	} else if verbosity == cksumVerbosityStatus {
		return
	}
	escaped, prefix := checksumEscapeFilename(name, false)
	_, _ = io.WriteString(inv.Stdout, prefix+escaped+": "+status+"\n")
}

func parseCksumLine(lineText string, cached **cksumLineFormat, cliAlgo *cksumAlgorithm) (*cksumLine, bool) {
	if line, ok := parseCksumAlgoLine(lineText, cliAlgo); ok {
		return line, true
	}
	if *cached != nil {
		switch **cached {
		case cksumLineFormatUntagged:
			return parseCksumUntaggedLine(lineText, cliAlgo)
		case cksumLineFormatSingleSpace:
			return parseCksumSingleSpaceLine(lineText, cliAlgo)
		}
	}
	if line, ok := parseCksumUntaggedLine(lineText, cliAlgo); ok {
		format := cksumLineFormatUntagged
		*cached = &format
		return line, true
	}
	if line, ok := parseCksumSingleSpaceLine(lineText, cliAlgo); ok {
		format := cksumLineFormatSingleSpace
		*cached = &format
		return line, true
	}
	return nil, false
}

func parseCksumAlgoLine(line string, cliAlgo *cksumAlgorithm) (*cksumLine, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	prefix := ""
	if strings.HasPrefix(trimmed, "\\") {
		prefix = "\\"
		trimmed = trimmed[1:]
	}
	open := strings.IndexByte(trimmed, '(')
	if open <= 0 {
		return nil, false
	}
	algoText := trimmed[:open]
	subcaseOpenSSL := !strings.HasSuffix(algoText, " ")
	if !subcaseOpenSSL {
		algoText = strings.TrimSuffix(algoText, " ")
	}
	var sep string
	if subcaseOpenSSL {
		sep = ")= "
	} else {
		sep = ") = "
	}
	rest := trimmed[open+1:]
	split := strings.LastIndex(rest, sep)
	if split < 0 {
		return nil, false
	}
	filename := rest[:split]
	sumText := rest[split+len(sep):]
	algo, ok := parseTaggedAlgorithm(algoText, cliAlgo)
	if !ok {
		return nil, false
	}
	if algo.bits == 0 {
		if inferred := inferCLIAlgorithm(algo, sumText); inferred != nil {
			algo = *inferred
		}
	}
	sum, ok := decodeExpectedDigest(sumText, algo.defaultBits())
	if !ok {
		return nil, false
	}
	return &cksumLine{algo: &algo, sum: sum, filename: filename, prefix: prefix, format: cksumLineFormatAlgo}, true
}

func parseTaggedAlgorithm(text string, cliAlgo *cksumAlgorithm) (cksumAlgorithm, bool) {
	text = strings.TrimSpace(text)
	parts := strings.SplitN(text, "-", 2)
	base := parts[0]
	bits := 0
	if len(parts) == 2 {
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return cksumAlgorithm{}, false
		}
		bits = n
	}

	var algo cksumAlgorithm
	switch base {
	case "MD5":
		algo = cksumAlgorithm{family: cksumMD5}
	case "SHA1":
		algo = cksumAlgorithm{family: cksumSHA1}
	case "SHA224":
		algo = cksumAlgorithm{family: cksumSHA2, bits: 224}
	case "SHA256":
		algo = cksumAlgorithm{family: cksumSHA2, bits: 256}
	case "SHA384":
		algo = cksumAlgorithm{family: cksumSHA2, bits: 384}
	case "SHA512":
		algo = cksumAlgorithm{family: cksumSHA2, bits: 512}
	case "SHA2":
		algo = cksumAlgorithm{family: cksumSHA2, bits: bits}
	case "SHA3":
		algo = cksumAlgorithm{family: cksumSHA3, bits: bits}
	case "BLAKE2b":
		algo = cksumAlgorithm{family: cksumBlake2b, bits: bits}
	case "SM3":
		algo = cksumAlgorithm{family: cksumSM3}
	case "SHAKE128":
		algo = cksumAlgorithm{family: cksumShake128, bits: bits}
	case "SHAKE256":
		algo = cksumAlgorithm{family: cksumShake256, bits: bits}
	default:
		return cksumAlgorithm{}, false
	}
	if (algo.family == cksumSHA2 || algo.family == cksumSHA3) && algo.bits != 0 {
		switch algo.bits {
		case 224, 256, 384, 512:
		default:
			return cksumAlgorithm{}, false
		}
	}
	if cliAlgo == nil {
		return algo, true
	}
	if cliAlgo.family == cksumSHA2 && algo.family == cksumSHA2 {
		return algo, algo.bits != 0
	}
	if cliAlgo.family == algo.family {
		return algo, true
	}
	return cksumAlgorithm{}, false
}

func parseCksumUntaggedLine(line string, cliAlgo *cksumAlgorithm) (*cksumLine, bool) {
	space := strings.IndexByte(line, ' ')
	if space <= 0 {
		return nil, false
	}
	sumText := line[:space]
	rest := line[space:]
	if !strings.HasPrefix(rest, "  ") && !strings.HasPrefix(rest, " *") {
		return nil, false
	}
	if cliAlgo == nil {
		return nil, false
	}
	algo := inferCLIAlgorithm(*cliAlgo, sumText)
	if algo == nil {
		return nil, false
	}
	sum, ok := decodeExpectedDigest(sumText, algo.defaultBits())
	if !ok {
		return nil, false
	}
	return &cksumLine{algo: algo, sum: sum, filename: rest[2:], format: cksumLineFormatUntagged}, true
}

func parseCksumSingleSpaceLine(line string, cliAlgo *cksumAlgorithm) (*cksumLine, bool) {
	space := strings.IndexByte(line, ' ')
	if space <= 0 || cliAlgo == nil {
		return nil, false
	}
	algo := inferCLIAlgorithm(*cliAlgo, line[:space])
	if algo == nil {
		return nil, false
	}
	sum, ok := decodeExpectedDigest(line[:space], algo.defaultBits())
	if !ok {
		return nil, false
	}
	return &cksumLine{algo: algo, sum: sum, filename: line[space+1:], format: cksumLineFormatSingleSpace}, true
}

func inferCLIAlgorithm(cli cksumAlgorithm, digestText string) *cksumAlgorithm {
	if cli.family == cksumSHA2 || cli.family == cksumSHA3 {
		if cli.bits != 0 {
			return &cli
		}
		digestLen, ok := inferDigestBitLength(digestText)
		if !ok || (digestLen != 224 && digestLen != 256 && digestLen != 384 && digestLen != 512) {
			return nil
		}
		cli.bits = digestLen
		return &cli
	}
	if cli.family == cksumBlake2b && cli.bits == 0 {
		if digestLen, ok := inferDigestBitLength(digestText); ok {
			if digestLen > 0 && digestLen <= 512 && digestLen%8 == 0 {
				cli.bits = digestLen
			}
		}
	}
	return &cli
}

func inferDigestBitLength(text string) (int, bool) {
	if raw, err := hex.DecodeString(text); err == nil {
		return len(raw) * 8, true
	}
	if raw, err := base64.StdEncoding.DecodeString(text); err == nil {
		return len(raw) * 8, true
	}
	return 0, false
}

func decodeExpectedDigest(text string, bitHint int) ([]byte, bool) {
	if text == "" {
		return nil, false
	}
	if raw, err := hex.DecodeString(text); err == nil {
		if bitHint == 0 || len(raw)*8 == bitHint || (bitHint%8 != 0 && len(raw)*8 >= bitHint) {
			return raw, true
		}
	}
	if raw, err := base64.StdEncoding.DecodeString(text); err == nil {
		if bitHint == 0 || len(raw)*8 == bitHint || (bitHint%8 != 0 && len(raw)*8 >= bitHint) {
			return raw, true
		}
	}
	return nil, false
}

func (c *Cksum) printDebug(w io.Writer) {
	_, _ = io.WriteString(w, "avx512 support not detected\n")
	_, _ = io.WriteString(w, "avx2 support not detected\n")
	_, _ = io.WriteString(w, "pclmul support not detected\n")
}

var _ Command = (*Cksum)(nil)
