package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixBash(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixBashEnv, "")

		_, _, err := resolveNixBash(context.Background())
		if !errors.Is(err, errNixBashUnset) {
			t.Fatalf("resolveNixBash() error = %v, want %v", err, errNixBashUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeBash(t, "GNU bash, version 5.2.37(1)-release")
		t.Setenv(nixBashEnv, path)

		_, _, err := resolveNixBash(context.Background())
		if err == nil {
			t.Fatal("resolveNixBash() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require bash "+pinnedNixBashVersion) {
			t.Fatalf("resolveNixBash() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "GNU bash, version 5.3.9(1)-release"
		path := writeFakeBash(t, wantFirstLine)
		t.Setenv(nixBashEnv, path)

		gotPath, gotFirstLine, err := resolveNixBash(context.Background())
		if err != nil {
			t.Fatalf("resolveNixBash() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixBash() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixBash() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeBash(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bash")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
