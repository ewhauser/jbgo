package builtins

import (
	"bytes"
	"context"
	"os"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestWCPathStreamPreservesRegularFiles0Width(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	writeWCFile(t, mem, "/tmp/2b", []byte("2\n"))
	writeWCFile(t, mem, "/tmp/2w", []byte("2 words\n"))
	writeWCFile(t, mem, "/tmp/names", []byte("/tmp/2b\x00/tmp/2w\x00"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		Stdout:     &stdout,
		Stderr:     &stderr,
		FileSystem: mem,
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/"},
		}),
	})

	opts := wcOptions{
		lines:     true,
		words:     true,
		bytes:     true,
		totalWhen: wcTotalNever,
	}
	columnWidth := wcFiles0StreamWidthForRegularSource(context.Background(), inv, opts, "/tmp/names")

	if err := wcRunFiles0FromPathStream(context.Background(), inv, opts, "/tmp/names", columnWidth); err != nil {
		t.Fatalf("wcRunFiles0FromPathStream() error = %v", err)
	}

	if got, want := stdout.String(), " 1  1  2 /tmp/2b\n 1  2  8 /tmp/2w\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func writeWCFile(tb testing.TB, fs gbfs.FileSystem, name string, data []byte) {
	tb.Helper()

	if err := fs.MkdirAll(context.Background(), "/tmp", 0o755); err != nil {
		tb.Fatalf("MkdirAll(/tmp) error = %v", err)
	}
	file, err := fs.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		tb.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(data); err != nil {
		tb.Fatalf("Write(%q) error = %v", name, err)
	}
}
