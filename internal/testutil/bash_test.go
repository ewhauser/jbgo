package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConformanceBash(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(conformanceBashEnv, "")

		_, _, err := resolveConformanceBash()
		if !errors.Is(err, errConformanceBashUnset) {
			t.Fatalf("resolveConformanceBash() error = %v, want %v", err, errConformanceBashUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeBash(t, "GNU bash, version 5.2.37(1)-release")
		t.Setenv(conformanceBashEnv, path)

		_, _, err := resolveConformanceBash()
		if err == nil {
			t.Fatal("resolveConformanceBash() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require bash "+pinnedConformanceBashVersion) {
			t.Fatalf("resolveConformanceBash() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "GNU bash, version 5.3.9(1)-release"
		path := writeFakeBash(t, wantFirstLine)
		t.Setenv(conformanceBashEnv, path)

		gotPath, gotFirstLine, err := resolveConformanceBash()
		if err != nil {
			t.Fatalf("resolveConformanceBash() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveConformanceBash() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveConformanceBash() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
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
