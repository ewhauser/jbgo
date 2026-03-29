package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixRipgrep(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixRipgrepEnv, "")

		_, _, err := resolveNixRipgrep(context.Background())
		if !errors.Is(err, errNixRipgrepUnset) {
			t.Fatalf("resolveNixRipgrep() error = %v, want %v", err, errNixRipgrepUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeRipgrep(t, "ripgrep 14.1.1")
		t.Setenv(nixRipgrepEnv, path)

		_, _, err := resolveNixRipgrep(context.Background())
		if err == nil {
			t.Fatal("resolveNixRipgrep() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require ripgrep "+pinnedNixRipgrepVersion) {
			t.Fatalf("resolveNixRipgrep() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "ripgrep 15.1.0"
		path := writeFakeRipgrep(t, wantFirstLine)
		t.Setenv(nixRipgrepEnv, path)

		gotPath, gotFirstLine, err := resolveNixRipgrep(context.Background())
		if err != nil {
			t.Fatalf("resolveNixRipgrep() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixRipgrep() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixRipgrep() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeRipgrep(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rg")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
