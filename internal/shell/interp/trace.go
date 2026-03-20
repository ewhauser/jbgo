package interp

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// tracer prints expressions like a shell would do if its
// options '-o' is set to either 'xtrace' or its shorthand, '-x'.
type tracer struct {
	buf         bytes.Buffer
	printer     *syntax.Printer
	output      io.Writer
	prefix      string
	needsPrefix bool
	cLocale     bool
}

func (r *Runner) tracer() *tracer {
	if !r.opts[optXTrace] || r.suppressXTrace {
		return nil
	}

	return &tracer{
		printer:     syntax.NewPrinter(),
		output:      r.stderr,
		prefix:      r.tracePrefix(),
		needsPrefix: true,
		cLocale:     runnerUsesCLocale(r),
	}
}

func (r *Runner) tracePrefix() string {
	vr := r.lookupVar("PS4")
	if !vr.IsSet() {
		return ""
	}
	src := vr.String()
	word, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Document(strings.NewReader(src))
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "reached EOF without matching `${` with `}`"):
			fmt.Fprintf(r.stderr, "%s: bad substitution\n", src)
		case strings.Contains(msg, "reached EOF without matching `$(` with `)`"):
			fmt.Fprintln(r.stderr, "unexpected EOF while looking for matching `)'")
		default:
			fmt.Fprintln(r.stderr, msg)
		}
		return ps4ParseFallback(src, err)
	}

	savedExit := r.exit
	savedLastExit := r.lastExit
	savedLastExpandExit := r.lastExpandExit
	savedSuppressXTrace := r.suppressXTrace
	defer func() {
		r.exit = savedExit
		r.lastExit = savedLastExit
		r.lastExpandExit = savedLastExpandExit
		r.suppressXTrace = savedSuppressXTrace
	}()
	r.suppressXTrace = true

	cfg := *r.ecfg
	cfg.ReportError = func(err error) {
		fmt.Fprintln(r.stderr, err.Error())
	}

	prefix, err := expand.Literal(&cfg, word)
	if err != nil {
		if msg, ok := ps4ArithmeticError(src, err); ok {
			fmt.Fprintln(r.stderr, msg)
		} else {
			fmt.Fprintln(r.stderr, err.Error())
		}
		return src
	}
	return prefix
}

func ps4ParseFallback(src string, err error) string {
	if err == nil {
		return src
	}
	if strings.Contains(err.Error(), "reached EOF without matching `${` with `}`") {
		return src
	}
	if strings.Contains(err.Error(), "unexpected EOF while looking for matching `)'") {
		if idx := strings.Index(src, "$("); idx >= 0 {
			return src[:idx]
		}
	}
	if strings.Contains(err.Error(), "reached EOF without matching `$(` with `)`") {
		if idx := strings.Index(src, "$("); idx >= 0 {
			return src[:idx]
		}
	}
	return src
}

func ps4ArithmeticError(src string, err error) (string, bool) {
	if err == nil || !strings.Contains(err.Error(), "division by 0") {
		return "", false
	}
	start := strings.Index(src, "$((")
	if start < 0 {
		return "", false
	}
	rest := src[start+3:]
	end := strings.Index(rest, "))")
	if end < 0 {
		return "", false
	}
	expr := strings.TrimLeft(rest[:end], " \t")
	token := expr
	for i := len(expr) - 1; i >= 0; i-- {
		switch expr[i] {
		case '+', '-', '*', '/', '%':
			token = strings.TrimLeft(expr[i+1:], " \t")
			return fmt.Sprintf("%s: division by 0 (error token is %q)", expr, token), true
		}
	}
	return "", false
}

func runnerUsesCLocale(r *Runner) bool {
	if r == nil {
		return false
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		switch r.envGet(name) {
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

func (t *tracer) startLine() {
	if t == nil || !t.needsPrefix {
		return
	}
	t.buf.WriteString(t.prefix)
	t.needsPrefix = false
}

// string writes s to tracer.buf if tracer is non-nil,
// prepending the expanded PS4 prefix if needed.
func (t *tracer) string(s string) {
	if t == nil {
		return
	}
	t.startLine()
	t.buf.WriteString(s)
}

func (t *tracer) stringf(f string, a ...any) {
	if t == nil {
		return
	}
	t.string(fmt.Sprintf(f, a...))
}

// expr prints x to tracer.buf if tracer is non-nil,
// prepending the expanded PS4 prefix if needed.
func (t *tracer) expr(x syntax.Node) {
	if t == nil {
		return
	}
	t.startLine()
	if err := t.printer.Print(&t.buf, x); err != nil {
		panic(err)
	}
}

// flush writes the contents of tracer.buf to the tracer stderr.
func (t *tracer) flush() {
	if t == nil {
		return
	}

	t.output.Write(t.buf.Bytes())
	t.buf.Reset()
}

// newLineFlush is like flush, but with an extra newline before tracer.buf gets flushed.
func (t *tracer) newLineFlush() {
	if t == nil {
		return
	}

	t.buf.WriteString("\n")
	t.flush()
	t.needsPrefix = true
}

func (t *tracer) traceArg(arg string) string {
	if arg == "'" {
		return `\'`
	}
	if t.cLocale && needsTraceANSIQuote(arg) {
		return traceANSIQuote(arg)
	}
	quoted, err := syntax.Quote(arg, syntax.LangBash)
	if err != nil {
		panic(err)
	}
	return quoted
}

func traceCallArg(arg string, traceArg func(string) string) string {
	name, value, ok := strings.Cut(arg, "=")
	appendValue := false
	if !ok {
		name, value, ok = strings.Cut(arg, "+=")
		appendValue = ok
	}
	if ok && syntax.ValidName(name) {
		quotedValue := traceArg(value)
		if quotedValue == value {
			if appendValue {
				return name + "+=" + value
			}
			return name + "=" + value
		}
	}
	return traceArg(arg)
}

func needsTraceANSIQuote(arg string) bool {
	for i := 0; i < len(arg); i++ {
		if arg[i] < 0x20 || arg[i] >= 0x7f {
			return true
		}
	}
	return false
}

func traceANSIQuote(arg string) string {
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(arg); i++ {
		switch c := arg[i]; c {
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\\', '\'':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
				continue
			}
			fmt.Fprintf(&b, "\\%03o", c)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// call prints a command and its arguments as xtrace output.
func (t *tracer) call(cmd string, args ...string) {
	if t == nil {
		return
	}
	if cmd == "set" {
		return
	}

	t.string(cmd)
	for _, arg := range args {
		t.string(" ")
		t.string(traceCallArg(arg, t.traceArg))
	}
}
