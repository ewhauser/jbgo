// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"os"
	"path"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/shell/pattern"
	"github.com/ewhauser/gbash/internal/shell/syntax"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// A Config specifies details about how shell expansion should be performed. The
// zero value is a valid configuration.
type Config struct {
	// Env is used to get and set environment variables when performing
	// shell expansions. Some special parameters are also expanded via this
	// interface, such as:
	//
	//   * "#", "@", "*", "0"-"9" for the shell's parameters
	//   * "?", "$", "PPID" for the shell's status and process
	//   * "HOME foo" to retrieve user foo's home directory
	//
	// If nil, there are no environment variables set. Use
	// ListEnviron(os.Environ()...) to use the system's environment
	// variables.
	Env Environ

	// StartupHome, when set, overrides the home directory used for plain
	// current-user tilde expansion. Callers must only set this from a trusted
	// sandbox boundary.
	StartupHome string

	// TildeEnv is used for ~ and ~user lookup. If nil, Env is used.
	TildeEnv Environ
	// CmdSubst expands a command substitution node, writing its standard
	// output to the provided [io.Writer].
	//
	// If nil, encountering a command substitution will result in an
	// UnexpectedCommandError.
	CmdSubst func(io.Writer, *syntax.CmdSubst) error

	// ProcSubst expands a process substitution node.
	ProcSubst func(*syntax.ProcSubst) (string, error)

	// ReportError is used for non-fatal expansion diagnostics where bash emits
	// stderr but still yields an empty string or zero value.
	ReportError func(error)

	// ReadDir is used for file path globbing.
	// If nil, globbing is disabled.
	ReadDir func(string) ([]fs.DirEntry, error)

	// GlobStar corresponds to the shell option which allows globbing with "**".
	GlobStar bool

	// DotGlob corresponds to the shell option which allows filenames beginning
	// with a dot to be matched by a pattern which does not begin with a dot.
	DotGlob bool

	// NoCaseGlob corresponds to the shell option which causes case-insensitive
	// pattern matching in pathname expansion.
	NoCaseGlob bool

	// NullGlob corresponds to the shell option which allows globbing
	// patterns which match nothing to result in zero fields.
	NullGlob bool

	// FailGlob corresponds to the shell option which treats pathname
	// expansion patterns which match nothing as errors.
	FailGlob bool

	// GlobSkipDots corresponds to the shell option which prevents pathname
	// expansion from matching "." and ".." unless disabled.
	GlobSkipDots bool

	// NoUnset corresponds to the shell option which treats unset variables
	// as errors.
	NoUnset bool

	// ExtGlob corresponds to the shell option which allows using extended
	// pattern matching features when performing pathname expansion (globbing).
	ExtGlob bool

	globIgnore         []string
	globIgnoreMatchers []func(string) bool

	bufferAlloc strings.Builder
	fieldAlloc  [4]fieldPart
	fieldsAlloc [4][]fieldPart

	ifs string
	// A pointer to a parameter expansion node, if we're inside one.
	// Necessary for ${LINENO}.
	curParam *syntax.ParamExp

	// CurrentLine overrides the line number reported by special parameters such
	// as LINENO when expansion should be anchored to the containing statement
	// rather than the current token.
	CurrentLine func() uint

	reportedParamErrors map[*syntax.ParamExp]map[string]struct{}
}

// UnexpectedCommandError is returned if a command substitution is encountered
// when [Config.CmdSubst] is nil.
type UnexpectedCommandError struct {
	Node *syntax.CmdSubst
}

func (u UnexpectedCommandError) Error() string {
	return fmt.Sprintf("unexpected command substitution at %s", u.Node.Pos())
}

type FailGlobError struct {
	Pattern string
}

func (e FailGlobError) Error() string {
	return fmt.Sprintf("no match: %s", e.Pattern)
}

var zeroConfig = &Config{}

// TODO: note that prepareConfig is modifying the user's config in place,
// which doesn't feel right - we should make a copy.

func prepareConfig(cfg *Config) *Config {
	cfg = cmp.Or(cfg, zeroConfig)
	cfg.Env = cmp.Or(cfg.Env, FuncEnviron(func(string) string { return "" }))
	cfg.TildeEnv = cmp.Or(cfg.TildeEnv, cfg.Env)

	cfg.ifs = " \t\n"
	if vr := cfg.Env.Get("IFS"); vr.IsSet() {
		cfg.ifs = vr.String()
	}
	if cfg.reportedParamErrors == nil {
		cfg.reportedParamErrors = make(map[*syntax.ParamExp]map[string]struct{})
	}
	cfg.prepareGlobIgnore()

	return cfg
}

func (cfg *Config) swallowNonFatal(err error) bool {
	if err == nil || cfg.ReportError == nil {
		return false
	}
	if !isBadArraySubscript(err) {
		var circular CircularNameRefError
		if !errors.As(err, &circular) {
			return false
		}
	}
	if cfg.ReportError == nil {
		return false
	}
	cfg.ReportError(err)
	return true
}

func (cfg *Config) reportParamErrorOnce(pe *syntax.ParamExp, err error) {
	if cfg.ReportError == nil || pe == nil || err == nil {
		return
	}
	key := err.Error()
	seen := cfg.reportedParamErrors[pe]
	if seen == nil {
		seen = make(map[string]struct{})
		cfg.reportedParamErrors[pe] = seen
	}
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	cfg.ReportError(err)
}

func (cfg *Config) reportNegativeSubstringLength(pe *syntax.ParamExp, length int) {
	cfg.reportParamErrorOnce(pe, fmt.Errorf(" %d: substring expression < 0", length))
}

func (cfg *Config) ifsRune(r rune) bool {
	for _, r2 := range cfg.ifs {
		if r == r2 {
			return true
		}
	}
	return false
}

func (cfg *Config) ifsWhitespaceRune(r rune) bool {
	if !cfg.ifsRune(r) {
		return false
	}
	return r == ' ' || r == '\t' || r == '\n'
}

type ifsRuneType uint8

const (
	ifsRuneNone ifsRuneType = iota
	ifsRuneWhitespace
	ifsRuneNonWhitespace
)

func (cfg *Config) classifyIFSRune(r rune) ifsRuneType {
	switch {
	case cfg.ifsWhitespaceRune(r):
		return ifsRuneWhitespace
	case cfg.ifsRune(r):
		return ifsRuneNonWhitespace
	default:
		return ifsRuneNone
	}
}

func (cfg *Config) ifsByte(b byte) bool {
	return strings.IndexByte(cfg.ifs, b) >= 0
}

func (cfg *Config) classifyIFSByte(b byte) ifsRuneType {
	switch {
	case cfg.ifsByte(b) && (b == ' ' || b == '\t' || b == '\n'):
		return ifsRuneWhitespace
	case cfg.ifsByte(b):
		return ifsRuneNonWhitespace
	default:
		return ifsRuneNone
	}
}

func (cfg *Config) classifyIFSStringAt(s string, start int) (ifsRuneType, int) {
	if start >= len(s) {
		return ifsRuneNone, 0
	}
	r, width := utf8.DecodeRuneInString(s[start:])
	if r == utf8.RuneError && width == 1 {
		return cfg.classifyIFSByte(s[start]), 1
	}
	return cfg.classifyIFSRune(r), width
}

func (cfg *Config) ifsJoin(strs []string) string {
	sep := ""
	if cfg.ifs != "" {
		sep = cfg.ifs[:1]
	}
	return strings.Join(strs, sep)
}

func (cfg *Config) strBuilder() *strings.Builder {
	b := &cfg.bufferAlloc
	b.Reset()
	return b
}

func (cfg *Config) envGet(name string) string {
	return cfg.Env.Get(name).String()
}

func (cfg *Config) envSet(name, value string) error {
	wenv, ok := cfg.Env.(WriteEnviron)
	if !ok {
		return fmt.Errorf("environment is read-only")
	}
	return wenv.Set(name, Variable{Set: true, Kind: String, Str: value})
}

func parseGlobIgnore(raw string) []string {
	if raw == "" {
		return nil
	}
	var (
		parts   []string
		current strings.Builder
		inClass bool
		escaped bool
	)
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if escaped {
			current.WriteByte(b)
			escaped = false
			continue
		}
		switch b {
		case '\\':
			current.WriteByte(b)
			escaped = true
		case '[':
			current.WriteByte(b)
			inClass = true
		case ']':
			current.WriteByte(b)
			inClass = false
		case ':':
			if inClass {
				current.WriteByte(b)
				continue
			}
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(b)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
}

func (cfg *Config) prepareGlobIgnore() {
	cfg.globIgnore = parseGlobIgnore(cfg.envGet("GLOBIGNORE"))
	if len(cfg.globIgnore) == 0 {
		cfg.globIgnoreMatchers = nil
		return
	}
	mode := pattern.Filenames | pattern.EntireString | pattern.GlobLeadingDot
	if cfg.NoCaseGlob {
		mode |= pattern.NoGlobCase
	}
	if cfg.ExtGlob {
		mode |= pattern.ExtendedOperators
	}
	matchers := make([]func(string) bool, 0, len(cfg.globIgnore))
	for _, pat := range cfg.globIgnore {
		matcher, err := pattern.ExtendedPatternMatcher(pat, mode)
		if err != nil {
			literal := pat
			matcher = func(name string) bool {
				return name == literal
			}
		}
		matchers = append(matchers, matcher)
	}
	cfg.globIgnoreMatchers = matchers
}

func (cfg *Config) globLeadingDot() bool {
	return cfg.DotGlob || len(cfg.globIgnore) > 0
}

func (cfg *Config) includeDotDotCandidates() bool {
	return !cfg.GlobSkipDots && len(cfg.globIgnore) == 0
}

func (cfg *Config) usesCLocale() bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		switch cfg.envGet(name) {
		case "":
			continue
		case "C", "POSIX":
			return true
		default:
			return false
		}
	}
	return false
}

// Literal expands a single shell word. It is similar to [Fields], but the result
// is a single string. This is the behavior when a word is used as the value in
// a shell variable assignment, for example.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Literal(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// AssignmentLiteral expands a single shell word using assignment-value
// semantics. It matches [Literal] except that backslashes in unquoted literal
// text consume the following byte, as they do in shell assignments.
func AssignmentLiteral(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteAssign)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// AssignmentWordLiteral expands a single shell word using assignment-word
// backslash rules without applying assignment-style tilde expansion.
func AssignmentWordLiteral(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteAssignNoTilde)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

func (cfg *Config) expandAssignmentTildeLiteral(s string, moreFields bool) string {
	if !strings.ContainsRune(s, '~') {
		return s
	}
	var b strings.Builder
	start := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) {
			if s[i] != ':' || assignmentTildeColonEscaped(s, i) {
				continue
			}
		}
		segment := s[start:i]
		if prefix, suffix, expanded := cfg.expandUser(segment, moreFields); expanded {
			b.WriteString(prefix)
			b.WriteString(suffix)
		} else {
			b.WriteString(segment)
		}
		if i == len(s) {
			break
		}
		b.WriteByte(':')
		start = i + 1
		moreFields = false
	}
	return b.String()
}

func assignmentTildeColonEscaped(s string, colon int) bool {
	backslashes := 0
	for i := colon - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// Document expands a single shell word as if it were a here-document body.
// It is similar to [Literal], but without brace expansion, tilde expansion, and
// globbing.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Document(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteHeredoc)
	if err != nil {
		return "", err
	}
	return cfg.fieldJoin(field), nil
}

// Pattern expands a shell pattern AST. Quoted parts are escaped via
// [pattern.QuoteMeta], while pattern operators such as `*`, `?`, bracket
// expressions, and extended globs are preserved. The result can be used on
// [pattern.Regexp] directly.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Pattern(cfg *Config, pat *syntax.Pattern) (string, error) {
	if pat == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	return cfg.patternString(pat, true)
}

// PatternWord expands a single shell word as a pattern. It is retained for
// classic test/[ operands, which still parse as generic words rather than the
// first-class pattern AST.
func PatternWord(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, part := range field {
		if part.quote > quoteNone {
			sb.WriteString(pattern.QuoteMeta(part.val, pattern.ExtendedOperators))
		} else {
			sb.WriteString(part.val)
		}
	}
	return sb.String(), nil
}

func (cfg *Config) patternString(pat *syntax.Pattern, allowLeadingTilde bool) (string, error) {
	if pat == nil {
		return "", nil
	}
	var sb strings.Builder
	for i, part := range pat.Parts {
		leading := allowLeadingTilde && i == 0
		if err := cfg.appendPatternPart(&sb, part, leading, i+1 < len(pat.Parts)); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

func (cfg *Config) patternLiteralString(pat *syntax.Pattern) (string, error) {
	if pat == nil {
		return "", nil
	}
	var sb strings.Builder
	for _, part := range pat.Parts {
		if err := cfg.appendPatternLiteralPart(&sb, part); err != nil {
			return "", err
		}
	}
	return sb.String(), nil
}

func (cfg *Config) appendPatternPart(sb *strings.Builder, part syntax.PatternPart, leading, more bool) error {
	switch part := part.(type) {
	case *syntax.PatternAny:
		sb.WriteByte('*')
	case *syntax.PatternSingle:
		sb.WriteByte('?')
	case *syntax.PatternCharClass:
		sb.WriteString(part.Value)
	case *syntax.Lit:
		s := part.Value
		if leading {
			if prefix, rest, expanded := cfg.expandUser(s, more); expanded {
				s = prefix + rest
			}
		}
		s, _, _ = strings.Cut(s, "\x00")
		sb.WriteString(s)
	case *syntax.SglQuoted:
		s := part.Value
		if part.Dollar {
			s = cfg.decodeANSICString(s)
			s, _, _ = strings.Cut(s, "\x00")
		}
		sb.WriteString(pattern.QuoteMeta(s, pattern.ExtendedOperators))
	case *syntax.DblQuoted:
		field, err := cfg.wordField(part.Parts, quoteDouble)
		if err != nil {
			return err
		}
		for _, fp := range field {
			sb.WriteString(pattern.QuoteMeta(fp.val, pattern.ExtendedOperators))
		}
	case *syntax.ParamExp:
		if parts, ok, err := cfg.paramExpWordField(part, quoteNone); err != nil {
			return err
		} else if ok {
			for _, fp := range parts {
				if fp.quote > quoteNone {
					sb.WriteString(pattern.QuoteMeta(fp.val, pattern.ExtendedOperators))
				} else {
					sb.WriteString(fp.val)
				}
			}
		} else {
			val, err := cfg.paramExp(part, quoteNone)
			if err != nil {
				return err
			}
			sb.WriteString(val)
		}
	case *syntax.CmdSubst:
		val, err := cfg.cmdSubst(part)
		if err != nil {
			return err
		}
		sb.WriteString(val)
	case *syntax.ArithmExp:
		n, err := Arithm(cfg, part.X)
		if err != nil {
			return err
		}
		sb.WriteString(strconv.Itoa(n))
	case *syntax.ProcSubst:
		procPath, err := cfg.ProcSubst(part)
		if err != nil {
			return err
		}
		sb.WriteString(procPath)
	case *syntax.ExtGlob:
		s, err := cfg.extGlobPatternString(part)
		if err != nil {
			return err
		}
		sb.WriteString(s)
	default:
		panic(fmt.Sprintf("unhandled pattern part: %T", part))
	}
	return nil
}

func (cfg *Config) appendPatternLiteralPart(sb *strings.Builder, part syntax.PatternPart) error {
	switch part := part.(type) {
	case *syntax.PatternAny:
		sb.WriteByte('*')
	case *syntax.PatternSingle:
		sb.WriteByte('?')
	case *syntax.PatternCharClass:
		sb.WriteString(part.Value)
	case *syntax.Lit:
		s := part.Value
		s, _, _ = strings.Cut(s, "\x00")
		sb.WriteString(s)
	case *syntax.SglQuoted:
		s := part.Value
		if part.Dollar {
			s = cfg.decodeANSICString(s)
			s, _, _ = strings.Cut(s, "\x00")
		}
		sb.WriteString(s)
	case *syntax.DblQuoted:
		field, err := cfg.wordField(part.Parts, quoteDouble)
		if err != nil {
			return err
		}
		sb.WriteString(cfg.fieldJoin(field))
	case *syntax.ParamExp:
		if parts, ok, err := cfg.paramExpWordField(part, quoteNone); err != nil {
			return err
		} else if ok {
			sb.WriteString(cfg.fieldJoin(parts))
		} else {
			val, err := cfg.paramExp(part, quoteNone)
			if err != nil {
				return err
			}
			sb.WriteString(val)
		}
	case *syntax.CmdSubst:
		val, err := cfg.cmdSubst(part)
		if err != nil {
			return err
		}
		sb.WriteString(val)
	case *syntax.ArithmExp:
		n, err := Arithm(cfg, part.X)
		if err != nil {
			return err
		}
		sb.WriteString(strconv.Itoa(n))
	case *syntax.ProcSubst:
		procPath, err := cfg.ProcSubst(part)
		if err != nil {
			return err
		}
		sb.WriteString(procPath)
	case *syntax.ExtGlob:
		s, err := cfg.extGlobLiteralString(part)
		if err != nil {
			return err
		}
		sb.WriteString(s)
	default:
		panic(fmt.Sprintf("unhandled pattern literal part: %T", part))
	}
	return nil
}

func (cfg *Config) extGlobPatternString(eg *syntax.ExtGlob) (string, error) {
	var sb strings.Builder
	sb.WriteString(eg.Op.String())
	for i, pat := range eg.Patterns {
		if i > 0 {
			sb.WriteByte('|')
		}
		str, err := cfg.patternString(pat, false)
		if err != nil {
			return "", err
		}
		sb.WriteString(str)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

func (cfg *Config) extGlobLiteralString(eg *syntax.ExtGlob) (string, error) {
	var sb strings.Builder
	sb.WriteString(eg.Op.String())
	for i, pat := range eg.Patterns {
		if i > 0 {
			sb.WriteByte('|')
		}
		str, err := cfg.patternLiteralString(pat)
		if err != nil {
			return "", err
		}
		sb.WriteString(str)
	}
	sb.WriteByte(')')
	return sb.String(), nil
}

// Regexp expands a single shell word for use as a Bash [[ =~ ]] regular
// expression, preserving regex semantics in unquoted parts while treating
// quoted parts as literals.
func Regexp(cfg *Config, word *syntax.Word) (string, error) {
	if word == nil {
		return "", nil
	}
	cfg = prepareConfig(cfg)
	field, err := cfg.wordField(word.Parts, quoteNone)
	if err != nil {
		return "", err
	}
	sb := cfg.strBuilder()
	for _, part := range field {
		if part.quote > quoteNone {
			sb.WriteString(regexp.QuoteMeta(part.val))
		} else {
			sb.WriteString(part.val)
		}
	}
	return sb.String(), nil
}

// Format expands a format string with a number of arguments, following the
// shell's format specifications. These include printf(1), among others.
//
// The resulting string is returned, along with the number of arguments used.
// Note that the resulting string may contain null bytes, for example
// if the format string used `\x00`. The caller should terminate the string
// at the first null byte if needed, such as when expanding for `$'foo\x00bar'`.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func Format(cfg *Config, format string, args []string) (string, int, error) {
	cfg = prepareConfig(cfg)
	sb := cfg.strBuilder()

	consumed, err := formatInto(sb, format, args)
	if err != nil {
		return "", 0, err
	}

	return sb.String(), consumed, err
}

func (cfg *Config) decodeANSICString(src string) string {
	var sb strings.Builder
	sb.Grow(len(src))
	cLocale := cfg != nil && cfg.usesCLocale()

	for i := 0; i < len(src); i++ {
		if src[i] != '\\' {
			sb.WriteByte(src[i])
			continue
		}
		if i+1 >= len(src) {
			sb.WriteByte('\\')
			break
		}

		i++
		switch c := src[i]; c {
		case 'a':
			sb.WriteByte('\a')
		case 'b':
			sb.WriteByte('\b')
		case 'e', 'E':
			sb.WriteByte('\x1b')
		case 'f':
			sb.WriteByte('\f')
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case 'v':
			sb.WriteByte('\v')
		case '\\', '\'', '"', '?':
			sb.WriteByte(c)
		case 'c':
			if i+1 >= len(src) {
				sb.WriteString(`\c`)
				break
			}
			i++
			sb.WriteByte(src[i] & 0x1f)
		case '0', '1', '2', '3', '4', '5', '6', '7':
			start := i
			for i+1 < len(src) && i-start < 2 && src[i+1] >= '0' && src[i+1] <= '7' {
				i++
			}
			n, _ := strconv.ParseUint(src[start:i+1], 8, 8)
			sb.WriteByte(byte(n))
		case 'x', 'u', 'U':
			maxDigits := 2
			if c == 'u' {
				maxDigits = 4
			} else if c == 'U' {
				maxDigits = 8
			}
			start := i + 1
			end := start
			for end < len(src) && end-start < maxDigits && isHex(src[end]) {
				end++
			}
			if start == end {
				sb.WriteByte('\\')
				sb.WriteByte(c)
				break
			}
			i = end - 1
			n, _ := strconv.ParseUint(src[start:end], 16, 32)
			if c == 'x' {
				sb.WriteByte(byte(n))
				break
			}
			if cLocale {
				if n <= 0xFFFF {
					fmt.Fprintf(&sb, "\\u%04X", n)
				} else {
					fmt.Fprintf(&sb, "\\U%08X", n)
				}
				break
			}
			r := rune(n)
			if !utf8.ValidRune(r) {
				sb.WriteString(src[start-2 : end])
				break
			}
			sb.WriteRune(r)
		default:
			sb.WriteByte('\\')
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

func isHex(b byte) bool {
	return '0' <= b && b <= '9' || 'a' <= b && b <= 'f' || 'A' <= b && b <= 'F'
}

func formatInto(sb *strings.Builder, format string, args []string) (int, error) {
	var fmts []byte
	initialArgs := len(args)

	for i := 0; i < len(format); i++ {
		// readDigits reads from 0 to max digits, either octal or
		// hexadecimal.
		readDigits := func(max int, hex bool) string {
			j := 0
			for ; j < max && i+j < len(format); j++ {
				c := format[i+j]
				if (c >= '0' && c <= '9') ||
					(hex && c >= 'a' && c <= 'f') ||
					(hex && c >= 'A' && c <= 'F') {
					// valid octal or hex char
				} else {
					break
				}
			}
			digits := format[i : i+j]
			i += j - 1 // -1 since the outer loop does i++
			return digits
		}
		c := format[i]
		switch {
		case c == '\\': // escaped
			i++
			if i >= len(format) {
				sb.WriteByte('\\')
				break
			}
			switch c = format[i]; c {
			case 'a': // bell
				sb.WriteByte('\a')
			case 'b': // backspace
				sb.WriteByte('\b')
			case 'e', 'E': // escape
				sb.WriteByte('\x1b')
			case 'f': // form feed
				sb.WriteByte('\f')
			case 'n': // new line
				sb.WriteByte('\n')
			case 'r': // carriage return
				sb.WriteByte('\r')
			case 't': // horizontal tab
				sb.WriteByte('\t')
			case 'v': // vertical tab
				sb.WriteByte('\v')
			case '\\', '\'', '"', '?': // just the character
				sb.WriteByte(c)
			case '0', '1', '2', '3', '4', '5', '6', '7':
				digits := readDigits(3, false)
				// if digits don't fit in 8 bits, 0xff via strconv
				n, _ := strconv.ParseUint(digits, 8, 8)
				sb.WriteByte(byte(n))
			case 'x', 'u', 'U':
				i++
				maxDigits := 2
				switch c {
				case 'u':
					maxDigits = 4
				case 'U':
					maxDigits = 8
				}
				digits := readDigits(maxDigits, true)
				if digits != "" {
					// can't error
					n, _ := strconv.ParseUint(digits, 16, 32)
					if c == 'x' {
						// always as a single byte
						sb.WriteByte(byte(n))
					} else {
						sb.WriteRune(rune(n))
					}
					break
				}
				fallthrough
			default: // no escape sequence
				sb.WriteByte('\\')
				sb.WriteByte(c)
			}
		case len(fmts) > 0:
			switch c {
			case '%':
				sb.WriteByte('%')
				fmts = nil
			case 'c':
				var b byte
				if len(args) > 0 {
					arg := ""
					arg, args = args[0], args[1:]
					if arg != "" {
						b = arg[0]
					}
				}
				sb.WriteByte(b)
				fmts = nil
			case '+', '-', ' ':
				if len(fmts) > 1 {
					return 0, fmt.Errorf("invalid format char: %c", c)
				}
				fmts = append(fmts, c)
			case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
				fmts = append(fmts, c)
			case 's', 'b', 'd', 'i', 'u', 'o', 'x':
				arg := ""
				if len(args) > 0 {
					arg, args = args[0], args[1:]
				}
				var farg any
				if c == 'b' {
					// Passing in nil for args ensures that % format
					// strings aren't processed; only escape sequences
					// will be handled.
					_, err := formatInto(sb, arg, nil)
					if err != nil {
						return 0, err
					}
				} else if c != 's' {
					n, _ := strconv.ParseInt(arg, 0, 0)
					if c == 'i' || c == 'd' {
						farg = int(n)
					} else {
						farg = uint(n)
					}
					if c == 'i' || c == 'u' {
						c = 'd'
					}
				} else {
					farg = arg
				}
				if farg != nil {
					fmts = append(fmts, c)
					fmt.Fprintf(sb, string(fmts), farg)
				}
				fmts = nil
			default:
				return 0, fmt.Errorf("invalid format char: %c", c)
			}
		case args != nil && c == '%':
			// if args == nil, we are not doing format
			// arguments
			fmts = []byte{c}
		default:
			sb.WriteByte(c)
		}
	}
	if len(fmts) > 0 {
		return 0, fmt.Errorf("missing format char")
	}
	return initialArgs - len(args), nil
}

func (cfg *Config) fieldJoin(parts []fieldPart) string {
	switch len(parts) {
	case 0:
		return ""
	case 1: // short-cut without a string copy
		return parts[0].val
	}
	sb := cfg.strBuilder()
	for _, part := range parts {
		sb.WriteString(part.val)
	}
	return sb.String()
}

func (cfg *Config) fieldJoinGlob(parts []fieldPart) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		if parts[0].glob != "" {
			return parts[0].glob
		}
		return parts[0].val
	}
	sb := cfg.strBuilder()
	for _, part := range parts {
		if part.glob != "" {
			sb.WriteString(part.glob)
			continue
		}
		sb.WriteString(part.val)
	}
	return sb.String()
}

type fieldSplitter struct {
	cfg            *Config
	fields         [][]fieldPart
	cur            []fieldPart
	haveField      bool
	curElideEmpty  bool
	clusterStarted bool
	clusterPrev    bool
	clusterNonWS   int
}

func newFieldSplitter(cfg *Config) fieldSplitter {
	return fieldSplitter{cfg: cfg}
}

func (s *fieldSplitter) flushField() {
	if !s.haveField {
		return
	}
	if len(s.cur) == 0 && s.curElideEmpty {
		s.cur = nil
		s.haveField = false
		s.curElideEmpty = false
		return
	}
	s.fields = append(s.fields, s.cur)
	s.cur = nil
	s.haveField = false
	s.curElideEmpty = false
}

func (s *fieldSplitter) finishCluster() {
	if !s.clusterStarted {
		return
	}
	empties := s.clusterNonWS
	if s.clusterPrev && empties > 0 {
		empties--
	}
	for range empties {
		s.fields = append(s.fields, nil)
	}
	s.clusterStarted = false
	s.clusterPrev = false
	s.clusterNonWS = 0
}

func (s *fieldSplitter) ensureField() {
	s.finishCluster()
	s.haveField = true
	s.curElideEmpty = false
}

func (s *fieldSplitter) appendPart(part fieldPart) {
	s.finishCluster()
	s.cur = append(s.cur, part)
	s.haveField = true
	s.curElideEmpty = false
}

func (s *fieldSplitter) appendFieldParts(parts []fieldPart) {
	for _, part := range parts {
		if part.quote > quoteNone {
			s.appendPart(part)
			continue
		}
		s.appendUnquoted(part.val)
	}
}

func (s *fieldSplitter) startCluster() {
	if s.clusterStarted {
		return
	}
	if s.haveField {
		s.flushField()
		s.clusterPrev = true
	} else {
		s.clusterPrev = false
	}
	s.clusterStarted = true
}

func (s *fieldSplitter) appendUnquoted(val string) {
	if val == "" {
		return
	}
	start := 0
	for i := 0; i < len(val); {
		rType, width := s.cfg.classifyIFSStringAt(val, i)
		if rType == ifsRuneNone {
			i += width
			continue
		}
		if start < i {
			s.appendPart(fieldPart{val: val[start:i]})
		}
		s.startCluster()
		if rType == ifsRuneNonWhitespace {
			s.clusterNonWS++
		}
		i += width
		start = i
	}
	if start < len(val) {
		s.appendPart(fieldPart{val: val[start:]})
	}
}

func (s *fieldSplitter) appendSplitFields(fields [][]fieldPart, elideEmpty bool) {
	s.finishCluster()
	for i, field := range fields {
		if i > 0 {
			s.flushField()
		}
		if len(field) > 0 {
			s.cur = append(s.cur, field...)
			s.curElideEmpty = false
		} else if !s.haveField {
			s.curElideEmpty = elideEmpty
		}
		s.haveField = true
	}
}

func (s *fieldSplitter) finish() [][]fieldPart {
	s.finishCluster()
	s.flushField()
	return s.fields
}

func (cfg *Config) splitFieldParts(parts []fieldPart) [][]fieldPart {
	splitter := newFieldSplitter(cfg)
	splitter.appendFieldParts(parts)
	return splitter.finish()
}

func (cfg *Config) escapedGlobField(parts []fieldPart) (escaped string, glob bool) {
	sb := cfg.strBuilder()
	mode := pattern.Mode(0)
	if cfg.ExtGlob {
		mode |= pattern.ExtendedOperators
	}
	for _, part := range parts {
		if part.quote > quoteNone {
			sb.WriteString(pattern.QuoteMeta(part.val, mode))
			continue
		}
		sb.WriteString(part.val)
		if pattern.HasMeta(part.val, mode) {
			glob = true
		}
	}
	if glob { // only copy the string if it will be used
		escaped = sb.String()
	}
	return escaped, glob
}

func (cfg *Config) filterGlobIgnore(matches []string) []string {
	if len(matches) == 0 || len(cfg.globIgnoreMatchers) == 0 {
		return matches
	}
	filtered := matches[:0]
nextMatch:
	for _, match := range matches {
		for _, matcher := range cfg.globIgnoreMatchers {
			if matcher(match) {
				continue nextMatch
			}
		}
		filtered = append(filtered, match)
	}
	return filtered
}

func (cfg *Config) expandPathField(dir string, field []fieldPart) ([]string, error) {
	literal := cfg.fieldJoin(field)
	globField := slices.Clone(field)
	for i := range globField {
		if globField[i].glob != "" {
			globField[i].val = globField[i].glob
		}
	}
	globPath, doGlob := cfg.escapedGlobField(globField)
	if !doGlob || cfg.ReadDir == nil {
		return []string{literal}, nil
	}
	matches, err := cfg.glob(dir, globPath)
	if err != nil {
		// We avoid [errors.As] as it allocates, and we know that [Config.glob]
		// returns [pattern.Regexp] errors without wrapping.
		if _, ok := err.(*pattern.SyntaxError); ok {
			return []string{literal}, nil
		}
		return nil, err
	}
	matches = cfg.filterGlobIgnore(matches)
	if len(matches) > 0 {
		return matches, nil
	}
	switch {
	case cfg.FailGlob:
		return nil, FailGlobError{Pattern: globPath}
	case cfg.NullGlob:
		return nil, nil
	default:
		return []string{literal}, nil
	}
}

// Fields is a pre-iterators API which now wraps [FieldsSeq].
func Fields(cfg *Config, words ...*syntax.Word) ([]string, error) {
	var fields []string
	for s, err := range FieldsSeq(cfg, words...) {
		if err != nil {
			return nil, err
		}
		fields = append(fields, s)
	}
	return fields, nil
}

// RedirectFields expands a single shell word for use as a file redirect target.
// Bash applies the normal field expansion pipeline and then treats multiple
// results as an ambiguous redirect.
func RedirectFields(cfg *Config, word *syntax.Word) ([]string, error) {
	if word == nil {
		return nil, nil
	}
	return Fields(cfg, word)
}

// FieldsSeq expands a number of words as if they were arguments in a shell
// command. This includes brace expansion, tilde expansion, parameter expansion,
// command substitution, arithmetic expansion, quote removal, and globbing.
func FieldsSeq(cfg *Config, words ...*syntax.Word) iter.Seq2[string, error] {
	cfg = prepareConfig(cfg)
	dir := cfg.envGet("PWD")
	return func(yield func(string, error) bool) {
		for _, word := range words {
			afterBraces := []*syntax.Word{word}
			if slices.ContainsFunc(word.Parts, func(part syntax.WordPart) bool {
				_, ok := part.(*syntax.BraceExp)
				return ok
			}) {
				var err error
				afterBraces, err = Braces(word)
				if err != nil {
					yield("", err)
					return
				}
			}
			for _, word2 := range afterBraces {
				wfields, err := cfg.wordFields(word2.Parts)
				if err != nil {
					yield("", err)
					return
				}
				for _, field := range wfields {
					expanded, err := cfg.expandPathField(dir, field)
					if err != nil {
						yield("", err)
						return
					}
					for _, s := range expanded {
						if !yield(s, nil) {
							return
						}
					}
				}
			}
		}
	}
}

type fieldPart struct {
	val   string
	glob  string
	quote quoteLevel
}

type quoteLevel uint

const (
	quoteNone quoteLevel = iota
	quoteAssign
	quoteAssignNoTilde
	quoteDouble
	quoteHeredoc
	quoteSingle
)

func (cfg *Config) braceFieldParts(br *syntax.BraceExp, ql quoteLevel, fieldFn func(*syntax.Word, quoteLevel) ([]fieldPart, error)) ([]fieldPart, error) {
	parts := []fieldPart{{val: "{"}}
	for i, elem := range br.Elems {
		if i > 0 {
			if br.Sequence {
				parts = append(parts, fieldPart{val: ".."})
			} else {
				parts = append(parts, fieldPart{val: ","})
			}
		}
		field, err := fieldFn(elem, ql)
		if err != nil {
			return nil, err
		}
		parts = append(parts, field...)
	}
	parts = append(parts, fieldPart{val: "}"})
	return parts, nil
}

func (cfg *Config) wordField(wps []syntax.WordPart, ql quoteLevel) ([]fieldPart, error) {
	var field []fieldPart
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 && ql == quoteAssign {
				s = cfg.expandAssignmentTildeLiteral(s, len(wps) > 1)
			} else if i == 0 && ql == quoteNone {
				if prefix, rest, expanded := cfg.expandUser(s, len(wps) > 1); expanded {
					// TODO: return two separate fieldParts,
					// like in wordFields?
					s = prefix + rest
				}
			}
			if (ql == quoteAssign || ql == quoteAssignNoTilde) && strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for i := 0; i < len(s); i++ {
					b := s[i]
					if b == '\\' {
						if i++; i >= len(s) {
							break
						}
						b = s[i]
					}
					sb.WriteByte(b)
				}
				s = sb.String()
			}
			if (ql == quoteDouble || ql == quoteHeredoc) && strings.Contains(s, "\\") {
				sb := cfg.strBuilder()
				for i := 0; i < len(s); i++ {
					b := s[i]
					if b == '\\' && i+1 < len(s) {
						switch s[i+1] {
						case '"':
							if ql != quoteDouble {
								break
							}
							fallthrough
						case '\\', '$', '`': // special chars
							i++
							b = s[i] // write the special char, skipping the backslash
						}
					}
					sb.WriteByte(b)
				}
				s = sb.String()
			}
			s, _, _ = strings.Cut(s, "\x00") // TODO: why is this needed?
			field = append(field, fieldPart{val: s})
		case *syntax.SglQuoted:
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val = cfg.decodeANSICString(fp.val)
				fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
			}
			field = append(field, fp)
		case *syntax.DblQuoted:
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				field = append(field, part)
			}
		case *syntax.ParamExp:
			if parts, ok, err := cfg.paramExpWordField(wp, ql); err != nil {
				return nil, err
			} else if ok {
				field = append(field, parts...)
			} else {
				val, err := cfg.paramExp(wp, ql)
				if err != nil {
					return nil, err
				}
				field = append(field, fieldPart{val: val})
			}
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: val})
		case *syntax.ArithmExp:
			sourceStart := wp.Left.Offset() + 3
			if wp.Bracket {
				sourceStart = wp.Left.Offset() + 2
			}
			n, err := ArithmWithSource(cfg, wp.X, wp.Source, sourceStart, wp.Right.Offset())
			if err != nil {
				if !cfg.swallowNonFatal(err) {
					return nil, err
				}
				n = 0
			}
			field = append(field, fieldPart{val: strconv.Itoa(n)})
		case *syntax.BraceExp:
			parts, err := cfg.braceFieldParts(wp, ql, func(word *syntax.Word, ql quoteLevel) ([]fieldPart, error) {
				return cfg.wordField(word.Parts, ql)
			})
			if err != nil {
				return nil, err
			}
			field = append(field, parts...)
		case *syntax.ProcSubst:
			procPath, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: procPath})
		case *syntax.ExtGlob:
			// Like how [Config.wordFields] deals with [syntax.ExtGlob],
			// except that we allow these through even when [Config.ExtGlob]
			// is false, as it only applies to pathname expansion.
			pat, err := cfg.extGlobPatternString(wp)
			if err != nil {
				return nil, err
			}
			raw, err := cfg.extGlobLiteralString(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: raw, glob: pat})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	return field, nil
}

func (cfg *Config) cmdSubst(cs *syntax.CmdSubst) (string, error) {
	if cfg.CmdSubst == nil {
		return "", UnexpectedCommandError{Node: cs}
	}
	sb := cfg.strBuilder()
	if err := cfg.CmdSubst(sb, cs); err != nil {
		return "", err
	}
	out := sb.String()
	out = strings.ReplaceAll(out, "\x00", "")
	return strings.TrimRight(out, "\n"), nil
}

func (cfg *Config) wordFields(wps []syntax.WordPart) ([][]fieldPart, error) {
	splitter := newFieldSplitter(cfg)
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 {
				prefix, rest, expanded := cfg.expandUser(s, len(wps) > 1)
				if expanded && (prefix != "" || rest == "") {
					splitter.appendPart(fieldPart{
						quote: quoteSingle,
						val:   prefix,
					})
				}
				if expanded {
					s = rest
				}
			}
			if strings.Contains(s, "\\") {
				start := 0
				for i := 0; i < len(s); i++ {
					if s[i] != '\\' {
						continue
					}
					if start < i {
						splitter.appendPart(fieldPart{val: s[start:i]})
					}
					if i+1 >= len(s) {
						start = len(s)
						break
					}
					i++
					splitter.appendPart(fieldPart{quote: quoteSingle, val: s[i : i+1]})
					start = i + 1
				}
				s = s[start:]
			}
			if s != "" {
				splitter.appendPart(fieldPart{val: s})
			}
		case *syntax.SglQuoted:
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val = cfg.decodeANSICString(fp.val)
				fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
			}
			splitter.appendPart(fp)
		case *syntax.DblQuoted:
			if dqFields, ok, err := cfg.dblQuotedFields(wp.Parts); err != nil {
				return nil, err
			} else if ok {
				splitter.appendSplitFields(dqFields, false)
				continue
			}
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			if len(wfield) == 0 {
				splitter.appendPart(fieldPart{quote: quoteDouble, val: ""})
				continue
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				splitter.appendPart(part)
			}
		case *syntax.ParamExp:
			if fields2, ok, elideEmpty, err := cfg.paramExpFields(wp); err != nil {
				return nil, err
			} else if ok {
				splitter.appendSplitFields(fields2, elideEmpty)
			} else if parts, ok, err := cfg.paramExpSplitValue(wp); err != nil {
				return nil, err
			} else if ok {
				splitter.appendFieldParts(parts)
			} else {
				val, err := cfg.paramExp(wp, quoteNone)
				if err != nil {
					return nil, err
				}
				splitter.appendUnquoted(val)
			}
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			splitter.appendUnquoted(val)
		case *syntax.ArithmExp:
			sourceStart := wp.Left.Offset() + 3
			if wp.Bracket {
				sourceStart = wp.Left.Offset() + 2
			}
			n, err := ArithmWithSource(cfg, wp.X, wp.Source, sourceStart, wp.Right.Offset())
			if err != nil {
				if !cfg.swallowNonFatal(err) {
					return nil, err
				}
				n = 0
			}
			splitter.appendPart(fieldPart{val: strconv.Itoa(n)})
		case *syntax.BraceExp:
			parts, err := cfg.braceFieldParts(wp, quoteNone, func(word *syntax.Word, ql quoteLevel) ([]fieldPart, error) {
				return cfg.wordField(word.Parts, ql)
			})
			if err != nil {
				return nil, err
			}
			for _, part := range parts {
				splitter.appendPart(part)
			}
		case *syntax.ProcSubst:
			procPath, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			splitter.appendUnquoted(procPath)
		case *syntax.ExtGlob:
			// We don't translate or interpret the pattern here in any way;
			// that's done later when globbing takes place via [pattern.Regexp].
			// Here, all we do is keep the extended globbing expression in string form.
			//
			// TODO(v4): perhaps the syntax parser should keep extended globbing expressions
			// as plain literal strings, because a custom node is not particularly helpful.
			// It's not like other globbing operators like `*` or `**` get their own nodes.
			pat, err := cfg.extGlobPatternString(wp)
			if err != nil {
				return nil, err
			}
			raw, err := cfg.extGlobLiteralString(wp)
			if err != nil {
				return nil, err
			}
			splitter.appendPart(fieldPart{val: raw, glob: pat})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	return splitter.finish(), nil
}

func (cfg *Config) dblQuotedFields(wps []syntax.WordPart) ([][]fieldPart, bool, error) {
	sawArray := false
	for _, wp := range wps {
		pe, ok := wp.(*syntax.ParamExp)
		if !ok {
			continue
		}
		if _, handled, err := cfg.quotedElemFields(pe); err != nil {
			return nil, false, err
		} else if handled {
			sawArray = true
			break
		}
	}
	if !sawArray {
		return nil, false, nil
	}

	var fields [][]fieldPart
	var curField []fieldPart
	flush := func() {
		copied := append([]fieldPart(nil), curField...)
		fields = append(fields, copied)
		curField = nil
	}
	for _, wp := range wps {
		if pe, ok := wp.(*syntax.ParamExp); ok {
			elems, handled, err := cfg.quotedElemFields(pe)
			if err != nil {
				return nil, false, err
			}
			if handled {
				switch len(elems) {
				case 0:
					continue
				case 1:
					curField = append(curField, fieldPart{quote: quoteDouble, val: elems[0]})
					continue
				}
				curField = append(curField, fieldPart{quote: quoteDouble, val: elems[0]})
				flush()
				for _, elem := range elems[1 : len(elems)-1] {
					fields = append(fields, []fieldPart{{quote: quoteDouble, val: elem}})
				}
				curField = []fieldPart{{quote: quoteDouble, val: elems[len(elems)-1]}}
				continue
			}
		}
		parts, err := cfg.wordField([]syntax.WordPart{wp}, quoteDouble)
		if err != nil {
			return nil, false, err
		}
		for _, part := range parts {
			part.quote = quoteDouble
			curField = append(curField, part)
		}
	}
	if len(curField) > 0 {
		fields = append(fields, append([]fieldPart(nil), curField...))
	}
	return fields, true, nil
}

// quotedElemFields returns the list of elements resulting from a quoted
// parameter expansion that should be treated especially, like "${foo[@]}".
func (cfg *Config) quotedElemFields(pe *syntax.ParamExp) ([]string, bool, error) {
	if pe == nil || pe.Length || pe.Width || pe.IsSet {
		return nil, false, nil
	}
	if err := invalidParamExpansion(pe); err != nil {
		return nil, false, err
	}
	name := pe.Param.Value
	if pe.Excl {
		state, err := cfg.paramExpState(indirectHolderParamExp(pe))
		if err != nil {
			return nil, false, err
		}
		switch indirectModeFor(pe, state) {
		case indirectResolve:
			_, target, err := cfg.resolveIndirectTargetState(state)
			if err != nil {
				return nil, false, err
			}
			if target != nil {
				resolvedPE := *pe
				resolvedPE.Excl = false
				resolvedPE.Param = target.Param
				resolvedPE.Index = target.Index
				if fields, ok, err := cfg.quotedElemFields(&resolvedPE); err != nil {
					return nil, false, err
				} else if ok {
					return fields, true, nil
				}
			}
			return nil, false, nil
		case indirectNames:
			switch pe.Names {
			case syntax.NamesPrefixWords: // "${!prefix@}"
				return cfg.namesByPrefix(pe.Param.Value), true, nil
			case syntax.NamesPrefix: // "${!prefix*}"
				return nil, false, nil
			}
		case indirectKeys:
			switch vr := cfg.Env.Get(name); vr.Kind {
			case Indexed:
				keys := make([]string, 0, vr.IndexedCount())
				for _, key := range vr.IndexedIndices() {
					keys = append(keys, strconv.Itoa(key))
				}
				if subscriptLit(pe.Index) == "*" {
					return []string{cfg.ifsJoin(keys)}, true, nil
				}
				return keys, true, nil
			case Associative:
				keys := sortedMapKeys(vr.Map)
				if subscriptLit(pe.Index) == "*" {
					return []string{cfg.ifsJoin(keys)}, true, nil
				}
				return keys, true, nil
			}
		}
		return nil, false, nil
	}
	fields, elems, ok := cfg.quotedArrayFields(pe)
	if !ok {
		return nil, false, nil
	}
	if pe.Exp == nil && pe.Repl == nil {
		return fields, true, nil
	}

	hasElems := len(elems) > 0
	null := arrayExpansionNull(pe, fields, elems)
	if pe.Exp != nil {
		switch pe.Exp.Op {
		case syntax.AlternateUnset, syntax.AlternateUnsetOrNull:
			if pe.Exp.Op == syntax.AlternateUnset && hasElems || pe.Exp.Op == syntax.AlternateUnsetOrNull && !null {
				word, err := cfg.quotedParamWord(pe.Exp.Word)
				return word, true, err
			}
		case syntax.DefaultUnset, syntax.DefaultUnsetOrNull:
			if pe.Exp.Op == syntax.DefaultUnset && !hasElems || pe.Exp.Op == syntax.DefaultUnsetOrNull && null {
				word, err := cfg.quotedParamWord(pe.Exp.Word)
				return word, true, err
			}
		case syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
			if pe.Exp.Op == syntax.ErrorUnset && !hasElems || pe.Exp.Op == syntax.ErrorUnsetOrNull && null {
				return nil, false, nil
			}
		case syntax.AssignUnset, syntax.AssignUnsetOrNull:
			if pe.Exp.Op == syntax.AssignUnset && !hasElems || pe.Exp.Op == syntax.AssignUnsetOrNull && null {
				return nil, false, nil
			}
		}
	}
	state, err := cfg.paramExpState(pe)
	if err != nil {
		return nil, false, err
	}
	elems, err = cfg.transformArrayElems(pe, state, elems)
	if err != nil {
		return nil, false, err
	}
	if arrayExpansionIsStar(pe) {
		return []string{cfg.ifsJoin(elems)}, true, nil
	}
	return elems, true, nil
}

func quotedIndirectArrayTarget(pe *syntax.ParamExp) bool {
	switch pe.Param.Value {
	case "@", "*":
		return true
	}
	switch subscriptLit(pe.Index) {
	case "@", "*":
		return true
	default:
		return false
	}
}

func (cfg *Config) quotedParamWord(word *syntax.Word) ([]string, error) {
	var parts []syntax.WordPart
	if word != nil {
		parts = word.Parts
	}
	fields, err := cfg.wordFields([]syntax.WordPart{&syntax.DblQuoted{Parts: parts}})
	if err != nil {
		return nil, err
	}
	out := make([]string, len(fields))
	for i, field := range fields {
		out[i] = cfg.fieldJoin(field)
	}
	return out, nil
}

func (cfg *Config) quotedArrayFields(pe *syntax.ParamExp) ([]string, []string, bool) {
	switch name := pe.Param.Value; name {
	case "*": // "${*}" or "${*:offset:length}"
		elems := cfg.sliceElems(pe, cfg.Env.Get(name).IndexedValues(), nil, true, false)
		return []string{cfg.ifsJoin(elems)}, elems, true
	case "@": // "${@}" or "${@:offset:length}"
		elems := cfg.sliceElems(pe, cfg.Env.Get(name).IndexedValues(), nil, true, false)
		return elems, elems, true
	}

	name := pe.Param.Value
	ref, vr, err := cfg.Env.Get(name).ResolveRef(cfg.Env, &syntax.VarRef{
		Name:  pe.Param,
		Index: pe.Index,
	})
	if err != nil {
		return nil, nil, false
	}
	index := pe.Index
	if ref != nil {
		index = ref.Index
	}
	switch subscriptLit(index) {
	case "@": // "${name[@]}"
		switch vr.Kind {
		case Indexed:
			elems := cfg.sliceElems(pe, vr.IndexedValues(), vr.IndexedIndices(), false, false)
			return elems, elems, true
		case Associative:
			elems := cfg.sliceElems(pe, sortedMapValues(vr.Map), nil, false, true)
			return elems, elems, true
		case Unknown:
			if !vr.IsSet() {
				// An unset variable expanded as "${name[@]}" produces
				// zero fields, just like an empty array.
				return []string{}, nil, true
			}
		}
	case "*": // "${name[*]}"
		if vr.Kind == Indexed {
			elems := cfg.sliceElems(pe, vr.IndexedValues(), vr.IndexedIndices(), false, false)
			return []string{cfg.ifsJoin(elems)}, elems, true
		}
		if vr.Kind == Associative {
			elems := cfg.sliceElems(pe, sortedMapValues(vr.Map), nil, false, true)
			return []string{cfg.ifsJoin(elems)}, elems, true
		}
	}
	return nil, nil, false
}

// sliceElems applies ${var:offset:length} slicing to a list of elements.
// When positional is true, $0 is prepended to the list before slicing.
// When assocAll is true, positive offsets use bash's associative-array quirk:
// offsets are effectively 1-based, so 0 and 1 both select the first element.
// In bash, positional parameter offsets ($@ and $*) are 1-based and
// offset 0 includes $0 (the shell or script name). Negative offsets
// count from $# + 1, so $0 is reachable via large enough negative values.
func (cfg *Config) sliceElems(pe *syntax.ParamExp, elems []string, indices []int, positional bool, assocAll bool) []string {
	if pe.Slice == nil {
		return elems
	}
	if positional {
		elems = append([]string{cfg.Env.Get("0").Str}, elems...)
		indices = nil
	}
	if len(indices) > 0 {
		start := 0
		if pe.Slice.Offset != nil {
			offset, err := Arithm(cfg, pe.Slice.Offset)
			if err != nil {
				return elems
			}
			if offset < 0 {
				offset = indices[len(indices)-1] + 1 + offset
				if offset < 0 {
					return nil
				}
			}
			start = len(elems)
			for i, index := range indices {
				if index >= offset {
					start = i
					break
				}
			}
			elems = elems[start:] //nolint:nilaway // elems is non-nil here: start is set by iterating indices which is non-empty (len(indices)>0 guard above)
			indices = indices[start:]
		}
		if pe.Slice.Length != nil {
			length, err := Arithm(cfg, pe.Slice.Length)
			if err != nil {
				return elems
			}
			if length < 0 {
				cfg.reportNegativeSubstringLength(pe, length)
				return nil
			}
			if length == 0 {
				return nil
			}
			if length < len(elems) {
				elems = elems[:length]
			}
		}
		return elems
	}
	slicePos := func(n int) int {
		if n < 0 {
			n = len(elems) + n
			if n < 0 {
				n = len(elems)
			}
		} else if n > len(elems) {
			n = len(elems)
		}
		return n
	}
	if pe.Slice.Offset != nil {
		offset, err := Arithm(cfg, pe.Slice.Offset)
		if err != nil {
			return elems
		}
		if assocAll && offset > 0 {
			offset--
		}
		elems = elems[slicePos(offset):]
	}
	if pe.Slice.Length != nil {
		length, err := Arithm(cfg, pe.Slice.Length)
		if err != nil {
			return elems
		}
		if length < 0 {
			cfg.reportNegativeSubstringLength(pe, length)
			return nil
		}
		elems = elems[:slicePos(length)]
	}
	return elems
}

func (cfg *Config) expandUser(field string, moreFields bool) (prefix, rest string, expanded bool) {
	name, ok := strings.CutPrefix(field, "~")
	if !ok {
		// No tilde prefix to expand, e.g. "foo".
		return "", field, false
	}
	i := strings.IndexByte(name, '/')
	if i < 0 && moreFields {
		// There is a tilde prefix, but followed by more fields, e.g. "~'foo'".
		// We only proceed if an unquoted slash was found in this field, e.g. "~/'foo'".
		return "", field, false
	}
	if i >= 0 {
		rest = name[i:]
		name = name[:i]
	}
	if name == "" {
		// Current user; try via "HOME", otherwise fall back to the
		// system's appropriate home dir env var. Don't use os/user, as
		// that's overkill. We can't use [os.UserHomeDir], because we want
		// to use cfg.Env, and we always want to check "HOME" first.

		if cfg.StartupHome != "" {
			prefix, rest := joinTildeHome(cfg.StartupHome, rest)
			return prefix, rest, true
		}
		if vr := cfg.TildeEnv.Get("HOME"); vr.IsSet() {
			prefix, rest := joinTildeHome(vr.String(), rest)
			return prefix, rest, true
		}

		if runtime.GOOS == "windows" {
			if vr := cfg.TildeEnv.Get("USERPROFILE"); vr.IsSet() {
				prefix, rest := joinTildeHome(vr.String(), rest)
				return prefix, rest, true
			}
		}
		return "", field, false
	}

	if vr := cfg.TildeEnv.Get("HOME " + name); vr.IsSet() {
		prefix, rest := joinTildeHome(vr.String(), rest)
		return prefix, rest, true
	}
	return "", field, false
}

func joinTildeHome(home, rest string) (string, string) {
	if home == "/" && strings.HasPrefix(rest, "/") {
		rest = strings.TrimPrefix(rest, "/")
	}
	return home, rest
}

func findAllIndex(pat, name string, n int) [][]int {
	expr, err := pattern.Regexp(pat, pattern.ExtendedOperators)
	if err != nil {
		return nil
	}
	rx := regexp.MustCompile(expr)
	return rx.FindAllStringIndex(name, n)
}

var (
	rxGlobStar        = regexp.MustCompile(`^[^/.][^/]*$`)
	rxGlobStarDotGlob = regexp.MustCompile(`^[^/]*$`)
)

// pathJoin2 is a simpler version of [path.Join] without cleaning the result,
// since that's needed for globbing.
func pathJoin2(elem1, elem2 string) string {
	if elem1 == "" {
		return elem2
	}
	if strings.HasSuffix(elem1, "/") {
		return elem1 + elem2
	}
	return elem1 + "/" + elem2
}

// pathSplit splits a POSIX shell path into its elements, retaining empty ones.
func pathSplit(name string) []string {
	return strings.Split(name, "/")
}

// EstimateGlobOperations returns the shell-core budget cost for one pathname
// glob pattern after quote removal and field splitting.
func EstimateGlobOperations(pat string) int64 {
	if pat == "" || !pattern.HasMeta(pat, pattern.ExtendedOperators) {
		return 0
	}
	ops := int64(1)
	for _, part := range pathSplit(pat) {
		if part == "" {
			continue
		}
		if pattern.HasMeta(part, pattern.ExtendedOperators) {
			ops++
		}
	}
	return ops
}

func (cfg *Config) glob(base, pat string) ([]string, error) {
	parts := pathSplit(pat)
	matches := []string{""}
	if path.IsAbs(pat) {
		matches[0] = "/"
		parts = parts[1:]
	}
	// TODO: as an optimization, we could do chunks of the path all at once,
	// like doing a single stat for "/foo/bar" in "/foo/bar/*".

	// TODO: Another optimization would be to reduce the number of ReadDir calls.
	// For example, /foo/* can end up doing one duplicate call:
	//
	//    ReadDir("/foo") to ensure that "/foo/" exists and only matches a directory
	//    ReadDir("/foo") glob "*"

	metaMode := pattern.Mode(0)
	if cfg.ExtGlob {
		metaMode |= pattern.ExtendedOperators
	}
	for i, part := range parts {
		// Keep around for debugging.
		// log.Printf("matches %q part %d %q", matches, i, part)

		wantDir := i < len(parts)-1
		switch {
		case part == "", part == ".", part == "..":
			for i, dir := range matches {
				matches[i] = pathJoin2(dir, part)
			}
			continue
		case !pattern.HasMeta(part, metaMode):
			var newMatches []string
			for _, dir := range matches {
				match := dir
				if !path.IsAbs(match) {
					match = path.Join(base, match)
				}
				match = pathJoin2(match, part)
				// We can't use [Config.ReadDir] on the parent and match the directory
				// entry by name, because short paths on Windows break that.
				// Our only option is to [Config.ReadDir] on the directory entry itself,
				// which can be wasteful if we only want to see if it exists,
				// but at least it's correct in all scenarios.
				if _, err := cfg.ReadDir(match); err != nil {
					if isWindowsErrPathNotFound(err) {
						// Unfortunately, [os.File.Readdir] on a regular file on
						// Windows returns an error that satisfies [fs.ErrNotExist].
						// Luckily, it returns a special "path not found" rather
						// than the normal "file not found" for missing files,
						// so we can use that knowledge to work around the bug.
						// See https://github.com/golang/go/issues/46734.
						// TODO: remove when the Go issue above is resolved.
					} else if errors.Is(err, fs.ErrNotExist) {
						continue // simply doesn't exist
					}
					if wantDir {
						continue // exists but not a directory
					}
				}
				newMatches = append(newMatches, pathJoin2(dir, part))
			}
			matches = newMatches
			continue
		case part == "**" && cfg.GlobStar:
			// Find all recursive matches for "**".
			// Note that we need the results to be in depth-first order,
			// and to avoid recursion, we use a slice as a stack.
			// Since we pop from the back, we populate the stack backwards.
			stack := make([]string, 0, len(matches))
			for _, match := range slices.Backward(matches) {
				// "a/**" should match "a/ a/b a/b/cfg ...";
				// note how the zero-match case there has a trailing separator.
				stack = append(stack, pathJoin2(match, ""))
			}
			matches = matches[:0]
			var newMatches []string // to reuse its capacity
			for len(stack) > 0 {
				dir := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				matches = append(matches, dir)

				// If dir is not a directory, we keep the stack as-is and continue.
				newMatches = newMatches[:0]
				rx := rxGlobStar.MatchString
				if cfg.globLeadingDot() {
					rx = rxGlobStarDotGlob.MatchString
				}
				newMatches, _ = cfg.globDir(base, dir, rx, wantDir, newMatches)
				for _, match := range slices.Backward(newMatches) {
					stack = append(stack, match)
				}
			}
			continue
		}
		mode := pattern.Filenames | pattern.EntireString | pattern.NoGlobStar
		if cfg.NoCaseGlob {
			mode |= pattern.NoGlobCase
		}
		if cfg.globLeadingDot() {
			mode |= pattern.GlobLeadingDot
		}
		if cfg.ExtGlob {
			mode |= pattern.ExtendedOperators
		}
		matcher, err := pattern.ExtendedPatternMatcher(part, mode)
		if err != nil {
			return nil, err
		}
		var newMatches []string
		for _, dir := range matches {
			newMatches, err = cfg.globDir(base, dir, matcher, wantDir, newMatches)
			if err != nil {
				return nil, err
			}
		}
		matches = newMatches
	}
	// Note that the results need to be sorted.
	// TODO: above we do a BFS; if we did a DFS, the matches would already be sorted.
	cfg.sortGlobMatches(matches)
	matches = slices.Compact(matches)
	// Remove any empty matches left behind from "**".
	if len(matches) > 0 && matches[0] == "" {
		matches = matches[1:]
	}
	return matches, nil
}

func (cfg *Config) sortGlobMatches(matches []string) {
	if len(matches) < 2 {
		return
	}
	collator := cfg.globCollator()
	if collator == nil {
		slices.Sort(matches)
		return
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return collator.CompareString(matches[i], matches[j]) < 0
	})
}

func (cfg *Config) globCollator() *collate.Collator {
	locale := cfg.globCollationLocale()
	if locale == "" || usesByteCollation(locale) {
		return nil
	}
	tag, err := language.Parse(normalizeLocaleTag(locale))
	if err != nil {
		return nil
	}
	return collate.New(tag)
}

func (cfg *Config) globCollationLocale() string {
	if cfg == nil || cfg.Env == nil {
		return ""
	}
	for _, name := range []string{"LC_ALL", "LC_COLLATE", "LANG"} {
		if value := strings.TrimSpace(cfg.envGet(name)); value != "" {
			return value
		}
	}
	return ""
}

func usesByteCollation(locale string) bool {
	switch strings.ToUpper(strings.TrimSpace(locale)) {
	case "", "C", "POSIX", "C.UTF-8", "C.UTF_8":
		return true
	default:
		return false
	}
}

func normalizeLocaleTag(locale string) string {
	locale = strings.TrimSpace(locale)
	if idx := strings.IndexByte(locale, '@'); idx >= 0 {
		locale = locale[:idx]
	}
	if idx := strings.IndexByte(locale, '.'); idx >= 0 {
		locale = locale[:idx]
	}
	return strings.ReplaceAll(locale, "_", "-")
}

func (cfg *Config) globDir(base, dir string, matcher func(string) bool, wantDir bool, matches []string) ([]string, error) {
	fullDir := dir
	if !path.IsAbs(dir) {
		fullDir = path.Join(base, dir)
	}
	infos, err := cfg.ReadDir(fullDir)
	if err != nil {
		// We still want to return matches, for the sake of reusing slices.
		return matches, err
	}
	for _, info := range infos {
		name := info.Name()
		if !wantDir {
			// No filtering.
		} else if mode := info.Type(); mode&os.ModeSymlink != 0 {
			// We need to know if the symlink points to a directory.
			// This requires an extra syscall, as [Config.ReadDir] on the parent directory
			// does not follow symlinks for each of the directory entries.
			// ReadDir is somewhat wasteful here, as we only want its error result,
			// but we could try to reuse its result as per the TODO in [Config.glob].
			if _, err := cfg.ReadDir(path.Join(fullDir, info.Name())); err != nil {
				continue
			}
		} else if !mode.IsDir() {
			// Not a symlink nor a directory.
			continue
		}
		if matcher(name) {
			matches = append(matches, pathJoin2(dir, name))
		}
	}
	if cfg.includeDotDotCandidates() {
		for _, name := range []string{".", ".."} {
			if matcher(name) {
				matches = append(matches, pathJoin2(dir, name))
			}
		}
	}
	return matches, nil
}

// ReadFields splits and returns n fields from s, like the "read" shell builtin.
// If raw is set, backslash escape sequences are not interpreted.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
type ReadFieldChar struct {
	Value   byte
	Escaped bool
}

type readFieldSpan struct {
	start int
	end   int
}

func readFieldString(chars []ReadFieldChar, start, end int) string {
	if start >= end {
		return ""
	}
	buf := make([]byte, end-start)
	for i := range buf {
		buf[i] = chars[start+i].Value
	}
	return string(buf)
}

func readIFSClass(cfg *Config, chars []ReadFieldChar, start int) (ifsRuneType, int) {
	if start >= len(chars) {
		return ifsRuneNone, 0
	}
	if chars[start].Escaped {
		return ifsRuneNone, 1
	}
	if chars[start].Value < utf8.RuneSelf {
		return cfg.classifyIFSByte(chars[start].Value), 1
	}

	var buf [utf8.UTFMax]byte
	buf[0] = chars[start].Value
	n := 1
	for n < utf8.UTFMax && start+n < len(chars) {
		if chars[start+n].Escaped {
			break
		}
		buf[n] = chars[start+n].Value
		n++
		if utf8.FullRune(buf[:n]) {
			break
		}
	}
	r, width := utf8.DecodeRune(buf[:n])
	if r == utf8.RuneError && width == 1 {
		return cfg.classifyIFSByte(chars[start].Value), 1
	}
	return cfg.classifyIFSRune(r), width
}

func trimReadLeadingIFSWhitespace(cfg *Config, chars []ReadFieldChar, start, end int) int {
	for start < end {
		class, width := readIFSClass(cfg, chars, start)
		if class != ifsRuneWhitespace {
			break
		}
		start += width
	}
	return start
}

func trimReadTrailingIFSWhitespace(cfg *Config, chars []ReadFieldChar, start, end int) int {
	for end > start {
		class, width := readIFSClass(cfg, chars, end-1)
		if class != ifsRuneWhitespace || width != 1 {
			break
		}
		end--
	}
	return end
}

func ReadFieldsFromChars(cfg *Config, chars []ReadFieldChar, n int) []string {
	cfg = prepareConfig(cfg)
	if n == 0 || len(chars) == 0 {
		return nil
	}

	start := trimReadLeadingIFSWhitespace(cfg, chars, 0, len(chars))
	end := trimReadTrailingIFSWhitespace(cfg, chars, start, len(chars))
	if start >= end {
		return nil
	}

	fields := make([]readFieldSpan, 0, 4)
	fieldStart := start
	for i := start; i < end; {
		class, width := readIFSClass(cfg, chars, i)
		if class == ifsRuneNone {
			i += width
			continue
		}

		fields = append(fields, readFieldSpan{start: fieldStart, end: i})
		if class == ifsRuneWhitespace {
			for i < end {
				nextClass, nextWidth := readIFSClass(cfg, chars, i)
				if nextClass != ifsRuneWhitespace {
					break
				}
				i += nextWidth
			}
			if i < end {
				nextClass, nextWidth := readIFSClass(cfg, chars, i)
				if nextClass == ifsRuneNonWhitespace {
					i += nextWidth
					for i < end {
						spaceClass, spaceWidth := readIFSClass(cfg, chars, i)
						if spaceClass != ifsRuneWhitespace {
							break
						}
						i += spaceWidth
					}
				}
			}
		} else {
			i += width
			for i < end {
				spaceClass, spaceWidth := readIFSClass(cfg, chars, i)
				if spaceClass != ifsRuneWhitespace {
					break
				}
				i += spaceWidth
			}
		}
		fieldStart = i
	}
	if fieldStart < end {
		fields = append(fields, readFieldSpan{start: fieldStart, end: end})
	}
	if len(fields) == 0 {
		return nil
	}

	switch {
	case n == -1 || len(fields) <= n:
		out := make([]string, len(fields))
		for i, field := range fields {
			out[i] = readFieldString(chars, field.start, field.end)
		}
		return out
	default:
		out := make([]string, 0, n)
		for i := 0; i < n-1; i++ {
			field := fields[i]
			out = append(out, readFieldString(chars, field.start, field.end))
		}
		last := fields[len(fields)-1]
		combinedEnd := last.end
		if last.start == last.end {
			combinedEnd = end
		}
		out = append(out, readFieldString(chars, fields[n-1].start, combinedEnd))
		return out
	}
}

func ReadFields(cfg *Config, s string, n int, raw bool) []string {
	chars := make([]ReadFieldChar, 0, len(s))
	escaped := false
	for i := 0; i < len(s); i++ {
		if !raw {
			if escaped {
				chars = append(chars, ReadFieldChar{Value: s[i], Escaped: true})
				escaped = false
				continue
			}
			if s[i] == '\\' {
				escaped = true
				continue
			}
		}
		chars = append(chars, ReadFieldChar{Value: s[i]})
	}
	return ReadFieldsFromChars(cfg, chars, n)
}
