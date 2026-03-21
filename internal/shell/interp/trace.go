package interp

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/ewhauser/gbash/internal/shell/expand"
	"github.com/ewhauser/gbash/internal/shell/syntax"
)

// tracer prints expressions like a shell would do if its
// options '-o' is set to either 'xtrace' or its shorthand, '-x'.
type tracer struct {
	buf          bytes.Buffer
	printer      *syntax.Printer
	output       io.Writer
	activeRunner *Runner
	prefixRunner *Runner
	syncedVars   map[string]expand.Variable
	needsPrefix  bool
	cLocale      bool
}

func (r *Runner) tracer() *tracer {
	if !r.opts[optXTrace] || r.suppressXTrace {
		return nil
	}
	output := r.stderr
	if r.traceOutput != nil {
		output = r.traceOutput
	}

	return &tracer{
		printer:      syntax.NewPrinter(),
		output:       output,
		activeRunner: r,
		prefixRunner: r.subshell(true),
		syncedVars:   traceVarsSnapshot(r.writeEnv),
		needsPrefix:  true,
		cLocale:      runnerUsesCLocale(r),
	}
}

func (r *Runner) traceErrorWriter() io.Writer {
	if r == nil {
		return io.Discard
	}
	if r.traceOutput != nil {
		return r.traceOutput
	}
	if r.stderr != nil {
		return r.stderr
	}
	return io.Discard
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
			fmt.Fprintf(r.traceErrorWriter(), "%s: bad substitution\n", src)
		case strings.Contains(msg, "reached EOF without matching `$(` with `)`"):
			fmt.Fprintln(r.traceErrorWriter(), "unexpected EOF while looking for matching `)'")
		default:
			fmt.Fprintln(r.traceErrorWriter(), msg)
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
		fmt.Fprintln(r.traceErrorWriter(), err.Error())
	}

	prefix, err := expand.Literal(&cfg, word)
	if err != nil {
		if msg, ok := ps4ArithmeticError(src, err); ok {
			fmt.Fprintln(r.traceErrorWriter(), msg)
		} else {
			fmt.Fprintln(r.traceErrorWriter(), err.Error())
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

func cloneTraceVar(vr expand.Variable) expand.Variable {
	vr.List = slices.Clone(vr.List)
	vr.Indices = slices.Clone(vr.Indices)
	vr.Map = maps.Clone(vr.Map)
	return vr
}

func sameTraceVar(a, b expand.Variable) bool {
	return a.Set == b.Set &&
		a.Local == b.Local &&
		a.Exported == b.Exported &&
		a.ReadOnly == b.ReadOnly &&
		a.Integer == b.Integer &&
		a.Lower == b.Lower &&
		a.Trace == b.Trace &&
		a.Upper == b.Upper &&
		a.Kind == b.Kind &&
		a.Str == b.Str &&
		slices.Equal(a.List, b.List) &&
		slices.Equal(a.Indices, b.Indices) &&
		maps.Equal(a.Map, b.Map)
}

func traceVarsSnapshot(env expand.Environ) map[string]expand.Variable {
	vars := make(map[string]expand.Variable)
	if env == nil {
		return vars
	}
	env.Each(func(name string, vr expand.Variable) bool {
		vars[name] = cloneTraceVar(vr)
		return true
	})
	return vars
}

func (t *tracer) syncPrefixSideEffects() {
	if t == nil || t.activeRunner == nil || t.prefixRunner == nil {
		return
	}

	current := traceVarsSnapshot(t.prefixRunner.writeEnv)
	active := t.activeRunner
	savedExit := active.exit
	savedLastExit := active.lastExit
	savedLastExpandExit := active.lastExpandExit
	defer func() {
		active.exit = savedExit
		active.lastExit = savedLastExit
		active.lastExpandExit = savedLastExpandExit
	}()

	for name, vr := range current {
		prev, ok := t.syncedVars[name]
		if ok && sameTraceVar(prev, vr) {
			continue
		}
		active.setVar(name, cloneTraceVar(vr))
		t.syncedVars[name] = cloneTraceVar(vr)
	}
}

func (t *tracer) refreshPrefixContext() {
	if t == nil || t.activeRunner == nil {
		return
	}
	t.prefixRunner = t.activeRunner.subshell(true)
	t.syncedVars = traceVarsSnapshot(t.activeRunner.writeEnv)
}

func (t *tracer) startLine() {
	if t == nil || !t.needsPrefix {
		return
	}
	if t.prefixRunner != nil {
		t.buf.WriteString(t.prefixRunner.tracePrefix())
		t.syncPrefixSideEffects()
	}
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

	t.string(cmd)
	for _, arg := range args {
		t.string(" ")
		t.string(traceCallArg(arg, t.traceArg))
	}
}
