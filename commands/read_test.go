package commands

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestReadAllEnforcesMaxFileBytes(t *testing.T) {
	t.Parallel()
	inv := &Invocation{
		Limits: policy.Limits{MaxFileBytes: 3},
	}

	_, err := ReadAll(context.Background(), inv, strings.NewReader("abcd"))
	if err == nil {
		t.Fatal("ReadAll() error = nil, want max-file-bytes failure")
	}
	if code, ok := ExitCode(err); !ok || code != 1 {
		t.Fatalf("ExitCode(err) = (%d, %v), want (1, true)", code, ok)
	}
	if got, ok := DiagnosticMessage(err); !ok || got != "input exceeds maximum file size of 3 bytes" {
		t.Fatalf("DiagnosticMessage(err) = (%q, %v), want (%q, true)", got, ok, "input exceeds maximum file size of 3 bytes")
	}
}

func TestReadAllStdinUsesInvocationStdin(t *testing.T) {
	t.Parallel()
	inv := &Invocation{
		Stdin:  strings.NewReader("abc"),
		Limits: policy.Limits{MaxFileBytes: 3},
	}

	data, err := ReadAllStdin(context.Background(), inv)
	if err != nil {
		t.Fatalf("ReadAllStdin() error = %v", err)
	}
	if got := string(data); got != "abc" {
		t.Fatalf("ReadAllStdin() = %q, want %q", got, "abc")
	}
}

func TestCommandFSReadFileEnforcesMaxFileBytes(t *testing.T) {
	t.Parallel()
	mem := gbfs.NewMemory()
	file, err := mem.OpenFile(context.Background(), "/input.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.Write([]byte("abcd")); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	inv := NewInvocation(&InvocationOptions{
		FileSystem: mem,
		Policy:     policy.NewStatic(&policy.Config{}),
	})
	inv.Limits.MaxFileBytes = 3
	inv.FS.limits = inv.Limits

	_, err = inv.FS.ReadFile(context.Background(), "/input.txt")
	if err == nil {
		t.Fatal("ReadFile() error = nil, want max-file-bytes failure")
	}
	if code, ok := ExitCode(err); !ok || code != 1 {
		t.Fatalf("ExitCode(err) = (%d, %v), want (1, true)", code, ok)
	}
	if got, ok := DiagnosticMessage(err); !ok || got != "input exceeds maximum file size of 3 bytes" {
		t.Fatalf("DiagnosticMessage(err) = (%q, %v), want (%q, true)", got, ok, "input exceeds maximum file size of 3 bytes")
	}
}

func TestCommandFSOpenFileCreateRequiresWritePolicy(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	inv := NewInvocation(&InvocationOptions{
		FileSystem: mem,
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:  []string{"/"},
			WriteRoots: []string{"/tmp"},
		}),
	})

	_, err := inv.FS.OpenFile(context.Background(), "/escape.txt", os.O_CREATE|os.O_RDONLY, 0o644)
	if err == nil {
		t.Fatal("OpenFile() error = nil, want write policy denial")
	}
	if got := err.Error(); got != "write \"/escape.txt\" denied: outside allowed roots" {
		t.Fatalf("OpenFile() error = %q, want %q", got, "write \"/escape.txt\" denied: outside allowed roots")
	}
	if code, ok := ExitCode(err); !ok || code != 126 {
		t.Fatalf("ExitCode(err) = (%d, %v), want (126, true)", code, ok)
	}
	if !policy.IsDenied(err) {
		t.Fatalf("policy.IsDenied(err) = false, want true")
	}
	if _, statErr := mem.Stat(context.Background(), "/escape.txt"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Stat(/escape.txt) error = %v, want not-exist", statErr)
	}
}
