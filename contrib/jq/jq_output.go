package jq

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	stdfs "io/fs"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ewhauser/gbash/commands"
	"github.com/ewhauser/gbash/internal/commandutil"
	"golang.org/x/term"
)

type jqFormatter struct {
	marshaler  jqMarshaler
	out        io.Writer
	join       bool
	rawLike    bool
	rawOutput0 bool
	seq        bool
	unbuffered bool
}

func newJQFormatter(inv *commands.Invocation, opts *jqOptions) *jqFormatter {
	indent := 2
	switch {
	case opts.compact:
		indent = -1
	case opts.tab:
		indent = 1
	case opts.indent != nil:
		indent = *opts.indent
	}

	encoder := newJQEncoder(opts.tab, indent, opts.asciiOutput, jqColorEnabled(inv, opts))
	marshaler := jqMarshaler(encoder)
	rawLike := opts.raw || opts.rawOutput0 || opts.join
	if rawLike {
		marshaler = &jqRawMarshaler{m: encoder, checkNUL: opts.rawOutput0}
	}

	return &jqFormatter{
		marshaler:  marshaler,
		out:        inv.Stdout,
		join:       opts.join,
		rawLike:    rawLike,
		rawOutput0: opts.rawOutput0,
		seq:        opts.seq,
		unbuffered: opts.unbuffered,
	}
}

func (f *jqFormatter) WriteValue(value any) error {
	if f == nil || f.out == nil {
		return nil
	}
	if f.seq && !f.rawLike {
		if _, err := f.out.Write([]byte{0x1e}); err != nil {
			return &commands.ExitError{Code: 1, Err: err}
		}
	}
	if err := f.marshaler.marshal(value, f.out); err != nil {
		return err
	}
	switch {
	case f.rawOutput0:
		if _, err := f.out.Write([]byte{0x00}); err != nil {
			return &commands.ExitError{Code: 1, Err: err}
		}
	case !f.join:
		if _, err := f.out.Write([]byte{'\n'}); err != nil {
			return &commands.ExitError{Code: 1, Err: err}
		}
	}
	if f.unbuffered {
		if flusher, ok := f.out.(interface{ Flush() error }); ok {
			if err := flusher.Flush(); err != nil {
				return &commands.ExitError{Code: 1, Err: err}
			}
		}
	}
	return nil
}

type jqMarshaler interface {
	marshal(any, io.Writer) error
}

type jqRawMarshaler struct {
	m        jqMarshaler
	checkNUL bool
}

func (m *jqRawMarshaler) marshal(value any, out io.Writer) error {
	if s, ok := value.(string); ok {
		if m.checkNUL && strings.ContainsRune(s, '\x00') {
			return fmt.Errorf("cannot output a string containing NUL character: %q", s)
		}
		_, err := out.Write([]byte(s))
		return err
	}
	return m.m.marshal(value, out)
}

type jqEncoder struct {
	out         io.Writer
	buf         *bytes.Buffer
	tab         bool
	indent      int
	depth       int
	asciiOutput bool
	useColor    bool
	scratch     [64]byte
}

func newJQEncoder(tab bool, indent int, asciiOutput, useColor bool) *jqEncoder {
	return &jqEncoder{
		buf:         new(bytes.Buffer),
		tab:         tab,
		indent:      indent,
		asciiOutput: asciiOutput,
		useColor:    useColor,
	}
}

func (e *jqEncoder) marshal(value any, out io.Writer) error {
	e.out = out
	return cmp.Or(e.encode(value), e.flush())
}

func (e *jqEncoder) flush() error {
	_, err := e.out.Write(e.buf.Bytes())
	e.buf.Reset()
	return err
}

func (e *jqEncoder) encode(value any) error {
	switch value := value.(type) {
	case nil:
		e.write([]byte("null"), jqNullColor)
	case bool:
		if value {
			e.write([]byte("true"), jqTrueColor)
		} else {
			e.write([]byte("false"), jqFalseColor)
		}
	case int:
		e.write(strconv.AppendInt(e.scratch[:0], int64(value), 10), jqNumberColor)
	case float64:
		e.encodeFloat64(value)
	case *big.Int:
		e.write(value.Append(e.scratch[:0], 10), jqNumberColor)
	case json.Number:
		e.write([]byte(value.String()), jqNumberColor)
	case string:
		e.encodeString(value, jqStringColor)
	case []any:
		return e.encodeArray(value)
	case map[string]any:
		return e.encodeObject(value)
	default:
		return fmt.Errorf("invalid jq value type: %T", value)
	}
	if e.buf.Len() > 8*1024 {
		return e.flush()
	}
	return nil
}

func (e *jqEncoder) encodeFloat64(value float64) {
	if math.IsNaN(value) {
		e.write([]byte("null"), jqNullColor)
		return
	}
	value = min(max(value, -math.MaxFloat64), math.MaxFloat64)
	format := byte('f')
	if x := math.Abs(value); (x != 0 && x < 1e-6) || x >= 1e21 {
		format = 'e'
	}
	buf := strconv.AppendFloat(e.scratch[:0], value, format, -1, 64)
	if format == 'e' {
		if n := len(buf); n >= 4 && buf[n-4] == 'e' && buf[n-3] == '-' && buf[n-2] == '0' {
			buf[n-2] = buf[n-1]
			buf = buf[:n-1]
		}
	}
	e.write(buf, jqNumberColor)
}

func (e *jqEncoder) encodeString(value string, color []byte) {
	if color != nil {
		e.setColor(color)
	}
	e.buf.WriteByte('"')
	start := 0
	for i := 0; i < len(value); {
		if b := value[i]; b < utf8.RuneSelf {
			if ' ' <= b && b <= '~' && b != '"' && b != '\\' {
				i++
				continue
			}
			if start < i {
				e.buf.WriteString(value[start:i])
			}
			e.writeEscapedByte(b)
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && size == 1 {
			if start < i {
				e.buf.WriteString(value[start:i])
			}
			e.writeUnicodeEscape('\ufffd')
			i += size
			start = i
			continue
		}
		if !e.asciiOutput {
			i += size
			continue
		}
		if start < i {
			e.buf.WriteString(value[start:i])
		}
		e.writeUnicodeEscape(r)
		i += size
		start = i
	}
	if start < len(value) {
		e.buf.WriteString(value[start:])
	}
	e.buf.WriteByte('"')
	if color != nil {
		e.setColor(jqResetColor)
	}
}

func (e *jqEncoder) encodeArray(values []any) error {
	e.writeByte('[', jqArrayColor)
	e.depth += e.indent
	for i, value := range values {
		if i > 0 {
			e.writeByte(',', jqArrayColor)
		}
		if e.indent >= 0 {
			e.writeIndent()
		}
		if err := e.encode(value); err != nil {
			return err
		}
	}
	e.depth -= e.indent
	if len(values) > 0 && e.indent >= 0 {
		e.writeIndent()
	}
	e.writeByte(']', jqArrayColor)
	return nil
}

func (e *jqEncoder) encodeObject(values map[string]any) error {
	e.writeByte('{', jqObjectColor)
	e.depth += e.indent
	type keyValue struct {
		key   string
		value any
	}
	kvs := make([]keyValue, 0, len(values))
	for key, value := range values {
		kvs = append(kvs, keyValue{key: key, value: value})
	}
	sort.Slice(kvs, func(i, j int) bool {
		return kvs[i].key < kvs[j].key
	})
	for i, kv := range kvs {
		if i > 0 {
			e.writeByte(',', jqObjectColor)
		}
		if e.indent >= 0 {
			e.writeIndent()
		}
		e.encodeString(kv.key, jqObjectKeyColor)
		e.writeByte(':', jqObjectColor)
		if e.indent >= 0 {
			e.buf.WriteByte(' ')
		}
		if err := e.encode(kv.value); err != nil {
			return err
		}
	}
	e.depth -= e.indent
	if len(values) > 0 && e.indent >= 0 {
		e.writeIndent()
	}
	e.writeByte('}', jqObjectColor)
	return nil
}

func (e *jqEncoder) writeEscapedByte(b byte) {
	switch b {
	case '"':
		e.buf.WriteString(`\"`)
	case '\\':
		e.buf.WriteString(`\\`)
	case '\b':
		e.buf.WriteString(`\b`)
	case '\f':
		e.buf.WriteString(`\f`)
	case '\n':
		e.buf.WriteString(`\n`)
	case '\r':
		e.buf.WriteString(`\r`)
	case '\t':
		e.buf.WriteString(`\t`)
	default:
		e.writeUnicodeEscape(rune(b))
	}
}

func (e *jqEncoder) writeUnicodeEscape(r rune) {
	if r < 0x10000 {
		e.buf.WriteString(`\u`)
		_, _ = fmt.Fprintf(e.buf, "%04x", r)
		return
	}
	hi, lo := utf16.EncodeRune(r)
	e.writeUnicodeEscape(hi)
	e.writeUnicodeEscape(lo)
}

func (e *jqEncoder) write(data, color []byte) {
	if color != nil {
		e.setColor(color)
	}
	e.buf.Write(data)
	if color != nil {
		e.setColor(jqResetColor)
	}
}

func (e *jqEncoder) writeByte(b byte, color []byte) {
	if color != nil {
		e.setColor(color)
	}
	e.buf.WriteByte(b)
	if color != nil {
		e.setColor(jqResetColor)
	}
}

func (e *jqEncoder) writeIndent() {
	e.buf.WriteByte('\n')
	if e.indent <= 0 {
		return
	}
	if e.tab {
		for i := 0; i < e.depth; i++ {
			e.buf.WriteByte('\t')
		}
		return
	}
	for i := 0; i < e.depth; i++ {
		e.buf.WriteByte(' ')
	}
}

func (e *jqEncoder) setColor(color []byte) {
	if e.useColor {
		e.buf.Write(color)
	}
}

func jqColorEnabled(inv *commands.Invocation, opts *jqOptions) bool {
	if opts == nil {
		return false
	}
	switch {
	case opts.monoOutput:
		return false
	case opts.colorOutput:
		return true
	case inv != nil && inv.Env != nil && inv.Env["NO_COLOR"] != "":
		return false
	case inv != nil && inv.Env != nil && inv.Env["TERM"] == "dumb":
		return false
	default:
		return jqWriterIsTTY(inv.Stdout)
	}
}

func jqWriterIsTTY(writer io.Writer) bool {
	if writer == nil {
		return false
	}
	if meta, ok := writer.(commandutil.RedirectMetadata); ok {
		if jqRecognizedTTYPath(meta.RedirectPath()) {
			return true
		}
	}
	if statter, ok := writer.(interface {
		Stat() (stdfs.FileInfo, error)
	}); ok {
		if info, err := statter.Stat(); err == nil && info.Mode()&stdfs.ModeCharDevice != 0 {
			return true
		}
	}
	if fd, ok := writer.(interface{ Fd() uintptr }); ok {
		if descriptor := fd.Fd(); descriptor != 0 && term.IsTerminal(int(descriptor)) {
			return true
		}
	}
	return false
}

func jqRecognizedTTYPath(name string) bool {
	cleaned := strings.TrimSpace(name)
	if cleaned == "" {
		return false
	}
	cleaned = strings.TrimRight(cleaned, "/")
	switch {
	case cleaned == "/dev/tty", cleaned == "/dev/console":
		return true
	case strings.HasPrefix(cleaned, "/dev/tty"):
		return true
	case strings.HasPrefix(cleaned, "/dev/pts/"):
		return true
	default:
		return false
	}
}

var (
	jqResetColor     = []byte("\x1b[0m")
	jqNullColor      = []byte("\x1b[90m")
	jqFalseColor     = []byte("\x1b[33m")
	jqTrueColor      = []byte("\x1b[33m")
	jqNumberColor    = []byte("\x1b[36m")
	jqStringColor    = []byte("\x1b[32m")
	jqObjectKeyColor = []byte("\x1b[34;1m")
	jqArrayColor     = []byte(nil)
	jqObjectColor    = []byte(nil)
)
