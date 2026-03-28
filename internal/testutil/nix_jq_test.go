package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixJQ(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixJQEnv, "")

		_, _, err := resolveNixJQ(context.Background())
		if !errors.Is(err, errNixJQUnset) {
			t.Fatalf("resolveNixJQ() error = %v, want %v", err, errNixJQUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeJQ(t, "jq-1.7.1")
		t.Setenv(nixJQEnv, path)

		_, _, err := resolveNixJQ(context.Background())
		if err == nil {
			t.Fatal("resolveNixJQ() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require jq "+pinnedNixJQVersion) {
			t.Fatalf("resolveNixJQ() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "jq-1.8.1"
		path := writeFakeJQ(t, wantFirstLine)
		t.Setenv(nixJQEnv, path)

		gotPath, gotFirstLine, err := resolveNixJQ(context.Background())
		if err != nil {
			t.Fatalf("resolveNixJQ() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixJQ() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixJQ() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeJQ(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "jq")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
