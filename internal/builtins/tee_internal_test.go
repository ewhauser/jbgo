package builtins

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
)

type teeTestWriter struct {
	writes [][]byte
	err    error
}

func (w *teeTestWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	cp := append([]byte(nil), p...)
	w.writes = append(w.writes, cp)
	return len(p), nil
}

type teeTestFlusher struct {
	teeTestWriter
	flushErr error
	flushed  int
}

type teeTestCloser struct {
	closed int
}

func (c *teeTestCloser) Close() error {
	c.closed++
	return nil
}

func (w *teeTestFlusher) Flush() error {
	w.flushed++
	return w.flushErr
}

func TestParseTeeArgsSupportsLongFlagsAndOperands(t *testing.T) {
	inv := &Invocation{Args: []string{"--append", "--ignore-interrupts", "--output-error=warn-", "-", "out.txt"}}

	got, err := parseTeeArgs(inv)
	if err != nil {
		t.Fatalf("parseTeeArgs() error = %v", err)
	}
	if !got.append {
		t.Fatalf("append = false, want true")
	}
	if !got.ignoreInterrupts {
		t.Fatalf("ignoreInterrupts = false, want true")
	}
	if got.outputError == nil || *got.outputError != teeOutputErrorWarnNoPipe {
		t.Fatalf("outputError = %#v, want warn-nopipe", got.outputError)
	}
	if want := []string{"-", "out.txt"}; !equalStrings(got.files, want) {
		t.Fatalf("files = %#v, want %#v", got.files, want)
	}
}

func TestParseTeeArgsExplicitOutputErrorOverridesPipeMode(t *testing.T) {
	inv := &Invocation{Args: []string{"-p", "--output-error=exit", "out.txt"}}

	got, err := parseTeeArgs(inv)
	if err != nil {
		t.Fatalf("parseTeeArgs() error = %v", err)
	}
	if got.outputError == nil || *got.outputError != teeOutputErrorExit {
		t.Fatalf("outputError = %#v, want exit", got.outputError)
	}
}

func TestParseTeeArgsShortHelpAndVersion(t *testing.T) {
	tests := []struct {
		args        []string
		wantHelp    bool
		wantVersion bool
	}{
		{args: []string{"-h"}, wantHelp: true},
		{args: []string{"--ver"}, wantVersion: true},
	}

	for _, tc := range tests {
		inv := &Invocation{Args: tc.args}
		got, err := parseTeeArgs(inv)
		if err != nil {
			t.Fatalf("parseTeeArgs(%v) error = %v", tc.args, err)
		}
		if got.showHelp != tc.wantHelp || got.showVersion != tc.wantVersion {
			t.Fatalf("parseTeeArgs(%v) = help:%v version:%v", tc.args, got.showHelp, got.showVersion)
		}
	}
}

func TestParseTeeArgsRejectsInvalidOutputErrorValue(t *testing.T) {
	inv := &Invocation{
		Args:   []string{"--output-error=nope"},
		Stderr: &bytes.Buffer{},
	}

	err := parseTeeExpectError(t, inv)
	if !strings.Contains(err.Error(), "invalid argument 'nope'") {
		t.Fatalf("error = %q, want invalid argument", err.Error())
	}
}

func TestParseTeeArgsRejectsUnknownLongOption(t *testing.T) {
	inv := &Invocation{
		Args:   []string{"--definitely-invalid"},
		Stderr: &bytes.Buffer{},
	}

	err := parseTeeExpectError(t, inv)
	if !strings.Contains(err.Error(), "unrecognized option '--definitely-invalid'") {
		t.Fatalf("error = %q, want unknown option", err.Error())
	}
}

func TestTeeMultiWriterDefaultBrokenPipeAbortsQuietly(t *testing.T) {
	stdout := &teeTestWriter{err: syscall.EPIPE}
	fileOut := &teeTestWriter{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
		{name: "out.txt", writer: fileOut},
	}, nil, stderr)

	err := multi.writeAndFlush([]byte("hello"))
	if !errors.Is(err, errTeeAbortQuiet) {
		t.Fatalf("writeAndFlush() error = %v, want quiet abort", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if len(fileOut.writes) != 0 {
		t.Fatalf("file writes = %#v, want none after quiet abort", fileOut.writes)
	}
}

func TestTeeMultiWriterWarnNoPipeContinuesPastBrokenPipe(t *testing.T) {
	mode := teeOutputErrorWarnNoPipe
	stdout := &teeTestWriter{err: syscall.EPIPE}
	fileOut := &teeTestWriter{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
		{name: "out.txt", writer: fileOut},
	}, &mode, stderr)

	err := multi.writeAndFlush([]byte("hello"))
	if err != nil {
		t.Fatalf("writeAndFlush() error = %v, want nil", err)
	}
	if got := joinedWrites(fileOut.writes); got != "hello" {
		t.Fatalf("file output = %q, want %q", got, "hello")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestTeeMultiWriterWarnReportsWriteErrors(t *testing.T) {
	mode := teeOutputErrorWarn
	stdout := &teeTestWriter{err: io.ErrClosedPipe}
	fileOut := &teeTestWriter{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
		{name: "out.txt", writer: fileOut},
	}, &mode, stderr)

	err := multi.writeAndFlush([]byte("hello"))
	if err != nil {
		t.Fatalf("writeAndFlush() error = %v, want nil", err)
	}
	if !strings.Contains(stderr.String(), "tee: standard output:") {
		t.Fatalf("stderr = %q, want stdout write error", stderr.String())
	}
	if got := joinedWrites(fileOut.writes); got != "hello" {
		t.Fatalf("file output = %q, want %q", got, "hello")
	}
}

func TestTeeMultiWriterExitAbortsOnWriteError(t *testing.T) {
	mode := teeOutputErrorExit
	stdout := &teeTestWriter{err: io.ErrClosedPipe}
	fileOut := &teeTestWriter{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
		{name: "out.txt", writer: fileOut},
	}, &mode, stderr)

	err := multi.writeAndFlush([]byte("hello"))
	if !errors.Is(err, errTeeAbort) {
		t.Fatalf("writeAndFlush() error = %v, want abort", err)
	}
	if len(fileOut.writes) != 0 {
		t.Fatalf("file writes = %#v, want none after abort", fileOut.writes)
	}
}

func TestTeeMultiWriterFlushesPerChunk(t *testing.T) {
	stdout := &teeTestFlusher{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
	}, nil, stderr)

	if err := multi.writeAndFlush([]byte("hello")); err != nil {
		t.Fatalf("writeAndFlush() error = %v", err)
	}
	if stdout.flushed != 1 {
		t.Fatalf("Flush() count = %d, want 1", stdout.flushed)
	}
}

func TestTeeMultiWriterClosesDroppedWriters(t *testing.T) {
	closer := &teeTestCloser{}
	stdout := &teeTestWriter{err: syscall.EPIPE}
	fileOut := &teeTestWriter{}
	stderr := &bytes.Buffer{}
	multi := newTeeMultiWriter([]*teeWriter{
		{name: "standard output", writer: stdout},
		{name: "out.txt", writer: fileOut, closer: closer, path: "/tmp/out.txt"},
	}, nil, stderr)

	err := multi.writeAndFlush([]byte("hello"))
	if !errors.Is(err, errTeeAbortQuiet) {
		t.Fatalf("writeAndFlush() error = %v, want quiet abort", err)
	}
	if closeErr := multi.closeAndTrace(&Invocation{}); closeErr != nil {
		t.Fatalf("closeAndTrace() error = %v", closeErr)
	}
	if closer.closed != 1 {
		t.Fatalf("closer.Close() count = %d, want 1", closer.closed)
	}
}

func parseTeeExpectError(t *testing.T, inv *Invocation) error {
	t.Helper()
	_, err := parseTeeArgs(inv)
	if err == nil {
		t.Fatal("parseTeeArgs() error = nil, want error")
	}
	return err
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func joinedWrites(chunks [][]byte) string {
	var buf bytes.Buffer
	for _, chunk := range chunks {
		buf.Write(chunk)
	}
	return buf.String()
}
