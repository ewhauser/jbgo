// Copyright (c) 2016, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package syntax

import (
	"bytes"
	"fmt"
	"io"
	"iter"
	"math/bits"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParserOption is a function which can be passed to NewParser
// to alter its behavior. To apply option to existing Parser
// call it directly, for example KeepComments(true)(parser).
type ParserOption func(*Parser)

// AliasSpec describes a shell alias value as raw shell source.
//
// Value should preserve the exact alias replacement text, including any
// trailing blanks or newlines.
type AliasSpec struct {
	Value string
}

// EndsWithBlank reports whether the alias replacement should keep alias
// expansion enabled for the next shell word.
func (a AliasSpec) EndsWithBlank() bool {
	return strings.TrimRight(a.Value, " \t") != a.Value
}

// AliasResolver returns a raw alias replacement for an unquoted command word.
type AliasResolver func(name string) (AliasSpec, bool)

// KeepComments makes the parser parse comments and attach them to
// nodes, as opposed to discarding them.
func KeepComments(enabled bool) ParserOption {
	return func(p *Parser) { p.keepComments = enabled }
}

// ExpandAliases configures parse-time alias expansion for unquoted command
// words.
func ExpandAliases(resolver AliasResolver) ParserOption {
	return func(p *Parser) { p.aliasResolver = resolver }
}

// LangVariant describes a shell language variant to use when tokenizing and
// parsing shell code. The zero value is [LangBash].
//
// This type implements [flag.Value] so that it can be used as a CLI flag.
type LangVariant int

// TODO(v4): the zero value should be left as an unset and invalid value.
// TODO(v4): the type should be uint32 now that we use this as a bitset;
// an unsigned integer is clearer, and being agnostic to uint size avoids issues.

const (
	// LangBash corresponds to the GNU Bash language, as described in its
	// manual at https://www.gnu.org/software/bash/manual/bash.html.
	//
	// We currently follow Bash version 5.2.
	//
	// Its string representation is "bash".
	LangBash LangVariant = 1 << iota

	// LangPOSIX corresponds to the POSIX Shell language, as described at
	// https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html.
	//
	// Its string representation is "posix" or "sh".
	LangPOSIX

	// LangMirBSDKorn corresponds to the MirBSD Korn Shell, also known as
	// mksh, as described at http://www.mirbsd.org/htman/i386/man1/mksh.htm.
	// Note that it shares some features with Bash, due to the shared
	// ancestry that is ksh.
	//
	// We currently follow mksh version 59.
	//
	// Its string representation is "mksh".
	LangMirBSDKorn

	// LangBats corresponds to the Bash Automated Testing System language,
	// as described at https://github.com/bats-core/bats-core. Note that
	// it's just a small extension of the Bash language.
	//
	// Its string representation is "bats".
	LangBats

	// LangZsh corresponds to the Z shell, as described at https://www.zsh.org/.
	//
	// Note that its support in the syntax package is experimental and
	// incomplete for now. See https://github.com/mvdan/sh/issues/120.
	//
	// We currently follow Zsh version 5.9.
	//
	// Its string representation is "zsh".
	LangZsh

	// LangAuto corresponds to automatic language detection,
	// commonly used by end-user applications like shfmt,
	// which can guess a file's language variant given its filename or shebang.
	//
	// At this time, [Variant] does not support LangAuto.
	LangAuto

	// langBashLegacy is what [LangBash] used to be, when it was zero.
	// We still support it for the sake of backwards compatibility.
	langBashLegacy LangVariant = 0

	// langResolvedVariants contains all known variants except [LangAuto],
	// which is meant to resolve to another variant.
	langResolvedVariants = LangBash | LangPOSIX | LangMirBSDKorn | LangBats | LangZsh

	// langResolvedVariantsCount is langResolvedVariants.count() as a constant.
	// TODO: Can we compute this as a constant expression somehow?
	// For example, if we had log2, we could do log2(LangAuto).
	langResolvedVariantsCount = 5

	// langBashLike contains Bash plus all variants which are extensions of it.
	langBashLike = LangBash | LangBats
)

// Variant changes the shell language variant that the parser will
// accept.
//
// The passed language variant must be one of the constant values defined in
// this package.
func Variant(l LangVariant) ParserOption {
	switch l {
	case langBashLegacy:
		l = LangBash
	case LangBash, LangPOSIX, LangMirBSDKorn, LangBats, LangZsh:
	case LangAuto:
		panic("LangAuto is not supported by the parser at this time")
	default:
		panic(fmt.Sprintf("unknown shell language variant: %#b", l))
	}
	return func(p *Parser) { p.lang = l }
}

func (l LangVariant) String() string {
	switch l {
	case langBashLegacy, LangBash:
		return "bash"
	case LangPOSIX:
		return "posix"
	case LangMirBSDKorn:
		return "mksh"
	case LangBats:
		return "bats"
	case LangZsh:
		return "zsh"
	case LangAuto:
		return "auto"
	}
	return "unknown shell language variant"
}

func (l *LangVariant) Set(s string) error {
	switch s {
	case "bash":
		*l = LangBash
	case "posix", "sh":
		*l = LangPOSIX
	case "mksh":
		*l = LangMirBSDKorn
	case "bats":
		*l = LangBats
	case "zsh":
		*l = LangZsh
	case "auto":
		*l = LangAuto
	default:
		return fmt.Errorf("unknown shell language variant: %q", s)
	}
	return nil
}

func (l LangVariant) in(l2 LangVariant) bool {
	return l&l2 == l
}

func (l LangVariant) count() int {
	return bits.OnesCount32(uint32(l))
}

func (l LangVariant) index() int {
	return bits.TrailingZeros32(uint32(l))
}

func (l LangVariant) bits() iter.Seq[LangVariant] {
	return func(yield func(LangVariant) bool) {
		for n := LangVariant(1); n < langResolvedVariants; n <<= 1 {
			if l&n == 0 {
				continue
			}
			if !yield(n) {
				return
			}
		}
	}
}

// StopAt configures the lexer to stop at an arbitrary word, treating it
// as if it were the end of the input. It can contain any characters
// except whitespace, and cannot be over four bytes in size.
//
// This can be useful to embed shell code within another language, as
// one can use a special word to mark the delimiters between the two.
//
// As a word, it will only apply when following whitespace or a
// separating token. For example, StopAt("$$") will act on the inputs
// "foo $$" and "foo;$$", but not on "foo '$$'".
//
// The match is done by prefix, so the example above will also act on
// "foo $$bar".
func StopAt(word string) ParserOption {
	if len(word) > 4 {
		panic("stop word can't be over four bytes in size")
	}
	if strings.ContainsAny(word, " \t\n\r") {
		panic("stop word can't contain whitespace characters")
	}
	return func(p *Parser) { p.stopAt = []byte(word) }
}

// RecoverErrors allows the parser to skip up to a maximum number of
// errors in the given input on a best-effort basis.
// This can be useful to tab-complete an interactive shell prompt,
// or when providing diagnostics on slightly incomplete shell source.
//
// Currently, this only helps with mandatory tokens from the shell grammar
// which are not present in the input. They result in position fields
// or nodes whose position report [Pos.IsRecovered] as true.
//
// For example, given the input
//
//	(foo |
//
// the result will contain two recovered positions; first, the pipe requires
// a statement to follow, and as [Stmt.Pos] reports, the entire node is recovered.
// Second, the subshell needs to be closed, so [Subshell.Rparen] is recovered.
func RecoverErrors(maximum int) ParserOption {
	return func(p *Parser) { p.recoverErrorsMax = maximum }
}

// LegacyBashCompat enables parser diagnostics that match the older bash
// behavior used by the in-shell `bash` builtin conformance path.
func LegacyBashCompat(enabled bool) ParserOption {
	return func(p *Parser) { p.legacyBashCompat = enabled }
}

// ParseExtGlob controls whether the parser should recognize Bash extended glob
// operators like `@(foo)` and `!(bar)` as dedicated syntax nodes.
func ParseExtGlob(enabled bool) ParserOption {
	return func(p *Parser) { p.parseExtGlob = enabled }
}

// NewParser allocates a new [Parser] and applies any number of options.
func NewParser(options ...ParserOption) *Parser {
	p := &Parser{
		lang:         LangBash,
		parseExtGlob: true,
	}
	for _, opt := range options {
		opt(p)
	}
	return p
}

// Parse reads and parses a shell program with an optional name. It
// returns the parsed program if no issues were encountered. Otherwise,
// an error is returned. Reads from r are buffered.
//
// Parse can be called more than once, but not concurrently. That is, a
// Parser can be reused once it is done working.
func (p *Parser) Parse(r io.Reader, name string) (*File, error) {
	p.reset()
	p.f = &File{Name: name}
	p.src = r
	p.rune()
	p.next()
	p.f.Stmts, p.f.Last = p.stmtList()
	if p.err == nil {
		// EOF immediately after heredoc word so no newline to
		// trigger the parsing error.
		p.doHeredocs()
	}
	return p.f, p.err
}

// Stmts is a pre-iterators API which now wraps [Parser.StmtsSeq].
//
// Deprecated: use [Parser.StmtsSeq].
func (p *Parser) Stmts(r io.Reader, fn func(*Stmt) bool) error {
	for stmt, err := range p.StmtsSeq(r) {
		if err != nil {
			return err
		}
		if !fn(stmt) {
			break
		}
	}
	return nil
}

// StmtsSeq reads and parses statements one at a time via an iterator.
func (p *Parser) StmtsSeq(r io.Reader) iter.Seq2[*Stmt, error] {
	p.reset()
	p.f = &File{}
	p.src = r
	return func(yield func(*Stmt, error) bool) {
		p.rune()
		p.next()
		p.stmts(yield)
		if p.err == nil {
			// EOF immediately after heredoc word so no newline to
			// trigger the parsing error.
			p.doHeredocs()
		}
		if p.err != nil {
			// Yield any final error from the parser.
			yield(nil, p.err)
		}
	}
}

type wrappedReader struct {
	p  *Parser
	rd io.Reader

	lastLine    int64
	accumulated []*Stmt
	yield       func([]*Stmt, error) bool
}

func (w *wrappedReader) Read(p []byte) (n int, err error) {
	// If we lexed a newline for the first time, we just finished a line, so
	// we may need to give a callback for the edge cases below not covered
	// by [Parser.Stmts].
	if (w.p.r == '\n' || w.p.r == escNewl) && w.p.line > w.lastLine {
		if w.p.Incomplete() {
			// Incomplete statement; call back to print "> ".
			if !w.yield(w.accumulated, w.p.err) {
				return 0, io.EOF
			}
		} else if len(w.accumulated) == 0 {
			// Nothing was parsed; call back to print another "$ ".
			if !w.yield(nil, w.p.err) {
				return 0, io.EOF
			}
		}
		w.lastLine = w.p.line
	}
	return w.rd.Read(p)
}

// Interactive is a pre-iterators API which now wraps [Parser.InteractiveSeq].
//
// Deprecated: use [Parser.InteractiveSeq].
func (p *Parser) Interactive(r io.Reader, fn func([]*Stmt) bool) error {
	for stmts, err := range p.InteractiveSeq(r) {
		if err != nil {
			return err
		}
		if !fn(stmts) {
			break
		}
	}
	return nil
}

// InteractiveSeq implements what is necessary to parse statements in an
// interactive shell. The parser will call the given function under two
// circumstances outlined below.
//
// If a line containing any number of statements is parsed, the function will be
// called with said statements.
//
// If a line ending in an incomplete statement is parsed, the function will be
// called with any fully parsed statements, and [Parser.Incomplete] will return true.
//
// One can imagine a simple interactive shell implementation as follows:
//
//	fmt.Fprintf(os.Stdout, "$ ")
//	parser.Interactive(os.Stdin, func(stmts []*syntax.Stmt) bool {
//		if parser.Incomplete() {
//			fmt.Fprintf(os.Stdout, "> ")
//			return true
//		}
//		run(stmts)
//		fmt.Fprintf(os.Stdout, "$ ")
//		return true
//	}
//
// If the callback function returns false, parsing is stopped and the function
// is not called again.
func (p *Parser) InteractiveSeq(r io.Reader) iter.Seq2[[]*Stmt, error] {
	return func(yield func([]*Stmt, error) bool) {
		w := wrappedReader{p: p, rd: r, yield: yield}
		for stmts, err := range p.StmtsSeq(&w) {
			w.accumulated = append(w.accumulated, stmts)
			if err != nil {
				if !yield(w.accumulated, err) {
					break
				}
				// If the caller wishes, they can continue in the presence of parse errors.
				// TODO: does this even work? Write tests for it. This only came up
				continue
			}
			// We finished parsing a statement and we're at a newline token,
			// so we finished fully parsing a number of statements. Call
			// back to run the statements and print "$ ".
			if p.tok == _Newl {
				if !yield(w.accumulated, nil) {
					break
				}
				w.accumulated = w.accumulated[:0]
				// The callback above would already print "$ ", so we
				// don't want the subsequent wrappedReader.Read to cause
				// another "$ " print thinking that nothing was parsed.
				w.lastLine = w.p.line + 1
			}
		}
	}
}

// Words is a pre-iterators API which now wraps [Parser.WordsSeq].
//
// Deprecated: use [Parser.WordsSeq].
func (p *Parser) Words(r io.Reader, fn func(*Word) bool) error {
	for w, err := range p.WordsSeq(r) {
		if err != nil {
			return err
		}
		if !fn(w) {
			break
		}
	}
	return nil
}

// WordsSeq reads and parses a sequence of words alongside any error encountered.
//
// Newlines are skipped, meaning that multi-line input will work fine. If the
// parser encounters a token that isn't a word, such as a semicolon, an error
// will be returned.
//
// Note that the lexer doesn't currently tokenize spaces, so it may need to read
// a non-space byte such as a newline or a letter before finishing the parsing
// of a word. This will be fixed in the future.
func (p *Parser) WordsSeq(r io.Reader) iter.Seq2[*Word, error] {
	p.reset()
	p.f = &File{}
	p.src = r
	return func(yield func(*Word, error) bool) {
		p.rune()
		p.next()
		for {
			p.got(_Newl)
			w := p.getWord()
			if w == nil {
				if p.tok != _EOF {
					p.curErr("%#q is not a valid word", p.tok)
				}
				if p.err != nil {
					yield(nil, p.err)
				}
				return
			}
			if !yield(w, nil) {
				return
			}
		}
	}
}

// Document parses a single here-document word. That is, it parses the input as
// if they were lines following a <<EOF redirection.
//
// In practice, this is the same as parsing the input as if it were within
// double quotes, but without having to escape all double quote characters.
// Similarly, the here-document word parsed here cannot be ended by any
// delimiter other than reaching the end of the input.
func (p *Parser) Document(r io.Reader) (*Word, error) {
	return p.document(r, NewPos(0, 1, 1))
}

func (p *Parser) document(r io.Reader, start Pos) (*Word, error) {
	p.reset()
	p.f = &File{}
	p.src = r
	p.offs = int64(start.Offset())
	p.line = int64(start.Line())
	p.col = int64(start.Col())
	p.rune()
	p.quote = hdocBody
	p.parsingDoc = true
	p.next()
	w := p.getWord()
	return w, p.err
}

// Arithmetic parses a single arithmetic expression. That is, as if the input
// were within the $(( and )) tokens.
func (p *Parser) Arithmetic(r io.Reader) (ArithmExpr, error) {
	p.reset()
	p.f = &File{}
	p.src = r
	p.quote = arithmExpr
	p.rune()
	p.next()
	expr := p.arithmExpr(false)
	if p.err == nil && p.tok != _EOF {
		switch p.tok {
		case _Lit, _LitWord:
			p.curErr("not a valid arithmetic operator: %#q", p.val)
		case leftBrack:
			p.curErr("%#q must follow a name", leftBrack)
		case colon:
			p.curErr("ternary operator missing %#q before %#q", quest, colon)
		default:
			p.curErr("not a valid arithmetic operator: %#q", p.tok)
		}
	}
	return expr, p.err
}

// ParseVarRef is a convenience function that parses a shell variable reference
// from a string, such as "foo", "a[1]", or `assoc["k"]`.
func ParseVarRef(src string) (*VarRef, error) {
	p := NewParser()
	return p.VarRef(strings.NewReader(src))
}

// VarRef parses a single shell variable reference, such as "foo", "a[1]", or
// `assoc["k"]`.
func (p *Parser) VarRef(r io.Reader) (*VarRef, error) {
	p.reset()
	p.f = &File{}
	p.src = r
	p.rune()
	p.next()
	ref := p.varRef()
	if p.err == nil && p.tok != _EOF {
		p.curErr("unexpected token in variable reference: %#q", p.tok)
	}
	return ref, p.err
}

// DeclOperand parses a single Bash-style declaration operand, such as "-a",
// "foo", "foo=bar", or `assoc["k"]=value`.
func (p *Parser) DeclOperand(r io.Reader) (DeclOperand, error) {
	p.reset()
	p.f = &File{}
	p.src = r
	p.rune()
	p.next()
	op := p.declOperand(false)
	if p.err == nil && p.tok != _EOF {
		p.curErr("unexpected token in declaration operand: %#q", p.tok)
	}
	return op, p.err
}

// Parser holds the internal state of the parsing mechanism of a
// program.
type Parser struct {
	src io.Reader
	bs  []byte // current chunk of read bytes
	bsp uint   // offset within [Parser.bs] for the rune after [Parser.r]
	r   rune   // next rune; [utf8.RuneSelf] when it went past EOF, or we stopped
	w   int    // width of [Parser.r]

	sourceBs   []byte
	sourceOffs int64

	f *File

	spaced       bool              // whether [Parser.tok] has whitespace on its left
	tokSeparator CallExprSeparator // whitespace trivia on the left of [Parser.tok]

	err     error // lexer/parser error
	readErr error // got a read error, but bytes left
	readEOF bool  // [Parser.src] already gave us an [io.EOF] error

	tok token  // current token
	val string // current value (valid if tok is _Lit*)

	// position of [Parser.r], to be converted to [Parser.pos] later
	offs, line, col int64

	pos Pos // position of tok

	quote   quoteState // current lexer state
	eqlOffs int        // position of '=' in [Parser.val] (a literal)

	keepComments bool
	lang         LangVariant
	// legacyBashCompat switches a handful of [[ =~ ]] diagnostics to match
	// the older bash behavior observed through the bash builtin conformance path.
	legacyBashCompat bool
	parseExtGlob     bool

	stopAt []byte

	recoveredErrors  int
	recoverErrorsMax int

	forbidNested bool

	// list of pending heredoc bodies
	buriedHdocs int
	heredocs    []*Redirect

	hdocStops []heredocStop // stack of open heredoc closer matchers

	parsingDoc bool // true if using [Parser.Document]

	// openNodes tracks how many entire statements or words we're currently parsing.
	// A non-zero number means that we require certain tokens or words before
	// reaching EOF, used for [Parser.Incomplete].
	openNodes int
	// openBquotes is how many levels of backquotes are open at the moment.
	openBquotes int
	// openBquoteDquotes tracks how many open backquote levels were entered
	// from within double quotes, where backslash-quote needs the extra
	// backquote escaping semantics that bash applies.
	openBquoteDquotes int

	// lastBquoteEsc is how many times the last backquote token was escaped
	lastBquoteEsc int

	accComs []Comment
	curComs *[]Comment

	litBatch  []Lit
	wordBatch []wordAlloc

	readBuf        [bufSize]byte
	litBuf         [bufSize]byte
	litBs          []byte
	wordRawBs      []byte
	captureWordRaw bool

	pendingArrayWord    string
	pendingArrayWordPos Pos

	aliasResolver   AliasResolver
	aliasChain      []*AliasExpansion
	aliasBlankNext  bool
	aliasInputStack []aliasInputState
	aliasActive     map[string]int
	aliasSource     *AliasExpansion
	tokAliasChain   []*AliasExpansion

	// Probe parsers used while resolving $(( ambiguity skip nested $(( probes
	// so malformed arithmetic words cannot recurse without consuming input.
	parenAmbiguityDisabled   bool
	parenAmbiguityProbeDepth int

	pendingHeredocWarningPos    Pos
	pendingHeredocWarningWanted string
}

type heredocStop struct {
	word  []byte
	close heredocCloseCapture
}

type heredocCloseCapture struct {
	pos, end      Pos
	raw           string
	candidate     *HeredocCloseCandidate
	matched       bool
	eofTerminated bool
	trailingText  string
	indentTabs    uint16
}

type aliasInputState struct {
	src     io.Reader
	bs      []byte
	bsp     uint
	r       rune
	w       int
	offs    int64
	line    int64
	col     int64
	readErr error
	readEOF bool

	aliasChain     []*AliasExpansion
	aliasBlankNext bool
	aliasSource    *AliasExpansion
}

type parserCursorSnapshot struct {
	tok  token
	pos  Pos
	offs int64
	bsp  uint
	r    rune
}

func (p *Parser) cursorSnapshot() parserCursorSnapshot {
	return parserCursorSnapshot{
		tok:  p.tok,
		pos:  p.pos,
		offs: p.offs,
		bsp:  p.bsp,
		r:    p.r,
	}
}

func (s parserCursorSnapshot) progressed(p *Parser) bool {
	return s.tok != p.tok || s.pos != p.pos || s.offs != p.offs || s.bsp != p.bsp || s.r != p.r
}

// Incomplete reports whether the parser needs more input bytes
// to finish properly parsing a statement or word.
//
// It is only safe to call while the parser is blocked on a read. For an example
// use case, see [Parser.Interactive].
func (p *Parser) Incomplete() bool {
	// If there are any open nodes, we need to finish them.
	// If we're constructing a literal, we need to finish it.
	return p.openNodes > 0 || len(p.litBs) > 0
}

const bufSize = 1 << 10

func (p *Parser) reset() {
	p.tok, p.val = illegalTok, ""
	p.eqlOffs = 0
	p.bs, p.bsp = nil, 0
	p.sourceBs = nil
	p.sourceOffs = 0
	p.offs, p.line, p.col = 0, 1, 1
	p.r, p.w = 0, 0
	p.err, p.readErr, p.readEOF = nil, nil, false
	p.quote, p.forbidNested = noState, false
	p.openNodes = 0
	p.recoveredErrors = 0
	p.heredocs, p.buriedHdocs = p.heredocs[:0], 0
	p.hdocStops = nil
	p.parsingDoc = false
	p.openBquotes = 0
	p.openBquoteDquotes = 0
	p.accComs = nil
	p.accComs, p.curComs = nil, &p.accComs
	p.litBatch = nil
	p.wordBatch = nil
	p.litBs = nil
	p.wordRawBs = nil
	p.captureWordRaw = false
	p.pendingArrayWord = ""
	p.pendingArrayWordPos = Pos{}
	p.aliasChain = nil
	p.aliasBlankNext = false
	p.aliasInputStack = p.aliasInputStack[:0]
	p.aliasSource = nil
	p.tokAliasChain = nil
	p.tokSeparator = CallExprSeparator{}
	p.pendingHeredocWarningPos = Pos{}
	p.pendingHeredocWarningWanted = ""
	p.parenAmbiguityProbeDepth = 0
	if p.aliasActive != nil {
		clear(p.aliasActive)
	}
}

// nextPos returns the position of the next rune, [Parser.r].
func (p *Parser) nextPos() Pos {
	// Basic protection against offset overflow;
	// note that an offset of 0 is valid, so we leave the maximum.
	offset := min(p.offs+int64(p.bsp)-int64(p.w), offsetMax)
	var line, col uint
	if p.line <= lineMax {
		line = uint(p.line)
	}
	if p.col <= colMax {
		col = uint(p.col)
	}
	return NewPos(uint(offset), line, col)
}

func (p *Parser) lit(pos Pos, val string) *Lit {
	if len(p.litBatch) == 0 {
		p.litBatch = make([]Lit, 32)
	}
	l := &p.litBatch[0]
	p.litBatch = p.litBatch[1:]
	l.ValuePos = pos
	l.ValueEnd = p.nextPos()
	l.Value = val
	return l
}

func (p *Parser) rawLit(start, end Pos, val string) *Lit {
	if len(p.litBatch) == 0 {
		p.litBatch = make([]Lit, 32)
	}
	l := &p.litBatch[0]
	p.litBatch = p.litBatch[1:]
	l.ValuePos = start
	l.ValueEnd = end
	l.Value = val
	return l
}

func (p *Parser) emptyWord(pos Pos) *Word {
	return p.wordOne(p.rawLit(pos, pos, ""))
}

type wordAlloc struct {
	word  Word
	parts [1]WordPart
}

func (p *Parser) wordAnyNumber() *Word {
	if len(p.wordBatch) == 0 {
		p.wordBatch = make([]wordAlloc, 32)
	}
	alloc := &p.wordBatch[0]
	p.wordBatch = p.wordBatch[1:]
	w := &alloc.word
	w.Parts = p.wordParts(alloc.parts[:0])
	w.AliasExpansions = append(w.AliasExpansions[:0], p.tokAliasChain...)
	return p.finishWord(w)
}

func (p *Parser) wordOne(part WordPart) *Word {
	if len(p.wordBatch) == 0 {
		p.wordBatch = make([]wordAlloc, 32)
	}
	alloc := &p.wordBatch[0]
	p.wordBatch = p.wordBatch[1:]
	w := &alloc.word
	w.Parts = alloc.parts[:1]
	w.Parts[0] = part
	w.AliasExpansions = append(w.AliasExpansions[:0], p.tokAliasChain...)
	return p.finishWord(w)
}

func (p *Parser) wordOneWithAliases(part WordPart, aliases []*AliasExpansion) *Word {
	w := p.wordOne(part)
	w.AliasExpansions = append(w.AliasExpansions[:0], aliases...)
	return w
}

func (p *Parser) call(w *Word) *CallExpr {
	var alloc struct {
		ce CallExpr
		ws [4]*Word
	}
	ce := &alloc.ce
	ce.Args = alloc.ws[:1]
	ce.Args[0] = w
	return ce
}

type quoteState uint32

const (
	// The initial state of the parser.
	noState quoteState = 1 << iota

	// Used when parsing parameter expansions; use with [Parser.rune],
	// [Parser.next] always returns [illegalTok].
	runeByRune

	// unquotedWordCont exists purely so that the '#' in $foo#bar does not
	// get parsed as a comment; it's a tiny variation on [noState].
	unquotedWordCont

	subCmd
	subCmdBckquo
	dblQuotes
	hdocWord
	hdocBody
	hdocBodyTabs
	arithmExpr
	arithmExprLet
	arithmExprCmd
	testExpr
	switchCase
	paramExpArithm
	paramExpRepl
	paramExpExp
	arrayElems

	allKeepSpaces = runeByRune | paramExpRepl | dblQuotes | hdocBody |
		hdocBodyTabs | paramExpRepl | paramExpExp
	allRegTokens = noState | unquotedWordCont | subCmd | subCmdBckquo | hdocWord |
		switchCase | arrayElems | testExpr
	allArithmExpr = arithmExpr | arithmExprLet | arithmExprCmd | paramExpArithm
	allParamExp   = paramExpArithm | paramExpRepl | paramExpExp
)

type saveState struct {
	quote       quoteState
	buriedHdocs int
}

func (p *Parser) preNested(quote quoteState) (s saveState) {
	s.quote, s.buriedHdocs = p.quote, p.buriedHdocs
	p.buriedHdocs, p.quote = len(p.heredocs), quote
	return s
}

func (p *Parser) postNested(s saveState) {
	p.quote, p.buriedHdocs = s.quote, s.buriedHdocs
}

func aliasEndsWithBlank(src string) bool {
	return strings.TrimRight(src, " \t") != src
}

func (p *Parser) resumeAliasInput() bool {
	if len(p.aliasInputStack) == 0 {
		return false
	}
	blankNext := p.aliasSource != nil && aliasEndsWithBlank(p.aliasSource.Value)
	if p.aliasSource != nil && p.aliasActive != nil {
		if depth := p.aliasActive[p.aliasSource.Name]; depth > 1 {
			p.aliasActive[p.aliasSource.Name] = depth - 1
		} else {
			delete(p.aliasActive, p.aliasSource.Name)
		}
	}

	last := len(p.aliasInputStack) - 1
	state := p.aliasInputStack[last]
	p.aliasInputStack = p.aliasInputStack[:last]

	p.src = state.src
	p.bs = state.bs
	p.bsp = state.bsp
	p.r = state.r
	p.w = state.w
	p.offs = state.offs
	p.line = state.line
	p.col = state.col
	p.readErr = state.readErr
	p.readEOF = state.readEOF
	p.aliasChain = state.aliasChain
	p.aliasSource = state.aliasSource
	p.aliasBlankNext = blankNext
	return true
}

func (p *Parser) commandAliasCandidate() bool {
	if p.aliasResolver == nil {
		return false
	}
	switch p.tok {
	case _Lit, _LitWord:
	default:
		return false
	}
	if p.val == "" || p.atRsrv(
		rsrvIf, rsrvThen, rsrvElif, rsrvElse, rsrvFi,
		rsrvWhile, rsrvUntil, rsrvFor, rsrvSelect, rsrvIn,
		rsrvDo, rsrvDone, rsrvCase, rsrvEsac,
		rsrvLeftBrace, rsrvRightBrace,
	) {
		return false
	}
	return p.aliasActive == nil || p.aliasActive[p.val] == 0
}

func (p *Parser) expandCommandAlias() bool {
	if !p.commandAliasCandidate() {
		return false
	}
	spec, ok := p.aliasResolver(p.val)
	if !ok {
		return false
	}

	expansion := &AliasExpansion{
		Name:  p.val,
		Value: spec.Value,
		Pos:   p.pos,
	}
	p.f.AliasExpansions = append(p.f.AliasExpansions, expansion)

	state := aliasInputState{
		src:            p.src,
		bs:             append([]byte(nil), p.bs...),
		bsp:            p.bsp,
		r:              p.r,
		w:              p.w,
		offs:           p.offs,
		line:           p.line,
		col:            p.col,
		readErr:        p.readErr,
		readEOF:        p.readEOF,
		aliasChain:     append([]*AliasExpansion(nil), p.aliasChain...),
		aliasBlankNext: p.aliasBlankNext,
		aliasSource:    p.aliasSource,
	}
	p.aliasInputStack = append(p.aliasInputStack, state)

	if p.aliasActive == nil {
		p.aliasActive = make(map[string]int)
	}
	p.aliasActive[expansion.Name]++

	p.src = strings.NewReader(spec.Value)
	p.bs = nil
	p.bsp = 0
	p.r = 0
	p.w = 0
	p.offs = int64(expansion.Pos.Offset())
	p.line = int64(expansion.Pos.Line())
	p.col = int64(expansion.Pos.Col())
	p.readErr = nil
	p.readEOF = false
	p.aliasChain = append(append([]*AliasExpansion(nil), state.aliasChain...), expansion)
	p.aliasSource = expansion
	p.aliasBlankNext = false

	p.rune()
	p.next()
	return true
}

func heredocIndentMode(op RedirOperator) HeredocIndentMode {
	if op == DashHdoc {
		return HeredocIndentStripTabs
	}
	return HeredocIndentNone
}

func (p *Parser) heredocDelimFromWord(w *Word, raw string, op RedirOperator) *HeredocDelim {
	if w == nil {
		return nil
	}
	value, quoted := wordUnquotedBytesRaw(w, []byte(raw))
	return &HeredocDelim{
		Parts:       w.Parts,
		Value:       string(value),
		Quoted:      quoted,
		BodyExpands: !quoted,
		IndentMode:  heredocIndentMode(op),
	}
}

func (p *Parser) setHeredocCloseCapture(delim *HeredocDelim, capture heredocCloseCapture) {
	if delim == nil {
		return
	}
	delim.ClosePos = capture.pos
	delim.CloseEnd = capture.end
	delim.CloseRaw = capture.raw
	delim.CloseCandidate = capture.candidate
	delim.Matched = capture.matched
	delim.EOFTerminated = capture.eofTerminated
	delim.TrailingText = capture.trailingText
	delim.IndentTabs = capture.indentTabs
}

func (p *Parser) currentHeredocStop() *heredocStop {
	if len(p.hdocStops) == 0 {
		return nil
	}
	return &p.hdocStops[len(p.hdocStops)-1]
}

func (p *Parser) updateHeredocStop(stop *heredocStop, rawPos, end Pos, rawLine, normalized []byte, indentTabs uint16, eof, hasLine bool) {
	if stop == nil {
		return
	}
	if !hasLine {
		if eof {
			stop.close.eofTerminated = true
		}
		return
	}
	rawLineText := string(rawLine)
	raw := p.sourceRange(rawPos, end)
	if !eof && strings.HasSuffix(raw, "\r") {
		raw = strings.TrimSuffix(raw, "\r")
		end = posSubCol(end, 1)
	}
	if raw == "" || raw != rawLineText {
		raw = rawLineText
		rawPos = posSubCol(end, len(rawLineText))
	}
	prefix := bytes.HasPrefix(normalized, stop.word)
	matched := bytes.Equal(normalized, stop.word)
	var candidate *HeredocCloseCandidate
	if prefix || heredocCloseCandidateMayMatch(rawLine, stop.word) {
		candidate = newHeredocCloseCandidate(rawPos, rawLine, stop.word, p.lang, p.parseExtGlob, p.legacyBashCompat)
	}
	if prefix || candidate != nil {
		close := heredocCloseCapture{
			pos:           rawPos,
			end:           end,
			raw:           raw,
			candidate:     candidate,
			matched:       matched,
			eofTerminated: eof && !matched,
			indentTabs:    indentTabs,
		}
		if prefix {
			close.trailingText = string(normalized[len(stop.word):])
		}
		stop.close = close
		return
	}
	if eof {
		stop.close.eofTerminated = true
	}
}

func heredocCloseCandidateMayMatch(rawLine, delim []byte) bool {
	if len(rawLine) == 0 || len(delim) == 0 {
		return false
	}
	if bytes.Contains(rawLine, delim) {
		return true
	}
	if bytes.IndexByte(rawLine, delim[0]) < 0 {
		return false
	}
	if len(delim) > 1 && bytes.IndexByte(rawLine, delim[len(delim)-1]) < 0 {
		return false
	}
	return bytes.IndexAny(rawLine, "'\"\\$`") >= 0
}

func heredocCloseCandidateWordStart(tok token) bool {
	switch tok {
	case _Lit, _LitWord,
		sglQuote, dollSglQuote, dblQuote, dollDblQuote, bckQuote,
		dollar, dollBrace, dollBrack, dollParen, dollDblParen,
		cmdIn, assgnParen, cmdOut:
		return true
	default:
		return false
	}
}

func newHeredocCloseCandidate(rawPos Pos, rawLine, delim []byte, lang LangVariant, parseExtGlob, legacyBashCompat bool) *HeredocCloseCandidate {
	if len(rawLine) == 0 || len(delim) == 0 {
		return nil
	}
	candidateParser := NewParser(
		Variant(lang),
		ParseExtGlob(parseExtGlob),
		LegacyBashCompat(legacyBashCompat),
	)
	candidateParser.reset()
	candidateParser.f = &File{}
	candidateParser.loadReplay(rawPos, rawLine, io.EOF)
	candidateParser.next()
	for candidateParser.tok != _EOF {
		if candidateParser.err != nil {
			return nil
		}
		if !heredocCloseCandidateWordStart(candidateParser.tok) {
			candidateParser.next()
			continue
		}
		word, raw := candidateParser.getWordRaw()
		if candidateParser.err != nil {
			return nil
		}
		if word == nil || raw == "" {
			continue
		}
		rawBytes := []byte(raw)
		rawMismatch := !bytes.Equal(rawBytes, delim)
		if rawMismatch {
			unquoted, _ := wordUnquotedBytesRaw(word, rawBytes)
			if !bytes.Equal(unquoted, delim) {
				continue
			}
		}
		offset := int(word.Pos().Offset()) - int(rawPos.Offset())
		if offset < 0 || offset > len(rawLine) {
			return nil
		}
		leadingStart := offset
		for leadingStart > 0 {
			switch rawLine[leadingStart-1] {
			case ' ', '\t':
				leadingStart--
			default:
				return &HeredocCloseCandidate{
					Pos:               word.Pos(),
					End:               word.End(),
					Raw:               raw,
					DelimOffset:       uint(offset),
					LeadingWhitespace: string(rawLine[leadingStart:offset]),
					RawTokenMismatch:  rawMismatch,
				}
			}
		}
		return &HeredocCloseCandidate{
			Pos:               word.Pos(),
			End:               word.End(),
			Raw:               raw,
			DelimOffset:       uint(offset),
			LeadingWhitespace: string(rawLine[leadingStart:offset]),
			RawTokenMismatch:  rawMismatch,
		}
	}
	return nil
}

func (p *Parser) doHeredocs() {
	hdocs := p.heredocs[p.buriedHdocs:]
	if len(hdocs) == 0 {
		// Nothing do do; don't even issue a read.
		return
	}
	p.rune() // consume '\n', since we know p.tok == _Newl
	old := p.quote
	p.heredocs = p.heredocs[:p.buriedHdocs]
	for i, r := range hdocs {
		if p.err != nil {
			break
		}
		p.quote = hdocBody
		if r.Op == DashHdoc {
			p.quote = hdocBodyTabs
		}
		var stop []byte
		if r.HdocDelim != nil {
			stop = []byte(r.HdocDelim.Value)
		}
		p.hdocStops = append(p.hdocStops, heredocStop{word: stop})
		if i > 0 && p.r == '\n' {
			p.rune()
		}
		bodyStart := p.nextPos()
		raw := p.quotedHdocWord()
		if p.err != nil {
			break
		}
		if r.HdocDelim != nil && r.HdocDelim.BodyExpands && raw != nil {
			bodyOpts := []ParserOption{Variant(p.lang), KeepComments(p.keepComments)}
			if p.aliasResolver != nil {
				bodyOpts = append(bodyOpts, ExpandAliases(p.aliasResolver))
			}
			bodyParser := NewParser(bodyOpts...)
			if p.parenAmbiguityDisabled {
				bodyParser.parenAmbiguityDisabled = true
				bodyParser.parenAmbiguityProbeDepth = p.parenAmbiguityProbeDepth
			}
			r.Hdoc, p.err = bodyParser.document(strings.NewReader(raw.Lit()), bodyStart)
			if p.err == nil && r.Hdoc != nil {
				setWordEnd(r.Hdoc, raw.End())
			}
		} else {
			r.Hdoc = raw
		}
		if p.err == nil && raw != nil && r.Hdoc == nil {
			r.Hdoc = raw
		}
		if len(p.hdocStops) > 0 {
			p.setHeredocCloseCapture(r.HdocDelim, p.hdocStops[len(p.hdocStops)-1].close)
		}
		if p.err == nil {
			if stop := p.hdocStops[len(p.hdocStops)-1]; !stop.close.matched {
				if p.lang.in(langBashLike) && heredocNeedsEOFWarning(r.HdocDelim) {
					p.pendingHeredocWarningPos = r.Pos()
					p.pendingHeredocWarningWanted = r.HdocDelim.Value
				} else {
					p.posErrWithMetadata(r.Pos(), parseErrorMetadata{
						kind:       ParseErrorKindUnclosed,
						construct:  ParseErrorSymbolHereDocument,
						unexpected: ParseErrorSymbolEOF,
						expected:   []ParseErrorSymbol{ParseErrorSymbol(r.HdocDelim.Value)},
					}, "unclosed here-document %#q", r.HdocDelim.Value)
				}
			}
		}
		p.hdocStops = p.hdocStops[:len(p.hdocStops)-1]
	}
	p.quote = old
}

func heredocNeedsEOFWarning(delim *HeredocDelim) bool {
	if delim == nil || !delim.Quoted {
		return false
	}
	return heredocPartsNeedEOFWarning(delim.Parts)
}

func heredocPartsNeedEOFWarning(parts []WordPart) bool {
	for _, part := range parts {
		switch part := part.(type) {
		case *ParamExp, *CmdSubst, *ArithmExp, *ProcSubst:
			return true
		case *DblQuoted:
			if heredocPartsNeedEOFWarning(part.Parts) {
				return true
			}
		}
	}
	return false
}

func posSubCol(p Pos, n int) Pos {
	if n <= 0 {
		return p
	}
	offset := p.Offset()
	line := p.Line()
	col := p.Col()
	if int(offset) < n {
		offset = 0
	} else {
		offset -= uint(n)
	}
	if int(col) < n {
		col = 0
	} else {
		col -= uint(n)
	}
	return NewPos(offset, line, col)
}

func setWordEnd(w *Word, end Pos) {
	if w == nil || len(w.Parts) == 0 {
		return
	}
	setWordPartEnd(w.Parts[len(w.Parts)-1], end)
}

func setWordPartEnd(part WordPart, end Pos) {
	switch part := part.(type) {
	case *Lit:
		part.ValueEnd = end
	case *SglQuoted:
		part.Right = posSubCol(end, 1)
	case *DblQuoted:
		part.Right = posSubCol(end, 1)
	case *CmdSubst:
		part.Right = posSubCol(end, 1)
	case *ParamExp:
		if !part.Short {
			part.Rbrace = posSubCol(end, 1)
			return
		}
		if part.Index != nil {
			part.Index.Right = posSubCol(end, 1)
			return
		}
		if part.Param != nil {
			part.Param.ValueEnd = end
		}
	case *ArithmExp:
		width := 2
		if part.Bracket {
			width = 1
		}
		part.Right = posSubCol(end, width)
	case *ProcSubst:
		part.Rparen = posSubCol(end, 1)
	case *ExtGlob:
		part.Rparen = posSubCol(end, 1)
	case *BraceExp:
		if len(part.Elems) == 0 {
			return
		}
		setWordEnd(part.Elems[len(part.Elems)-1], posSubCol(end, 1))
	}
}

func (p *Parser) got(tok token) bool {
	if p.tok == tok {
		p.next()
		return true
	}
	return false
}

type reservedWord string

const (
	rsrvIf         reservedWord = "if"
	rsrvThen       reservedWord = "then"
	rsrvElif       reservedWord = "elif"
	rsrvElse       reservedWord = "else"
	rsrvFi         reservedWord = "fi"
	rsrvWhile      reservedWord = "while"
	rsrvUntil      reservedWord = "until"
	rsrvFor        reservedWord = "for"
	rsrvSelect     reservedWord = "select"
	rsrvIn         reservedWord = "in"
	rsrvDo         reservedWord = "do"
	rsrvDone       reservedWord = "done"
	rsrvCase       reservedWord = "case"
	rsrvEsac       reservedWord = "esac"
	rsrvLeftBrace  reservedWord = "{"
	rsrvRightBrace reservedWord = "}"
)

func (p *Parser) atLitWord(val string) bool {
	return p.tok == _LitWord && p.val == val
}

func (p *Parser) gotLitWord(val string) (Pos, bool) {
	pos := p.pos
	if p.atLitWord(val) {
		p.next()
		return pos, true
	}
	return pos, false
}

func (p *Parser) atRsrv(words ...reservedWord) bool {
	if p.tok != _LitWord {
		return false
	}
	cur := reservedWord(p.val)
	for _, word := range words {
		if cur == word {
			return true
		}
	}
	return false
}

func (p *Parser) gotRsrv(val reservedWord) (Pos, bool) {
	pos := p.pos
	if p.atRsrv(val) {
		p.next()
		return pos, true
	}
	return pos, false
}

func (p *Parser) recoverError() bool {
	if p.recoveredErrors < p.recoverErrorsMax {
		p.recoveredErrors++
		return true
	}
	return false
}

type noQuote string

func (s noQuote) Format(f fmt.State, verb rune) {
	f.Write([]byte(s))
}

func (t token) Format(f fmt.State, verb rune) {
	if t < _realTokenBoundary && verb == 'q' {
		// EOF, Lit and the others should not be quoted in error messages
		// as they are not real shell syntax like `if` or `{`.
		f.Write([]byte(t.String()))
	} else {
		fmt.Fprintf(f, fmt.FormatString(f, verb), t.String())
	}
}

func (p *Parser) unexpectedTokenErr(pos Pos, unexpected ParseErrorSymbol, quoted string) {
	p.posErrWithMetadata(pos, parseErrorMetadata{
		kind:       ParseErrorKindUnexpected,
		unexpected: unexpected,
	}, "syntax error near unexpected token %s", quoted)
}

func strayReservedWordMetadata(word reservedWord) (parseErrorMetadata, bool) {
	meta := parseErrorMetadata{
		kind:       ParseErrorKindUnexpected,
		unexpected: parseErrorSymbolFromReserved(word),
	}
	switch word {
	case rsrvThen, rsrvElif, rsrvFi:
		meta.construct = ParseErrorSymbol("if")
	case rsrvDo, rsrvDone:
		meta.construct = ParseErrorSymbol("loop")
	case rsrvEsac:
		meta.construct = ParseErrorSymbol("case")
	case rsrvRightBrace:
		meta.construct = ParseErrorSymbolLeftBrace
	default:
		return parseErrorMetadata{}, false
	}
	return meta, true
}

func (p *Parser) strayReservedWordErr(pos Pos, word reservedWord, format string, args ...any) {
	meta, ok := strayReservedWordMetadata(word)
	if !ok {
		p.posErr(pos, format, args...)
		return
	}
	p.posErrWithMetadata(pos, meta, format, args...)
}

func (p *Parser) followErr(pos Pos, left, right any) {
	meta := parseErrorMetadata{
		kind:       ParseErrorKindMissing,
		construct:  parseErrorSymbolFromAny(left),
		unexpected: p.currentUnexpectedTokenSymbol(),
		expected:   parseErrorSymbols(right),
	}
	if ctx, ok := parseErrorFollowContext(left, right); ok {
		ctx = withFuncOpenBashToken(ctx, p.currentUnexpectedFuncOpenToken())
		p.posErrWithMetadataContext(pos, meta, ctx, "%#q must be followed by %#q", left, right)
		return
	}
	p.posErrWithMetadata(pos, meta, "%#q must be followed by %#q", left, right)
}

func (p *Parser) followErrExp(pos Pos, left any) {
	p.posErrWithMetadata(pos, parseErrorMetadata{
		kind:       ParseErrorKindMissing,
		construct:  parseErrorSymbolFromAny(left),
		unexpected: p.currentUnexpectedTokenSymbol(),
		expected:   []ParseErrorSymbol{ParseErrorSymbolExpression},
	}, "%#q must be followed by %#q", left, noQuote("an expression"))
}

func (p *Parser) follow(lpos Pos, left string, tok token) {
	if !p.got(tok) {
		p.followErr(lpos, left, tok)
	}
}

func parseErrorFollowContext(left, right any) (parseErrorContext, bool) {
	leftText, ok := left.(string)
	if !ok {
		return parseErrorContext{}, false
	}
	tok, ok := right.(token)
	if !ok || tok != rightParen {
		return parseErrorContext{}, false
	}
	switch leftText {
	case "foo(", "function foo(":
		return parseErrorContext{kind: parseErrorContextFuncOpen}, true
	default:
		return parseErrorContext{}, false
	}
}

func (p *Parser) followRsrv(lpos Pos, left string, val reservedWord) Pos {
	pos, ok := p.gotRsrv(val)
	if !ok {
		if p.recoverError() {
			return recoveredPos
		}
		p.followErr(lpos, left, val)
	}
	return pos
}

func (p *Parser) followStmts(left string, lpos Pos, stops ...reservedWord) ([]*Stmt, []Comment) {
	// Language variants disallowing empty command lists:
	// * [LangPOSIX]: "A list is a sequence of one or more AND-OR lists...".
	// * [LangBash]: "A list is a sequence of one or more pipelines..."
	//
	// Language variants allowing empty command lists:
	// * [LangZsh]: "A list is a sequence of zero or more sublists...".
	// * [LangMirBSDKorn]: "Lists of commands can be created by separating pipelines...";
	//   note that the man page is not explicit, but the shell clearly allows e.g. `{ }`.
	if p.got(semicolon) {
		if p.lang.in(LangZsh | LangMirBSDKorn) {
			return nil, nil // allow an empty list
		}
		p.followErr(lpos, left, noQuote("a statement list"))
		return nil, nil
	}
	stmts, last := p.stmtList(stops...)
	if len(stmts) < 1 {
		if p.lang.in(LangZsh | LangMirBSDKorn) {
			return nil, nil // allow an empty list
		}
		if p.recoverError() {
			return []*Stmt{{Position: recoveredPos}}, nil
		}
		if p.lang.in(LangBash|LangBats) && p.atRsrv(rsrvFi, rsrvDone) {
			unexpected, quoted := p.currentUnexpectedTokenDiagnostic()
			p.unexpectedTokenErr(p.pos, unexpected, quoted)
			return nil, nil
		}
		p.followErr(lpos, left, noQuote("a statement list"))
	}
	return stmts, last
}

func (p *Parser) followWordTok(tok token, pos Pos) *Word {
	w := p.getWord()
	if w == nil {
		if p.recoverError() {
			return p.wordOne(&Lit{ValuePos: recoveredPos})
		}
		p.followErr(pos, tok, noQuote("a word"))
	}
	return w
}

func (p *Parser) followWordTokRaw(tok token, pos Pos) (*Word, string) {
	w, raw := p.getWordRaw()
	if w == nil {
		if p.recoverError() {
			return p.wordOne(&Lit{ValuePos: recoveredPos}), ""
		}
		p.followErr(pos, tok, noQuote("a word"))
	}
	return w, raw
}

func (p *Parser) stmtEnd(n Node, start string, end reservedWord) Pos {
	pos, ok := p.gotRsrv(end)
	if !ok {
		if p.recoverError() {
			return recoveredPos
		}
		p.posErrWithMetadata(n.Pos(), parseErrorMetadata{
			kind:       ParseErrorKindMissing,
			construct:  parseErrorSymbolFromText(start),
			unexpected: p.currentUnexpectedTokenSymbol(),
			expected:   []ParseErrorSymbol{parseErrorSymbolFromReserved(end)},
		}, "%#q statement must end with %#q", start, end)
	}
	return pos
}

func (p *Parser) quoteErr(lpos Pos, quote token) {
	p.posErrWithMetadata(lpos, parseErrorMetadata{
		kind:       ParseErrorKindUnclosed,
		construct:  parseErrorSymbolFromToken(quote),
		unexpected: p.currentUnexpectedTokenSymbol(),
		expected:   []ParseErrorSymbol{parseErrorSymbolFromToken(quote)},
	}, "reached %#q without closing quote %#q", p.tok, quote)
}

func (p *Parser) matchingErr(lpos Pos, left, right token) {
	unexpected := p.currentUnexpectedTokenSymbol()
	p.posErrWithMetadata(lpos, parseErrorMetadata{
		kind:       missingCloseKind(unexpected),
		construct:  parseErrorSymbolFromToken(left),
		unexpected: unexpected,
		expected:   []ParseErrorSymbol{parseErrorSymbolFromToken(right)},
	}, "reached %#q without matching %#q with %#q", p.tok, left, right)
}

func (p *Parser) matched(lpos Pos, left, right token) Pos {
	pos := p.pos
	if !p.got(right) {
		if p.recoverError() {
			return recoveredPos
		}
		p.matchingErr(lpos, left, right)
	}
	return pos
}

func (p *Parser) errPass(err error) {
	if p.err == nil {
		if parseErr, ok := err.(ParseError); ok && p.pendingHeredocWarningWanted != "" {
			if token, ok := bashCompatUnexpectedEOFCommand(parseErr); ok {
				parseErr.bashHeredocWanted = p.pendingHeredocWarningWanted
				parseErr.bashUnexpectedEOFCommand = token
				parseErr.SecondaryPos = parseErr.Pos
				parseErr.Pos = p.pendingHeredocWarningPos
				parseErr.SourceLine = ""
				parseErr.SourceLinePos = Pos{}
				parseErr.noSourceLine = true
				err = parseErr
			}
		}
	}
	if p.err == nil {
		p.err = err
		p.bsp = uint(len(p.bs)) + 1
		p.r = utf8.RuneSelf
		p.w = 1
		p.tok = _EOF
	}
}

// IsIncomplete reports whether a Parser error could have been avoided with
// extra input bytes. For example, if an [io.EOF] was encountered while there was
// an unclosed quote or parenthesis.
func IsIncomplete(err error) bool {
	perr, ok := err.(ParseError)
	return ok && perr.Incomplete
}

// TODO: probably redo with a [LangVariant] argument.
// Perhaps offer an iterator version as well.

// IsKeyword returns true if the given word is a language keyword
// in POSIX Shell or Bash.
func IsKeyword(word string) bool {
	// This list has been copied from the bash 5.1 source code, file y.tab.c +4460
	// TODO: should we include entries for zsh here? e.g. "{}", "repeat", "always", ...
	switch word {
	case
		"!",
		"[[", // only if COND_COMMAND is defined
		"]]", // only if COND_COMMAND is defined
		"case",
		"coproc", // only if COPROCESS_SUPPORT is defined
		"do",
		"done",
		"else",
		"esac",
		"fi",
		"for",
		"function",
		"if",
		"in",
		"select", // only if SELECT_COMMAND is defined
		"then",
		"time", // only if COMMAND_TIMING is defined
		"until",
		"while",
		"{",
		"}":
		return true
	}
	return false
}

// ParseError represents an error found when parsing a source file, from which
// the parser cannot recover.
type parseErrorContextKind uint8

const (
	parseErrorContextNone parseErrorContextKind = iota
	parseErrorContextFuncOpen
	parseErrorContextUnexpectedToken
	parseErrorContextNearToken
)

type parseErrorContext struct {
	kind  parseErrorContextKind
	token string
}

type ParseError struct {
	Filename string
	Pos      Pos
	Text     string

	Kind          ParseErrorKind
	Construct     ParseErrorSymbol
	Unexpected    ParseErrorSymbol
	Expected      []ParseErrorSymbol
	IsRecoverable bool

	Incomplete bool

	// SecondaryText is an optional second diagnostic line that BashError emits
	// before the source snippet. Bash uses this for some syntax errors like
	// "syntax error near ...".
	SecondaryText string
	SecondaryPos  Pos

	// SourceLine is the content of the source line where the error occurred.
	// When set, BashError includes it as a second diagnostic line.
	SourceLine string
	// SourceLinePos optionally overrides the line number used for SourceLine.
	SourceLinePos Pos

	bashText                 string
	bashSecondaryText        string
	bashHeredocWanted        string
	bashUnexpectedEOFCommand string
	bashPrefix               string
	bashPrefixNoLine         bool
	noSourceLine             bool
	typedContext             parseErrorContext
}

func (e ParseError) Error() string {
	if e.Filename == "" {
		return fmt.Sprintf("%s: %s", e.Pos, e.Text)
	}
	return fmt.Sprintf("%s:%s: %s", e.Filename, e.Pos, e.Text)
}

func (e ParseError) bashCompat() ParseError {
	if e.bashText != "" || e.bashSecondaryText != "" || e.bashHeredocWanted != "" || e.SecondaryText != "" || e.noSourceLine {
		return e
	}
	sourceLine := strings.TrimSpace(e.SourceLine)
	if bashText, ok := bashCompatUnexpectedEOFText(e); ok {
		e.bashText = bashText
		e.SourceLine = ""
		e.SourceLinePos = Pos{}
		e.noSourceLine = true
		return e
	}
	if bashText, ok := bashCompatTypedContextText(e, sourceLine); ok {
		e.bashText = bashText
		return e
	}
	switch {
	case e.Text == "`for` must be followed by a literal" && strings.HasSuffix(sourceLine, "for"):
		e.bashText = bashCompatUnexpectedTokenText("newline")
	case e.Text == "`case` must be followed by a word" && strings.HasSuffix(sourceLine, "case"):
		e.bashText = bashCompatUnexpectedTokenText("newline")
	case e.Text == "`<` must be followed by a word" && strings.HasSuffix(sourceLine, "<"):
		e.bashText = bashCompatUnexpectedTokenText("newline")
	case e.Text == "`>` must be followed by a word" && strings.HasSuffix(sourceLine, ">"):
		e.bashText = bashCompatUnexpectedTokenText("newline")
	case e.Text == "reached EOF without matching `$(` with `)`":
		e.bashText = "unexpected EOF while looking for matching `)'"
		e.SourceLine = ""
		e.SourceLinePos = Pos{}
		e.noSourceLine = true
	case e.Text == "reached EOF without closing quote `'`":
		e.bashText = "unexpected EOF while looking for matching `''"
		e.SourceLine = ""
		e.SourceLinePos = Pos{}
		e.noSourceLine = true
	case e.Text == "reached EOF without closing quote \"`\"":
		e.bashText = "unexpected EOF while looking for matching ``'"
		e.SourceLine = ""
		e.SourceLinePos = Pos{}
		e.noSourceLine = true
	case e.Text == "`do` can only be used in a loop":
		e.bashText = "syntax error near unexpected token `do'"
	case e.Text == "`;;` can only be used in a case clause":
		e.bashText = "syntax error near unexpected token `;;'"
	case e.Text == "`}` can only be used to close a block":
		e.bashText = "syntax error near unexpected token `}'"
	case e.Text == "`case x` must be followed by `in`" && bashCompatArrayLiteralToken(sourceLine):
		e.bashText = "syntax error near unexpected token `('"
	case e.Text == "word list can only contain words" && bashCompatArrayLiteralToken(sourceLine):
		e.bashText = "syntax error near unexpected token `('"
	case bashCompatUnexpectedFollowError(e.Text):
		if token, ok := bashCompatUnexpectedTokenAtPos(e); ok {
			e.bashText = bashCompatUnexpectedTokenText(token)
		}
	case bashCompatFuncOpenError(e.Text):
		if token, ok := bashCompatFuncOpenToken(sourceLine); ok {
			e.bashText = bashCompatUnexpectedTokenText(token)
		}
	}
	return e
}

func bashCompatArrayLiteralToken(sourceLine string) bool {
	return strings.Contains(sourceLine, "=()")
}

func bashCompatUnexpectedEOFCommand(e ParseError) (string, bool) {
	switch {
	case e.Incomplete &&
		(e.Text == "`if` must be followed by a statement list" || e.Text == "`if <cond>` must be followed by `then`"):
		return "if", true
	case e.Incomplete &&
		(e.Text == "`while` must be followed by a statement list" || e.Text == "`while <cond>` must be followed by `do`"):
		return "while", true
	case e.Incomplete && e.Text == "`case` statement must end with `esac`":
		return "case", true
	case e.Incomplete &&
		(e.Text == "`{` statement must end with `}`" || e.Text == "reached EOF without matching `{` with `}`"):
		return "{", true
	default:
		return "", false
	}
}

func bashCompatUnexpectedEOFText(e ParseError) (string, bool) {
	token, ok := bashCompatUnexpectedEOFCommand(e)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("syntax error: unexpected end of file from `%s' command on line %d", token, e.Pos.Line()), true
}

func bashCompatTypedContextText(e ParseError, sourceLine string) (string, bool) {
	switch e.typedContext.kind {
	case parseErrorContextFuncOpen:
		if e.typedContext.token != "" {
			return bashCompatUnexpectedTokenText(e.typedContext.token), true
		}
		token, ok := bashCompatFuncOpenToken(sourceLine)
		if !ok {
			return "", false
		}
		return bashCompatUnexpectedTokenText(token), true
	case parseErrorContextUnexpectedToken:
		if e.typedContext.token == "" {
			return "", false
		}
		return bashCompatUnexpectedTokenText(e.typedContext.token), true
	case parseErrorContextNearToken:
		if e.typedContext.token == "" {
			return "", false
		}
		return fmt.Sprintf("syntax error near %s", bashQuoteString(e.typedContext.token)), true
	default:
		return "", false
	}
}

func bashCompatUnexpectedTokenText(token string) string {
	if token == "newline" {
		return "syntax error near unexpected token `newline'"
	}
	return fmt.Sprintf("syntax error near unexpected token %s", bashQuoteString(token))
}

func withFuncOpenBashToken(ctx parseErrorContext, token string) parseErrorContext {
	if ctx.kind != parseErrorContextFuncOpen || ctx.token != "" {
		return ctx
	}
	if token == "" {
		token = "newline"
	}
	ctx.token = token
	return ctx
}

func bashCompatFuncOpenError(text string) bool {
	switch text {
	case "`foo(` must be followed by `)`", "`function foo(` must be followed by `)`":
		return true
	default:
		return false
	}
}

func bashCompatFuncOpenToken(sourceLine string) (string, bool) {
	open := strings.IndexRune(sourceLine, '(')
	if open < 0 {
		return "", false
	}
	rest := strings.TrimLeftFunc(sourceLine[open+1:], unicode.IsSpace)
	if rest == "" {
		return "newline", true
	}
	switch rest[0] {
	case '&', '|', ';', '<', '>', ')':
		return bashCompatOperatorToken(rest), true
	}
	for i, r := range rest {
		if unicode.IsSpace(r) || strings.ContainsRune("&|;<>)", r) {
			if i == 0 {
				return "newline", true
			}
			return rest[:i], true
		}
	}
	return rest, true
}

func bashCompatOperatorToken(rest string) string {
	if len(rest) >= 2 {
		switch rest[:2] {
		case "&&", "||", ";;", "<<", ">>", "<&", ">&", "<>", "((", "))", "|&":
			return rest[:2]
		}
	}
	return rest[:1]
}

func bashCompatUnexpectedFollowError(text string) bool {
	switch text {
	case "`if <cond>` must be followed by `then`",
		"`elif <cond>` must be followed by `then`",
		"`while <cond>` must be followed by `do`",
		"`until <cond>` must be followed by `do`",
		"`for foo [in words]` must be followed by `do`",
		"`select foo [in words]` must be followed by `do`",
		"`for foo` must be followed by `in`, `do`, `;`, or a newline",
		"`select foo` must be followed by `in`, `do`, `;`, or a newline":
		return true
	default:
		return false
	}
}

func bashCompatLeadingToken(rest string) (string, bool) {
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if rest == "" {
		return "newline", true
	}
	switch rest[0] {
	case '&', '|', ';', '<', '>', '(', ')':
		return bashCompatOperatorToken(rest), true
	}
	for i, r := range rest {
		if unicode.IsSpace(r) || strings.ContainsRune("&|;<>()", r) {
			if i == 0 {
				return "newline", true
			}
			return rest[:i], true
		}
	}
	return rest, true
}

func bashCompatTrailingToken(sourceLine string) (string, bool) {
	sourceLine = strings.TrimRightFunc(sourceLine, unicode.IsSpace)
	if sourceLine == "" {
		return "newline", true
	}
	if strings.ContainsRune("&|;<>()", rune(sourceLine[len(sourceLine)-1])) {
		start := len(sourceLine) - 1
		if start > 0 {
			if token := bashCompatOperatorToken(sourceLine[start-1:]); len(token) > 1 {
				start--
			}
		}
		return bashCompatLeadingToken(sourceLine[start:])
	}
	start := len(sourceLine) - 1
	for start >= 0 && !unicode.IsSpace(rune(sourceLine[start])) && !strings.ContainsRune("&|;<>()", rune(sourceLine[start])) {
		start--
	}
	return bashCompatLeadingToken(sourceLine[start+1:])
}

func bashCompatUnexpectedTokenAtPos(e ParseError) (string, bool) {
	if e.SourceLine == "" {
		return "", false
	}
	switch e.Text {
	case "`if <cond>` must be followed by `then`",
		"`elif <cond>` must be followed by `then`",
		"`while <cond>` must be followed by `do`",
		"`until <cond>` must be followed by `do`":
		return bashCompatTrailingToken(e.SourceLine)
	case "`for foo [in words]` must be followed by `do`",
		"`select foo [in words]` must be followed by `do`":
		if semi := strings.IndexRune(e.SourceLine, ';'); semi >= 0 {
			return bashCompatLeadingToken(e.SourceLine[semi+1:])
		}
		return bashCompatTrailingToken(e.SourceLine)
	case "`for foo` must be followed by `in`, `do`, `;`, or a newline",
		"`select foo` must be followed by `in`, `do`, `;`, or a newline":
		fields := strings.Fields(e.SourceLine)
		if len(fields) >= 2 {
			return bashCompatLeadingToken(strings.TrimPrefix(e.SourceLine, fields[0]+" "+fields[1]))
		}
	}
	return "", false
}

// BashError returns the error message formatted like bash does.
func (e ParseError) BashError() string {
	e = e.bashCompat()
	secondaryPos := e.Pos
	if e.SecondaryPos.IsValid() {
		secondaryPos = e.SecondaryPos
	}
	sourceLinePos := e.Pos
	if e.SourceLinePos.IsValid() {
		sourceLinePos = e.SourceLinePos
	}
	var first string
	text := e.Text
	if e.bashHeredocWanted != "" {
		text = fmt.Sprintf(
			"warning: here-document at line %d delimited by end-of-file (wanted `%s')",
			e.Pos.Line(),
			e.bashHeredocWanted,
		)
	} else if e.bashText != "" {
		text = e.bashText
	}
	if e.bashPrefix != "" && e.bashPrefixNoLine {
		first = fmt.Sprintf("%s: %s", e.bashPrefix, text)
	} else if e.bashPrefix != "" {
		first = fmt.Sprintf("%s: line %d: %s", e.bashPrefix, e.Pos.Line(), text)
	} else if e.Filename == "" {
		first = fmt.Sprintf("line %d: %s", e.Pos.Line(), text)
	} else {
		first = fmt.Sprintf("%s: line %d: %s", e.Filename, e.Pos.Line(), text)
	}
	lines := []string{first}
	secondaryText := e.SecondaryText
	if e.bashHeredocWanted != "" {
		secondaryText = fmt.Sprintf(
			"syntax error: unexpected end of file from `%s' command on line %d",
			e.bashUnexpectedEOFCommand,
			secondaryPos.Line(),
		)
	} else if e.bashSecondaryText != "" {
		secondaryText = e.bashSecondaryText
	}
	if secondaryText != "" {
		if e.bashPrefix != "" && e.bashPrefixNoLine {
			lines = append(lines, fmt.Sprintf("%s: %s", e.bashPrefix, secondaryText))
		} else if e.bashPrefix != "" {
			lines = append(lines, fmt.Sprintf("%s: line %d: %s", e.bashPrefix, secondaryPos.Line(), secondaryText))
		} else if e.Filename == "" {
			lines = append(lines, fmt.Sprintf("line %d: %s", secondaryPos.Line(), secondaryText))
		} else {
			lines = append(lines, fmt.Sprintf("%s: line %d: %s", e.Filename, secondaryPos.Line(), secondaryText))
		}
	}
	if e.SourceLine == "" || e.noSourceLine {
		return strings.Join(lines, "\n")
	}
	var second string
	if e.Filename == "" {
		second = fmt.Sprintf("line %d: `%s'", sourceLinePos.Line(), e.SourceLine)
	} else {
		second = fmt.Sprintf("%s: line %d: `%s'", e.Filename, sourceLinePos.Line(), e.SourceLine)
	}
	lines = append(lines, second)
	return strings.Join(lines, "\n")
}

func (e ParseError) WantsSourceLine() bool {
	return !e.noSourceLine
}

func (e ParseError) Recoverable() bool {
	return e.IsRecoverable
}

func (e ParseError) WithInteractiveCommandStringPrefix(name string) ParseError {
	name = strings.TrimSpace(name)
	if name == "" {
		return e
	}
	e.bashPrefix = name
	e.bashPrefixNoLine = true
	e.noSourceLine = true
	return e
}

// LangError is returned when the parser encounters code that is only valid in
// other shell language variants. The error includes what feature is not present
// in the current language variant, and what languages support it.
type LangError struct {
	Filename string
	Pos      Pos
	// End is the optional exclusive end position of the exact token sequence
	// that triggered the variant-gated syntax diagnostic.
	End Pos

	// TODO: consider replacing the Langs slice with a bitset.

	// Feature briefly describes which language feature caused the error.
	// This compatibility text remains populated for callers matching the old
	// free-form surface. New code should prefer FeatureID.
	Feature string
	// FeatureID identifies the stable variant-gated syntax family.
	FeatureID FeatureID
	// FeatureSubtype identifies a more specific stable surface form within
	// FeatureID when the parser can distinguish it.
	FeatureSubtype FeatureSubtype
	// FeatureDetail carries any operator, spelling-specific detail, or other
	// stable raw fragment associated with FeatureSubtype when rendering Feature.
	FeatureDetail string
	// Langs lists some of the language variants which support the feature.
	Langs []LangVariant
	// LangUsed is the language variant used which led to the error.
	LangUsed LangVariant
}

func (e LangError) Error() string {
	feature := e.Feature
	if feature == "" {
		feature = e.FeatureID.Format(e.FeatureDetail)
	}
	var sb strings.Builder
	if e.Filename != "" {
		sb.WriteString(e.Filename)
		sb.WriteString(":")
	}
	sb.WriteString(e.Pos.String())
	sb.WriteString(": ")
	sb.WriteString(feature)
	if strings.HasSuffix(feature, "s") {
		sb.WriteString(" are a ")
	} else {
		sb.WriteString(" is a ")
	}
	for i, lang := range e.Langs {
		if i > 0 {
			sb.WriteString("/")
		}
		sb.WriteString(lang.String())
	}
	sb.WriteString(" feature; tried parsing as ")
	sb.WriteString(e.LangUsed.String())
	return sb.String()
}

func (p *Parser) newParseError(pos Pos, text string, meta parseErrorMetadata) ParseError {
	pe := ParseError{
		Filename:   p.f.Name,
		Pos:        pos,
		Text:       text,
		Incomplete: p.tok == _EOF && p.Incomplete(),
	}
	meta.apply(&pe)
	// When a syntax error occurs during alias expansion, use the
	// expanded alias text as the source line so the error message
	// shows the problematic expansion rather than the original alias
	// invocation.
	if p.aliasSource != nil {
		pe.SourceLine = p.aliasSource.Value
	}
	return pe
}

func (p *Parser) posErrWithMetadata(pos Pos, meta parseErrorMetadata, format string, args ...any) {
	p.errPass(p.newParseError(pos, fmt.Sprintf(format, args...), meta))
}

func (p *Parser) posErrWithMetadataContext(pos Pos, meta parseErrorMetadata, ctx parseErrorContext, format string, args ...any) {
	pe := p.newParseError(pos, fmt.Sprintf(format, args...), meta)
	pe.typedContext = ctx
	p.errPass(pe)
}

func (p *Parser) posErr(pos Pos, format string, args ...any) {
	p.posErrWithMetadata(pos, parseErrorMetadata{}, format, args...)
}

func (p *Parser) posErrContext(pos Pos, ctx parseErrorContext, format string, args ...any) {
	p.posErrWithMetadataContext(pos, parseErrorMetadata{}, ctx, format, args...)
}

func (p *Parser) posErrSecondary(pos Pos, secondary, format string, args ...any) {
	pe := p.newParseError(pos, fmt.Sprintf(format, args...), parseErrorMetadata{})
	pe.SecondaryText = secondary
	pe.SecondaryPos = pos
	p.errPass(pe)
}

func (p *Parser) posErrSecondaryWithMetadata(pos Pos, meta parseErrorMetadata, secondary, format string, args ...any) {
	pe := p.newParseError(pos, fmt.Sprintf(format, args...), meta)
	pe.SecondaryText = secondary
	pe.SecondaryPos = pos
	p.errPass(pe)
}

func (p *Parser) posErrSecondaryDetailed(pos, secondaryPos, sourceLinePos Pos, sourceLine, secondary, format string, args ...any) {
	pe := p.newParseError(pos, fmt.Sprintf(format, args...), parseErrorMetadata{})
	pe.SecondaryText = secondary
	pe.SecondaryPos = secondaryPos
	pe.SourceLine = sourceLine
	pe.SourceLinePos = sourceLinePos
	p.errPass(pe)
}

func (p *Parser) posRecoverableErr(pos Pos, format string, args ...any) {
	p.posErrWithMetadata(pos, parseErrorMetadata{isRecoverable: true}, format, args...)
}

func (p *Parser) curErr(format string, args ...any) {
	p.posErr(p.pos, format, args...)
}

func (p *Parser) curErrContext(ctx parseErrorContext, format string, args ...any) {
	p.posErrContext(p.pos, ctx, format, args...)
}

func (p *Parser) curErrSecondary(secondary, format string, args ...any) {
	p.posErrSecondary(p.pos, secondary, format, args...)
}

func (p *Parser) checkLang(pos Pos, langSet LangVariant, featureID FeatureID, detail ...string) {
	p.checkLangSpan(pos, Pos{}, langSet, featureID, detail...)
}

func (p *Parser) checkLangSpan(pos, end Pos, langSet LangVariant, featureID FeatureID, detail ...string) {
	featureDetail := ""
	if len(detail) > 0 {
		featureDetail = detail[0]
	}
	p.checkLangSpanDetailed(pos, end, langSet, featureID, FeatureSubtypeUnknown, featureDetail)
}

func (p *Parser) checkLangDetailed(pos Pos, langSet LangVariant, featureID FeatureID, featureSubtype FeatureSubtype, featureDetail string) {
	p.checkLangSpanDetailed(pos, Pos{}, langSet, featureID, featureSubtype, featureDetail)
}

func (p *Parser) checkLangSpanDetailed(pos, end Pos, langSet LangVariant, featureID FeatureID, featureSubtype FeatureSubtype, featureDetail string) {
	if p.lang.in(langSet) {
		return
	}
	if langBashLike.in(langSet) {
		// If we're reporting an error because a feature is for bash-like funcs,
		// just mention "bash" rather than "bash/bats" for the sake of clarity.
		langSet &^= LangBats
	}
	p.errPass(LangError{
		Filename:       p.f.Name,
		Pos:            pos,
		End:            end,
		Feature:        featureID.Format(featureDetail),
		FeatureID:      featureID,
		FeatureSubtype: featureSubtype,
		FeatureDetail:  featureDetail,
		Langs:          slices.Collect(langSet.bits()),
		LangUsed:       p.lang,
	})
}

func (p *Parser) currentUnexpectedTokenQuote() string {
	switch p.tok {
	case _Lit, _LitWord, _LitRedir:
		return bashQuoteString(p.val)
	case sglQuote, dollSglQuote, dblQuote, dollDblQuote, bckQuote, dollar, dollBrace,
		dollDblParen, dollParen, dollBrack, cmdIn, assgnParen, cmdOut:
		if _, raw := p.getWordRaw(); raw != "" {
			return bashQuoteString(raw)
		}
	}
	return p.tok.bashQuote()
}

func (p *Parser) currentUnexpectedFuncOpenToken() string {
	switch p.tok {
	case _EOF, _Newl:
		return "newline"
	case _Lit, _LitWord, _LitRedir:
		return p.val
	case sglQuote, dollSglQuote, dblQuote, dollDblQuote, bckQuote, dollar, dollBrace,
		dollDblParen, dollParen, dollBrack, cmdIn, assgnParen, cmdOut:
		if _, raw := p.getWordRaw(); raw != "" {
			return raw
		}
	}
	return p.tok.String()
}

func (p *Parser) posCurrentUnexpectedErr(pos Pos) {
	unexpected, quoted := p.currentUnexpectedTokenDiagnostic()
	p.unexpectedTokenErr(pos, unexpected, quoted)
}

func (p *Parser) posCurrentUnexpectedErrContext(pos Pos, ctx parseErrorContext) {
	unexpected, quoted := p.currentUnexpectedTokenDiagnostic()
	ctx = withFuncOpenBashToken(ctx, p.currentUnexpectedFuncOpenToken())
	p.posErrWithMetadataContext(pos, parseErrorMetadata{
		kind:       ParseErrorKindUnexpected,
		unexpected: unexpected,
	}, ctx, "syntax error near unexpected token %s", quoted)
}

func (p *Parser) curUnexpectedErr() {
	p.posCurrentUnexpectedErr(p.pos)
}

func (p *Parser) stmts(yield func(*Stmt, error) bool, stops ...reservedWord) {
	gotEnd := true
loop:
	for p.tok != _EOF {
		newLine := p.got(_Newl)
		switch p.tok {
		case _LitWord:
			if p.atRsrv(stops...) {
				break loop
			}
			if p.atRsrv(rsrvRightBrace) {
				p.strayReservedWordErr(p.pos, rsrvRightBrace, `%#q can only be used to close a block`, rightBrace)
			}
		case rightParen:
			if p.quote == subCmd {
				break loop
			}
		case bckQuote:
			if p.backquoteEnd() {
				break loop
			}
		case dblSemicolon, semiAnd, dblSemiAnd, semiOr:
			if p.quote == switchCase {
				break loop
			}
			p.curErr("%#q can only be used in a case clause", p.tok)
		}
		if !newLine && !gotEnd {
			p.curErr("statements must be separated by &, ; or a newline")
		}
		if p.tok == _EOF {
			break
		}
		start := p.cursorSnapshot()
		p.openNodes++
		s := p.getStmt(true, false, false)
		p.openNodes--
		if s == nil {
			p.invalidStmtStart()
			break
		}
		gotEnd = s.Semicolon.IsValid()
		if !yield(s, p.err) {
			break
		}
		if p.err != nil {
			break
		}
		if !start.progressed(p) {
			p.posRecoverableErr(start.pos, "internal parser error: no progress parsing statement")
			break
		}
	}
}

func (p *Parser) stmtList(stops ...reservedWord) ([]*Stmt, []Comment) {
	var stmts []*Stmt
	var last []Comment
	fn := func(s *Stmt, err error) bool {
		stmts = append(stmts, s)
		return true
	}
	p.stmts(fn, stops...)
	split := len(p.accComs)
	if p.atRsrv(rsrvElif, rsrvElse, rsrvFi) {
		// Split the comments, so that any aligned with an opening token
		// get attached to it. For example:
		//
		//     if foo; then
		//         # inside the body
		//     # document the else
		//     else
		//     fi
		// TODO(mvdan): look into deduplicating this with similar logic
		// in caseItems.
		for i, c := range slices.Backward(p.accComs) {
			if c.Pos().Col() != p.pos.Col() {
				break
			}
			split = i
		}
	}
	if split > 0 { // keep last nil if empty
		last = p.accComs[:split]
	}
	p.accComs = p.accComs[split:]
	return stmts, last
}

func (p *Parser) invalidStmtStart() {
	p.curUnexpectedErr()
}

func (p *Parser) getWord() *Word {
	if w := p.wordAnyNumber(); len(w.Parts) > 0 && p.err == nil {
		return w
	}
	return nil
}

func (p *Parser) getWordRaw() (*Word, string) {
	p.startWordCapture()
	w := p.getWord()
	p.captureWordRaw = false
	if w == nil {
		p.wordRawBs = p.wordRawBs[:0]
		return nil, ""
	}
	span := int(w.End().Offset() - w.Pos().Offset())
	if span < 0 || span > len(p.wordRawBs) {
		span = len(p.wordRawBs)
	}
	raw := string(p.wordRawBs[:span])
	p.wordRawBs = p.wordRawBs[:0]
	if w.raw == "" {
		w.raw = raw
	}
	return w, raw
}

func (p *Parser) startWordCapture() {
	p.wordRawBs = append(p.wordRawBs[:0], p.currentTokenCaptureSeed()...)
	p.captureWordRaw = true
}

func (p *Parser) stopWordCapture() string {
	p.captureWordRaw = false
	raw := strings.TrimRight(string(p.wordRawBs), " \t\r\n")
	p.wordRawBs = p.wordRawBs[:0]
	return raw
}

func (p *Parser) startCandidateCapture() {
	p.wordRawBs = append(p.wordRawBs[:0], p.currentTokenSource()...)
	p.wordRawBs = append(p.wordRawBs, p.currentRuneSource()...)
	p.captureWordRaw = true
}

func (p *Parser) stopCandidateCapture(start, end Pos) string {
	p.captureWordRaw = false
	span := int(end.Offset() - start.Offset())
	if span < 0 || span > len(p.wordRawBs) {
		span = len(p.wordRawBs)
	}
	raw := strings.TrimRight(string(p.wordRawBs[:span]), " \t\r\n")
	p.wordRawBs = p.wordRawBs[:0]
	return raw
}

func (p *Parser) currentTokenCaptureSeed() string {
	seed := p.currentTokenSource()
	switch p.tok {
	case dollar, dollSglQuote, dollDblQuote, dollBrace, dollBrack, dollParen, dollDblParen,
		sglQuote, dblQuote, cmdIn, assgnParen, cmdOut:
		seed += p.currentRuneSource()
	}
	return seed
}

func (p *Parser) currentTokenSource() string {
	switch p.tok {
	case _Lit, _LitWord, _LitRedir:
		return p.val
	default:
		return p.tok.String()
	}
}

func (p *Parser) currentRuneSource() string {
	if p.r == utf8.RuneSelf || p.w <= 0 {
		return ""
	}
	start := int(p.bsp) - p.w
	if start < 0 || start > len(p.bs) || int(p.bsp) > len(p.bs) {
		return ""
	}
	return string(p.bs[start:p.bsp])
}

func (p *Parser) bufferedSourceTail() string {
	var b strings.Builder
	if src := p.currentRuneSource(); src != "" {
		b.WriteString(src)
	}
	if idx := int(p.bsp); idx >= 0 && idx <= len(p.bs) {
		b.Write(p.bs[idx:])
	}
	return b.String()
}

func (p *Parser) sourceFromPos(pos Pos) string {
	start, ok := p.bufferIndex(pos.Offset())
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	return string(buf[start:])
}

func (p *Parser) sourceBetween(left, right Pos) string {
	start, end, ok := p.bufferRange(left, right)
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	return string(buf[start:end])
}

func (p *Parser) sourceLineAtPos(pos Pos) string {
	if !pos.IsValid() {
		return ""
	}
	start, ok := p.bufferIndex(pos.Offset())
	if !ok {
		return ""
	}
	end, ok := p.bufferIndex(pos.Offset())
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	for start > 0 && buf[start-1] != '\n' {
		start--
	}
	for end < len(buf) && buf[end] != '\n' {
		end++
	}
	return string(buf[start:end])
}

func (p *Parser) arithmInnerSource(left, right Pos, bracket bool) string {
	start, end, ok := p.bufferRange(left, right)
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	if !bracket {
		start += 3 // $((
	} else {
		start += 2 // $[
	}
	if start < 0 || start > end || end > len(buf) {
		return ""
	}
	return string(buf[start:end])
}

func (p *Parser) arithmCmdSource(left, right Pos) string {
	start, end, ok := p.bufferRange(left, right)
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	start += 2 // ((
	if start < 0 || start > end || end > len(buf) {
		return ""
	}
	return string(buf[start:end])
}

func (p *Parser) sourceRange(start, end Pos) string {
	if !start.IsValid() || !end.IsValid() {
		return ""
	}
	from, to, ok := p.bufferRange(start, end)
	if !ok {
		return ""
	}
	buf, _ := p.sourceBuffer()
	return string(buf[from:to])
}

func (p *Parser) sourceBuffer() ([]byte, int64) {
	if p.sourceBs != nil {
		return p.sourceBs, p.sourceOffs
	}
	return p.bs, p.offs
}

func (p *Parser) bufferIndex(offset uint) (int, bool) {
	buf, baseOffs := p.sourceBuffer()
	base := int(baseOffs)
	idx := int(offset) - base
	if idx < 0 || idx > len(buf) {
		return 0, false
	}
	return idx, true
}

func (p *Parser) bufferRange(start, end Pos) (int, int, bool) {
	from, ok := p.bufferIndex(start.Offset())
	if !ok {
		return 0, 0, false
	}
	to, ok := p.bufferIndex(end.Offset())
	if !ok || to < from {
		return 0, 0, false
	}
	return from, to, true
}

func (p *Parser) currentSourceTail() ([]byte, error) {
	var tail []byte
	if src := p.currentRuneSource(); src != "" {
		tail = append(tail, src...)
	}
	if idx := int(p.bsp); idx >= 0 && idx <= len(p.bs) {
		tail = append(tail, p.bs[idx:]...)
	}
	rest, err := p.unreadSourceTail()
	tail = append(tail, rest...)
	return tail, err
}

func (p *Parser) lookaheadSourceTail(limit int, done func([]byte) bool) string {
	tail := make([]byte, 0, limit)
	if src := p.currentRuneSource(); src != "" {
		tail = append(tail, src...)
	}
	if idx := int(p.bsp); idx >= 0 && idx <= len(p.bs) {
		tail = append(tail, p.bs[idx:]...)
	}
	if limit <= 0 || len(tail) >= limit || done(tail) || p.src == nil || p.readEOF || p.readErr != nil {
		return string(tail)
	}

	reader := p.src
	extra := make([]byte, 0, limit-len(tail))
	var one [1]byte
	for len(tail) < limit && !done(tail) {
		n, err := reader.Read(one[:])
		if n > 0 {
			extra = append(extra, one[:n]...)
			tail = append(tail, one[:n]...)
		}
		if err != nil || n == 0 {
			break
		}
	}
	if len(extra) > 0 {
		p.src = io.MultiReader(bytes.NewReader(extra), reader)
	}
	return string(tail)
}

func paramExpFlagsMetadataTailDone(tail []byte) bool {
	if len(tail) == 0 || tail[0] != '(' {
		return true
	}
	return bytes.IndexByte(tail[1:], ')') >= 0
}

func paramExpIndexMetadataTailDone(tail []byte) bool {
	if len(tail) == 0 || tail[0] != '[' {
		return true
	}
	for _, b := range tail[1:] {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return true
		}
	}
	return false
}

func (p *Parser) unreadSourceTail() ([]byte, error) {
	switch p.readErr {
	case nil:
	case io.EOF:
		return nil, nil
	default:
		return nil, p.readErr
	}
	if p.readEOF || p.src == nil {
		return nil, nil
	}
	var buf bytes.Buffer
	_, err := buf.ReadFrom(p.src)
	rest := buf.Bytes()
	if err != nil && err != io.EOF {
		return rest, err
	}
	return rest, nil
}

func (p *Parser) loadReplay(pos Pos, src []byte, readErr error) {
	p.src = bytes.NewReader(nil)
	p.bs = append(p.bs[:0], src...)
	p.offs = int64(pos.Offset())
	p.line = int64(pos.Line())
	p.col = int64(pos.Col())
	p.err = nil
	p.readErr = readErr
	p.readEOF = false
	if len(p.bs) == 0 {
		p.r = utf8.RuneSelf
		p.w = 1
		p.bsp = 1
		return
	}
	r, w := utf8.DecodeRune(p.bs)
	p.r = r
	p.w = w
	p.bsp = uint(w)
}

func parseErrRecoverableInBackquotes(err ParseError) bool {
	if err.Unexpected != ParseErrorSymbolEOF && err.Unexpected != ParseErrorSymbolBackquote {
		return false
	}
	switch err.Kind {
	case ParseErrorKindUnclosed:
		return len(err.Expected) == 1 && ((err.Expected[0] == ParseErrorSymbolSingleQuote || err.Expected[0] == ParseErrorSymbolDoubleQuote) ||
			(err.Expected[0] == ParseErrorSymbolRightBrace && err.Construct == ParseErrorSymbolLeftBrace))
	case ParseErrorKindUnmatched:
		return len(err.Expected) == 1 && err.Expected[0] == ParseErrorSymbolRightBrace && err.Construct == ParseErrorSymbolLeftBrace
	default:
		return false
	}
}

func advancePosBytes(pos Pos, src []byte) Pos {
	for _, b := range src {
		switch b {
		case '\n':
			pos.lineCol += 1 << colBitSize
			pos.lineCol &^= colBitMask
			pos.lineCol++
		default:
			pos.lineCol++
		}
		pos.offs++
	}
	return pos
}

func findClosingBackquote(src []byte) int {
	for i := 1; i < len(src); i++ {
		switch src[i] {
		case '\\':
			if i+1 < len(src) {
				i++
			}
		case '`':
			return i
		}
	}
	return -1
}

func hasContinuedScriptAfterRecoveredBackquote(src []byte) bool {
	src = bytes.TrimLeft(src, " \t\r")
	if len(src) == 0 {
		return false
	}
	switch src[0] {
	case '\n', ';', '&', '|':
		// Only recover a leading malformed backquote when there is later script
		// for bash to keep parsing after the recovered command substitution.
		return len(bytes.TrimSpace(src[1:])) > 0
	default:
		return false
	}
}

func (p *Parser) recoverableBackquoteSource(left Pos) ([]byte, error) {
	if src := p.sourceFromPos(left); src != "" {
		rest, err := p.unreadSourceTail()
		full := make([]byte, 0, len(src)+len(rest))
		full = append(full, src...)
		full = append(full, rest...)
		return full, err
	}
	tail, err := p.currentSourceTail()
	if len(tail) == 0 {
		return nil, err
	}
	full := make([]byte, 1+len(tail))
	full[0] = '`'
	copy(full[1:], tail)
	return full, err
}

func (p *Parser) recoverBackquoteCmdSubst(cs *CmdSubst, old saveState, src []byte, readErr error) bool {
	if len(src) == 0 {
		return false
	}
	if src[0] != '`' {
		full := make([]byte, 1+len(src))
		full[0] = '`'
		copy(full[1:], src)
		src = full
	}
	closeIndex := findClosingBackquote(src)
	if closeIndex < 0 {
		return false
	}
	if cs.Left.Offset() == 0 && !hasContinuedScriptAfterRecoveredBackquote(src[closeIndex+1:]) {
		return false
	}

	p.postNested(old)
	if old.quote == dblQuotes {
		p.openBquoteDquotes--
	}
	p.openBquotes--

	p.err = nil
	cs.Stmts = nil
	cs.Last = nil
	cs.Right = advancePosBytes(cs.Left, src[:closeIndex])
	nextPos := advancePosBytes(cs.Left, src[:closeIndex+1])
	p.loadReplay(nextPos, src[closeIndex+1:], readErr)
	p.next()
	return true
}

func (p *Parser) setSourceBuffer(pos Pos, src []byte) {
	p.sourceBs = append(p.sourceBs[:0], src...)
	p.sourceOffs = int64(pos.Offset())
}

func cloneHeredocStops(src []heredocStop) []heredocStop {
	if len(src) == 0 {
		return nil
	}
	dst := make([]heredocStop, len(src))
	for i, stop := range src {
		dst[i] = stop
		dst[i].word = append([]byte(nil), stop.word...)
	}
	return dst
}

func copyAliasActive(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (p *Parser) parenAmbiguityClone() *Parser {
	clone := *p
	clone.err = nil
	clone.readErr = nil
	clone.readEOF = false
	clone.bs = nil
	clone.bsp = 0
	clone.sourceBs = nil
	clone.sourceOffs = 0
	clone.r = 0
	clone.w = 0
	clone.accComs = nil
	clone.curComs = &clone.accComs
	clone.litBatch = nil
	clone.wordBatch = nil
	clone.litBs = nil
	clone.wordRawBs = nil
	clone.captureWordRaw = false
	clone.pendingArrayWord = ""
	clone.pendingArrayWordPos = Pos{}
	clone.keepComments = false
	clone.recoveredErrors = 0
	clone.heredocs = append([]*Redirect(nil), p.heredocs...)
	clone.hdocStops = cloneHeredocStops(p.hdocStops)
	clone.aliasChain = append([]*AliasExpansion(nil), p.aliasChain...)
	clone.aliasInputStack = append([]aliasInputState(nil), p.aliasInputStack...)
	clone.aliasActive = copyAliasActive(p.aliasActive)
	clone.tokAliasChain = nil
	clone.parenAmbiguityDisabled = true
	return &clone
}

func (p *Parser) allowParenAmbiguityFallback(stmts []*Stmt, last []Comment, outerRight Pos) bool {
	if len(stmts) == 0 || !stmtStartsWithSubshell(stmts[0]) {
		return false
	}
	if len(stmts) != 1 || len(last) != 0 {
		return true
	}
	stmt := stmts[0]
	if stmt == nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown ||
		stmt.Semicolon.IsValid() || len(stmt.Redirs) > 0 {
		return true
	}
	sub, ok := stmt.Cmd.(*Subshell)
	if !ok {
		return true
	}
	gap := p.sourceRange(posAddCol(sub.Rparen, 1), outerRight)
	return gap != "" && strings.TrimSpace(gap) == ""
}

func keepParenAmbiguityArithError(stmts []*Stmt, last []Comment) bool {
	if len(stmts) != 1 || len(last) != 0 {
		return false
	}
	stmt := stmts[0]
	if stmt == nil {
		return false
	}
	sub, ok := stmt.Cmd.(*Subshell)
	if !ok {
		return false
	}
	return len(sub.Stmts) != 1 || len(sub.Last) != 0
}

func hasInternalNewline(src []byte) bool {
	trimmed := strings.TrimRight(string(src), "\r\n")
	return strings.Contains(trimmed, "\n")
}

func hasNestedDblRightParen(src []byte) bool {
	return bytes.Count(src, []byte("))")) > 1
}

func parenAmbiguityInnerRight(stmts []*Stmt) Pos {
	if len(stmts) != 1 {
		return Pos{}
	}
	stmt := stmts[0]
	if stmt == nil {
		return Pos{}
	}
	sub, ok := stmt.Cmd.(*Subshell)
	if !ok {
		return Pos{}
	}
	return sub.Rparen
}

func stmtStartsWithSubshell(stmt *Stmt) bool {
	if stmt == nil {
		return false
	}
	return commandStartsWithSubshell(stmt.Cmd)
}

func commandStartsWithSubshell(cmd Command) bool {
	switch cmd := cmd.(type) {
	case *Subshell:
		return true
	case *BinaryCmd:
		return stmtStartsWithSubshell(cmd.X)
	default:
		return false
	}
}

func regexNearFragment(s string) string {
	var b strings.Builder
	escaped := false
	inClass := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			if ch == ' ' || ch == '\t' || ch == '\n' {
				return b.String()
			}
			b.WriteByte(ch)
			escaped = false
			continue
		}
		if inClass {
			b.WriteByte(ch)
			if ch == ']' {
				inClass = false
			}
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '[':
			inClass = true
			b.WriteByte(ch)
		case ' ', '\t', '\n', ';', '&', '|':
			return b.String()
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func literalRegexUnexpectedTopLevelRParen(expr string) (int, string, string, bool) {
	escaped := false
	inClass := false
	depth := 0
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		if escaped {
			escaped = false
			continue
		}
		if inClass {
			switch ch {
			case '\\':
				escaped = true
			case ']':
				inClass = false
			}
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '[':
			inClass = true
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i, ")", regexNearFragment(expr), true
			}
			depth--
		}
	}
	return 0, "", "", false
}

func patternSource(pat *Pattern) string {
	if pat == nil {
		return ""
	}
	var buf bytes.Buffer
	printer := NewPrinter(Minify(true))
	if err := printer.Print(&buf, pat); err != nil {
		panic(err)
	}
	return buf.String()
}

func (p *Parser) getLit() *Lit {
	switch p.tok {
	case _Lit, _LitWord, _LitRedir:
		l := p.lit(p.pos, p.val)
		p.next()
		return l
	}
	return nil
}

func (p *Parser) wordParts(wps []WordPart) []WordPart {
	if p.quote == noState {
		p.quote = unquotedWordCont
		defer func() { p.quote = noState }()
	}
	for {
		start := p.cursorSnapshot()
		p.openNodes++
		n := p.wordPart()
		p.openNodes--
		if n == nil {
			if len(wps) == 0 {
				return nil // normalize empty lists into nil
			}
			return wps
		}
		wps = append(wps, n)
		if !start.progressed(p) {
			p.posRecoverableErr(start.pos, "internal parser error: no progress parsing word part")
			return wps
		}
		if p.spaced {
			return wps
		}
	}
}

func (p *Parser) finishWord(w *Word) *Word {
	if w == nil || len(w.Parts) == 0 {
		return w
	}
	if w.raw == "" {
		w.raw = p.sourceRange(w.Pos(), w.End())
	}
	if p.braceWordPartsAllowed() {
		w.Parts = splitBraceWordParts(w.Parts)
	}
	return w
}

func (p *Parser) braceWordPartsAllowed() bool {
	if !p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
		return false
	}
	switch p.quote {
	case runeByRune, dblQuotes, hdocWord, hdocBody, hdocBodyTabs:
		return false
	case arithmExpr, arithmExprLet, arithmExprCmd, paramExpArithm:
		return false
	default:
		return true
	}
}

func functionNameNeedsRuntimeValidation(w *Word) bool {
	if w == nil {
		return false
	}
	for _, part := range w.Parts {
		switch part.(type) {
		case *ParamExp, *CmdSubst, *ArithmExp, *ProcSubst:
			return true
		}
	}
	return false
}

func functionNameSource(w *Word) string {
	if w == nil {
		return ""
	}
	if lit := w.Lit(); lit != "" {
		return lit
	}
	var sb strings.Builder
	if err := NewPrinter().Print(&sb, w); err != nil {
		return ""
	}
	return sb.String()
}

func (p *Parser) ensureNoNested(pos Pos) {
	if p.forbidNested {
		p.posErr(pos, "expansions not allowed in heredoc words")
	}
}

func (p *Parser) arithmWordPartTo(salvageTail bool, tailEnd Pos) *ArithmExp {
	left := p.tok
	ar := &ArithmExp{Left: p.pos, Bracket: left == dollBrack}
	old := p.preNested(arithmExpr)
	p.next()
	if p.got(hash) {
		p.checkLang(ar.Pos(), LangMirBSDKorn, FeatureArithmeticUnsignedExpr)
		ar.Unsigned = true
	}
	ar.X = p.followArithm(left, ar.Left)
	if ar.Bracket {
		if p.tok != rightBrack {
			p.arithmMatchingErr(ar.Left, dollBrack, rightBrack)
		}
		p.postNested(old)
		ar.Right = p.pos
		p.next()
	} else {
		expr := ar.X
		ar.Right = p.arithmEndExpr(&expr, dollDblParen, ar.Left, old, salvageTail, tailEnd)
		ar.X = expr
	}
	ar.Source = p.arithmInnerSource(ar.Left, ar.Right, ar.Bracket)
	return ar
}

func (p *Parser) arithmWordPart(salvageTail bool) *ArithmExp {
	return p.arithmWordPartTo(salvageTail, Pos{})
}

func (p *Parser) parenAmbiguityWordPart() WordPart {
	p.parenAmbiguityProbeDepth++
	defer func() { p.parenAmbiguityProbeDepth-- }()

	afterToken, err := p.currentSourceTail()
	start := p.pos
	fullSrc := append([]byte(dollDblParen.String()), afterToken...)
	arithPos := posAddCol(start, len(dollDblParen.String()))
	arith := p.parenAmbiguityClone()
	arith.tok = dollDblParen
	arith.pos = start
	arith.loadReplay(arithPos, afterToken, err)
	arith.setSourceBuffer(start, fullSrc)
	part := arith.arithmWordPart(false)
	arithOK := arith.err == nil
	arithRight := Pos{}
	if part != nil {
		arithRight = part.Right
	}

	fallbackSrc := append([]byte{'('}, afterToken...)
	fallbackPos := posAddCol(start, len(dollParen.String()))
	cmd := p.parenAmbiguityClone()
	cmd.tok = dollParen
	cmd.pos = start
	cmd.loadReplay(fallbackPos, fallbackSrc, err)
	cmd.setSourceBuffer(start, fullSrc)
	cs := cmd.cmdSubst()
	fallbackIncomplete := cmd.err != nil && IsIncomplete(cmd.err) && hasInternalNewline(afterToken)
	if fallbackIncomplete {
		p.tok = dollParen
		p.pos = start
		p.loadReplay(fallbackPos, fallbackSrc, err)
		p.setSourceBuffer(start, fullSrc)
		return p.cmdSubst()
	}
	fallbackOK := cmd.err == nil && cmd.allowParenAmbiguityFallback(cs.Stmts, cs.Last, cs.Right)
	if fallbackOK {
		p.tok = dollParen
		p.pos = start
		p.loadReplay(fallbackPos, fallbackSrc, err)
		p.setSourceBuffer(start, fullSrc)
		return p.cmdSubst()
	}
	arithEndedEarly := false
	fallbackTailEnd := Pos{}
	if arithOK && cmd.err == nil {
		fallbackTailEnd = parenAmbiguityInnerRight(cs.Stmts)
		if fallbackTailEnd.After(arithRight) {
			arithEndedEarly = true
		}
	} else if cmd.err == nil {
		fallbackTailEnd = parenAmbiguityInnerRight(cs.Stmts)
	}

	p.tok = dollDblParen
	p.pos = start
	p.loadReplay(arithPos, afterToken, err)
	p.setSourceBuffer(start, fullSrc)
	if arithOK && !arithEndedEarly {
		return p.arithmWordPart(true)
	}
	if cmd.err == nil && keepParenAmbiguityArithError(cs.Stmts, cs.Last) && !arithEndedEarly && !hasNestedDblRightParen(afterToken) {
		return p.arithmWordPart(false)
	}
	if cmd.err == nil && fallbackTailEnd.IsValid() && hasNestedDblRightParen(afterToken) {
		return p.arithmWordPartTo(true, fallbackTailEnd)
	}
	return p.arithmWordPart(true)
}

func (p *Parser) wordPart() WordPart {
	switch p.tok {
	case _Lit, _LitWord, _LitRedir:
		l := p.lit(p.pos, p.val)
		p.next()
		return l
	case dollBrace:
		p.ensureNoNested(p.pos)
		switch p.r {
		case '|':
			p.checkLang(p.pos, langBashLike|LangMirBSDKorn, FeatureSubstitutionReplyVarCmdSubst)
			fallthrough
		case ' ', '\t', '\n':
			p.checkLang(p.pos, langBashLike|LangMirBSDKorn, FeatureSubstitutionTempFileCmdSubst)
			cs := &CmdSubst{
				Left:     p.pos,
				TempFile: p.r != '|',
				ReplyVar: p.r == '|',
			}
			old := p.preNested(subCmd)
			p.rune() // don't tokenize '|'
			p.next()
			cs.Stmts, cs.Last = p.stmtList(rsrvRightBrace)
			p.postNested(old)
			pos, ok := p.gotRsrv(rsrvRightBrace)
			if !ok {
				p.matchingErr(cs.Left, dollBrace, rightBrace)
			}
			cs.Right = pos
			return cs
		default:
			return p.paramExp()
		}
	case dollDblParen, dollBrack:
		p.ensureNoNested(p.pos)
		if p.tok == dollDblParen {
			if p.parenAmbiguityDisabled {
				return p.arithmWordPart(true)
			}
			return p.parenAmbiguityWordPart()
		}
		return p.arithmWordPart(true)
	case dollParen:
		p.ensureNoNested(p.pos)
		return p.cmdSubst()
	case dollar:
		pe := p.paramExp()
		if pe == nil { // was not actually a parameter expansion, like: "foo$"
			l := p.lit(p.pos, "$")
			p.next()
			return l
		}
		p.ensureNoNested(pe.Dollar)
		return pe
	case assgnParen:
		p.checkLang(p.pos, LangZsh, FeatureSubstitutionProcess, fmt.Sprintf("%#q", p.tok))
		fallthrough
	case cmdIn, cmdOut:
		p.ensureNoNested(p.pos)
		ps := &ProcSubst{Op: ProcOperator(p.tok), OpPos: p.pos}
		old := p.preNested(subCmd)
		p.next()
		ps.Stmts, ps.Last = p.stmtList()
		p.postNested(old)
		ps.Rparen = p.matched(ps.OpPos, token(ps.Op), rightParen)
		return ps
	case sglQuote, dollSglQuote:
		sq := &SglQuoted{Left: p.pos, Dollar: p.tok == dollSglQuote}
		r := p.r
		for p.newLit(r); ; r = p.rune() {
			switch r {
			case '\\':
				if sq.Dollar {
					p.rune()
				}
			case '\'':
				sq.Right = p.nextPos()
				sq.Value = p.endLit()

				p.rune()
				p.next()
				return sq
			case escNewl:
				p.litBs = append(p.litBs, '\\', '\n')
			case utf8.RuneSelf:
				p.tok = _EOF
				if p.recoverError() {
					sq.Right = recoveredPos
					return sq
				}
				p.quoteErr(sq.Pos(), sglQuote)
				return nil
			}
		}
	case dblQuote, dollDblQuote:
		if p.quote == dblQuotes {
			// p.tok == dblQuote, as "foo$" puts $ in the lit
			return nil
		}
		return p.dblQuoted()
	case bckQuote:
		if p.backquoteEnd() {
			return nil
		}
		p.ensureNoNested(p.pos)
		cs := &CmdSubst{Left: p.pos, Backquotes: true}
		old := p.preNested(subCmdBckquo)
		p.openBquotes++
		if old.quote == dblQuotes {
			p.openBquoteDquotes++
		}

		// The lexer didn't call p.rune for us, so that it could have
		// the right p.openBquotes to properly handle backslashes.
		p.rune()

		p.next()
		cs.Stmts, cs.Last = p.stmtList()
		if p.err != nil {
			if parseErr, ok := p.err.(ParseError); ok && parseErrRecoverableInBackquotes(parseErr) {
				backquoteSrc, backquoteErr := p.recoverableBackquoteSource(cs.Left)
				if p.recoverBackquoteCmdSubst(cs, old, backquoteSrc, backquoteErr) {
					return cs
				}
			}
		}
		if p.tok == bckQuote && p.lastBquoteEsc < p.openBquotes-1 {
			// e.g. found ` before the nested backquote \` was closed.
			p.tok = _EOF
			p.quoteErr(cs.Pos(), bckQuote)
		}
		p.postNested(old)
		if old.quote == dblQuotes {
			p.openBquoteDquotes--
		}
		p.openBquotes--
		cs.Right = p.pos

		// Like above, the lexer didn't call p.rune for us.
		p.rune()
		if !p.got(bckQuote) {
			if p.recoverError() {
				cs.Right = recoveredPos
			} else {
				p.quoteErr(cs.Pos(), bckQuote)
			}
		}
		return cs
	case leftParen:
		if p.lang.in(LangZsh) && p.r != ')' {
			// Zsh glob qualifier like *(N) or .(:a); the only case where
			// ( immediately after a word is not a glob qualifier is ()
			// for a function declaration, which the parser handles earlier.
			pos := p.pos
			p.pos = p.nextPos()
			for p.newLit(p.r); p.r != utf8.RuneSelf && p.r != ')'; p.rune() {
			}
			if p.r != ')' {
				p.tok = _EOF // we can only get here due to EOF
				p.matchingErr(pos, leftParen, rightParen)
			}
			p.rune()
			p.val = p.endLit()
			l := p.lit(pos, "("+p.val)
			p.next()
			return l
		}
		return nil
	case globQuest, globStar, globPlus, globAt, globExcl:
		p.checkLang(p.pos, langBashLike|LangMirBSDKorn, FeaturePatternExtendedGlob)
		return p.extGlob(GlobOperator(p.tok), p.pos)
	default:
		return nil
	}
}

func sliceLit(lit *Lit, start, end int) *Lit {
	if lit == nil || start >= end {
		return nil
	}
	return &Lit{
		ValuePos: posAddCol(lit.ValuePos, start),
		ValueEnd: posAddCol(lit.ValuePos, end),
		Value:    lit.Value[start:end],
	}
}

func patternCharClassEnd(raw string, start int) int {
	i := start + 1
	if i >= len(raw) {
		return len(raw)
	}
	switch raw[i] {
	case '!', '^':
		i++
	}
	if i < len(raw) && raw[i] == ']' {
		i++
	}
	for i < len(raw) {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
				continue
			}
		case ']':
			return i + 1
		}
		i++
	}
	return len(raw)
}

func splitPatternLit(lit *Lit) []PatternPart {
	if lit == nil || lit.Value == "" {
		return nil
	}
	var parts []PatternPart
	flush := func(start, end int) {
		if part := sliceLit(lit, start, end); part != nil {
			parts = append(parts, part)
		}
	}
	partStart := 0
	for i := 0; i < len(lit.Value); i++ {
		switch lit.Value[i] {
		case '\\':
			if i+1 < len(lit.Value) {
				i++
			}
		case '*':
			flush(partStart, i)
			parts = append(parts, &PatternAny{Asterisk: posAddCol(lit.ValuePos, i)})
			partStart = i + 1
		case '?':
			flush(partStart, i)
			parts = append(parts, &PatternSingle{Question: posAddCol(lit.ValuePos, i)})
			partStart = i + 1
		case '[':
			end := patternCharClassEnd(lit.Value, i)
			flush(partStart, i)
			parts = append(parts, &PatternCharClass{
				ValuePos: posAddCol(lit.ValuePos, i),
				ValueEnd: posAddCol(lit.ValuePos, end),
				Value:    lit.Value[i:end],
			})
			i = end - 1
			partStart = end
		}
	}
	flush(partStart, len(lit.Value))
	return parts
}

func globOperatorFromByte(b byte) GlobOperator {
	switch b {
	case '?':
		return GlobZeroOrOne
	case '*':
		return GlobZeroOrMore
	case '+':
		return GlobOneOrMore
	case '@':
		return GlobOne
	case '!':
		return GlobExcept
	default:
		return 0
	}
}

func trailingExtGlobOp(lit *Lit) (prefix *Lit, op GlobOperator, opPos Pos, ok bool) {
	if lit == nil || len(lit.Value) == 0 {
		return nil, 0, Pos{}, false
	}
	i := len(lit.Value) - 1
	if i > 0 {
		backslashes := 0
		for j := i - 1; j >= 0 && lit.Value[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			return nil, 0, Pos{}, false
		}
	}
	op = globOperatorFromByte(lit.Value[i])
	if op == 0 {
		return nil, 0, Pos{}, false
	}
	return sliceLit(lit, 0, i), op, posAddCol(lit.ValuePos, i), true
}

type rawPatternParser struct {
	lang LangVariant
}

type rawWordPartParser struct {
	lang LangVariant
}

func (r rawWordPartParser) parse(raw string, base Pos) []WordPart {
	var parts []WordPart
	flushLit := func(from, to int) {
		if from >= to {
			return
		}
		parts = append(parts, &Lit{
			ValuePos: posAddCol(base, from),
			ValueEnd: posAddCol(base, to),
			Value:    raw[from:to],
		})
	}
	partStart := 0
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			flushLit(partStart, i)
			part, consumed, ok := parseRawWordPart(raw[i:], posAddCol(base, i), r.lang)
			if !ok {
				i++
				partStart = i
				continue
			}
			parts = append(parts, part)
			i += consumed
			partStart = i
			continue
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				flushLit(partStart, i)
				part, consumed, ok := parseRawWordPart(raw[i:], posAddCol(base, i), r.lang)
				if ok {
					parts = append(parts, part)
					i += consumed
					partStart = i
					continue
				}
			}
		}
		i++
	}
	flushLit(partStart, len(raw))
	return parts
}

func (r rawPatternParser) parse(raw string, base Pos) *Pattern {
	return r.parseWithBareGroups(raw, base, true)
}

func (r rawPatternParser) parseWithBareGroups(raw string, base Pos, allowBareGroups bool) *Pattern {
	parts, end, _ := r.parseParts(raw, base, 0, false, allowBareGroups)
	return &Pattern{
		Start:  base,
		EndPos: posAddCol(base, end),
		Parts:  parts,
		raw:    raw[:end],
	}
}

func (r rawPatternParser) parseList(raw string, base Pos) []*Pattern {
	patterns := make([]*Pattern, 0, 1)
	start := 0
	for {
		end, delim := r.findPatternListSplit(raw, base, start)
		patterns = append(patterns, r.parseWithBareGroups(raw[start:end], posAddCol(base, start), false))
		if delim != '|' {
			return patterns
		}
		start = end + 1
	}
}

func (r rawPatternParser) parseParts(raw string, base Pos, start int, splitOnBar, allowBareGroups bool) ([]PatternPart, int, byte) {
	var parts []PatternPart
	flushLit := func(from, to int) {
		if from >= to {
			return
		}
		parts = append(parts, splitPatternLit(&Lit{
			ValuePos: posAddCol(base, from),
			ValueEnd: posAddCol(base, to),
			Value:    raw[from:to],
		})...)
	}
	partStart := start
	for i := start; i < len(raw); {
		if splitOnBar && raw[i] == '|' {
			flushLit(partStart, i)
			return parts, i, '|'
		}
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			flushLit(partStart, i)
			part, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if !ok {
				i++
				partStart = i
				continue
			}
			parts = append(parts, part)
			i += consumed
			partStart = i
			continue
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				flushLit(partStart, i)
				part, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					parts = append(parts, part)
					i += consumed
					partStart = i
					continue
				}
			}
		case '(':
			if allowBareGroups {
				group, end, ok := r.parsePatternGroup(raw, base, i)
				if end > i {
					if ok {
						flushLit(partStart, i)
						parts = append(parts, group)
						partStart = end
					}
					i = end
					continue
				}
			}
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				flushLit(partStart, i)
				part, end := r.parseExtGlob(raw, base, i)
				parts = append(parts, part)
				i = end
				partStart = i
				continue
			}
			switch raw[i] {
			case '*':
				flushLit(partStart, i)
				parts = append(parts, &PatternAny{Asterisk: posAddCol(base, i)})
				i++
				partStart = i
				continue
			case '?':
				flushLit(partStart, i)
				parts = append(parts, &PatternSingle{Question: posAddCol(base, i)})
				i++
				partStart = i
				continue
			}
		case '[':
			flushLit(partStart, i)
			end := patternCharClassEnd(raw, i)
			parts = append(parts, &PatternCharClass{
				ValuePos: posAddCol(base, i),
				ValueEnd: posAddCol(base, end),
				Value:    raw[i:end],
			})
			i = end
			partStart = i
			continue
		}
		i++
	}
	flushLit(partStart, len(raw))
	return parts, len(raw), 0
}

func trimPatternRawSegment(raw string, start, end int) (int, int) {
	for start < end {
		if raw[start] == '\\' && start+1 < end && raw[start+1] == '\n' {
			start += 2
			for start < end && (raw[start] == ' ' || raw[start] == '\t') {
				start++
			}
			continue
		}
		switch raw[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			goto trimRight
		}
	}
trimRight:
	for end > start {
		switch raw[end-1] {
		case ' ', '\t', '\n', '\r':
			end--
		default:
			return start, end
		}
	}
	return start, end
}

func (r rawPatternParser) findInvalidCasePatternOperator(raw string, base Pos) (int, byte) {
	for i := 0; i < len(raw); {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				if raw[i+1] == '\n' {
					i += 2
					for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
						i++
					}
				} else {
					i += 2
				}
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if ok {
				i += consumed
				continue
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					i += consumed
					continue
				}
			}
		case '[':
			i = patternCharClassEnd(raw, i)
			continue
		case '(':
			_, end, _ := r.parsePatternGroup(raw, base, i)
			if end > i {
				i = end
				continue
			}
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, end := r.parseExtGlob(raw, base, i)
				i = end
				continue
			}
		case '&':
			return i, '&'
		}
		i++
	}
	return 0, 0
}

func (r rawPatternParser) findPatternListSplit(raw string, base Pos, start int) (int, byte) {
	for i := start; i < len(raw); {
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if ok {
				i += consumed
				continue
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					i += consumed
					continue
				}
			}
		case '[':
			i = patternCharClassEnd(raw, i)
			continue
		case '(':
			_, end, _ := r.parsePatternGroup(raw, base, i)
			if end > i {
				i = end
				continue
			}
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, end := r.parseExtGlob(raw, base, i)
				i = end
				continue
			}
		case '|':
			return i, '|'
		}
		i++
	}
	return len(raw), 0
}

func (r rawPatternParser) hasTopLevelWhitespace(raw string, base Pos) bool {
	for i := 0; i < len(raw); {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			return true
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if ok {
				i += consumed
				continue
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					i += consumed
					continue
				}
			}
		case '[':
			i = patternCharClassEnd(raw, i)
			continue
		case '(':
			_, end, _ := r.parsePatternGroup(raw, base, i)
			if end > i {
				i = end
				continue
			}
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, end := r.parseExtGlob(raw, base, i)
				i = end
				continue
			}
		}
		i++
	}
	return false
}

func (r rawPatternParser) parsePatternGroup(raw string, base Pos, start int) (*PatternGroup, int, bool) {
	group := &PatternGroup{Lparen: posAddCol(base, start)}
	innerStart := start + 1
	i := innerStart
	armStart := innerStart
	depth := 1
	hasTopLevelBar := false
	appendArm := func(end int) {
		group.Patterns = append(group.Patterns, r.parseWithBareGroups(raw[armStart:end], posAddCol(base, armStart), true))
	}
	for i <= len(raw) {
		if i >= len(raw) {
			return nil, len(raw), false
		}
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if ok {
				i += consumed
				continue
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					i += consumed
					continue
				}
			}
		case '[':
			i = patternCharClassEnd(raw, i)
			continue
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, end := r.parseExtGlob(raw, base, i)
				i = end
				continue
			}
		case '(':
			depth++
			i++
			continue
		case '|':
			if depth == 1 {
				appendArm(i)
				armStart = i + 1
				hasTopLevelBar = true
			}
			i++
			continue
		case ')':
			depth--
			if depth == 0 {
				if !hasTopLevelBar {
					return nil, i + 1, false
				}
				appendArm(i)
				group.Rparen = posAddCol(base, i)
				return group, i + 1, true
			}
			i++
			continue
		}
		i++
	}
	return nil, len(raw), false
}

func (r rawPatternParser) parseExtGlob(raw string, base Pos, start int) (*ExtGlob, int) {
	eg := &ExtGlob{OpPos: posAddCol(base, start), Op: globOperatorFromByte(raw[start])}
	innerStart := start + 2
	i := innerStart
	armStart := innerStart
	appendArm := func(end int) {
		eg.Patterns = append(eg.Patterns, r.parseWithBareGroups(raw[armStart:end], posAddCol(base, armStart), false))
	}
	for i <= len(raw) {
		if i >= len(raw) {
			appendArm(len(raw))
			eg.Rparen = posAddCol(base, len(raw))
			return eg, len(raw)
		}
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
			continue
		case '\'', '"', '`', '$':
			_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
			if ok {
				i += consumed
				continue
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, consumed, ok := r.parseShellPart(raw[i:], posAddCol(base, i))
				if ok {
					i += consumed
					continue
				}
			}
		case '[':
			i = patternCharClassEnd(raw, i)
			continue
		case '(':
			_, end, _ := r.parsePatternGroup(raw, base, i)
			if end > i {
				i = end
				continue
			}
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				_, end := r.parseExtGlob(raw, base, i)
				i = end
				continue
			}
		case '|':
			appendArm(i)
			armStart = i + 1
			i++
			continue
		case ')':
			appendArm(i)
			eg.Rparen = posAddCol(base, i)
			return eg, i + 1
		}
		i++
	}
	return eg, len(raw)
}

func (r rawPatternParser) parseShellPart(raw string, base Pos) (PatternPart, int, bool) {
	part, consumed, ok := parseRawWordPart(raw, base, r.lang)
	if !ok {
		return nil, 0, false
	}
	patPart, ok := part.(PatternPart)
	if !ok {
		return nil, 0, false
	}
	return patPart, consumed, true
}

func parseRawWordPart(raw string, base Pos, lang LangVariant) (WordPart, int, bool) {
	sub := NewParser(Variant(lang))
	sub.reset()
	sub.f = &File{}
	sub.src = strings.NewReader(raw)
	sub.offs = int64(base.Offset())
	sub.line = int64(base.Line())
	sub.col = int64(base.Col())
	sub.rune()
	sub.next()
	part := sub.wordPart()
	if sub.err != nil || part == nil {
		return nil, 0, false
	}
	consumed := int(part.End().Offset() - base.Offset())
	if consumed <= 0 {
		return nil, 0, false
	}
	if ar, ok := part.(*ArithmExp); ok && ar.Source == "" {
		start := int(ar.Left.Offset() - base.Offset())
		end := int(ar.Right.Offset() - base.Offset())
		if ar.Bracket {
			start += 2
		} else {
			start += 3
		}
		if start >= 0 && start <= end && end <= len(raw) {
			ar.Source = raw[start:end]
		}
	}
	return part, consumed, true
}

func firstPatternGroup(pat *Pattern) *PatternGroup {
	if pat == nil {
		return nil
	}
	for _, part := range pat.Parts {
		if group := firstPatternGroupPart(part); group != nil {
			return group
		}
	}
	return nil
}

func firstPatternGroupPart(part PatternPart) *PatternGroup {
	switch part := part.(type) {
	case *PatternGroup:
		return part
	case *ExtGlob:
		for _, pat := range part.Patterns {
			if group := firstPatternGroup(pat); group != nil {
				return group
			}
		}
	}
	return nil
}

func firstPatternGroupInList(patterns []*Pattern) *PatternGroup {
	for _, pat := range patterns {
		if group := firstPatternGroup(pat); group != nil {
			return group
		}
	}
	return nil
}

func (p *Parser) extGlob(op GlobOperator, opPos Pos) *ExtGlob {
	eg := &ExtGlob{OpPos: opPos, Op: op}
	lparens := 1
	r := p.r
	globStart := posAddCol(opPos, 2)
globLoop:
	for p.newLit(r); ; r = p.rune() {
		switch r {
		case utf8.RuneSelf:
			break globLoop
		case '(':
			lparens++
		case ')':
			if lparens--; lparens == 0 {
				break globLoop
			}
		}
	}
	raw := p.endLit()
	rparen := p.nextPos()
	p.rune()
	if lparens != 0 {
		p.tok = _EOF
		p.matchingErr(opPos, token(op), rightParen)
	}
	eg.Rparen = rparen
	eg.Patterns = rawPatternParser{lang: p.lang}.parseList(raw, globStart)
	p.next()
	return eg
}

func (p *Parser) getPattern(stops ...token) *Pattern {
	if pat := (&Pattern{Parts: p.patternParts(nil, stops...)}); len(pat.Parts) > 0 {
		pat.Start = pat.Parts[0].Pos()
		pat.EndPos = pat.Parts[len(pat.Parts)-1].End()
		pat.raw = p.sourceRange(pat.Pos(), pat.End())
		return pat
	}
	return nil
}

func (p *Parser) getPatternWithLiteralLeadingSlash(stops ...token) *Pattern {
	if p.tok != slash {
		return p.getPattern(stops...)
	}
	slashPos := p.pos
	p.next()
	parts := splitPatternLit(p.rawLit(slashPos, posAddCol(slashPos, 1), "/"))
	parts = p.patternParts(parts, stops...)
	if len(parts) == 0 {
		return nil
	}
	return &Pattern{
		Start:  parts[0].Pos(),
		EndPos: parts[len(parts)-1].End(),
		Parts:  parts,
		raw:    p.sourceRange(parts[0].Pos(), parts[len(parts)-1].End()),
	}
}

func (p *Parser) patternParts(pps []PatternPart, stops ...token) []PatternPart {
	if p.quote == noState {
		p.quote = unquotedWordCont
		defer func() { p.quote = noState }()
	}
	stop := func(tok token) bool {
		for _, stopTok := range stops {
			if tok == stopTok {
				return true
			}
		}
		return false
	}
	for {
		if stop(p.tok) {
			if len(pps) == 0 {
				return nil
			}
			return pps
		}
		switch p.tok {
		case _Lit, _LitWord, _LitRedir:
			lit := p.lit(p.pos, p.val)
			p.next()
			if p.tok == leftParen {
				if prefix, op, opPos, ok := trailingExtGlobOp(lit); ok {
					pps = append(pps, splitPatternLit(prefix)...)
					pps = append(pps, p.extGlob(op, opPos))
					if p.spaced {
						return pps
					}
					continue
				}
			}
			pps = append(pps, splitPatternLit(lit)...)
		default:
			wp := p.wordPart()
			if wp == nil {
				if len(pps) == 0 {
					return nil
				}
				return pps
			}
			pps = append(pps, wp.(PatternPart))
		}
		if p.spaced {
			return pps
		}
	}
}

func (p *Parser) cmdSubst() *CmdSubst {
	cs := &CmdSubst{Left: p.pos}
	old := p.preNested(subCmd)
	p.next()
	cs.Stmts, cs.Last = p.stmtList()
	p.postNested(old)
	cs.Right = p.matched(cs.Left, dollParen, rightParen)
	return cs
}

func (p *Parser) dblQuoted() *DblQuoted {
	alloc := &struct {
		quoted DblQuoted
		parts  [1]WordPart
	}{
		quoted: DblQuoted{Left: p.pos, Dollar: p.tok == dollDblQuote},
	}
	q := &alloc.quoted
	old := p.quote
	p.quote = dblQuotes
	p.next()
	q.Parts = p.wordParts(alloc.parts[:0])
	p.quote = old
	q.Right = p.pos
	if !p.got(dblQuote) {
		if p.recoverError() {
			q.Right = recoveredPos
		} else {
			p.quoteErr(q.Pos(), dblQuote)
		}
	}
	return q
}

// paramExp parses a short or full parameter expansion, depending on whether
// [Parser.tok] is [dollar] or [dollBrace]. It returns nil if a [dollar] token
// does not form a valid parameter expansion, in which case it should be parsed
// as a literal.
func (p *Parser) paramExp() *ParamExp {
	old := p.quote
	p.quote = runeByRune
	// [ParamExp.Short] means we are parsing $exp rather than ${exp}.
	pe := &ParamExp{
		Dollar: p.pos,
		Short:  p.tok == dollar,
	}
	if !pe.Short && p.r == '(' {
		subtype, detail := FeatureSubtypeParameterExpansionFlag, ""
		if !p.lang.in(LangZsh) {
			subtype, detail = p.paramExpFlagsFeatureMetadata()
		}
		p.checkLangSpanDetailed(pe.Pos(), posAddCol(p.nextPos(), 1), LangZsh, FeatureParameterExpansionFlags, subtype, detail)
		// For now, for simplicity, we parse flags as just a literal.
		// In the future, parsing as a word is better for cases like
		// `${(ps.$sep.)val}`.
		lparen := p.nextPos()
		p.rune()
		p.pos = p.nextPos()
		for p.newLit(p.r); p.r != utf8.RuneSelf && p.r != ')'; p.rune() {
		}
		p.val = p.endLit()
		if p.r != ')' {
			p.tok = _EOF // we can only get here due to EOF
			p.matchingErr(lparen, leftParen, rightParen)
		}
		pe.Flags = p.lit(p.pos, p.val)
		p.rune()
	}
	if !pe.Short || p.lang.in(LangZsh) {
		// Prefixes, like ${#name} to get the length of a variable.
		// Note that in Zsh, the short form like $#name is allowed too.
		switch p.r {
		case '#':
			if r := p.peek(); r == utf8.RuneSelf || singleRuneParam(r) || paramNameRune(r) || r == '"' {
				pe.Length = true
				p.rune()
			}
		case '%':
			if r := p.peek(); r == utf8.RuneSelf || singleRuneParam(r) || paramNameRune(r) || r == '"' {
				p.checkLangSpan(pe.Pos(), posAddCol(p.nextPos(), 1), LangMirBSDKorn, FeatureParameterExpansionWidthPrefix)
				pe.Width = true
				p.rune()
			}
		case '!':
			if r := p.peek(); r == utf8.RuneSelf || singleRuneParam(r) || paramNameRune(r) || r == '"' {
				p.checkLangSpan(pe.Pos(), posAddCol(p.nextPos(), 1), langBashLike|LangMirBSDKorn, FeatureParameterExpansionIndirectPrefix)
				pe.Excl = true
				p.rune()
			}
		case '+':
			if r := p.peek(); r == utf8.RuneSelf || singleRuneParam(r) || paramNameRune(r) || r == '"' {
				p.checkLangSpan(pe.Pos(), posAddCol(p.nextPos(), 1), LangZsh, FeatureParameterExpansionIsSetPrefix)
				pe.IsSet = true
				p.rune()
			}
		case '~':
			if r := p.peek(); r == utf8.RuneSelf || singleRuneParam(r) || paramNameRune(r) || r == '"' {
				p.checkLang(pe.Pos(), LangZsh, FeatureParameterExpansionGlobSubstPrefix)
				pe.GlobSubst = true
				p.rune()
			}
		}
	}
	if pe = p.paramExpParameter(pe); pe == nil {
		p.quote = old
		return nil // just "$"
	}
	if pe.Invalid != "" {
		p.quote = old
		p.next()
		return pe
	}
	// In short mode, any indexing or suffixes is not allowed, and we don't require '}'.
	// Zsh is an exception: $foo[1] and $foo[1,3] are valid.
	if pe.Short {
		if p.lang.in(LangZsh) && p.r == '[' {
			p.pos = p.nextPos()
			p.rune()
			pe.Index = p.eitherIndex()
		}
		p.quote = old
		p.next()
		return pe
	}
	// Index expressions like ${foo[1]}. Note that expansion suffixes can be combined,
	// like ${foo[@]//replace/with}.
	if p.r == '[' {
		subtype, detail := FeatureSubtypeUnknown, ""
		if !p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
			subtype, detail = p.paramExpIndexFeatureMetadata()
		}
		p.checkLangDetailed(p.nextPos(), langBashLike|LangMirBSDKorn|LangZsh, FeatureArraySyntax, subtype, detail)
		if pe.Param != nil && !ValidName(pe.Param.Value) {
			p.posErr(p.nextPos(), "cannot index a special parameter name")
		}
		p.pos = p.nextPos()
		p.rune()
		pe.Index = p.eitherIndex()
		if pe.Index != nil && !pe.Index.Right.IsValid() {
			if p.tok == rightBrace && p.pos.IsValid() {
				pe.Rbrace = p.pos
				pe.Invalid = p.sourceRange(pe.Dollar, posAddCol(pe.Rbrace, 1))
				p.quote = old
				p.next()
				return pe
			}
			return p.invalidParamExp(pe, old)
		}
	}
	if p.r == '&' {
		return p.invalidParamExp(pe, old)
	}
	tokRune := p.r
	p.pos = p.nextPos()
	p.tok = p.paramToken(p.r)
	if p.tok == rightBrace {
		pe.Rbrace = p.pos
		if pe.Index != nil && !pe.Index.Right.IsValid() {
			pe.Invalid = p.sourceRange(pe.Dollar, posAddCol(pe.Rbrace, 1))
		}
		p.quote = old
		p.next()
		return pe
	}
	switch p.tok {
	case slash, dblSlash: // pattern search and replace
		p.checkLang(p.pos, langBashLike|LangMirBSDKorn|LangZsh, FeatureParameterExpansionSearchReplace)
		pe.Repl = &Replace{All: p.tok == dblSlash}
		p.quote = paramExpRepl
		if p.tok == slash {
			switch p.r {
			case '#':
				pe.Repl.Anchor = ReplaceAnchorPrefix
				p.rune()
			case '%':
				pe.Repl.Anchor = ReplaceAnchorSuffix
				p.rune()
			}
		}
		p.next()
		if pe.Repl.Anchor == ReplaceAnchorNone {
			pe.Repl.Orig = p.getPatternWithLiteralLeadingSlash(slash, rightBrace)
		} else {
			pe.Repl.Orig = p.getPattern(slash, rightBrace)
		}
		p.quote = paramExpExp
		if p.got(slash) {
			pe.Repl.With = p.getWord()
		}
	case colon: // slicing
		if p.lang.in(LangZsh) && (p.r == '&' || asciiLetter(p.r)) {
			pos := p.pos
		loop:
			for p.newLit(p.r); ; p.rune() {
				switch p.r {
				case utf8.RuneSelf:
					p.tok = _EOF
					p.matchingErr(pe.Dollar, dollBrace, rightBrace)
					break loop
				case '}':
					pe.Modifiers = append(pe.Modifiers, p.lit(pos, p.endLit()))
					pe.Rbrace = p.nextPos()
					p.rune()
					break loop
				case ':':
					pe.Modifiers = append(pe.Modifiers, p.lit(pos, p.endLit()))
					p.rune()
					pos = p.nextPos()
					p.newLit(p.r)
				}
			}
			p.quote = old
			p.next()
			return pe
		}
		p.checkLang(p.pos, langBashLike|LangMirBSDKorn|LangZsh, FeatureParameterExpansionSlice)
		pe.Slice = &Slice{}
		colonPos := p.pos
		p.quote = paramExpArithm
		if p.next(); p.tok != colon {
			if p.tok == rightBrace && !p.spaced {
				pe.Slice.MissingOffset = true
			} else {
				pe.Slice.Offset = p.followArithm(colon, colonPos)
			}
		}
		colonPos = p.pos
		if p.got(colon) {
			pe.Slice.Length = p.followArithm(colon, colonPos)
		}
		// Need to use a different matched style so arithm errors
		// get reported correctly
		p.quote = old
		pe.Rbrace = p.pos
		p.matchedArithm(pe.Dollar, dollBrace, rightBrace)
		return pe
	case caret, dblCaret, comma, dblComma: // upper/lower case
		p.checkLang(p.pos, langBashLike, FeatureParameterExpansionCaseOperator)
		pe.Exp = p.paramExpExp()
	case at, star:
		switch {
		case p.tok == star && !pe.Excl:
			p.curErr("not a valid parameter expansion operator: %#q", p.tok)
		case pe.Excl && p.r == '}':
			p.checkLang(pe.Pos(), langBashLike, FeatureParameterExpansionNameOperator, p.tok.String())
			pe.Names = ParNamesOperator(p.tok)
			p.next()
		case p.tok == at:
			p.checkLang(p.pos, langBashLike|LangMirBSDKorn, FeatureParameterExpansionNameOperator)
			fallthrough
		default:
			pe.Exp = p.paramExpExp()
		}
	case plus, colPlus, minus, colMinus, quest, colQuest, assgn, colAssgn,
		perc, dblPerc, hash, dblHash, colHash, colPipe, colStar:
		pe.Exp = p.paramExpExp()
	case and:
		return p.invalidParamExp(pe, old)
	case _EOF:
	default:
		if tokRune == '[' && pe.Index != nil {
			return p.invalidParamExp(pe, old)
		}
		if paramNameRune(tokRune) {
			if pe.Param != nil {
				p.curErr("%#q cannot be followed by a word", pe.Param.Value)
			} else {
				p.curErr("nested parameter expansion cannot be followed by a word")
			}
		} else {
			p.curErr("not a valid parameter expansion operator: %#q", string(tokRune))
		}
	}
	if p.tok != _EOF && p.tok != rightBrace {
		p.tok = p.paramToken(p.r)
	}
	p.quote = old
	pe.Rbrace = p.matched(pe.Dollar, dollBrace, rightBrace)
	if pe.Index != nil && !pe.Index.Right.IsValid() && pe.Invalid == "" && pe.Rbrace.IsValid() {
		pe.Invalid = p.sourceRange(pe.Dollar, posAddCol(pe.Rbrace, 1))
	}
	return pe
}

func (p *Parser) paramExpFlagsFeatureMetadata() (FeatureSubtype, string) {
	tail := p.lookaheadSourceTail(256, paramExpFlagsMetadataTailDone)
	if !strings.HasPrefix(tail, "(") {
		return FeatureSubtypeParameterExpansionFlag, ""
	}
	flags := tail[1:]
	if end := strings.IndexByte(flags, ')'); end >= 0 {
		flags = flags[:end]
	}
	if paramExpFlagsContainTildeFlag(flags) {
		return FeatureSubtypeParameterExpansionTildeFlag, flags
	}
	return FeatureSubtypeParameterExpansionFlag, flags
}

type paramExpFlagDelims struct {
	open  byte
	close byte
}

func paramExpFlagDelimiters(b byte) paramExpFlagDelims {
	switch b {
	case '(':
		return paramExpFlagDelims{open: '(', close: ')'}
	case '{':
		return paramExpFlagDelims{open: '{', close: '}'}
	case '[':
		return paramExpFlagDelims{open: '[', close: ']'}
	case '<':
		return paramExpFlagDelims{open: '<', close: '>'}
	default:
		return paramExpFlagDelims{open: b, close: b}
	}
}

func paramExpConsumeFlagArg(flags string, i int) (paramExpFlagDelims, int, bool) {
	if i >= len(flags) {
		return paramExpFlagDelims{}, len(flags), false
	}
	delims := paramExpFlagDelimiters(flags[i])
	next, ok := paramExpConsumeFlagArgWithDelims(flags, i+1, delims)
	return delims, next, ok
}

func paramExpConsumeFlagArgWithDelims(flags string, i int, delims paramExpFlagDelims) (int, bool) {
	for i < len(flags) {
		switch flags[i] {
		case '\\':
			if i+1 < len(flags) {
				i += 2
				continue
			}
		case delims.close:
			return i + 1, true
		}
		i++
	}
	return len(flags), false
}

func paramExpFlagsContainTildeFlag(flags string) bool {
	for i := 0; i < len(flags); {
		switch flags[i] {
		case '~':
			return true
		case 'q':
			i++
			if i < len(flags) && (flags[i] == '-' || flags[i] == '+') {
				i++
			}
		case 'g', 'j', 's', 'Z', 'I', '_':
			_, next, ok := paramExpConsumeFlagArg(flags, i+1)
			if !ok {
				return false
			}
			i = next
		case 'l', 'r':
			delims, next, ok := paramExpConsumeFlagArg(flags, i+1)
			if !ok {
				return false
			}
			i = next
			for optional := 0; optional < 2 && i < len(flags) && flags[i] == delims.open; optional++ {
				next, ok = paramExpConsumeFlagArgWithDelims(flags, i+1, delims)
				if !ok {
					return false
				}
				i = next
			}
		default:
			i++
		}
	}
	return false
}

func (p *Parser) paramExpIndexFeatureMetadata() (FeatureSubtype, string) {
	tail := p.lookaheadSourceTail(64, paramExpIndexMetadataTailDone)
	if !strings.HasPrefix(tail, "[") {
		return FeatureSubtypeUnknown, ""
	}
	inner := strings.TrimLeft(tail[1:], " \t\r\n")
	if inner == "" {
		return FeatureSubtypeUnknown, ""
	}
	switch inner[0] {
	case '\'', '"':
		return FeatureSubtypeParameterExpansionQuotedIndex, inner[:1]
	default:
		return FeatureSubtypeUnknown, ""
	}
}

func (p *Parser) invalidParamExp(pe *ParamExp, old quoteState) *ParamExp {
	if pe == nil {
		return nil
	}
	for p.r != utf8.RuneSelf && p.r != '}' {
		p.rune()
	}
	if p.r == '}' {
		pe.Rbrace = p.nextPos()
		pe.Invalid = p.sourceRange(pe.Dollar, posAddCol(pe.Rbrace, 1))
		p.rune()
	} else {
		pe.Invalid = p.sourceFromPos(pe.Dollar)
		p.quote = old
		pe.Rbrace = p.matched(pe.Dollar, dollBrace, rightBrace)
		return pe
	}
	p.quote = old
	p.next()
	return pe
}

func (p *Parser) nestedParameterStart(pe *ParamExp) (left token, quotePos Pos) {
	if pe.Short {
		return illegalTok, Pos{}
	}
	if p.r == '"' {
		quotePos = p.nextPos()
		p.rune()
	}
	if p.r != '$' {
		if quotePos.IsValid() {
			return dollar, quotePos
		}
		return illegalTok, Pos{}
	}
	switch p1 := p.peek(); p1 {
	case '{', '(':
		p.pos = p.nextPos()
		spanEnd := posAddCol(pe.Pos(), 2)
		if quotePos.IsValid() {
			spanEnd = posAddCol(quotePos, 1)
		}
		p.checkLangSpanDetailed(pe.Pos(), spanEnd, LangZsh, FeatureParameterExpansionNested, FeatureSubtypeParameterExpansionNested, "")
		if p.err != nil {
			return illegalTok, Pos{} // xxx given that we overwrite p.tok below
		}
		p.rune()
		p.rune()
		if p1 == '{' {
			left = dollBrace
		} else { // '('
			left = dollParen
		}
	}
	return left, quotePos
}

func (p *Parser) paramExpParameter(pe *ParamExp) *ParamExp {
	// Check for Zsh nested parameter expressions like ${(f)"$(foo)"}.
	if left, quotePos := p.nestedParameterStart(pe); left != illegalTok {
		var wp WordPart
		switch p.tok = left; p.tok {
		case dollBrace: // ${#${nested parameter}}
			p.tok = dollBrace
			wp = p.paramExp()
		case dollParen: // ${#$(nested command)}
			wp = p.cmdSubst()
		default: // dollar
			p.posErr(pe.Pos(), "invalid nested parameter expansion")
		}
		if quotePos.IsValid() {
			if p.r != '"' {
				p.tok = p.paramToken(p.r)
				if p.tok == illegalTok {
					p.posErr(pe.Pos(), "invalid nested parameter expansion")
				} else {
					p.quoteErr(quotePos, dblQuote)
				}
			}
			pe.NestedParam = &DblQuoted{
				Left:  quotePos,
				Right: p.nextPos(),
				Parts: []WordPart{wp},
			}
			p.rune()
		} else {
			pe.NestedParam = wp
		}
		return pe
	}
	if pe.Short {
		for p.r == escNewl {
			p.rune()
		}
	}
	// The parameter name itself, like $foo or $?.
	switch p.r {
	case '?', '-', '#':
		if pe.Length && p.peek() != '}' {
			// actually ${#-default} or ${###}, not ${#-} or ${##};
			// fix the ambiguity in bash-compatible parses by treating the
			// leading '#' as the parameter name and the current rune as the
			// start of the expansion operator.
			pe.Length = false
			pos := p.nextPos()
			pe.Param = p.lit(posAddCol(pos, -1), "#")
			pe.Param.ValueEnd = pos
			break
		}
		fallthrough
	case '@', '*', '!', '$':
		r, pos := p.r, p.nextPos()
		p.rune()
		pe.Param = p.lit(pos, string(r))
	default:
		// Note that $1a is equivalent to ${1}a, but ${1a} is not.
		// POSIX Shell says the latter is unspecified behavior, so match Bash's behavior.
		pos := p.nextPos()
		if pe.Short && singleRuneParam(p.r) {
			p.val = string(p.r)
			p.rune()
		} else {
			for p.newLit(p.r); p.r != utf8.RuneSelf; p.rune() {
				if !paramNameRune(p.r) && p.r != escNewl {
					break
				}
			}
			p.val = p.endLit()
			if !numberLiteral(p.val) && !ValidName(p.val) {
				if pe.Short {
					return nil // just "$"
				}
				if p.val == "" && p.r != utf8.RuneSelf && p.r < utf8.RuneSelf &&
					p.peek() == '}' && !singleRuneParam(p.r) && !paramNameRune(p.r) {
					pe.Rbrace = posAddCol(p.nextPos(), 1)
					pe.Invalid = p.sourceRange(pe.Dollar, posAddCol(pe.Rbrace, 1))
					p.rune()
					p.rune()
					return pe
				}
				p.posErr(pos, "invalid parameter name")
			}
		}
		pe.Param = p.lit(pos, p.val)
	}
	return pe
}

func (p *Parser) paramExpExp() *Expansion {
	op := ParExpOperator(p.tok)
	switch op {
	case MatchEmpty, ArrayExclude, ArrayIntersect:
		p.checkLang(p.pos, LangZsh, FeatureParameterExpansionMatchOperator, op.String())
	}
	p.quote = paramExpExp
	p.next()
	if op == OtherParamOps {
		switch p.tok {
		case _Lit, _LitWord:
		default:
			p.curErr("@ expansion operator requires a literal")
		}
		switch p.val {
		case "a", "k", "u", "A", "E", "K", "L", "P", "U":
			p.checkLang(p.pos, langBashLike, FeatureParameterExpansionCaseOperator)
		case "#":
			p.checkLang(p.pos, LangMirBSDKorn, FeatureParameterExpansionMatchOperator)
		case "Q":
		default:
			p.curErr("invalid @ expansion operator %#q", p.val)
		}
	}
	exp := &Expansion{Op: op}
	if patternExpOp(op) {
		exp.Pattern = p.getPattern(rightBrace)
	} else {
		exp.Word = p.getWord()
	}
	return exp
}

func patternExpOp(op ParExpOperator) bool {
	switch op {
	case MatchEmpty, ArrayExclude, ArrayIntersect,
		RemSmallPrefix, RemLargePrefix,
		RemSmallSuffix, RemLargeSuffix,
		UpperFirst, UpperAll,
		LowerFirst, LowerAll:
		return true
	default:
		return false
	}
}

func (p *Parser) eitherIndex() *Subscript {
	old := p.quote
	lpos := p.pos
	innerStart := posAddCol(lpos, 1)
	sub := &Subscript{Left: lpos, Kind: SubscriptExpr, Mode: SubscriptAuto}
	p.quote = paramExpArithm
	p.next()
	switch p.tok {
	case star:
		sub.Kind = SubscriptStar
		sub.Expr = p.wordOne(p.lit(p.pos, star.String()))
		p.next()
	case at:
		sub.Kind = SubscriptAt
		sub.Expr = p.wordOne(p.lit(p.pos, at.String()))
		p.next()
	default:
		if p.tok == _LitWord && (p.val == "@" || p.val == "*") {
			if p.val == "@" {
				sub.Kind = SubscriptAt
			} else {
				sub.Kind = SubscriptStar
			}
			sub.Expr = p.wordOne(p.lit(p.pos, p.val))
			p.next()
			break
		}
		arithSaved := *p
		sub.Expr = p.followArithm(leftBrack, lpos)
		if p.err != nil && p.lang.in(langBashLike) {
			scan := arithSaved
			for scan.tok != rightBrack && scan.tok != _EOF && scan.tok != _Newl {
				start := scan.cursorSnapshot()
				scan.nextArith(false)
				if !start.progressed(&scan) {
					scan.posRecoverableErr(start.pos, "internal parser error: no progress scanning subscript")
					break
				}
			}
			if scan.tok == rightBrack {
				next := scan
				next.next()
				if next.tok == assgn || next.tok == assgnParen || (next.val != "" && next.val[0] == '+') {
					*p = scan
					p.err = nil
					if raw := p.sourceBetween(innerStart, p.pos); raw != "" {
						sub.Expr = p.wordOne(p.rawLit(innerStart, p.pos, raw))
					}
				}
			}
		}
	}
	if p.lang.in(langBashLike) && p.tok == hash {
		for p.tok != rightBrack && p.tok != _EOF && p.tok != _Newl {
			start := p.cursorSnapshot()
			p.next()
			if !start.progressed(p) {
				p.posRecoverableErr(start.pos, "internal parser error: no progress scanning subscript")
				break
			}
		}
		if p.tok == rightBrack {
			if sub.Expr == nil {
				raw := p.sourceBetween(innerStart, p.pos)
				if raw != "" {
					sub.Expr = p.wordOne(p.lit(innerStart, raw))
				}
			}
		}
	}
	if p.tok == rightBrack {
		sub.Right = p.pos
		sub.raw = p.sourceBetween(innerStart, p.pos)
	}
	if p.lang.in(langBashLike) && old == runeByRune && p.tok == rightBrace {
		sub.raw = p.sourceBetween(innerStart, p.pos)
		p.quote = old
		return sub
	}
	p.quote = old
	p.matchedArithm(lpos, leftBrack, rightBrack)
	return sub
}

func subscriptModeFromArrayExprMode(mode ArrayExprMode) SubscriptMode {
	switch mode {
	case ArrayExprIndexed:
		return SubscriptIndexed
	case ArrayExprAssociative:
		return SubscriptAssociative
	default:
		return SubscriptAuto
	}
}

func stampSubscriptMode(index *Subscript, mode SubscriptMode) {
	if index == nil || index.AllElements() || mode == SubscriptAuto {
		return
	}
	index.Mode = mode
}

func stampVarRefSubscriptMode(ref *VarRef, mode SubscriptMode) {
	if ref == nil {
		return
	}
	stampSubscriptMode(ref.Index, mode)
}

func stampArrayExprSubscriptModes(array *ArrayExpr, mode SubscriptMode) {
	if array == nil {
		return
	}
	for _, elem := range array.Elems {
		if elem == nil || elem.Kind == ArrayElemSequential {
			continue
		}
		stampSubscriptMode(elem.Index, mode)
	}
}

func (p *Parser) zshSubFlags() *FlagsArithm {
	zf := &FlagsArithm{}
	// Lex flags as raw text, like paramExp does for ${(flags)...}.
	lparen := p.pos
	old := p.quote
	p.quote = runeByRune
	p.pos = p.nextPos()
	for p.newLit(p.r); p.r != utf8.RuneSelf && p.r != ')'; p.rune() {
	}
	p.val = p.endLit()
	if p.r != ')' {
		p.tok = _EOF
		p.matchingErr(lparen, leftParen, rightParen)
	}
	zf.Flags = p.lit(p.pos, p.val)
	p.rune()
	p.quote = old
	// Parse the expression; use arithmExprAssign so commas are left for ranges.
	p.next()
	if p.tok == star || p.tok == at {
		p.tok, p.val = _LitWord, p.tok.String()
	}
	zf.X = p.arithmExprAssign(false)
	return zf
}

func (p *Parser) stopToken() bool {
	switch p.tok {
	case _EOF, _Newl, semicolon, and, or, andAnd, orOr, orAnd, andPipe, andBang,
		dblSemicolon, semiAnd, dblSemiAnd, semiOr, rightParen:
		return true
	case bckQuote:
		return p.backquoteEnd()
	}
	return false
}

func (p *Parser) backquoteEnd() bool {
	return p.lastBquoteEsc < p.openBquotes
}

// ValidName returns whether val is a valid name as per the POSIX spec.
func ValidName(val string) bool {
	if val == "" {
		return false
	}
	for i, r := range val {
		switch {
		case asciiLetter(r), r == '_':
		case i > 0 && asciiDigit(r):
		default:
			return false
		}
	}
	return true
}

func numberLiteral[T string | []byte](val T) bool {
	if len(val) == 0 {
		return false
	}
	for _, r := range string(val) {
		if !asciiDigit(r) {
			return false
		}
	}
	return true
}

func (p *Parser) hasValidIdent() bool {
	if p.tok != _Lit && p.tok != _LitWord {
		return false
	}
	if end := p.validEqlOffs(); end > 0 {
		if p.val[end-1] == '+' && p.lang.in(langBashLike|LangMirBSDKorn|LangZsh) {
			end-- // a+=x
		}
		if ValidName(p.val[:end]) {
			return true
		}
	} else if !ValidName(p.val) {
		return false // *[i]=x
	}
	return p.r == '[' // a[i]=x
}

func (p *Parser) validEqlOffs() int {
	if p.eqlOffs <= 0 || p.eqlOffs >= len(p.val) {
		return -1
	}
	return p.eqlOffs
}

func (p *Parser) varRef() *VarRef {
	if p.tok != _Lit && p.tok != _LitWord {
		p.curErr("not a valid variable reference: %#q", p.tok)
		return nil
	}
	if !ValidName(p.val) {
		p.curErr("not a valid variable reference: %q", p.val)
		return nil
	}
	ref := &VarRef{Name: p.lit(p.pos, p.val)}
	if p.r != '[' {
		ref.raw = ref.Name.Value
		p.next()
		return ref
	}
	// The lexer leaves the opening bracket in p.r for names like a[1].
	p.rune()
	p.pos = posAddCol(p.pos, 1)
	ref.Index = p.eitherIndex()
	if ref.Index != nil {
		switch {
		case ref.Index.Right.IsValid():
			ref.raw = p.sourceBetween(ref.Pos(), posAddCol(ref.Index.Right, 1))
		default:
			if raw := p.sourceFromPos(ref.Pos()); raw != "" {
				if close := strings.IndexByte(raw, ']'); close >= 0 {
					ref.raw = raw[:close+1]
				}
			}
			ref.raw = strings.TrimRight(ref.raw, " \t\r\n")
			if ref.raw == "" {
				ref.raw = strings.TrimRight(p.sourceBetween(ref.Pos(), p.pos), " \t\r\n")
			}
			if ref.raw == "" {
				ref.raw = strings.TrimRight(p.sourceFromPos(ref.Pos()), " \t\r\n")
			}
		}
		if left := strings.IndexByte(ref.raw, '['); left >= 0 && strings.HasSuffix(ref.raw, "]") {
			ref.Index.raw = ref.raw[left+1 : len(ref.raw)-1]
		}
	}
	return ref
}

func (p *Parser) recordPendingArrayWord(start Pos, fallback string) {
	end := p.pos
	consumedCurrent := false
	if !p.spaced {
		switch p.tok {
		case _Lit, _LitWord, _LitRedir:
			consumedCurrent = true
			if p.val != "" {
				end = posAddCol(p.pos, len(p.val))
			}
		}
	}
	raw := strings.TrimRight(p.sourceBetween(start, end), " \t\r\n")
	if raw == "" || len(raw) < len(fallback) {
		raw = fallback
	}
	if raw == "" {
		raw = strings.TrimRight(p.sourceFromPos(start), " \t\r\n")
	}
	p.pendingArrayWord = raw
	p.pendingArrayWordPos = start
	if consumedCurrent {
		p.next()
	}
}

func (p *Parser) takePendingArrayWord() *Word {
	if p.pendingArrayWord == "" {
		return nil
	}
	word := p.wordOne(p.lit(p.pendingArrayWordPos, p.pendingArrayWord))
	p.pendingArrayWord = ""
	p.pendingArrayWordPos = Pos{}
	return word
}

func (p *Parser) normalizeArrayLikeEOFError() {
	parseErr, ok := p.err.(ParseError)
	if !ok {
		return
	}
	if p.tok != _EOF && p.tok != _Newl {
		return
	}
	parseErr.bashText = "unexpected EOF while looking for matching `]'"
	if runtime.GOOS == "darwin" {
		parseErr.bashSecondaryText = "syntax error: unexpected end of file"
	} else {
		parseErr.bashSecondaryText = ""
	}
	parseErr.SourceLine = ""
	parseErr.SourceLinePos = Pos{}
	parseErr.noSourceLine = true
	p.err = parseErr
}

func subscriptHasWhitespace(sub *Subscript) bool {
	if sub == nil || !sub.Left.IsValid() || !sub.Right.IsValid() {
		return false
	}
	sourceLen := int(sub.Right.Offset()-sub.Left.Offset()) - 1
	if sourceLen <= 0 {
		return false
	}
	canonicalLen := 1
	if sub.Kind == SubscriptExpr {
		canonicalLen = len(ArithmExprString(sub.Expr))
	}
	return sourceLen > canonicalLen
}

func varRefSubscriptHasWhitespace(ref *VarRef) bool {
	if ref == nil || ref.Index == nil {
		return false
	}
	if subscriptHasWhitespace(ref.Index) {
		return true
	}
	raw := ref.RawText()
	if raw == "" {
		return false
	}
	left := strings.IndexByte(raw, '[')
	right := strings.LastIndexByte(raw, ']')
	if left < 0 || right <= left {
		return false
	}
	return strings.IndexAny(raw[left+1:right], " \t\r\n") >= 0
}

func varRefSubscriptClosed(ref *VarRef) bool {
	if ref == nil || ref.Index == nil {
		return false
	}
	if ref.Index.Right.IsValid() {
		return true
	}
	return strings.HasSuffix(ref.RawText(), "]")
}

func varRefRawFromCandidate(candidate string) string {
	candidate = strings.TrimRight(candidate, " \t\r\n")
	if candidate == "" {
		return ""
	}
	if close := strings.IndexByte(candidate, ']'); close >= 0 {
		return candidate[:close+1]
	}
	return candidate
}

func (p *Parser) bracketStartsWithSpace() bool {
	if p.r != '[' || p.bsp >= uint(len(p.bs)) {
		return false
	}
	switch p.bs[p.bsp] {
	case ' ', '\t', '\n':
		return true
	default:
		return false
	}
}

func (p *Parser) finishAssign(as *Assign, appendScalarWord bool) *Assign {
	if p.spaced || p.stopToken() {
		return as
	}
	if as.Value == nil && p.tok == leftParen {
		p.checkLang(p.pos, langBashLike|LangMirBSDKorn|LangZsh, FeatureArraySyntax)
		as.Array = &ArrayExpr{Lparen: p.pos, Mode: ArrayExprInherit}
		newQuote := p.quote
		if p.lang.in(langBashLike | LangZsh) {
			newQuote = arrayElems
		}
		old := p.preNested(newQuote)
		p.next()
		p.got(_Newl)
		for p.tok != _EOF && p.tok != rightParen {
			ae := &ArrayElem{Kind: ArrayElemSequential}
			ae.Comments, p.accComs = p.accComs, nil
			if p.tok == leftBrack {
				left := p.pos
				ae.Index = p.eitherIndex()
				if p.tok == assgnParen {
					p.curErr("arrays cannot be nested")
					return nil
				}
				switch {
				case p.tok == assgn:
					ae.Kind = ArrayElemKeyed
					p.next()
				case p.val != "" && p.val[0] == '+':
					ae.Kind = ArrayElemKeyedAppend
					p.val = p.val[1:]
					p.pos = posAddCol(p.pos, 1)
					if len(p.val) < 1 || p.val[0] != '=' {
						p.followErr(left, `[x]+`, assgn)
						return nil
					}
					p.pos = posAddCol(p.pos, 1)
					p.val = p.val[1:]
					if p.val == "" {
						p.next()
					}
				default:
					p.followErr(left, `[x]`, assgn)
					return nil
				}
			}
			// For explicit [key]= and [key]+= elements, a spaced word starts
			// the next array element rather than extending the current value.
			spacedNextElem := ae.Index != nil && p.spaced
			if !spacedNextElem {
				ae.Value = p.getWord()
			}
			if ae.Value == nil {
				if spacedNextElem {
					// Leave the token in place so the next loop iteration can
					// parse it as a separate array element.
					goto arrayElemDone
				}
				switch p.tok {
				case assgnParen, leftParen:
					p.posErrWithMetadata(p.pos, parseErrorMetadata{
						kind:          ParseErrorKindUnexpected,
						unexpected:    ParseErrorSymbolLeftParen,
						isRecoverable: true,
					}, "syntax error near unexpected token %s", leftParen.bashQuote())
					return nil
				case _Newl, rightParen, leftBrack:
					// TODO: support [index]=[
				case and:
					p.posErrWithMetadata(p.pos, parseErrorMetadata{
						kind:          ParseErrorKindUnexpected,
						unexpected:    parseErrorSymbolFromToken(p.tok),
						isRecoverable: true,
					}, "syntax error near unexpected token %s", p.tok.bashQuote())
					return nil
				default:
					p.curErr("array element values must be words")
					return nil
				}
			}
		arrayElemDone:
			if len(p.accComs) > 0 {
				c := p.accComs[0]
				if c.Pos().Line() == ae.End().Line() {
					ae.Comments = append(ae.Comments, c)
					p.accComs = p.accComs[1:]
				}
			}
			as.Array.Elems = append(as.Array.Elems, ae)
			p.got(_Newl)
		}
		as.Array.Last, p.accComs = p.accComs, nil
		p.postNested(old)
		as.Array.Rparen = p.matched(as.Array.Lparen, leftParen, rightParen)
	} else if as.Value == nil {
		if w := p.getWord(); w != nil {
			as.Value = w
		}
	} else if appendScalarWord {
		if w := p.getWord(); w != nil {
			as.Value.Parts = append(as.Value.Parts, w.Parts...)
			p.finishWord(as.Value)
		}
	}
	return as
}

func (p *Parser) getAssignAfterRef(ref *VarRef) *Assign {
	return p.getAssignAfterRefMode(ref, true)
}

func (p *Parser) getAssignAfterRefMode(ref *VarRef, appendScalarWord bool) *Assign {
	as := &Assign{Ref: ref}
	if p.tok == assgnParen {
		// assgnParen consumed both '=' and '(', so rewrite as leftParen for
		// array parsing below. Bash still parses a[i]=(values...), then rejects
		// it later during execution.
		p.tok = leftParen
		p.pos = posAddCol(p.pos, 1)
	} else {
		if p.val != "" && p.val[0] == '+' {
			as.Append = true
			p.val = p.val[1:]
			p.pos = posAddCol(p.pos, 1)
		}
		if len(p.val) < 1 || p.val[0] != '=' {
			if as.Append {
				p.followErr(as.Pos(), "a[b]+", assgn)
			} else {
				p.followErr(as.Pos(), "a[b]", assgn)
			}
			return nil
		}
		p.pos = posAddCol(p.pos, 1)
		p.val = p.val[1:]
		if p.val == "" {
			p.next()
		}
	}
	return p.finishAssign(as, appendScalarWord)
}

func (p *Parser) tryAssignAfterRef(ref *VarRef) (*Assign, bool) {
	as := &Assign{Ref: ref}
	if p.tok == assgnParen {
		p.tok = leftParen
		p.pos = posAddCol(p.pos, 1)
	} else {
		if p.val != "" && p.val[0] == '+' {
			as.Append = true
			p.val = p.val[1:]
			p.pos = posAddCol(p.pos, 1)
		}
		if len(p.val) < 1 || p.val[0] != '=' {
			return nil, false
		}
		p.pos = posAddCol(p.pos, 1)
		p.val = p.val[1:]
		if p.val == "" {
			p.next()
		}
	}
	return p.finishAssign(as, true), true
}

func (p *Parser) tryAssignCandidate(declMode bool) (*Assign, bool) {
	if eqIndex := p.validEqlOffs(); eqIndex > 0 {
		return p.getAssign(), true
	}
	saved := *p
	start := p.pos
	p.startCandidateCapture()
	ref := p.varRef()
	if ref == nil {
		p.wordRawBs = p.wordRawBs[:0]
		p.captureWordRaw = false
		return nil, false
	}
	spacedSubscript := varRefSubscriptHasWhitespace(ref)
	if declMode && spacedSubscript {
		p.wordRawBs = p.wordRawBs[:0]
		p.captureWordRaw = false
		*p = saved
		p.err = nil
		return nil, false
	}
	finishCandidate := func() string {
		raw := p.stopCandidateCapture(start, p.pos)
		if varRefSubscriptClosed(ref) && !strings.Contains(raw, "]") {
			raw += "]"
		}
		if ref.raw == "" {
			ref.raw = varRefRawFromCandidate(raw)
		}
		if ref.Index != nil && ref.Index.raw == "" {
			rawRef := ref.raw
			if left := strings.IndexByte(rawRef, '['); left >= 0 && strings.HasSuffix(rawRef, "]") {
				ref.Index.raw = rawRef[left+1 : len(rawRef)-1]
			}
		}
		return raw
	}
	if p.spaced || p.stopToken() {
		candidateRaw := finishCandidate()
		if !declMode && varRefSubscriptClosed(ref) && spacedSubscript {
			p.recordPendingArrayWord(start, candidateRaw)
			return nil, false
		}
		if spacedSubscript {
			*p = saved
			p.err = nil
			return nil, false
		}
		if !p.spaced && (strings.HasSuffix(candidateRaw, "+") || (p.val != "" && p.val[0] == '+') || p.r == '+') {
			p.followErr(ref.Pos(), "a[b]+", assgn)
		} else {
			p.followErr(ref.Pos(), "a[b]", assgn)
		}
		return nil, false
	}
	as, ok := p.tryAssignAfterRef(ref)
	candidateRaw := finishCandidate()
	if !ok && p.err == nil {
		if !declMode && varRefSubscriptClosed(ref) && spacedSubscript {
			p.recordPendingArrayWord(start, candidateRaw)
			return nil, false
		}
		if spacedSubscript {
			*p = saved
			return nil, false
		}
		if p.val != "" && p.val[0] == '+' {
			p.followErr(ref.Pos(), "a[b]+", assgn)
		} else {
			p.followErr(ref.Pos(), "a[b]", assgn)
		}
	}
	return as, ok
}

func (p *Parser) getAssign() *Assign {
	return p.getAssignMode(true)
}

func (p *Parser) getAssignMode(appendScalarWord bool) *Assign {
	as := &Assign{}
	if eqIndex := p.validEqlOffs(); eqIndex > 0 { // foo=bar
		nameEnd := eqIndex
		if p.lang.in(langBashLike|LangMirBSDKorn|LangZsh) && p.val[eqIndex-1] == '+' {
			// a+=b
			as.Append = true
			nameEnd--
		}
		as.Ref = &VarRef{Name: p.lit(p.pos, p.val[:nameEnd])}
		// since we're not using the entire p.val
		as.Ref.Name.ValueEnd = posAddCol(as.Ref.Name.ValuePos, nameEnd)
		left := p.lit(posAddCol(p.pos, 1), p.val[eqIndex+1:])
		if left.Value != "" {
			left.ValuePos = posAddCol(left.ValuePos, eqIndex)
			as.Value = p.wordOne(left)
		}
		p.next()
		return p.finishAssign(as, appendScalarWord)
	}
	ref := p.varRef()
	if ref == nil {
		return nil
	}
	if p.spaced || p.stopToken() {
		p.followErr(ref.Pos(), "a[b]", assgn)
		return nil
	}
	return p.getAssignAfterRefMode(ref, appendScalarWord)
}

func looksLikeDeclFlagWord(tok token, val string) bool {
	return tok == _LitWord && val != "" && (val[0] == '-' || val[0] == '+')
}

func declArrayModeFromFlagWord(word *Word, current ArrayExprMode) ArrayExprMode {
	if word == nil {
		return current
	}
	lit := word.Lit()
	if lit == "" || len(lit) < 2 {
		return current
	}
	switch lit[0] {
	case '-':
		for _, r := range lit[1:] {
			switch r {
			case 'a':
				current = ArrayExprIndexed
			case 'A':
				current = ArrayExprAssociative
			}
		}
	case '+':
		for _, r := range lit[1:] {
			switch r {
			case 'a', 'A':
				current = ArrayExprInherit
			}
		}
	}
	return current
}

func (p *Parser) declOperand(allowInvalidAssign bool) DeclOperand {
	if eqIndex := p.validEqlOffs(); eqIndex > 0 {
		nameEnd := eqIndex
		if p.lang.in(langBashLike|LangMirBSDKorn|LangZsh) && p.val[eqIndex-1] == '+' {
			nameEnd--
		}
		name := p.val[:nameEnd]
		if !ValidName(name) {
			if !strings.ContainsAny(name, "[]") && !strings.Contains(name, "{") {
				if allowInvalidAssign {
					if w := p.getWord(); w != nil {
						return &DeclDynamicWord{Word: w}
					}
					return nil
				}
				p.curErr("invalid var name")
				return nil
			}
			if w := p.getWord(); w != nil {
				return &DeclDynamicWord{Word: w}
			}
			return nil
		}
		if as := p.getAssignMode(false); as != nil {
			return &DeclAssign{Assign: as}
		}
		return nil
	}
	if p.hasValidIdent() {
		if p.bracketStartsWithSpace() {
			start := p.pos
			saved := *p
			ref := p.varRef()
			if ref != nil && varRefSubscriptClosed(ref) && varRefSubscriptHasWhitespace(ref) {
				p.recordPendingArrayWord(start, ref.RawText())
				if w := p.takePendingArrayWord(); w != nil {
					return &DeclDynamicWord{Word: w}
				}
				return nil
			}
			*p = saved
			if w := p.getWord(); w != nil {
				return &DeclDynamicWord{Word: w}
			}
			return nil
		}
		ref := p.varRef()
		if ref == nil {
			return nil
		}
		if p.spaced || p.stopToken() {
			return &DeclName{Ref: ref}
		}
		if as := p.getAssignAfterRefMode(ref, false); as != nil {
			return &DeclAssign{Assign: as}
		}
		return nil
	}
	if p.tok == _LitWord && ValidName(p.val) {
		return &DeclName{Ref: &VarRef{Name: p.getLit()}}
	}
	if looksLikeDeclFlagWord(p.tok, p.val) {
		if w := p.getWord(); w != nil {
			return &DeclFlag{Word: w}
		}
		return nil
	}
	if w := p.getWord(); w != nil {
		return &DeclDynamicWord{Word: w}
	}
	return nil
}

func (p *Parser) peekRedir() bool {
	switch p.tok {
	case _LitRedir, rdrOut, appOut, rdrIn, rdrInOut, dplIn, dplOut,
		rdrClob, appClob, hdoc, dashHdoc, wordHdoc,
		rdrAll, rdrAllClob, appAll, appAllClob:
		return true
	}
	return false
}

func (p *Parser) doRedirect(s *Stmt) {
	var r *Redirect
	if s.Redirs == nil {
		var alloc struct {
			redirs [4]*Redirect
			redir  Redirect
		}
		s.Redirs = alloc.redirs[:0]
		r = &alloc.redir
		s.Redirs = append(s.Redirs, r)
	} else {
		r = &Redirect{}
		s.Redirs = append(s.Redirs, r)
	}
	r.N = p.getLit()
	if r.N != nil && r.N.Value[0] == '{' {
		p.checkLang(r.N.Pos(), langBashLike, FeatureRedirectionNamedFileDescriptor)
	}
	r.Op, r.OpPos = RedirOperator(p.tok), p.pos
	switch r.Op {
	case RdrAll, AppAll:
		p.checkLang(p.pos, langBashLike|LangMirBSDKorn|LangZsh, FeatureRedirectionOperator, fmt.Sprintf("%#q", r.Op))
	case AppClob, RdrAllClob, AppAllClob:
		p.checkLang(p.pos, LangZsh, FeatureRedirectionOperator, fmt.Sprintf("%#q", r.Op))
	}
	p.next()
	switch r.Op {
	case Hdoc, DashHdoc:
		oldQuote, oldForbidNested := p.quote, p.forbidNested
		p.quote = hdocWord
		p.heredocs = append(p.heredocs, r)
		w, raw := p.followWordTokRaw(token(r.Op), r.OpPos)
		r.HdocDelim = p.heredocDelimFromWord(w, raw, r.Op)
		p.quote, p.forbidNested = oldQuote, oldForbidNested
		if p.tok == _Newl {
			if len(p.accComs) > 0 {
				c := p.accComs[0]
				if c.Pos().Line() == s.End().Line() {
					s.Comments = append(s.Comments, c)
					p.accComs = p.accComs[1:]
				}
			}
			p.doHeredocs()
		}
	case WordHdoc:
		p.checkLang(r.OpPos, langBashLike|LangMirBSDKorn|LangZsh, FeatureRedirectionHereString)
		fallthrough
	default:
		r.Word = p.followWordTok(token(r.Op), r.OpPos)
	}
}

func (p *Parser) expandCommandAliases(initial bool) {
	allow := initial || p.aliasBlankNext
	for allow {
		if p.expandCommandAlias() {
			allow = true
			continue
		}
		p.aliasBlankNext = false
		return
	}
}

func (p *Parser) getStmt(readEnd, binCmd, fnBody bool) *Stmt {
	pos, ok := p.gotLitWord("!")
	s := &Stmt{Position: pos}
	if ok {
		s.Negated = true
		if p.stopToken() {
			p.posErr(s.Pos(), `%#q cannot form a statement alone`, exclMark)
		}
		if _, ok := p.gotLitWord("!"); ok {
			p.posErr(s.Pos(), `cannot negate a command multiple times`)
		}
	}
	if s = p.gotStmtPipe(s, false); s == nil {
		return nil
	}
	if p.err != nil && !stmtHasHeredocMetadata(s) {
		return nil
	}
	// instead of using recursion, iterate manually
	for p.tok == andAnd || p.tok == orOr {
		if binCmd {
			// left associativity: in a list of BinaryCmds, the
			// right recursion should only read a single element
			return s
		}
		b := &BinaryCmd{
			OpPos: p.pos,
			Op:    BinCmdOperator(p.tok),
			X:     s,
		}
		p.next()
		p.got(_Newl)
		b.Y = p.getStmt(false, true, false)
		if b.Y == nil || p.err != nil {
			if p.recoverError() {
				b.Y = &Stmt{Position: recoveredPos}
			} else {
				p.followErr(b.OpPos, b.Op, noQuote("a statement"))
				return nil
			}
		}
		s = &Stmt{Position: s.Position}
		s.Cmd = b
		s.Comments, b.X.Comments = b.X.Comments, nil
	}
	if readEnd {
		switch p.tok {
		case semicolon:
			s.Semicolon = p.pos
			p.next()
		case and:
			s.Semicolon = p.pos
			p.next()
			s.Background = true
		case orAnd:
			s.Semicolon = p.pos
			p.next()
			s.Coprocess = true
		case andPipe, andBang:
			s.Semicolon = p.pos
			p.next()
			s.Disown = true
		}
	}
	if len(p.accComs) > 0 && !binCmd && !fnBody {
		c := p.accComs[0]
		if c.Pos().Line() == s.End().Line() {
			s.Comments = append(s.Comments, c)
			p.accComs = p.accComs[1:]
		}
	}
	return s
}

func stmtHasHeredocMetadata(s *Stmt) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Redirs {
		if d := r.HdocDelim; d != nil && (d.ClosePos.IsValid() ||
			d.CloseEnd.IsValid() ||
			d.CloseRaw != "" ||
			d.CloseCandidate != nil ||
			d.Matched ||
			d.EOFTerminated ||
			d.TrailingText != "" ||
			d.IndentMode != HeredocIndentNone ||
			d.IndentTabs != 0) {
			return true
		}
	}
	return false
}

func (p *Parser) gotStmtPipe(s *Stmt, binCmd bool) *Stmt {
	s.Comments, p.accComs = p.accComs, nil
	for p.peekRedir() {
		p.doRedirect(s)
	}
	redirsStart := len(s.Redirs)
	allowCompound := redirsStart == 0 || !p.lang.in(langBashLike) || p.lang.in(LangZsh)
	p.expandCommandAliases(true)
	switch p.tok {
	case _LitWord:
		if allowCompound {
			switch rsrv := reservedWord(p.val); rsrv {
			case rsrvLeftBrace:
				p.block(s)
			case "{}":
				// Zsh treats closing braces in a special way, allowing this.
				if p.lang.in(LangZsh) {
					s.Cmd = &Block{Lbrace: p.pos, Rbrace: posAddCol(p.pos, 1)}
					p.next()
				}
			case rsrvIf:
				p.ifClause(s)
			case rsrvWhile, rsrvUntil:
				// TODO(zsh): "repeat"
				p.whileClause(s, rsrv == rsrvUntil)
			case rsrvFor:
				p.forClause(s)
			case rsrvCase:
				p.caseClause(s)
			// TODO(zsh): { try-list } "always" { always-list }
			case rsrvRightBrace:
				p.strayReservedWordErr(p.pos, rsrvRightBrace, `%#q can only be used to close a block`, rightBrace)
			case rsrvThen, rsrvElif:
				if p.lang.in(LangBash | LangBats) {
					p.strayReservedWordErr(p.pos, rsrv, "syntax error near unexpected token %s", bashQuoteString(p.val))
					break
				}
				p.strayReservedWordErr(p.pos, rsrv, "%#q can only be used in an `if`", p.val)
			case rsrvFi:
				if p.lang.in(LangBash | LangBats) {
					p.strayReservedWordErr(p.pos, rsrv, "syntax error near unexpected token %s", bashQuoteString(p.val))
					break
				}
				p.strayReservedWordErr(p.pos, rsrv, "%#q can only be used to end an `if`", p.val)
			case rsrvDo:
				if p.lang.in(LangBash | LangBats) {
					p.strayReservedWordErr(p.pos, rsrv, "syntax error near unexpected token %s", bashQuoteString(p.val))
					break
				}
				p.strayReservedWordErr(p.pos, rsrv, `%#q can only be used in a loop`, p.val)
			case rsrvDone:
				if p.lang.in(LangBash | LangBats) {
					p.strayReservedWordErr(p.pos, rsrv, "syntax error near unexpected token %s", bashQuoteString(p.val))
					break
				}
				p.strayReservedWordErr(p.pos, rsrv, `%#q can only be used to end a loop`, p.val)
			case rsrvEsac:
				if p.lang.in(LangBash | LangBats) {
					p.strayReservedWordErr(p.pos, rsrv, "syntax error near unexpected token %s", bashQuoteString(p.val))
					break
				}
				p.strayReservedWordErr(p.pos, rsrv, "%#q can only be used to end a `case`", p.val)
			case "!":
				if !s.Negated {
					p.curErr(`%#q can only be used in full statements`, exclMark)
					break
				}
			case "[[":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.testClause(s)
				}
			case "]]":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.posErrWithMetadataContext(
						p.pos,
						parseErrorMetadata{
							kind:       ParseErrorKindUnexpected,
							unexpected: p.currentUnexpectedTokenSymbol(),
						},
						parseErrorContext{kind: parseErrorContextUnexpectedToken, token: dblRightBrack.String()},
						`%#q can only be used to close a test`,
						dblRightBrack,
					)
				}
			case "let":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.letClause(s)
				}
			case "function":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.bashFuncDecl(s)
				}
			case "declare":
				if p.lang.in(langBashLike | LangZsh) { // Note that mksh lacks this one.
					p.declClause(s)
				}
			case "local", "export", "readonly", "typeset", "nameref":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.declClause(s)
				}
			case "time":
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.timeClause(s)
				}
			case "coproc":
				if p.lang.in(langBashLike) { // Note that mksh lacks this one.
					p.coprocClause(s)
				}
			case rsrvSelect:
				if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
					p.selectClause(s)
				}
			case "@test":
				if p.lang.in(LangBats) {
					p.testDecl(s)
				}
			}
		}
		if s.Cmd != nil {
			break
		}
		if p.hasValidIdent() && p.r != '$' {
			p.callExpr(s, nil, true)
			break
		}
		if p.r == '$' || p.r == '(' {
			// The name may contain expansions; use getWordRaw to capture
			// the full source text for runtime function-name validation.
			aliases := append([]*AliasExpansion(nil), p.tokAliasChain...)
			w, _ := p.getWordRaw()
			if w == nil {
				break
			}
			if len(w.AliasExpansions) == 0 && len(aliases) > 0 {
				w.AliasExpansions = aliases
			}
			if name := w.Lit(); name != "" && p.tok == leftParen && (!p.lang.in(LangZsh) || p.r == ')') {
				lit := p.rawLit(w.Pos(), w.End(), name)
				p.next()
				p.follow(lit.ValuePos, "foo(", rightParen)
				if p.lang.in(LangPOSIX) && !ValidName(lit.Value) {
					p.posErr(lit.Pos(), "invalid func name")
				}
				p.funcDecl(s, lit.ValuePos, false, true, lit)
				break
			}
			if p.tok == leftParen && p.lang.in(langBashLike) && functionNameNeedsRuntimeValidation(w) {
				if p.r == ')' {
					name := p.rawLit(w.Pos(), w.End(), functionNameSource(w))
					p.next()
					p.follow(w.Pos(), "foo(", rightParen)
					p.funcDecl(s, w.Pos(), false, true, name)
					break
				}
				p.next()
				p.posCurrentUnexpectedErrContext(p.pos, parseErrorContext{kind: parseErrorContextFuncOpen})
				break
			}
			p.callExpr(s, w, false)
			break
		}
		aliases := append([]*AliasExpansion(nil), p.tokAliasChain...)
		name := p.lit(p.pos, p.val)
		p.next()
		// In zsh, ( after a word is a glob qualifier unless followed
		// immediately by ), which is the func declaration syntax.
		if p.tok == leftParen && (!p.lang.in(LangZsh) || p.r == ')') {
			p.next()
			p.follow(name.ValuePos, "foo(", rightParen)
			if p.lang.in(LangPOSIX) && !ValidName(name.Value) {
				p.posErr(name.Pos(), "invalid func name")
			}
			p.funcDecl(s, name.ValuePos, false, true, name)
		} else {
			w := p.wordOneWithAliases(name, aliases)
			if p.lang.in(LangZsh) && !p.spaced {
				w.Parts = append(w.Parts, p.wordParts(nil)...)
				p.finishWord(w)
			}
			p.callExpr(s, w, false)
		}
	case bckQuote:
		if p.backquoteEnd() {
			break
		}
		fallthrough
	case _Lit, dollBrace, dollDblParen, dollParen, dollar, cmdIn, assgnParen, cmdOut,
		sglQuote, dollSglQuote, dblQuote, dollDblQuote, dollBrack,
		globQuest, globStar, globPlus, globAt, globExcl:
		if p.hasValidIdent() {
			p.callExpr(s, nil, true)
			break
		}
		w, _ := p.getWordRaw()
		if w == nil {
			break
		}
		if p.tok == leftParen && p.lang.in(langBashLike) && functionNameNeedsRuntimeValidation(w) {
			if p.r == ')' {
				name := p.rawLit(w.Pos(), w.End(), functionNameSource(w))
				p.next()
				p.follow(w.Pos(), "foo(", rightParen)
				p.funcDecl(s, w.Pos(), false, true, name)
				break
			}
			p.next()
			p.posCurrentUnexpectedErrContext(p.pos, parseErrorContext{kind: parseErrorContextFuncOpen})
			break
		}
		if p.got(leftParen) {
			p.posErr(w.Pos(), "invalid func name")
		}
		p.callExpr(s, w, false)
	case leftParen:
		if p.r == ')' {
			p.rune()
			fpos := p.pos
			p.next()
			if p.atRsrv(rsrvLeftBrace) {
				p.checkLang(fpos, LangZsh, FeatureFunctionAnonymous)
			}
			p.funcDecl(s, fpos, false, true)
			break
		}
		p.subshell(s)
	case dblLeftParen:
		p.arithmExpCmd(s)
	}
	if s.Cmd == nil && len(s.Redirs) == 0 {
		return nil // no statement found
	}
	if redirsStart > 0 && s.Cmd != nil {
		if _, ok := s.Cmd.(*CallExpr); !ok {
			p.checkLang(s.Pos(), LangZsh, FeatureRedirectionBeforeCompound)
		}
	}
	for p.peekRedir() {
		p.doRedirect(s)
	}
	// instead of using recursion, iterate manually
	for p.tok == or || p.tok == orAnd {
		if binCmd {
			// left associativity: in a list of BinaryCmds, the
			// right recursion should only read a single element
			return s
		}
		if p.tok == orAnd && p.lang.in(LangMirBSDKorn) {
			// No need to check for LangPOSIX, as on that language
			// we parse |& as two tokens.
			break
		}
		b := &BinaryCmd{OpPos: p.pos, Op: BinCmdOperator(p.tok), X: s}
		p.next()
		p.got(_Newl)
		if b.Y = p.gotStmtPipe(&Stmt{Position: p.pos}, true); b.Y == nil || p.err != nil {
			if p.recoverError() {
				b.Y = &Stmt{Position: recoveredPos}
			} else {
				p.followErr(b.OpPos, b.Op, noQuote("a statement"))
				break
			}
		}
		s = &Stmt{Position: s.Position}
		s.Cmd = b
		s.Comments, b.X.Comments = b.X.Comments, nil
		// in "! x | y", the bang applies to the entire pipeline
		s.Negated = b.X.Negated
		b.X.Negated = false
	}
	return s
}

func (p *Parser) subshell(s *Stmt) {
	sub := &Subshell{Lparen: p.pos}
	old := p.preNested(subCmd)
	p.next()
	sub.Stmts, sub.Last = p.followStmts("(", sub.Lparen)
	p.postNested(old)
	sub.Rparen = p.matched(sub.Lparen, leftParen, rightParen)
	s.Cmd = sub
}

func (p *Parser) arithmExpCmdWithTailTo(s *Stmt, salvageTail bool, tailEnd Pos) {
	ar := &ArithmCmd{Left: p.pos}
	old := p.preNested(arithmExprCmd)
	p.next()
	if p.got(hash) {
		p.checkLang(ar.Pos(), LangMirBSDKorn, FeatureArithmeticUnsignedExpr)
		ar.Unsigned = true
	}
	ar.X = p.followArithm(dblLeftParen, ar.Left)
	expr := ar.X
	ar.Right = p.arithmEndExpr(&expr, dblLeftParen, ar.Left, old, salvageTail, tailEnd)
	ar.X = expr
	ar.Source = p.arithmCmdSource(ar.Left, ar.Right)
	s.Cmd = ar
}

func (p *Parser) arithmExpCmdWithTail(s *Stmt, salvageTail bool) {
	p.arithmExpCmdWithTailTo(s, salvageTail, Pos{})
}

func (p *Parser) parenAmbiguityStmt(s *Stmt) {
	p.parenAmbiguityProbeDepth++
	defer func() { p.parenAmbiguityProbeDepth-- }()

	afterToken, err := p.currentSourceTail()
	start := p.pos
	fullSrc := append([]byte(dblLeftParen.String()), afterToken...)
	arithPos := posAddCol(start, len(dblLeftParen.String()))
	arith := p.parenAmbiguityClone()
	arith.tok = dblLeftParen
	arith.pos = start
	arith.loadReplay(arithPos, afterToken, err)
	arith.setSourceBuffer(start, fullSrc)
	probeStmt := &Stmt{Position: s.Position}
	arith.arithmExpCmdWithTail(probeStmt, false)
	arithOK := arith.err == nil
	arithRight := Pos{}
	if arithCmd, ok := probeStmt.Cmd.(*ArithmCmd); ok {
		arithRight = arithCmd.Right
	}

	fallbackSrc := append([]byte{'('}, afterToken...)
	fallbackPos := posAddCol(start, len(leftParen.String()))
	sub := p.parenAmbiguityClone()
	sub.tok = leftParen
	sub.pos = start
	sub.loadReplay(fallbackPos, fallbackSrc, err)
	sub.setSourceBuffer(start, fullSrc)
	probeStmt = &Stmt{Position: s.Position}
	sub.subshell(probeStmt)
	fallbackIncomplete := false
	if sub.err != nil && IsIncomplete(sub.err) {
		fallbackIncomplete = hasInternalNewline(afterToken)
	}
	if fallbackIncomplete {
		p.tok = leftParen
		p.pos = start
		p.loadReplay(fallbackPos, fallbackSrc, err)
		p.setSourceBuffer(start, fullSrc)
		p.subshell(s)
		return
	}
	fallbackOK := false
	if sub.err == nil {
		if outer, ok := probeStmt.Cmd.(*Subshell); ok && sub.allowParenAmbiguityFallback(outer.Stmts, outer.Last, outer.Rparen) {
			fallbackOK = true
			p.tok = leftParen
			p.pos = start
			p.loadReplay(fallbackPos, fallbackSrc, err)
			p.setSourceBuffer(start, fullSrc)
			p.subshell(s)
			return
		}
	}
	arithEndedEarly := false
	fallbackTailEnd := Pos{}
	if arithOK && sub.err == nil {
		if outer, ok := probeStmt.Cmd.(*Subshell); ok {
			fallbackTailEnd = parenAmbiguityInnerRight(outer.Stmts)
			if fallbackTailEnd.After(arithRight) {
				arithEndedEarly = true
			}
		}
	} else if sub.err == nil {
		if outer, ok := probeStmt.Cmd.(*Subshell); ok {
			fallbackTailEnd = parenAmbiguityInnerRight(outer.Stmts)
		}
	}
	_ = fallbackOK

	p.tok = dblLeftParen
	p.pos = start
	p.loadReplay(arithPos, afterToken, err)
	p.setSourceBuffer(start, fullSrc)
	if arithOK && !arithEndedEarly {
		p.arithmExpCmdWithTail(s, true)
		return
	}
	if sub.err == nil {
		if outer, ok := probeStmt.Cmd.(*Subshell); ok && keepParenAmbiguityArithError(outer.Stmts, outer.Last) && !arithEndedEarly && !hasNestedDblRightParen(afterToken) {
			p.arithmExpCmdWithTail(s, false)
			return
		}
	}
	if sub.err == nil && fallbackTailEnd.IsValid() && hasNestedDblRightParen(afterToken) {
		p.arithmExpCmdWithTailTo(s, true, fallbackTailEnd)
		return
	}
	p.arithmExpCmdWithTail(s, true)
}

func (p *Parser) arithmExpCmd(s *Stmt) {
	if p.parenAmbiguityDisabled && p.parenAmbiguityProbeDepth > 1 {
		p.arithmExpCmdWithTail(s, true)
		return
	}
	p.parenAmbiguityStmt(s)
}

func (p *Parser) block(s *Stmt) {
	b := &Block{Lbrace: p.pos}
	p.next()
	b.Stmts, b.Last = p.followStmts("{", b.Lbrace, rsrvRightBrace)
	if pos, ok := p.gotRsrv(rsrvRightBrace); ok {
		b.Rbrace = pos
	} else if p.recoverError() {
		b.Rbrace = recoveredPos
	} else {
		p.matchingErr(b.Lbrace, leftBrace, rightBrace)
	}
	s.Cmd = b
}

func (p *Parser) ifClauseHead(pos Pos, listStart, condLabel string) ([]*Stmt, []Comment, Pos, []*Stmt, []Comment) {
	cond, condLast := p.followStmts(listStart, pos, rsrvThen, rsrvFi, rsrvElif, rsrvElse)
	if p.atRsrv(rsrvFi, rsrvElif, rsrvElse) {
		if p.recoverError() {
			thenPos := recoveredPos
			if len(cond) > 1 {
				if split := p.ifMissingThenSplit(cond); split >= 0 {
					then, thenLast := cond[split:], condLast
					return cond[:split], nil, thenPos, then, thenLast
				}
			}
			return cond, condLast, thenPos, []*Stmt{{Position: recoveredPos}}, nil
		}
		p.followErr(pos, condLabel, rsrvThen)
		return cond, condLast, Pos{}, nil, nil
	}
	thenPos := p.followRsrv(pos, condLabel, rsrvThen)
	then, thenLast := p.followStmts("then", thenPos, rsrvFi, rsrvElif, rsrvElse)
	return cond, condLast, thenPos, then, thenLast
}

func (p *Parser) ifMissingThenSplit(stmts []*Stmt) int {
	if len(stmts) < 2 {
		return -1
	}
	for i := 1; i < len(stmts); i++ {
		if ifMissingThenBoundary(p.sourceBetween(stmts[i-1].End(), stmts[i].Pos())) {
			return i
		}
	}
	return -1
}

func ifMissingThenBoundary(src string) bool {
	backslashes := 0
	inComment := false
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '#':
			if !inComment {
				inComment = true
			}
			backslashes = 0
		case '\\':
			if !inComment {
				backslashes++
			}
		case '\r':
			if !inComment {
				continue
			}
		case '\n':
			if inComment || backslashes%2 == 0 {
				return true
			}
			backslashes = 0
		case ' ', '\t':
			if !inComment {
				backslashes = 0
			}
		default:
			if !inComment {
				backslashes = 0
			}
		}
	}
	return false
}

func (p *Parser) ifClause(s *Stmt) {
	rootIf := &IfClause{Position: p.pos, Kind: IfClauseIf}
	p.next()
	rootIf.Cond, rootIf.CondLast, rootIf.ThenPos, rootIf.Then, rootIf.ThenLast = p.ifClauseHead(rootIf.Position, "if", "if <cond>")
	curIf := rootIf
	for p.atRsrv(rsrvElif) {
		elf := &IfClause{Position: p.pos, Kind: IfClauseElif}
		curIf.Last = p.accComs
		p.accComs = nil
		p.next()
		elf.Cond, elf.CondLast, elf.ThenPos, elf.Then, elf.ThenLast = p.ifClauseHead(elf.Position, "elif", "elif <cond>")
		curIf.Else = elf
		curIf = elf
	}
	if elsePos, ok := p.gotRsrv(rsrvElse); ok {
		curIf.Last = p.accComs
		p.accComs = nil
		els := &IfClause{Position: elsePos, Kind: IfClauseElse}
		els.Then, els.ThenLast = p.followStmts("else", els.Position, rsrvFi)
		curIf.Else = els
		curIf = els
	}
	curIf.Last = p.accComs
	p.accComs = nil
	rootIf.FiPos = p.stmtEnd(rootIf, "if", rsrvFi)
	for els := rootIf.Else; els != nil; els = els.Else {
		// All the nested IfClauses share the same FiPos.
		els.FiPos = rootIf.FiPos
	}
	s.Cmd = rootIf
}

func (p *Parser) whileClause(s *Stmt, until bool) {
	wc := &WhileClause{WhilePos: p.pos, Until: until}
	rsrv := rsrvWhile
	rsrvCond := "while <cond>"
	if wc.Until {
		rsrv = rsrvUntil
		rsrvCond = "until <cond>"
	}
	p.next()
	wc.Cond, wc.CondLast = p.followStmts(string(rsrv), wc.WhilePos, rsrvDo)
	wc.DoPos = p.followRsrv(wc.WhilePos, rsrvCond, rsrvDo)
	wc.Do, wc.DoLast = p.followStmts("do", wc.DoPos, rsrvDone)
	wc.DonePos = p.stmtEnd(wc, string(rsrv), rsrvDone)
	s.Cmd = wc
}

func (p *Parser) forClause(s *Stmt) {
	fc := &ForClause{ForPos: p.pos}
	p.next()
	fc.Loop = p.loop(fc.ForPos)

	start, end := rsrvDo, rsrvDone
	if pos, ok := p.gotRsrv(rsrvLeftBrace); ok {
		p.checkLang(pos, langBashLike|LangMirBSDKorn, FeatureLoopBraceFor)
		fc.DoPos = pos
		fc.Braces = true
		start, end = rsrvLeftBrace, rsrvRightBrace
	} else {
		fc.DoPos = p.followRsrv(fc.ForPos, "for foo [in words]", start)
	}

	s.Comments = append(s.Comments, p.accComs...)
	p.accComs = nil
	fc.Do, fc.DoLast = p.followStmts(string(start), fc.DoPos, end)
	fc.DonePos = p.stmtEnd(fc, "for", end)
	s.Cmd = fc
}

func (p *Parser) loop(fpos Pos) Loop {
	switch p.tok {
	case leftParen, dblLeftParen:
		p.checkLang(p.pos, langBashLike|LangZsh, FeatureLoopCStyleFor)
	}
	if p.tok == dblLeftParen {
		cl := &CStyleLoop{Lparen: p.pos}
		old := p.preNested(arithmExprCmd)
		p.next()
		cl.Init = p.arithmExpr(false)
		if !p.got(dblSemicolon) {
			p.follow(p.pos, "expr", semicolon)
			cl.Cond = p.arithmExpr(false)
			p.follow(p.pos, "expr", semicolon)
		}
		cl.Post = p.arithmExpr(false)
		cl.Rparen = p.arithmEnd(dblLeftParen, cl.Lparen, old)
		p.got(semicolon)
		p.got(_Newl)
		return cl
	}
	return p.wordIter("for", fpos)
}

func (p *Parser) wordIter(ftok string, fpos Pos) *WordIter {
	wi := &WordIter{}
	if wi.Name = p.getLit(); wi.Name == nil {
		p.followErr(fpos, ftok, noQuote("a literal"))
	}
	if p.got(semicolon) {
		p.got(_Newl)
		return wi
	}
	p.got(_Newl)
	if pos, ok := p.gotRsrv(rsrvIn); ok {
		wi.InPos = pos
		for !p.stopToken() {
			if w := p.getWord(); w == nil {
				p.curErr("word list can only contain words")
			} else {
				wi.Items = append(wi.Items, w)
			}
		}
		p.got(semicolon)
		p.got(_Newl)
	} else if p.atRsrv(rsrvDo) {
	} else {
		p.followErr(fpos, ftok+" foo", noQuote("`in`, `do`, `;`, or a newline"))
	}
	return wi
}

func (p *Parser) selectClause(s *Stmt) {
	fc := &ForClause{ForPos: p.pos, Select: true}
	p.next()
	fc.Loop = p.wordIter("select", fc.ForPos)
	fc.DoPos = p.followRsrv(fc.ForPos, "select foo [in words]", rsrvDo)
	fc.Do, fc.DoLast = p.followStmts("do", fc.DoPos, rsrvDone)
	fc.DonePos = p.stmtEnd(fc, "select", rsrvDone)
	s.Cmd = fc
}

type patternScanMode uint8

const (
	patternScanConditional patternScanMode = iota
	patternScanCase
)

type scannedRawPattern struct {
	start          Pos
	raw            string
	firstBareParen Pos
}

func (p *Parser) patternAllowsBareGroups() bool {
	return p.lang.in(LangZsh)
}

func (p *Parser) scanRawPattern(mode patternScanMode) (*scannedRawPattern, bool) {
scanLeading:
	for {
		switch p.r {
		case escNewl:
			p.rune()
		case ' ', '\t', '\n':
			if mode == patternScanConditional && p.r == '\n' {
				break scanLeading
			}
			p.rune()
		default:
			break scanLeading
		}
	}

	start := p.nextPos()
	if p.patternScanBoundary(mode, 0) {
		p.next()
		return &scannedRawPattern{start: start}, true
	}
	p.newLit(p.r)
	scanned := &scannedRawPattern{start: start}
	if !p.scanPatternRaw(start, mode, scanned) {
		p.litBs = nil
		return nil, false
	}
	scanned.raw = p.endLit()
	p.next()
	return scanned, true
}

func (p *Parser) patternScanBoundary(mode patternScanMode, parenDepth int) bool {
	switch p.r {
	case utf8.RuneSelf:
		return true
	case ';', '&':
		return mode == patternScanConditional && parenDepth == 0
	case '\n', ' ', '\t':
		return mode == patternScanConditional && parenDepth == 0
	case ']':
		return mode == patternScanConditional && parenDepth == 0 && p.peek() == ']'
	case '|':
		return mode == patternScanConditional && parenDepth == 0 && p.peek() == '|'
	case ')':
		return parenDepth == 0
	default:
		return false
	}
}

func (p *Parser) scanPatternRaw(start Pos, mode patternScanMode, scanned *scannedRawPattern) bool {
	parenStack := make([]byte, 0, 8)
	extglobDepth := 0
	for {
		if p.patternScanBoundary(mode, len(parenStack)) {
			return true
		}
		switch p.r {
		case escNewl:
			p.rune()
		case '<', '>':
			if p.peek() == '(' {
				if !p.scanCondRegexProcSubst(start, tokenFromProcSubst(p.r)) {
					return false
				}
				continue
			}
			p.rune()
		case '?', '*', '+', '@', '!':
			if p.peek() == '(' {
				p.rune()
				parenStack = append(parenStack, 'e')
				extglobDepth++
				p.rune()
				continue
			}
			p.rune()
		case '(':
			if extglobDepth == 0 && !scanned.firstBareParen.IsValid() {
				scanned.firstBareParen = p.nextPos()
			}
			parenStack = append(parenStack, '(')
			p.rune()
		case ')':
			if n := len(parenStack); n > 0 {
				if parenStack[n-1] == 'e' {
					extglobDepth--
				}
				parenStack = parenStack[:n-1]
				p.rune()
				continue
			}
			return true
		case '[':
			p.scanPatternCharClass()
		case '\'':
			if !p.scanCondRegexSingleQuoted(start, sglQuote, false) {
				return false
			}
		case '"':
			if !p.scanCondRegexDoubleQuoted(start, dblQuote) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case '$':
			if !p.scanCondRegexDollar(start, true) {
				return false
			}
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanPatternCharClass() {
	p.rune()
	if p.r == '!' || p.r == '^' {
		p.rune()
	}
	if p.r == ']' {
		p.rune()
	}
	for {
		switch p.r {
		case utf8.RuneSelf:
			return
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return
			}
			p.rune()
		case ']':
			p.rune()
			return
		default:
			p.rune()
		}
	}
}

func (p *Parser) invalidCasePatternParen(pos Pos) {
	p.posErrWithMetadata(pos, parseErrorMetadata{
		kind:       ParseErrorKindUnexpected,
		construct:  ParseErrorSymbolPattern,
		unexpected: ParseErrorSymbolLeftParen,
	}, "syntax error near unexpected token %s", bashQuoteString("("))
}

func patternNearFragment(raw string, pos int) string {
	if pos < 0 || pos >= len(raw) {
		return ""
	}
	start := pos
	for start > 0 {
		switch raw[start-1] {
		case ' ', '\t', '\n', ';', '&', '|', ')':
			goto extendRight
		default:
			start--
		}
	}
extendRight:
	end := pos + 1
	for end < len(raw) {
		switch raw[end] {
		case ' ', '\t', '\n', ';', '&', '|', ')', '\\':
			return raw[start:end]
		default:
			end++
		}
	}
	return raw[start:end]
}

func (p *Parser) invalidCondPatternParen(pos Pos, raw string, start Pos) {
	near := "("
	if pos.IsValid() && start.IsValid() {
		offset := int(pos.Offset() - start.Offset())
		if frag := patternNearFragment(raw, offset); frag != "" {
			near = frag
		}
	}
	p.posErrSecondaryWithMetadata(pos, parseErrorMetadata{
		kind:       ParseErrorKindUnexpected,
		construct:  ParseErrorSymbolPattern,
		unexpected: ParseErrorSymbolLeftParen,
	}, fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
		"syntax error in conditional expression: unexpected token %s",
		bashQuoteString("("),
	)
}

func (p *Parser) casePatternUsesOptionalOpener() bool {
	if p.tok != leftParen {
		return false
	}
	tail := p.sourceFromPos(p.pos)
	if tail == "" || tail[0] != '(' {
		return true
	}
	_, groupEnd, grouped := (rawPatternParser{lang: p.lang}).parsePatternGroup(tail, p.pos, 0)
	if !grouped {
		return true
	}
	_, end := scanPatternText(tail, p.pos, patternScanCase, p.lang)
	if !p.readEOF && end == len(tail) {
		return true
	}
	return end <= groupEnd || end >= len(tail) || tail[end] != ')'
}

func patternTextBoundary(raw string, i int, mode patternScanMode, parenDepth int) bool {
	if i >= len(raw) {
		return true
	}
	switch raw[i] {
	case ';', '&':
		if parenDepth != 0 {
			return false
		}
		return mode == patternScanConditional || (mode == patternScanCase && raw[i] == ';')
	case '\n', ' ', '\t':
		return mode == patternScanConditional && parenDepth == 0
	case ']':
		return mode == patternScanConditional && parenDepth == 0 &&
			i+1 < len(raw) && raw[i+1] == ']'
	case '|':
		return mode == patternScanConditional && parenDepth == 0 &&
			i+1 < len(raw) && raw[i+1] == '|'
	case ')':
		return parenDepth == 0
	default:
		return false
	}
}

func scanPatternText(raw string, base Pos, mode patternScanMode, lang LangVariant) (*scannedRawPattern, int) {
	start := 0
	for start < len(raw) {
		switch raw[start] {
		case ' ', '\t':
			start++
		case '\n':
			if mode == patternScanConditional {
				goto scan
			}
			start++
		default:
			goto scan
		}
	}
scan:
	scanned := &scannedRawPattern{start: advancePosBytes(base, []byte(raw[:start]))}
	parenStack := make([]byte, 0, 8)
	extglobDepth := 0
	rawParser := rawPatternParser{lang: lang}
	for i := start; ; {
		if patternTextBoundary(raw, i, mode, len(parenStack)) {
			return scanned, i
		}
		switch raw[i] {
		case '\\':
			if i+1 < len(raw) {
				i += 2
			} else {
				i++
			}
		case '<', '>':
			if i+1 < len(raw) && raw[i+1] == '(' {
				if _, consumed, ok := rawParser.parseShellPart(raw[i:], posAddCol(base, i)); ok {
					i += consumed
					continue
				}
			}
			i++
		case '?', '*', '+', '@', '!':
			if i+1 < len(raw) && raw[i+1] == '(' {
				parenStack = append(parenStack, 'e')
				extglobDepth++
				i += 2
				continue
			}
			i++
		case '(':
			if extglobDepth == 0 && !scanned.firstBareParen.IsValid() {
				scanned.firstBareParen = posAddCol(base, i)
			}
			parenStack = append(parenStack, '(')
			i++
		case ')':
			if n := len(parenStack); n > 0 {
				if parenStack[n-1] == 'e' {
					extglobDepth--
				}
				parenStack = parenStack[:n-1]
				i++
				continue
			}
			return scanned, i
		case '[':
			i = patternCharClassEnd(raw, i)
		case '\'', '"', '`', '$':
			if _, consumed, ok := rawParser.parseShellPart(raw[i:], posAddCol(base, i)); ok {
				i += consumed
				continue
			}
			i++
		default:
			i++
		}
	}
}

func (p *Parser) scanRawPatternSource(start Pos, mode patternScanMode) (*scannedRawPattern, bool) {
	raw := p.sourceFromPos(start)
	scanned, end := scanPatternText(raw, start, mode, p.lang)
	if mode == patternScanCase && !p.readEOF && end == len(raw) {
		return nil, false
	}
	scanned.raw = raw[int(scanned.start.Offset()-start.Offset()):end]
	boundaryPos := posAddCol(start, end)
	for p.tok != _EOF && p.pos.Offset() < boundaryPos.Offset() {
		p.next()
	}
	if p.pos.Offset() != boundaryPos.Offset() {
		p.next()
	}
	return scanned, true
}

func (p *Parser) caseClause(s *Stmt) {
	cc := &CaseClause{Case: p.pos}
	p.next()
	cc.Word = p.getWord()
	if cc.Word == nil {
		if p.recoverError() {
			cc.Word = p.wordOne(&Lit{ValuePos: recoveredPos})
		} else {
			p.followErr(cc.Case, "case", noQuote("a word"))
			s.Cmd = cc
			return
		}
	}
	end := rsrvEsac
	p.got(_Newl)
	if pos, ok := p.gotRsrv(rsrvLeftBrace); ok {
		cc.In = pos
		cc.Braces = true
		p.checkLang(cc.Pos(), LangMirBSDKorn, FeatureCaseKornForm)
		end = rsrvRightBrace
	} else {
		cc.In = p.followRsrv(cc.Case, "case x", rsrvIn)
	}
	cc.Items = p.caseItems(end)
	cc.Last, p.accComs = p.accComs, nil
	cc.Esac = p.stmtEnd(cc, "case", end)
	s.Cmd = cc
}

func (p *Parser) caseItems(stop reservedWord) (items []*CaseItem) {
	p.got(_Newl)
	for p.tok != _EOF && !p.atRsrv(stop) {
		ci := &CaseItem{}
		ci.Comments, p.accComs = p.accComs, nil
		if p.casePatternUsesOptionalOpener() {
			p.next()
		}
		if scanned, ok := p.scanRawPatternSource(p.pos, patternScanCase); ok {
			rawParser := rawPatternParser{lang: p.lang}
			if idx, op := rawParser.findInvalidCasePatternOperator(scanned.raw, scanned.start); op != 0 {
				pos := posAddCol(scanned.start, idx)
				if idx == 0 {
					p.posErr(pos, "case patterns must consist of words")
				} else {
					p.posErr(pos, "case patterns must be separated with %#q", or)
				}
				return items
			}
			start := 0
			validPatterns := true
			for {
				end, delim := rawParser.findPatternListSplit(scanned.raw, scanned.start, start)
				partStart, partEnd := trimPatternRawSegment(scanned.raw, start, end)
				partRaw := scanned.raw[partStart:partEnd]
				partBase := advancePosBytes(scanned.start, []byte(scanned.raw[:partStart]))
				switch {
				case partRaw == "":
					validPatterns = false
				case rawParser.hasTopLevelWhitespace(partRaw, partBase):
					validPatterns = false
				default:
					ci.Patterns = append(ci.Patterns, rawParser.parse(partRaw, partBase))
				}
				if delim != '|' {
					break
				}
				start = end + 1
			}
			if group := firstPatternGroupInList(ci.Patterns); group != nil && !p.patternAllowsBareGroups() {
				if !p.recoverError() {
					p.invalidCasePatternParen(group.Pos())
					return items
				}
			} else if group == nil && scanned.firstBareParen.IsValid() && !p.patternAllowsBareGroups() {
				p.invalidCasePatternParen(scanned.firstBareParen)
				return items
			}
			if p.tok != rightParen && p.tok != _EOF {
				p.curErr("syntax error near unexpected token %s", p.tok.bashQuote())
				return items
			}
			if !validPatterns && len(ci.Patterns) > 0 {
				p.curErr("case patterns must consist of words")
			}
		} else {
			for p.tok != _EOF {
				if pat := p.getPattern(or, rightParen); pat == nil {
					p.curErr("case patterns must consist of words")
				} else {
					ci.Patterns = append(ci.Patterns, pat)
				}
				if p.tok == rightParen {
					break
				}
				if !p.got(or) {
					p.curErr("case patterns must be separated with %#q", or)
				}
			}
		}
		if len(ci.Patterns) == 0 {
			if p.recoverError() {
				ci.Patterns = append(ci.Patterns, &Pattern{
					Parts: []PatternPart{&Lit{ValuePos: recoveredPos}},
				})
			} else {
				p.curErr("case patterns must consist of words")
			}
		}
		old := p.preNested(switchCase)
		p.next()
		ci.Stmts, ci.Last = p.stmtList(stop)
		p.postNested(old)
		switch p.tok {
		case dblSemicolon, semiAnd, dblSemiAnd, semiOr:
		default:
			ci.Op = Break
			items = append(items, ci)
			return items
		}
		ci.Last = append(ci.Last, p.accComs...)
		p.accComs = nil
		ci.OpPos = p.pos
		ci.Op = CaseOperator(p.tok)
		p.next()
		p.got(_Newl)

		// Split the comments:
		//
		// case x in
		// a)
		//   foo
		//   ;;
		//   # comment for a
		// # comment for b
		// b)
		//   [...]
		split := len(p.accComs)
		for i, c := range slices.Backward(p.accComs) {
			if c.Pos().Col() != p.pos.Col() {
				break
			}
			split = i
		}
		ci.Comments = append(ci.Comments, p.accComs[:split]...) //nolint:nilaway // split is initialised to len(p.accComs), so [:split] on nil is [:0] which is safe
		p.accComs = p.accComs[split:]

		items = append(items, ci)
	}
	return items
}

func (p *Parser) testClause(s *Stmt) {
	tc := &TestClause{Left: p.pos}
	old := p.preNested(testExpr)
	p.next()
	if tc.X = p.condExprBinary(false); tc.X == nil {
		if p.isCondClosingTok() {
			p.posErrWithMetadataContext(
				p.pos,
				parseErrorMetadata{
					kind:       ParseErrorKindUnexpected,
					unexpected: p.currentUnexpectedTokenSymbol(),
				},
				parseErrorContext{kind: parseErrorContextNearToken, token: dblRightBrack.String()},
				"syntax error near %s",
				bashQuoteString(dblRightBrack.String()),
			)
		} else if p.tok == rightParen {
			p.curErrSecondary(
				fmt.Sprintf("syntax error near %s", p.tok.bashQuote()),
				"unexpected token %s in conditional command",
				p.tok.bashQuote(),
			)
		} else if p.tok == andAnd || p.tok == orOr {
			near := "&"
			if p.tok == orOr {
				near = "|"
			}
			p.curErrSecondary(
				fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
				"unexpected token %s in conditional command",
				p.tok.bashQuote(),
			)
		} else {
			p.followErrExp(tc.Left, dblLeftBrack)
		}
	}
	tc.Right = p.pos
	if _, ok := p.gotCondClosingTok(); !ok {
		p.matchingErr(tc.Left, dblLeftBrack, dblRightBrack)
	}
	p.postNested(old)
	s.Cmd = tc
}

func (p *Parser) isCondClosingTok() bool {
	if p.val != "]]" {
		return false
	}
	return p.tok == _LitWord || (p.tok == _Lit && p.r == '`')
}

func (p *Parser) gotCondClosingTok() (Pos, bool) {
	if !p.isCondClosingTok() {
		return Pos{}, false
	}
	pos := p.pos
	p.next()
	return pos, true
}

func condWordAsVarRef(word *Word) *VarRef {
	if word == nil || len(word.Parts) == 0 {
		return nil
	}
	name, ok := word.Parts[0].(*Lit)
	if !ok || !ValidName(name.Value) {
		return nil
	}
	if len(word.Parts) == 1 {
		return &VarRef{Name: name}
	}
	left, ok := word.Parts[1].(*Lit)
	if !ok || left.Value != "[" {
		return nil
	}
	right, ok := word.Parts[len(word.Parts)-1].(*Lit)
	if !ok || right.Value != "]" {
		return nil
	}
	if len(word.Parts) < 4 {
		return nil
	}
	parts := slices.Clone(word.Parts[2 : len(word.Parts)-1])
	sub := &Subscript{
		Left:  left.Pos(),
		Right: right.Pos(),
		Kind:  SubscriptExpr,
		Mode:  SubscriptAuto,
		Expr:  &Word{Parts: parts},
	}
	if len(parts) == 1 {
		if lit, ok := parts[0].(*Lit); ok {
			switch lit.Value {
			case "@":
				sub.Kind = SubscriptAt
			case "*":
				sub.Kind = SubscriptStar
			}
		}
	}
	return &VarRef{Name: name, Index: sub}
}

func (p *Parser) followCondWord(tok token, pos Pos) *CondWord {
	w := p.followWordTok(tok, pos)
	if w == nil {
		return nil
	}
	return &CondWord{Word: w}
}

func (p *Parser) followCondVarRefOrWord(tok token, pos Pos, context VarRefContext) CondExpr {
	w := p.followWordTok(tok, pos)
	if w == nil {
		return nil
	}
	if ref := condWordAsVarRef(w); ref != nil {
		ref.Context = context
		return &CondVarRef{Ref: ref}
	}
	return &CondWord{Word: w}
}

func (p *Parser) followCondPattern(tok token, pos Pos) *CondPattern {
	scanned, ok := p.scanRawPattern(patternScanConditional)
	if !ok {
		return nil
	}
	if scanned.raw == "" {
		p.followErr(pos, tok, noQuote("a word"))
		return nil
	}
	pat := rawPatternParser{lang: p.lang}.parse(scanned.raw, scanned.start)
	if group := firstPatternGroup(pat); group != nil && !p.patternAllowsBareGroups() {
		if !p.recoverError() {
			p.invalidCondPatternParen(group.Pos(), scanned.raw, scanned.start)
			return nil
		}
	} else if group == nil && scanned.firstBareParen.IsValid() && !p.patternAllowsBareGroups() {
		p.invalidCondPatternParen(scanned.firstBareParen, scanned.raw, scanned.start)
		return nil
	}
	return &CondPattern{Pattern: pat}
}

func (p *Parser) followCondRegex(tok token, pos Pos) *CondRegex {
	w := p.scanCondRegex(tok, pos)
	if w == nil {
		return nil
	}
	if lit := w.Lit(); lit != "" {
		if idx, tokenText, near, ok := literalRegexUnexpectedTopLevelRParen(lit); ok {
			errPos := posAddCol(w.Pos(), idx)
			p.posErrSecondary(
				errPos,
				fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
				"syntax error in conditional expression: unexpected token %s",
				bashQuoteString(tokenText),
			)
			return nil
		}
	}
	return &CondRegex{Word: w}
}

func condUnexpectedTokenFragment(src string) string {
	if src == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(src); {
		if b.Len() > 0 && strings.HasPrefix(src[i:], "]]") {
			break
		}
		r, size := utf8.DecodeRuneInString(src[i:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		switch r {
		case ' ', '\t', '\n', ';', '&', '|', ')':
			return b.String()
		}
		b.WriteString(src[i : i+size])
		i += size
		if r == '<' || r == '>' {
			return b.String()
		}
	}
	return b.String()
}

func (p *Parser) condUnexpectedTokenTextAndNear(pos Pos) (string, string) {
	near := condUnexpectedTokenFragment(p.sourceFromPos(pos))
	tokenText := p.currentTokenSource()
	switch p.tok {
	case _Lit, dollar:
		if near != "" {
			tokenText = near
		}
	case _LitRedir:
		if tokenText == "" {
			tokenText = near
		}
	default:
		if tokenText == "" {
			tokenText = near
		}
	}
	if near == "" {
		near = tokenText
	}
	return tokenText, near
}

func (p *Parser) condBinaryOperatorExpected(pos Pos) {
	tokenText, near := p.condUnexpectedTokenTextAndNear(pos)
	p.posErrSecondary(
		pos,
		fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
		"unexpected token %s, conditional binary operator expected",
		bashQuoteString(tokenText),
	)
}

func (p *Parser) condUnaryUnexpectedArgument(pos Pos) {
	tokenText, near := p.condUnexpectedTokenTextAndNear(pos)
	p.posErrSecondary(
		pos,
		fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
		"unexpected argument %s to conditional unary operator",
		bashQuoteString(tokenText),
	)
}

func (p *Parser) condUnexpectedToken(pos Pos) {
	tokenText, near := p.condUnexpectedTokenTextAndNear(pos)
	p.posErrSecondary(
		pos,
		fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
		"syntax error in conditional expression: unexpected token %s",
		bashQuoteString(tokenText),
	)
}

func (p *Parser) condBinaryUnexpectedToken(expr *CondBinary, pos Pos, tokenText string) {
	near := regexNearFragment(p.sourceFromPos(pos))
	if near == "" {
		near = tokenText
	}
	switch expr.Op {
	case TsReMatch:
		text := "syntax error in conditional expression: unexpected token %s"
		args := []any{bashQuoteString(tokenText)}
		if p.legacyBashCompat {
			text = "syntax error in conditional expression"
			args = nil
		}
		if pos.Line() > expr.Pos().Line() {
			p.posErrSecondaryDetailed(
				expr.Pos(),
				pos,
				pos,
				p.sourceLineAtPos(pos),
				fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
				text,
				args...,
			)
			return
		}
		p.posErrSecondary(
			pos,
			fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
			text,
			args...,
		)
	case TsMatchShort, TsMatch, TsNoMatch:
		if tokenText != "(" {
			p.posErr(pos, "not a valid test operator: %#q", tokenText)
			return
		}
		near = patternSource(expr.Y.(*CondPattern).Pattern) + near
		p.posErrSecondary(
			pos,
			fmt.Sprintf("syntax error near %s", bashQuoteString(near)),
			"syntax error in conditional expression: unexpected token %s",
			bashQuoteString(tokenText),
		)
	default:
		p.posErr(pos, "not a valid test operator: %#q", tokenText)
	}
}

func (p *Parser) condRegexUnexpectedStart() bool {
	switch p.r {
	case ')', ';', '&':
		return true
	case '|':
		return p.peek() == '|'
	case '<', '>':
		return p.peek() != '('
	default:
		return false
	}
}

func (p *Parser) condRegexRightParenBoundary() bool {
	switch next := p.peek(); next {
	case utf8.RuneSelf, ' ', '\t', '\n', ';', '&', '<', '>', ')':
		return true
	case ']':
		_, next2 := p.peekTwo()
		return next2 == ']'
	default:
		return false
	}
}

func (p *Parser) condRegexWhitespaceBoundary() bool {
	switch next, next2, next3 := p.peekThree(); next {
	case ']':
		return next2 == ']' && regexClauseDelimiterByte(next3)
	default:
		return false
	}
}

func regexClauseDelimiterByte(b byte) bool {
	switch b {
	case utf8.RuneSelf, ' ', '\t', '\n', ';', '&', '|', ')':
		return true
	default:
		return false
	}
}

func (p *Parser) scanCondRegex(tok token, pos Pos) *Word {
	for {
		switch p.r {
		case escNewl, ' ', '\t':
			p.rune()
		default:
			goto scan
		}
	}

scan:
	switch p.r {
	case '\n', utf8.RuneSelf:
		p.next()
		if p.recoverError() {
			return p.wordOne(&Lit{ValuePos: recoveredPos})
		}
		p.followErr(pos, tok, noQuote("a word"))
		return nil
	}
	if p.condRegexUnexpectedStart() {
		p.next()
		switch p.tok {
		case semicolon, and, andAnd, orOr, rdrIn, rdrOut, rdrInOut, rightParen:
			p.curErrSecondary(
				fmt.Sprintf("syntax error near %s", p.tok.bashQuote()),
				"syntax error in conditional expression: unexpected token %s",
				p.tok.bashQuote(),
			)
			return nil
		}
		if p.recoverError() {
			return p.wordOne(&Lit{ValuePos: recoveredPos})
		}
		p.followErr(pos, tok, noQuote("a word"))
		return nil
	}

	start := p.nextPos()
	p.newLit(p.r)
	if !p.scanCondRegexRaw(start) {
		p.litBs = nil
		return nil
	}
	raw := p.endLit()
	p.next()

	word := &Word{
		Parts:           rawWordPartParser{lang: p.lang}.parse(raw, start),
		AliasExpansions: append([]*AliasExpansion(nil), p.aliasChain...),
	}
	return p.finishWord(word)
}

func (p *Parser) scanCondRegexRaw(start Pos) bool {
	depth := 0
	for {
		switch p.r {
		case utf8.RuneSelf:
			return true
		case escNewl:
			p.rune()
		case ' ', '\t':
			if depth == 0 || p.condRegexWhitespaceBoundary() {
				return true
			}
			p.rune()
		case '\n':
			if depth == 0 || p.condRegexWhitespaceBoundary() {
				return true
			}
			p.rune()
		case ']':
			p.rune()
		case ';', '&':
			if depth == 0 {
				return true
			}
			p.rune()
		case '|':
			p.rune()
		case '<', '>':
			if p.peek() == '(' {
				if !p.scanCondRegexProcSubst(start, tokenFromProcSubst(p.r)) {
					return false
				}
				continue
			}
			if depth == 0 {
				return true
			}
			p.rune()
		case '(':
			depth++
			p.rune()
		case ')':
			if depth > 0 {
				depth--
			} else if p.condRegexRightParenBoundary() {
				return true
			}
			p.rune()
		case '\'':
			if !p.scanCondRegexSingleQuoted(start, sglQuote, false) {
				return false
			}
		case '"':
			if !p.scanCondRegexDoubleQuoted(start, dblQuote) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case '$':
			if !p.scanCondRegexDollar(start, true) {
				return false
			}
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		default:
			p.rune()
		}
	}
}

func tokenFromProcSubst(r rune) token {
	if r == '<' {
		return cmdIn
	}
	return cmdOut
}

func (p *Parser) scanCondRegexProcSubst(start Pos, left token) bool {
	p.rune()
	return p.scanCondRegexCommandGroup(start, left)
}

func (p *Parser) scanCondRegexSingleQuoted(start Pos, quote token, dollar bool) bool {
	p.rune()
	for {
		switch p.r {
		case '\\':
			if dollar {
				if p.rune() == utf8.RuneSelf {
					return true
				}
				p.rune()
				continue
			}
			p.rune()
		case '\'':
			p.rune()
			return true
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.quoteErr(start, quote)
			return false
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanCondRegexDoubleQuoted(start Pos, quote token) bool {
	p.rune()
	for {
		switch p.r {
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		case '"':
			p.rune()
			return true
		case '$':
			if !p.scanCondRegexDollar(start, false) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.quoteErr(start, quote)
			return false
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanCondRegexBackquote(start Pos) bool {
	p.rune()
	for {
		switch p.r {
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		case '`':
			p.rune()
			return true
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.quoteErr(start, bckQuote)
			return false
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanCondRegexDollar(start Pos, allowDollarQuotes bool) bool {
	switch r := p.rune(); r {
	case utf8.RuneSelf:
		return true
	case '\'':
		if !allowDollarQuotes {
			return true
		}
		return p.scanCondRegexSingleQuoted(start, dollSglQuote, true)
	case '"':
		if !allowDollarQuotes {
			return true
		}
		return p.scanCondRegexDoubleQuoted(start, dollDblQuote)
	case '{':
		return p.scanCondRegexBraced(start, dollBrace, rightBrace)
	case '[':
		return p.scanCondRegexBraced(start, dollBrack, rightBrack)
	case '(':
		if p.peek() == '(' {
			p.rune()
			return p.scanCondRegexDoubleParen(start)
		}
		return p.scanCondRegexCommandGroup(start, dollParen)
	default:
		if asciiLetter(r) || r == '_' {
			for {
				next := p.rune()
				if !asciiLetter(next) && !asciiDigit(next) && next != '_' {
					return true
				}
			}
		}
		if asciiDigit(r) || singleRuneParam(r) {
			p.rune()
		}
		return true
	}
}

func (p *Parser) scanCondRegexBraced(start Pos, left, right token) bool {
	depth := 1
	p.rune()
	for {
		switch p.r {
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.matchingErr(start, left, right)
			return false
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		case '\'':
			if !p.scanCondRegexSingleQuoted(start, sglQuote, false) {
				return false
			}
		case '"':
			if !p.scanCondRegexDoubleQuoted(start, dblQuote) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case '$':
			if !p.scanCondRegexDollar(start, true) {
				return false
			}
		case '{':
			if right == rightBrace {
				depth++
			}
			p.rune()
		case '}':
			if right == rightBrace {
				depth--
				p.rune()
				if depth == 0 {
					return true
				}
				continue
			}
			p.rune()
		case ']':
			if right == rightBrack {
				p.rune()
				return true
			}
			p.rune()
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanCondRegexCommandGroup(start Pos, left token) bool {
	depth := 1
	p.rune()
	for {
		switch p.r {
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.matchingErr(start, left, rightParen)
			return false
		case escNewl:
			p.rune()
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		case '\'':
			if !p.scanCondRegexSingleQuoted(start, sglQuote, false) {
				return false
			}
		case '"':
			if !p.scanCondRegexDoubleQuoted(start, dblQuote) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case '$':
			if !p.scanCondRegexDollar(start, true) {
				return false
			}
		case '<', '>':
			if p.peek() == '(' {
				if !p.scanCondRegexProcSubst(start, tokenFromProcSubst(p.r)) {
					return false
				}
				continue
			}
			p.rune()
		case '(':
			depth++
			p.rune()
		case ')':
			depth--
			p.rune()
			if depth == 0 {
				return true
			}
		default:
			p.rune()
		}
	}
}

func (p *Parser) scanCondRegexDoubleParen(start Pos) bool {
	depth := 0
	p.rune()
	for {
		switch p.r {
		case utf8.RuneSelf:
			p.tok = _EOF
			if p.recoverError() {
				return true
			}
			p.matchingErr(start, dollDblParen, dblRightParen)
			return false
		case escNewl:
			p.rune()
		case '\\':
			if p.rune() == utf8.RuneSelf {
				return true
			}
			p.rune()
		case '\'':
			if !p.scanCondRegexSingleQuoted(start, sglQuote, false) {
				return false
			}
		case '"':
			if !p.scanCondRegexDoubleQuoted(start, dblQuote) {
				return false
			}
		case '`':
			if !p.scanCondRegexBackquote(start) {
				return false
			}
		case '$':
			if !p.scanCondRegexDollar(start, true) {
				return false
			}
		case '(':
			depth++
			p.rune()
		case ')':
			if depth == 0 && p.peek() == ')' {
				p.rune()
				p.rune()
				return true
			}
			if depth > 0 {
				depth--
			}
			p.rune()
		default:
			p.rune()
		}
	}
}

func (p *Parser) condExprBinary(pastAndOr bool) CondExpr {
	p.got(_Newl)
	var left CondExpr
	if pastAndOr {
		left = p.condExprUnary()
	} else {
		left = p.condExprBinary(true)
	}
	if left == nil {
		return left
	}
	p.got(_Newl)
	switch p.tok {
	case andAnd, orOr:
	case _LitWord:
		if p.isCondClosingTok() {
			return left
		}
		opTok := token(testBinaryOp(p.val))
		if _, ok := left.(*CondWord); !ok {
			if opTok != illegalTok {
				p.posErr(p.pos, "expected %#q, %#q or %#q after complex expr",
					AndTest, OrTest, dblRightBrack)
			} else {
				if b, ok := left.(*CondBinary); ok {
					p.condBinaryUnexpectedToken(b, p.pos, p.val)
				} else if _, ok := left.(*CondUnary); ok {
					p.condUnexpectedToken(p.pos)
				} else {
					p.curErr("not a valid test operator: %#q", p.val)
				}
			}
			return left
		}
		if opTok == illegalTok {
			p.condBinaryOperatorExpected(p.pos)
		}
		p.tok = opTok
	case _Lit:
		if p.isCondClosingTok() {
			return left
		}
		if _, ok := left.(*CondWord); ok {
			p.condBinaryOperatorExpected(p.pos)
		} else {
			p.curErr("test operator words must consist of a single literal")
		}
	case _LitRedir:
		if _, ok := left.(*CondWord); ok {
			p.condBinaryOperatorExpected(p.pos)
		} else {
			p.curErr("not a valid test operator: %#q", p.currentTokenSource())
		}
	case rdrIn, rdrOut:
		if _, ok := left.(*CondWord); !ok {
			p.posErr(p.pos, "expected %#q, %#q or %#q after complex expr",
				AndTest, OrTest, dblRightBrack)
			return left
		}
	case _EOF, rightParen:
		return left
	default:
		if b, ok := left.(*CondBinary); ok {
			p.condBinaryUnexpectedToken(b, p.pos, p.tok.String())
		} else if _, ok := left.(*CondWord); ok {
			p.condBinaryOperatorExpected(p.pos)
		} else if _, ok := left.(*CondUnary); ok {
			p.condUnexpectedToken(p.pos)
		} else {
			p.curErr("not a valid test operator: %#q", p.tok)
		}
	}
	b := &CondBinary{
		OpPos: p.pos,
		Op:    BinTestOperator(p.tok),
		X:     left,
	}
	switch b.Op {
	case AndTest, OrTest:
		p.next()
		if b.Y = p.condExprBinary(false); b.Y == nil {
			p.followErrExp(b.OpPos, b.Op)
		}
	case TsReMatch:
		p.checkLang(p.pos, langBashLike|LangZsh, FeatureConditionalRegexTest)
		if b.Y = p.followCondRegex(token(b.Op), b.OpPos); b.Y == nil {
			return nil
		}
	case TsMatchShort, TsMatch, TsNoMatch:
		if b.Y = p.followCondPattern(token(b.Op), b.OpPos); b.Y == nil {
			return nil
		}
	default:
		p.next()
		if b.Y = p.followCondWord(token(b.Op), b.OpPos); b.Y == nil {
			return nil
		}
	}
	return b
}

func (p *Parser) condExprUnary() CondExpr {
	switch p.tok {
	case _EOF:
		return nil
	case rightParen:
		return nil
	case _LitWord:
		op := token(testUnaryOp(p.val))
		switch op {
		case illegalTok:
		case tsRefVar, tsModif:
			if p.lang.in(langBashLike) {
				p.tok = op
			}
		default:
			p.tok = op
		}
	}
	switch p.tok {
	case exclMark:
		u := &CondUnary{OpPos: p.pos, Op: TsNot}
		p.next()
		if u.X = p.condExprBinary(false); u.X == nil {
			p.followErrExp(u.OpPos, u.Op)
		}
		return u
	case tsExists, tsRegFile, tsDirect, tsCharSp, tsBlckSp, tsNmPipe,
		tsSocket, tsSmbLink, tsSticky, tsGIDSet, tsUIDSet, tsGrpOwn,
		tsUsrOwn, tsModif, tsRead, tsWrite, tsExec, tsNoEmpty,
		tsFdTerm, tsEmpStr, tsNempStr, tsOptSet, tsVarSet, tsRefVar:
		u := &CondUnary{OpPos: p.pos, Op: UnTestOperator(p.tok)}
		p.next()
		switch {
		case p.isCondClosingTok():
			p.condUnaryUnexpectedArgument(p.pos)
			return nil
		case p.tok == rdrIn || p.tok == rdrOut || p.tok == _LitRedir:
			p.condUnaryUnexpectedArgument(p.pos)
			return nil
		}
		if u.Op == TsVarSet || u.Op == TsRefVar {
			context := VarRefDefault
			if u.Op == TsVarSet {
				context = VarRefVarSet
			}
			u.X = p.followCondVarRefOrWord(token(u.Op), u.OpPos, context)
		} else {
			u.X = p.followCondWord(token(u.Op), u.OpPos)
		}
		return u
	case leftParen:
		pe := &CondParen{Lparen: p.pos}
		p.next()
		if pe.X = p.condExprBinary(false); pe.X == nil {
			if p.isCondClosingTok() {
				p.posErr(pe.Lparen, "expected %#q", rightParen)
			} else {
				p.followErrExp(pe.Lparen, leftParen)
			}
			return nil
		}
		if p.tok == rightParen {
			pe.Rparen = p.pos
			p.next()
		} else {
			p.posErr(pe.Lparen, "expected %#q", rightParen)
			return nil
		}
		return pe
	case _LitWord, _Lit:
		if p.isCondClosingTok() {
			return nil
		}
		fallthrough
	default:
		if w := p.getWord(); w != nil {
			return &CondWord{Word: w}
		}
		return nil
	}
}

func (p *Parser) testExprBinary(pastAndOr bool) TestExpr {
	p.got(_Newl)
	var left TestExpr
	if pastAndOr {
		left = p.testExprUnary()
	} else {
		left = p.testExprBinary(true)
	}
	if left == nil {
		return left
	}
	p.got(_Newl)
	switch p.tok {
	case andAnd, orOr:
	case _LitWord:
		if p.isCondClosingTok() {
			return left
		}
		if p.tok = token(testBinaryOp(p.val)); p.tok == illegalTok {
			p.curErr("not a valid test operator: %#q", p.val)
		}
	case rdrIn, rdrOut:
	case _EOF, rightParen:
		return left
	case _Lit:
		if p.isCondClosingTok() {
			return left
		}
		p.curErr("test operator words must consist of a single literal")
	default:
		p.curErr("not a valid test operator: %#q", p.tok)
	}
	b := &BinaryTest{
		OpPos: p.pos,
		Op:    BinTestOperator(p.tok),
		X:     left,
	}
	switch b.Op {
	case AndTest, OrTest:
		p.next()
		if b.Y = p.testExprBinary(false); b.Y == nil {
			p.followErrExp(b.OpPos, b.Op)
		}
	default:
		if _, ok := b.X.(*Word); !ok {
			p.posErr(b.OpPos, "expected %#q, %#q or %#q after complex expr",
				AndTest, OrTest, dblRightBrack)
		}
		p.next()
		b.Y = p.followWordTok(token(b.Op), b.OpPos)
	}
	return b
}

func (p *Parser) testExprUnary() TestExpr {
	switch p.tok {
	case _EOF, rightParen:
		return nil
	case _LitWord:
		op := token(testUnaryOp(p.val))
		switch op {
		case illegalTok:
		case tsRefVar, tsModif: // not available in mksh
			if p.lang.in(langBashLike) {
				p.tok = op
			}
		default:
			p.tok = op
		}
	}
	switch p.tok {
	case exclMark:
		u := &UnaryTest{OpPos: p.pos, Op: TsNot}
		p.next()
		if u.X = p.testExprBinary(false); u.X == nil {
			p.followErrExp(u.OpPos, u.Op)
		}
		return u
	case tsExists, tsRegFile, tsDirect, tsCharSp, tsBlckSp, tsNmPipe,
		tsSocket, tsSmbLink, tsSticky, tsGIDSet, tsUIDSet, tsGrpOwn,
		tsUsrOwn, tsModif, tsRead, tsWrite, tsExec, tsNoEmpty,
		tsFdTerm, tsEmpStr, tsNempStr, tsOptSet, tsVarSet, tsRefVar:
		u := &UnaryTest{OpPos: p.pos, Op: UnTestOperator(p.tok)}
		p.next()
		u.X = p.followWordTok(token(u.Op), u.OpPos)
		return u
	case leftParen:
		pe := &ParenTest{Lparen: p.pos}
		p.next()
		if pe.X = p.testExprBinary(false); pe.X == nil {
			p.followErrExp(pe.Lparen, leftParen)
		}
		pe.Rparen = p.matched(pe.Lparen, leftParen, rightParen)
		return pe
	case _LitWord, _Lit:
		if p.isCondClosingTok() {
			return nil
		}
		fallthrough
	default:
		if w := p.getWord(); w != nil {
			return w
		}
		// otherwise we'd return a typed nil above
		return nil
	}
}

func (p *Parser) declClause(s *Stmt) {
	ds := &DeclClause{Variant: p.lit(p.pos, p.val)}
	arrayMode := ArrayExprInherit
	p.next()
	for !p.stopToken() && !p.peekRedir() {
		allowInvalidAssign := ds.Variant.Value == "export" || ds.Variant.Value == "local"
		if op := p.declOperand(allowInvalidAssign); op != nil {
			subMode := subscriptModeFromArrayExprMode(arrayMode)
			switch op := op.(type) {
			case *DeclFlag:
				arrayMode = declArrayModeFromFlagWord(op.Word, arrayMode)
			case *DeclAssign:
				if op.Assign != nil {
					stampVarRefSubscriptMode(op.Assign.Ref, subMode)
				}
				if op.Assign != nil && op.Assign.Array != nil {
					op.Assign.Array.Mode = arrayMode
					stampArrayExprSubscriptModes(op.Assign.Array, subMode)
				}
			case *DeclName:
				stampVarRefSubscriptMode(op.Ref, subMode)
			}
			ds.Operands = append(ds.Operands, op)
		} else {
			p.followErr(p.pos, ds.Variant.Value, noQuote("names or assignments"))
		}
	}
	s.Cmd = ds
}

func isBashCompoundCommand(tok token, val string) bool {
	switch tok {
	case leftParen, dblLeftParen:
		return true
	case _LitWord:
		switch reservedWord(val) {
		case rsrvLeftBrace, rsrvIf, rsrvWhile, rsrvUntil, rsrvFor, rsrvCase, "[[",
			"coproc", "let", "function", "declare", "local",
			"export", "readonly", "typeset", "nameref":
			return true
		}
	}
	return false
}

func (p *Parser) timeClause(s *Stmt) {
	tc := &TimeClause{Time: p.pos}
	p.next()
	if _, ok := p.gotLitWord("-p"); ok {
		tc.PosixFormat = true
	}
	tc.Stmt = p.gotStmtPipe(&Stmt{Position: p.pos}, false)
	s.Cmd = tc
}

func (p *Parser) coprocClause(s *Stmt) {
	cc := &CoprocClause{Coproc: p.pos}
	if p.next(); isBashCompoundCommand(p.tok, p.val) {
		// has no name
		cc.Stmt = p.gotStmtPipe(&Stmt{Position: p.pos}, false)
		s.Cmd = cc
		return
	}
	cc.Name = p.getWord()
	cc.Stmt = p.gotStmtPipe(&Stmt{Position: p.pos}, false)
	if cc.Stmt == nil {
		if cc.Name == nil {
			p.posErr(cc.Coproc, "coproc clause requires a command")
			return
		}
		// name was in fact the stmt
		cc.Stmt = &Stmt{Position: cc.Name.Pos()}
		cc.Stmt.Cmd = p.call(cc.Name)
		cc.Name = nil
	} else if cc.Name != nil {
		if call, ok := cc.Stmt.Cmd.(*CallExpr); ok {
			// name was in fact the start of a call
			call.Args = append([]*Word{cc.Name}, call.Args...)
			cc.Name = nil
		}
	}
	s.Cmd = cc
}

func (p *Parser) letClause(s *Stmt) {
	lc := &LetClause{Let: p.pos}
	old := p.preNested(arithmExprLet)
	p.next()
	for !p.stopToken() && !p.peekRedir() {
		start := p.cursorSnapshot()
		if p.spaced && p.tok == hash {
			for p.tok != _Newl && p.tok != _EOF {
				commentStart := p.cursorSnapshot()
				p.next()
				if !commentStart.progressed(p) {
					p.posRecoverableErr(commentStart.pos, "internal parser error: no progress parsing let clause")
					break
				}
			}
			break
		}
		x := p.arithmExpr(true)
		if x == nil {
			if !start.progressed(p) {
				p.posRecoverableErr(start.pos, "internal parser error: no progress parsing let clause")
			}
			break
		}
		lc.Exprs = append(lc.Exprs, x)
		if !start.progressed(p) {
			p.posRecoverableErr(start.pos, "internal parser error: no progress parsing let clause")
			break
		}
	}
	if len(lc.Exprs) == 0 {
		p.followErrExp(lc.Let, "let")
	}
	p.postNested(old)
	s.Cmd = lc
}

func (p *Parser) bashFuncDecl(s *Stmt) {
	fpos := p.pos
	p.next()
	names := make([]*Lit, 0, 1)
	for p.tok == _LitWord && !p.atRsrv(rsrvLeftBrace) {
		names = append(names, p.lit(p.pos, p.val))
		p.next()
	}
	hasParens := p.got(leftParen)
	switch len(names) {
	case 0:
		if hasParens || p.atRsrv(rsrvLeftBrace) {
			p.checkLang(fpos, LangZsh, FeatureFunctionAnonymous)
		} else if !p.lang.in(LangZsh) {
			p.followErr(fpos, "function", noQuote("a name"))
		}
		names = nil // avoid non-nil zero-length slices
	case 1:
		// allowed in all variants
	default:
		p.checkLang(fpos, LangZsh, FeatureFunctionMultiName)
	}
	if hasParens {
		p.follow(fpos, "function foo(", rightParen)
	}
	p.funcDecl(s, fpos, true, hasParens, names...)
}

func (p *Parser) testDecl(s *Stmt) {
	td := &TestDecl{Position: p.pos}
	p.next()
	if td.Description = p.getWord(); td.Description == nil {
		p.followErr(td.Position, "@test", noQuote("a description word"))
	}
	if td.Body = p.getStmt(false, false, true); td.Body == nil {
		p.followErr(td.Position, `@test "desc"`, noQuote("a statement"))
	}
	s.Cmd = td
}

func (p *Parser) funcBodyUnexpectedToken() {
	unexpected, quoted := p.currentUnexpectedTokenDiagnostic()
	p.unexpectedTokenErr(p.pos, unexpected, quoted)
}

func (p *Parser) funcBodyStmt() *Stmt {
	if p.stopToken() {
		return nil
	}
	s := &Stmt{Position: p.pos}
	switch p.tok {
	case _LitWord:
		switch rsrv := reservedWord(p.val); rsrv {
		case rsrvLeftBrace:
			p.block(s)
		case rsrvIf:
			p.ifClause(s)
		case rsrvWhile, rsrvUntil:
			p.whileClause(s, rsrv == rsrvUntil)
		case rsrvFor:
			p.forClause(s)
		case rsrvCase:
			p.caseClause(s)
		case "[[":
			if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
				p.testClause(s)
			}
		case rsrvSelect:
			if p.lang.in(langBashLike | LangMirBSDKorn | LangZsh) {
				p.selectClause(s)
			}
		default:
			p.funcBodyUnexpectedToken()
			return nil
		}
	case leftParen:
		if p.r == ')' {
			p.unexpectedTokenErr(p.pos, ParseErrorSymbolRightParen, rightParen.bashQuote())
			return nil
		}
		p.subshell(s)
	case dblLeftParen:
		p.arithmExpCmd(s)
	default:
		p.funcBodyUnexpectedToken()
		return nil
	}
	if s.Cmd == nil || p.err != nil {
		if s.Cmd == nil && p.err == nil {
			p.funcBodyUnexpectedToken()
		}
		return nil
	}
	for p.peekRedir() {
		p.doRedirect(s)
	}
	return s
}

func (p *Parser) callExpr(s *Stmt, w *Word, assign bool) {
	ce := p.call(w)
	var separators []CallExprSeparator
	lastOperand := w != nil
	boundaryBroken := false
	appendAssign := func(sep CallExprSeparator, as *Assign) {
		if as == nil {
			return
		}
		if lastOperand {
			if boundaryBroken {
				separators = append(separators, CallExprSeparator{})
			} else {
				separators = append(separators, sep)
			}
		}
		ce.Assigns = append(ce.Assigns, as)
		lastOperand = true
		boundaryBroken = false
	}
	appendArg := func(sep CallExprSeparator, w *Word) {
		if w == nil {
			return
		}
		if lastOperand {
			if boundaryBroken {
				separators = append(separators, CallExprSeparator{})
			} else {
				separators = append(separators, sep)
			}
		}
		ce.Args = append(ce.Args, w)
		lastOperand = true
		boundaryBroken = false
	}
	finish := func() {
		if len(ce.Args) == 0 {
			ce.Args = nil
		}
		setCallExprSeparators(ce, separators)
		s.Cmd = ce
	}
	if w == nil {
		ce.Args = ce.Args[:0]
	}
	if assign {
		sep := p.tokSeparator
		if as, ok := p.tryAssignCandidate(false); ok {
			appendAssign(sep, as)
		} else if w := p.takePendingArrayWord(); w != nil {
			appendArg(sep, w)
		} else if p.err != nil {
			p.normalizeArrayLikeEOFError()
			finish()
			return
		}
	}
loop:
	for {
		switch p.tok {
		case _EOF, _Newl, semicolon, and, or, andAnd, orOr, orAnd, andPipe, andBang,
			dblSemicolon, semiAnd, dblSemiAnd, semiOr:
			break loop
		case _LitWord:
			if len(ce.Args) == 0 && p.hasValidIdent() {
				sep := p.tokSeparator
				if as, ok := p.tryAssignCandidate(false); ok {
					appendAssign(sep, as)
					break
				}
				if w := p.takePendingArrayWord(); w != nil {
					appendArg(sep, w)
					break
				}
				if p.err != nil {
					p.normalizeArrayLikeEOFError()
					break loop
				}
			}
			// When the command word follows environment bindings
			// (e.g. FOO=2 p_ FOO), expand aliases for the command word.
			if len(ce.Args) == 0 && len(ce.Assigns) > 0 {
				p.expandCommandAliases(true)
				if p.tok != _LitWord {
					continue
				}
			}
			// Avoid failing later with the confusing "} can only be used to close a block".
			if p.atRsrv(rsrvLeftBrace) && w != nil && w.Lit() == "function" {
				p.checkLang(p.pos, langBashLike, FeatureBuiltinFunctionKeyword)
			}
			// Zsh does not require a semicolon to close a block.
			if p.lang.in(LangZsh) && p.atRsrv(rsrvRightBrace) {
				break loop
			}
			sep := p.tokSeparator
			w := p.wordOne(p.lit(p.pos, p.val))
			p.next()
			if p.lang.in(LangZsh) && !p.spaced {
				w.Parts = append(w.Parts, p.wordParts(nil)...)
				p.finishWord(w)
			}
			appendArg(sep, w)
		case _Lit:
			if len(ce.Args) == 0 && p.hasValidIdent() {
				sep := p.tokSeparator
				if as, ok := p.tryAssignCandidate(false); ok {
					appendAssign(sep, as)
					break
				}
				if w := p.takePendingArrayWord(); w != nil {
					appendArg(sep, w)
					break
				}
				if p.err != nil {
					p.normalizeArrayLikeEOFError()
					break loop
				}
			}
			sep := p.tokSeparator
			appendArg(sep, p.wordAnyNumber())
		case bckQuote:
			if p.backquoteEnd() {
				break loop
			}
			fallthrough
		case dollBrace, dollDblParen, dollParen, dollar, cmdIn, assgnParen, cmdOut,
			sglQuote, dollSglQuote, dblQuote, dollDblQuote, dollBrack,
			globQuest, globStar, globPlus, globAt, globExcl:
			sep := p.tokSeparator
			appendArg(sep, p.wordAnyNumber())
		case dblLeftParen:
			p.curErr("%#q can only be used to open an arithmetic cmd", p.tok)
		case rightParen:
			if p.quote == subCmd {
				break loop
			}
			fallthrough
		default:
			if p.peekRedir() {
				p.doRedirect(s)
				boundaryBroken = lastOperand
				continue
			}
			// Note that we'll only keep the first error that happens.
			if len(ce.Args) > 0 {
				if cmd := ce.Args[0].Lit(); isBashCompoundCommand(_LitWord, cmd) {
					p.checkLang(p.pos, langBashLike, FeatureBuiltinKeywordLike, fmt.Sprintf("%#q", cmd))
				}
			}
			p.curUnexpectedErr()
		}
	}
	finish()
}

func (p *Parser) funcDecl(s *Stmt, pos Pos, long, withParens bool, names ...*Lit) {
	fd := &FuncDecl{
		Position: pos,
		RsrvWord: long,
		Parens:   withParens,
	}
	if len(names) == 1 {
		fd.Name = names[0]
	} else {
		fd.Names = names
	}
	p.got(_Newl)
	if fd.Body = p.funcBodyStmt(); fd.Body == nil {
		if p.err != nil {
			return
		}
		p.followErr(fd.Pos(), "foo()", noQuote("a statement"))
	}
	s.Cmd = fd
}
