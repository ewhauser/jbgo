package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixDiff(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixDiffEnv, "")

		_, _, err := resolveNixDiff(context.Background())
		if !errors.Is(err, errNixDiffUnset) {
			t.Fatalf("resolveNixDiff() error = %v, want %v", err, errNixDiffUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeDiff(t, "diff (GNU diffutils) 3.11")
		t.Setenv(nixDiffEnv, path)

		_, _, err := resolveNixDiff(context.Background())
		if err == nil {
			t.Fatal("resolveNixDiff() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require diffutils "+pinnedNixDiffVersion) {
			t.Fatalf("resolveNixDiff() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "diff (GNU diffutils) 3.12"
		path := writeFakeDiff(t, wantFirstLine)
		t.Setenv(nixDiffEnv, path)

		gotPath, gotFirstLine, err := resolveNixDiff(context.Background())
		if err != nil {
			t.Fatalf("resolveNixDiff() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixDiff() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixDiff() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeDiff(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "diff")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
