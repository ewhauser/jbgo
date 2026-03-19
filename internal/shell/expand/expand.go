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
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ewhauser/gbash/internal/shell/pattern"
	"github.com/ewhauser/gbash/internal/shell/syntax"
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

	// CmdSubst expands a command substitution node, writing its standard
	// output to the provided [io.Writer].
	//
	// If nil, encountering a command substitution will result in an
	// UnexpectedCommandError.
	CmdSubst func(io.Writer, *syntax.CmdSubst) error

	// ProcSubst expands a process substitution node.
	ProcSubst func(*syntax.ProcSubst) (string, error)

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

	// NoUnset corresponds to the shell option which treats unset variables
	// as errors.
	NoUnset bool

	// ExtGlob corresponds to the shell option which allows using extended
	// pattern matching features when performing pathname expansion (globbing).
	ExtGlob bool

	bufferAlloc strings.Builder
	fieldAlloc  [4]fieldPart
	fieldsAlloc [4][]fieldPart

	ifs string
	// A pointer to a parameter expansion node, if we're inside one.
	// Necessary for ${LINENO}.
	curParam *syntax.ParamExp
}

// UnexpectedCommandError is returned if a command substitution is encountered
// when [Config.CmdSubst] is nil.
type UnexpectedCommandError struct {
	Node *syntax.CmdSubst
}

func (u UnexpectedCommandError) Error() string {
	return fmt.Sprintf("unexpected command substitution at %s", u.Node.Pos())
}

var zeroConfig = &Config{}

// TODO: note that prepareConfig is modifying the user's config in place,
// which doesn't feel right - we should make a copy.

func prepareConfig(cfg *Config) *Config {
	cfg = cmp.Or(cfg, zeroConfig)
	cfg.Env = cmp.Or(cfg.Env, FuncEnviron(func(string) string { return "" }))

	cfg.ifs = " \t\n"
	if vr := cfg.Env.Get("IFS"); vr.IsSet() {
		cfg.ifs = vr.String()
	}

	return cfg
}

func (cfg *Config) ifsRune(r rune) bool {
	for _, r2 := range cfg.ifs {
		if r == r2 {
			return true
		}
	}
	return false
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
			sb.WriteString(pattern.QuoteMeta(part.val, 0))
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
			if prefix, rest := cfg.expandUser(s, more); prefix != "" {
				s = prefix + rest
			}
		}
		s, _, _ = strings.Cut(s, "\x00")
		sb.WriteString(s)
	case *syntax.SglQuoted:
		s := part.Value
		if part.Dollar {
			s = decodeANSICString(s)
			s, _, _ = strings.Cut(s, "\x00")
		}
		sb.WriteString(pattern.QuoteMeta(s, 0))
	case *syntax.DblQuoted:
		field, err := cfg.wordField(part.Parts, quoteDouble)
		if err != nil {
			return err
		}
		for _, fp := range field {
			sb.WriteString(pattern.QuoteMeta(fp.val, 0))
		}
	case *syntax.ParamExp:
		if parts, ok, err := cfg.paramExpWordField(part, quoteNone); err != nil {
			return err
		} else if ok {
			for _, fp := range parts {
				if fp.quote > quoteNone {
					sb.WriteString(pattern.QuoteMeta(fp.val, 0))
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
		s, err := cfg.extGlobString(part)
		if err != nil {
			return err
		}
		sb.WriteString(s)
	default:
		panic(fmt.Sprintf("unhandled pattern part: %T", part))
	}
	return nil
}

func (cfg *Config) extGlobString(eg *syntax.ExtGlob) (string, error) {
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

func decodeANSICString(src string) string {
	var sb strings.Builder
	sb.Grow(len(src))

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

func (cfg *Config) splitFieldParts(parts []fieldPart) [][]fieldPart {
	fields := cfg.fieldsAlloc[:0]
	curField := cfg.fieldAlloc[:0]
	flush := func() {
		if len(curField) == 0 {
			return
		}
		fields = append(fields, curField)
		curField = nil
	}
	for _, part := range parts {
		if part.quote > quoteNone {
			curField = append(curField, part)
			continue
		}
		fieldStart := -1
		for i, r := range part.val {
			if cfg.ifsRune(r) {
				if fieldStart >= 0 {
					curField = append(curField, fieldPart{val: part.val[fieldStart:i]})
					fieldStart = -1
				}
				flush()
			} else if fieldStart < 0 {
				fieldStart = i
			}
		}
		if fieldStart >= 0 {
			curField = append(curField, fieldPart{val: part.val[fieldStart:]})
		}
	}
	flush()
	return fields
}

func (cfg *Config) escapedGlobField(parts []fieldPart) (escaped string, glob bool) {
	sb := cfg.strBuilder()
	for _, part := range parts {
		if part.quote > quoteNone {
			sb.WriteString(pattern.QuoteMeta(part.val, 0))
			continue
		}
		sb.WriteString(part.val)
		if pattern.HasMeta(part.val, 0) {
			glob = true
		}
	}
	if glob { // only copy the string if it will be used
		escaped = sb.String()
	}
	return escaped, glob
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

// FieldsSeq expands a number of words as if they were arguments in a shell
// command. This includes brace expansion, tilde expansion, parameter expansion,
// command substitution, arithmetic expansion, quote removal, and globbing.
func FieldsSeq(cfg *Config, words ...*syntax.Word) iter.Seq2[string, error] {
	cfg = prepareConfig(cfg)
	dir := cfg.envGet("PWD")
	return func(yield func(string, error) bool) {
		for _, word := range words {
			word := *word // make a copy, since SplitBraces replaces the Parts slice
			afterBraces := []*syntax.Word{&word}
			if syntax.SplitBraces(&word) {
				afterBraces = Braces(&word)
			}
			for _, word2 := range afterBraces {
				wfields, err := cfg.wordFields(word2.Parts)
				if err != nil {
					yield("", err)
					return
				}
				for _, field := range wfields {
					globPath, doGlob := cfg.escapedGlobField(field)
					if doGlob && cfg.ReadDir != nil {
						// Note that globbing requires keeping a slice state, so it doesn't
						// really benefit from using an iterator.
						matches, err := cfg.glob(dir, globPath)
						if err != nil {
							// We avoid [errors.As] as it allocates,
							// and we know that [Config.glob] returns [pattern.Regexp] errors without wrapping.
							if _, ok := err.(*pattern.SyntaxError); !ok {
								yield("", err)
								return
							}
						} else if len(matches) > 0 || cfg.NullGlob {
							for _, m := range matches {
								if !yield(m, nil) {
									return
								}
							}
							continue
						}
					}
					if !yield(cfg.fieldJoin(field), nil) {
						return
					}
				}
			}
		}
	}
}

type fieldPart struct {
	val   string
	quote quoteLevel
}

type quoteLevel uint

const (
	quoteNone quoteLevel = iota
	quoteDouble
	quoteHeredoc
	quoteSingle
)

func (cfg *Config) wordField(wps []syntax.WordPart, ql quoteLevel) ([]fieldPart, error) {
	var field []fieldPart
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 && ql == quoteNone {
				if prefix, rest := cfg.expandUser(s, len(wps) > 1); prefix != "" {
					// TODO: return two separate fieldParts,
					// like in wordFields?
					s = prefix + rest
				}
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
				fp.val = decodeANSICString(fp.val)
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
			n, err := Arithm(cfg, wp.X)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: strconv.Itoa(n)})
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
			pat, err := cfg.extGlobString(wp)
			if err != nil {
				return nil, err
			}
			field = append(field, fieldPart{val: pat})
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
	fields := cfg.fieldsAlloc[:0]
	curField := cfg.fieldAlloc[:0]
	allowEmpty := false
	flush := func() {
		if len(curField) == 0 {
			return
		}
		fields = append(fields, curField)
		curField = nil
	}
	splitAdd := func(val string) {
		fieldStart := -1
		for i, r := range val {
			if cfg.ifsRune(r) {
				if fieldStart >= 0 { // ending a field
					curField = append(curField, fieldPart{val: val[fieldStart:i]})
					fieldStart = -1
				}
				flush()
			} else {
				if fieldStart < 0 { // starting a new field
					fieldStart = i
				}
			}
		}
		if fieldStart >= 0 { // ending a field without IFS
			curField = append(curField, fieldPart{val: val[fieldStart:]})
		}
	}
	for i, wp := range wps {
		switch wp := wp.(type) {
		case *syntax.Lit:
			s := wp.Value
			if i == 0 {
				prefix, rest := cfg.expandUser(s, len(wps) > 1)
				curField = append(curField, fieldPart{
					quote: quoteSingle,
					val:   prefix,
				})
				s = rest
			}
			if strings.Contains(s, "\\") {
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
			curField = append(curField, fieldPart{val: s})
		case *syntax.SglQuoted:
			allowEmpty = true
			fp := fieldPart{quote: quoteSingle, val: wp.Value}
			if wp.Dollar {
				fp.val = decodeANSICString(fp.val)
				fp.val, _, _ = strings.Cut(fp.val, "\x00") // cut the string if format included \x00
			}
			curField = append(curField, fp)
		case *syntax.DblQuoted:
			if len(wp.Parts) == 1 {
				pe, _ := wp.Parts[0].(*syntax.ParamExp)
				if elems, ok, err := cfg.quotedElemFields(pe); err != nil {
					return nil, err
				} else if ok {
					for i, elem := range elems {
						if i > 0 {
							flush()
						}
						curField = append(curField, fieldPart{
							quote: quoteDouble,
							val:   elem,
						})
					}
					continue
				}
			}
			allowEmpty = true
			wfield, err := cfg.wordField(wp.Parts, quoteDouble)
			if err != nil {
				return nil, err
			}
			for _, part := range wfield {
				part.quote = quoteDouble
				curField = append(curField, part)
			}
		case *syntax.ParamExp:
			if fields2, ok, err := cfg.paramExpFields(wp); err != nil {
				return nil, err
			} else if ok {
				for i, field2 := range fields2 {
					if i > 0 {
						flush()
					}
					curField = append(curField, field2...)
				}
			} else {
				val, err := cfg.paramExp(wp, quoteNone)
				if err != nil {
					return nil, err
				}
				splitAdd(val)
			}
		case *syntax.CmdSubst:
			val, err := cfg.cmdSubst(wp)
			if err != nil {
				return nil, err
			}
			splitAdd(val)
		case *syntax.ArithmExp:
			n, err := Arithm(cfg, wp.X)
			if err != nil {
				return nil, err
			}
			curField = append(curField, fieldPart{val: strconv.Itoa(n)})
		case *syntax.ProcSubst:
			procPath, err := cfg.ProcSubst(wp)
			if err != nil {
				return nil, err
			}
			splitAdd(procPath)
		case *syntax.ExtGlob:
			if !cfg.ExtGlob {
				return nil, fmt.Errorf("extended globbing operator used without the \"extglob\" option set")
			}
			// We don't translate or interpret the pattern here in any way;
			// that's done later when globbing takes place via [pattern.Regexp].
			// Here, all we do is keep the extended globbing expression in string form.
			//
			// TODO(v4): perhaps the syntax parser should keep extended globbing expressions
			// as plain literal strings, because a custom node is not particularly helpful.
			// It's not like other globbing operators like `*` or `**` get their own nodes.
			pat, err := cfg.extGlobString(wp)
			if err != nil {
				return nil, err
			}
			curField = append(curField, fieldPart{val: pat})
		default:
			panic(fmt.Sprintf("unhandled word part: %T", wp))
		}
	}
	flush()
	if allowEmpty && len(fields) == 0 {
		fields = append(fields, curField)
	}
	return fields, nil
}

// quotedElemFields returns the list of elements resulting from a quoted
// parameter expansion that should be treated especially, like "${foo[@]}".
func (cfg *Config) quotedElemFields(pe *syntax.ParamExp) ([]string, bool, error) {
	if pe == nil || pe.Length || pe.Width || pe.IsSet {
		return nil, false, nil
	}
	name := pe.Param.Value
	if pe.Excl {
		if pe.Names == 0 && pe.Index == nil {
			state, err := cfg.paramExpState(pe)
			if err != nil {
				return nil, false, err
			}
			if state.orig.Kind != NameRef && state.str != "" {
				target, err := indirectParamExp(state.str)
				if err != nil {
					return nil, false, err
				}
				if quotedIndirectArrayTarget(target) {
					if fields, _, ok := cfg.quotedArrayFields(target); ok {
						return fields, true, nil
					}
				}
			}
		}
		switch pe.Names {
		case syntax.NamesPrefixWords: // "${!prefix@}"
			return cfg.namesByPrefix(pe.Param.Value), true, nil
		case syntax.NamesPrefix: // "${!prefix*}"
			return nil, false, nil
		}
		switch subscriptLit(pe.Index) {
		case "@": // "${!name[@]}"
			switch vr := cfg.Env.Get(name); vr.Kind {
			case Indexed:
				// TODO: if an indexed array only has elements 0 and 10,
				// we should not return all indices in between those.
				keys := make([]string, 0, len(vr.List))
				for key := range vr.List {
					keys = append(keys, strconv.Itoa(key))
				}
				return keys, true, nil
			case Associative:
				return sortedMapKeys(vr.Map), true, nil
			}
		}
		return nil, false, nil
	}
	fields, elems, ok := cfg.quotedArrayFields(pe)
	if !ok {
		return nil, false, nil
	}
	if pe.Exp == nil {
		return fields, true, nil
	}

	hasElems := len(elems) > 0
	isAt := pe.Param.Value == "@" || subscriptLit(pe.Index) == "@"
	isStar := pe.Param.Value == "*" || subscriptLit(pe.Index) == "*"
	null := !hasElems
	if isAt && len(elems) == 1 && elems[0] == "" {
		null = true
	}
	if isStar && len(fields) == 1 && fields[0] == "" {
		null = true
	}
	switch pe.Exp.Op {
	case syntax.AlternateUnset, syntax.AlternateUnsetOrNull:
		if pe.Exp.Op == syntax.AlternateUnset && hasElems || pe.Exp.Op == syntax.AlternateUnsetOrNull && !null {
			word, err := cfg.quotedParamWord(pe.Exp.Word)
			return word, true, err
		}
		return fields, true, nil
	case syntax.DefaultUnset, syntax.DefaultUnsetOrNull:
		if pe.Exp.Op == syntax.DefaultUnset && hasElems || pe.Exp.Op == syntax.DefaultUnsetOrNull && !null {
			return fields, true, nil
		}
		word, err := cfg.quotedParamWord(pe.Exp.Word)
		return word, true, err
	case syntax.ErrorUnset, syntax.ErrorUnsetOrNull:
		if pe.Exp.Op == syntax.ErrorUnset && hasElems || pe.Exp.Op == syntax.ErrorUnsetOrNull && !null {
			return fields, true, nil
		}
		return nil, false, nil
	case syntax.AssignUnset, syntax.AssignUnsetOrNull:
		if pe.Exp.Op == syntax.AssignUnset && hasElems || pe.Exp.Op == syntax.AssignUnsetOrNull && !null {
			return fields, true, nil
		}
		return nil, false, nil
	default:
		return fields, true, nil
	}
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
		elems := cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
		return []string{cfg.ifsJoin(elems)}, elems, true
	case "@": // "${@}" or "${@:offset:length}"
		elems := cfg.sliceElems(pe, cfg.Env.Get(name).List, true)
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
			elems := cfg.sliceElems(pe, vr.List, false)
			return elems, elems, true
		case Associative:
			elems := sortedMapValues(vr.Map)
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
			elems := cfg.sliceElems(pe, vr.List, false)
			return []string{cfg.ifsJoin(elems)}, elems, true
		}
		if vr.Kind == Associative {
			elems := sortedMapValues(vr.Map)
			return []string{cfg.ifsJoin(elems)}, elems, true
		}
	}
	return nil, nil, false
}

// sliceElems applies ${var:offset:length} slicing to a list of elements.
// When positional is true, $0 is prepended to the list before slicing.
// In bash, positional parameter offsets ($@ and $*) are 1-based and
// offset 0 includes $0 (the shell or script name). Negative offsets
// count from $# + 1, so $0 is reachable via large enough negative values.
func (cfg *Config) sliceElems(pe *syntax.ParamExp, elems []string, positional bool) []string {
	if pe.Slice == nil {
		return elems
	}
	if positional {
		elems = append([]string{cfg.Env.Get("0").Str}, elems...)
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
		elems = elems[slicePos(offset):]
	}
	if pe.Slice.Length != nil {
		length, err := Arithm(cfg, pe.Slice.Length)
		if err != nil {
			return elems
		}
		elems = elems[:slicePos(length)]
	}
	return elems
}

func (cfg *Config) expandUser(field string, moreFields bool) (prefix, rest string) {
	name, ok := strings.CutPrefix(field, "~")
	if !ok {
		// No tilde prefix to expand, e.g. "foo".
		return "", field
	}
	i := strings.IndexByte(name, '/')
	if i < 0 && moreFields {
		// There is a tilde prefix, but followed by more fields, e.g. "~'foo'".
		// We only proceed if an unquoted slash was found in this field, e.g. "~/'foo'".
		return "", field
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

		if vr := cfg.Env.Get("HOME"); vr.IsSet() {
			return vr.String(), rest
		}

		if runtime.GOOS == "windows" {
			if vr := cfg.Env.Get("USERPROFILE"); vr.IsSet() {
				return vr.String(), rest
			}
		}
		return "", field
	}

	if vr := cfg.Env.Get("HOME " + name); vr.IsSet() {
		return vr.String(), rest
	}
	return "", field
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
		case !pattern.HasMeta(part, 0):
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
				if cfg.DotGlob {
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
		if cfg.DotGlob {
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
	slices.Sort(matches)
	// Remove any empty matches left behind from "**".
	if len(matches) > 0 && matches[0] == "" {
		matches = matches[1:]
	}
	return matches, nil
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
	return matches, nil
}

// ReadFields splits and returns n fields from s, like the "read" shell builtin.
// If raw is set, backslash escape sequences are not interpreted.
//
// The config specifies shell expansion options; nil behaves the same as an
// empty config.
func ReadFields(cfg *Config, s string, n int, raw bool) []string {
	cfg = prepareConfig(cfg)
	type pos struct {
		start, end int
	}
	var fpos []pos

	runes := make([]rune, 0, len(s))
	infield := false
	esc := false
	for _, r := range s {
		if infield {
			if cfg.ifsRune(r) && (raw || !esc) {
				fpos[len(fpos)-1].end = len(runes)
				infield = false
			}
		} else {
			if !cfg.ifsRune(r) && (raw || !esc) {
				fpos = append(fpos, pos{start: len(runes), end: -1})
				infield = true
			}
		}
		if r == '\\' {
			if raw || esc {
				runes = append(runes, r)
			}
			esc = !esc
			continue
		}
		runes = append(runes, r)
		esc = false
	}
	if len(fpos) == 0 {
		return nil
	}
	if infield {
		fpos[len(fpos)-1].end = len(runes)
	}

	switch {
	case n == 1:
		// include heading/trailing IFSs
		fpos[0].start, fpos[0].end = 0, len(runes)
		fpos = fpos[:1]
	case n != -1 && n < len(fpos):
		// combine to max n fields
		fpos[n-1].end = fpos[len(fpos)-1].end
		fpos = fpos[:n]
	}

	fields := make([]string, len(fpos))
	for i, p := range fpos {
		fields[i] = string(runes[p.start:p.end])
	}
	return fields
}
