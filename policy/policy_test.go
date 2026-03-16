package policy

import (
	"context"
	"testing"
)

func TestNewStaticDefaultsToSymlinkDeny(t *testing.T) {
	t.Parallel()
	pol := NewStatic(nil)

	if got, want := pol.SymlinkMode(), SymlinkDeny; got != want {
		t.Fatalf("SymlinkMode() = %q, want %q", got, want)
	}
}

func TestNewStaticDefaultsToBuiltinAllowOpen(t *testing.T) {
	t.Parallel()
	pol := NewStatic(nil)

	if err := pol.AllowBuiltin(context.Background(), "eval", []string{"eval", "echo ok"}); err != nil {
		t.Fatalf("AllowBuiltin(eval) error = %v, want nil", err)
	}
}

func TestNewStaticBuiltinAllowlistRejectsMissingBuiltin(t *testing.T) {
	t.Parallel()
	pol := NewStatic(&Config{AllowedBuiltins: []string{"cd"}})

	err := pol.AllowBuiltin(context.Background(), "eval", []string{"eval", "echo ok"})
	if err == nil {
		t.Fatal("AllowBuiltin(eval) error = nil, want denial")
	}
	if !IsDenied(err) {
		t.Fatalf("AllowBuiltin(eval) error = %v, want denied", err)
	}
}
