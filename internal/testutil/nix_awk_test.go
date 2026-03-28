package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixAWK(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixAWKEnv, "")

		_, _, err := resolveNixAWK(context.Background())
		if !errors.Is(err, errNixAWKUnset) {
			t.Fatalf("resolveNixAWK() error = %v, want %v", err, errNixAWKUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeAWK(t, "GNU Awk 5.3.1, API 4.0, PMA Avon 8-g1")
		t.Setenv(nixAWKEnv, path)

		_, _, err := resolveNixAWK(context.Background())
		if err == nil {
			t.Fatal("resolveNixAWK() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require awk "+pinnedNixAWKVersion) {
			t.Fatalf("resolveNixAWK() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "GNU Awk 5.3.2, API 4.0, PMA Avon 8-g1"
		path := writeFakeAWK(t, wantFirstLine)
		t.Setenv(nixAWKEnv, path)

		gotPath, gotFirstLine, err := resolveNixAWK(context.Background())
		if err != nil {
			t.Fatalf("resolveNixAWK() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixAWK() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixAWK() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeAWK(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "awk")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
