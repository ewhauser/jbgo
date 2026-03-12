package commands

import (
	"bytes"
	"context"
	"io"
	"slices"
	"testing"
)

func TestRegistryRegisterOverridesExistingCommand(t *testing.T) {
	registry := NewRegistry()
	first := DefineCommand("probe", nil)
	second := DefineCommand("probe", nil)

	if err := registry.Register(first); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatalf("Register(second) error = %v", err)
	}

	got, ok := registry.Lookup("probe")
	if !ok {
		t.Fatalf("Lookup(probe) ok = false, want true")
	}
	if got != second {
		t.Fatalf("Lookup(probe) returned %T, want second registration", got)
	}
}

func TestRegistryLazyCommandLoadsOnce(t *testing.T) {
	registry := NewRegistry()
	loads := 0

	if err := registry.RegisterLazy("lazyprobe", func() (Command, error) {
		loads++
		return DefineCommand("lazyprobe", func(_ context.Context, inv *Invocation) error {
			_, err := io.WriteString(inv.Stdout, "lazy\n")
			return err
		}), nil
	}); err != nil {
		t.Fatalf("RegisterLazy(lazyprobe) error = %v", err)
	}

	if loads != 0 {
		t.Fatalf("loader count = %d before lookup, want 0", loads)
	}
	if !slices.Contains(registry.Names(), "lazyprobe") {
		t.Fatalf("Names() = %v, want lazyprobe present", registry.Names())
	}

	cmd, ok := registry.Lookup("lazyprobe")
	if !ok {
		t.Fatalf("Lookup(lazyprobe) ok = false, want true")
	}
	if loads != 0 {
		t.Fatalf("loader count = %d after lookup, want 0", loads)
	}

	var stdout bytes.Buffer
	inv := NewInvocation(&InvocationOptions{Stdout: &stdout})
	if err := cmd.Run(context.Background(), inv); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := cmd.Run(context.Background(), inv); err != nil {
		t.Fatalf("Run() second error = %v", err)
	}
	if loads != 1 {
		t.Fatalf("loader count = %d after runs, want 1", loads)
	}
	if got, want := stdout.String(), "lazy\nlazy\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDefaultRegistryDoesNotIncludeSQLite3(t *testing.T) {
	if slices.Contains(DefaultRegistry().Names(), "sqlite3") {
		t.Fatalf("DefaultRegistry() unexpectedly includes sqlite3")
	}
}
